package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const financeReceiptProvider = "local-tesseract-receipt-v1"

type financeStoredReceiptSuggestions struct {
	RequestHash   string                       `json:"request_hash"`
	SourceHash    string                       `json:"source_hash"`
	SourceVersion int64                        `json:"source_version"`
	Input         FinanceReceiptRecognizeInput `json:"input"`
	Result        FinanceReceiptSuggestions    `json:"result"`
}

func (s *Store) FinanceReceiptSource(ctx context.Context, id auth.Identity, attachment string) (FinanceReceiptSource, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return FinanceReceiptSource{}, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return FinanceReceiptSource{}, err
	}
	source, err := financeReceiptSourceTx(ctx, tx, id, scope, attachment, "view")
	if err != nil {
		return FinanceReceiptSource{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FinanceReceiptSource{}, err
	}
	return source, nil
}

func financeReceiptSourceTx(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, attachment, intent string) (FinanceReceiptSource, error) {
	var source FinanceReceiptSource
	if !value.ValidUUID(attachment) {
		return source, ErrNotFound
	}
	stamp, err := financeResourceAccess(ctx, tx, id, scope, "attachment", attachment, intent)
	if err != nil {
		return source, err
	}
	err = tx.QueryRow(ctx, `SELECT a.id,a.storage_key,a.media_type,a.original_name,a.size_bytes,encode(a.sha256,'hex'),a.module_generation,
 (SELECT max(revision) FROM core.financial_artifact_scope_versions v WHERE v.household_id=a.household_id AND v.artifact_kind='attachment' AND v.object_id=a.id)
 FROM core.attachments a WHERE a.household_id=$1 AND a.id=$2 AND a.module_id='expenses' AND a.module_generation=$3 AND a.state='ready'`, id.HouseholdID, attachment, scope.generation).
		Scan(&source.AttachmentID, &source.StorageKey, &source.MediaType, &source.Name, &source.SizeBytes, &source.SHA256, &source.Generation, &source.SourceVersion)
	if err != nil {
		return source, financeNoRows(err)
	}
	if !value.ValidUUID(source.StorageKey) || source.SourceVersion <= 0 || source.SizeBytes < 1 || source.SizeBytes > 10<<20 || len(source.SHA256) != 64 ||
		!slices.Contains([]string{"image/png", "image/jpeg", "application/pdf"}, source.MediaType) {
		return FinanceReceiptSource{}, ErrNotFound
	}
	source.AccessVersion = stamp
	if id.CanWrite() {
		if err = tx.QueryRow(ctx, `SELECT core.finance_resource_visible($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,'attachment',$7,'edit')`,
			id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, attachment).Scan(&source.CanReview); err != nil {
			return FinanceReceiptSource{}, err
		}
	}
	return source, nil
}

func (s *Store) financeReceiptView(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, attachment string) (Record, error) {
	if attachment == "" {
		predicate, args, err := financeResourcePredicate(scope, "attachment", "view", "a", 3)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT a.id,a.original_name,a.media_type,a.size_bytes,CASE WHEN a.resource_type='entries' THEN a.resource_id ELSE NULL END FROM core.attachments a
	WHERE a.household_id=$1 AND a.module_id='expenses' AND a.module_generation=$2 AND a.state='ready' AND a.media_type IN('image/png','image/jpeg','application/pdf') AND `+predicate+
			` ORDER BY a.created_at DESC,a.id LIMIT 51`, append([]any{id.HouseholdID, scope.generation}, args...)...)
		if err != nil {
			return nil, err
		}
		items := []Record{}
		for rows.Next() {
			var rid, name, media string
			var entry *string
			var size int64
			if err = rows.Scan(&rid, &name, &media, &size, &entry); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, Record{"id": rid, "attachment_id": rid, "entry_id": entry, "name": name, "media_type": media, "size_bytes": size})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		more := len(items) > 50
		if more {
			items = items[:50]
		}
		return Record{"rows": items, "more": more, "warnings": []string{}}, nil
	}
	source, err := financeReceiptSourceTx(ctx, tx, id, scope, attachment, "view")
	if err != nil {
		return nil, err
	}
	actions := []string{}
	if source.CanReview {
		actions = []string{"review", "recognize"}
	}
	detail := Record{"attachment": Record{"id": source.AttachmentID, "name": source.Name, "media_type": source.MediaType, "size_bytes": source.SizeBytes}, "can_review": source.CanReview, "available_actions": actions,
		"source_hash": source.SHA256, "source_version": source.SourceVersion, "access_version": source.AccessVersion, "ocr_available": false, "pdf_preview_available": false,
		"page_count": nil, "pages": []Record{}, "capture_job_id": nil, "suggestions": FinanceReceiptSuggestions{RecognizedPages: []int{}, Lines: []FinanceReceiptLine{}, TotalCandidates: []FinanceReceiptTotalCandidate{}}, "reviews": []Record{}}
	if source.MediaType != "application/pdf" {
		detail["page_count"] = 1
		detail["pages"] = []Record{{"number": 1, "url": "/api/v1/expenses/finance/receipts/" + attachment + "/pages/1", "available": true, "reason": ""}}
	}
	predicate, args, err := financeResourcePredicate(scope, "capture", "view", "c", 3)
	if err != nil {
		return nil, err
	}
	var capture string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT c.id,c.extracted_values FROM core.capture_jobs c WHERE c.household_id=$1 AND c.source_attachment_id=$2
 AND c.provider='local-tesseract-receipt-v1' AND c.target_schema='receipt_reviews' AND c.module_id='expenses'
 AND c.extracted_values IS NOT NULL AND `+predicate+` ORDER BY c.created_at DESC,c.id LIMIT 1`, append([]any{id.HouseholdID, attachment}, args...)...).Scan(&capture, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		var stored financeStoredReceiptSuggestions
		if decodeFinancePayload(raw, &stored) != nil || stored.SourceHash != source.SHA256 || stored.SourceVersion != source.SourceVersion {
			return nil, ErrConflict
		}
		detail["capture_job_id"], detail["suggestions"] = capture, stored.Result
		if stored.Result.PageCount != nil {
			detail["page_count"] = *stored.Result.PageCount
		}
	}
	predicate, args, err = financeResourcePredicate(scope, "receipt_reviews", "view", "r", 3)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(r) FROM expenses.receipt_reviews r WHERE r.household_id=$1 AND r.attachment_id=$2 AND `+
		predicate+` ORDER BY r.created_at DESC,r.id DESC LIMIT 51`, append([]any{id.HouseholdID, attachment}, args...)...)
	if err != nil {
		return nil, err
	}
	reviews := []Record{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return nil, err
		}
		review, decodeErr := financeWireRecord(data)
		if decodeErr != nil {
			rows.Close()
			return nil, decodeErr
		}
		reviews = append(reviews, review)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	detail["reviews_more"] = len(reviews) > 50
	if len(reviews) > 50 {
		reviews = reviews[:50]
	}
	for _, review := range reviews {
		review["source_hash"] = financeReceiptHex(review["source_hash"])
		rid := String(review["id"])
		access, accessErr := financeAccessProjection(ctx, tx, id, scope, "receipt_reviews", rid)
		if accessErr != nil {
			return nil, accessErr
		}
		review["access"], review["access_version"] = access, access["access_version"]
		review["available_actions"] = []string{}
		if access["can_edit"] == true && review["state"] == "draft" {
			review["available_actions"] = []string{"review"}
		}
		predicate, lineArgs, lineErr := financeResourcePredicate(scope, "receipt_review_lines", "view", "l", 3)
		if lineErr != nil {
			return nil, lineErr
		}
		lineRows, lineErr := financeSQLRecords(ctx, tx, `SELECT to_jsonb(l) FROM expenses.receipt_review_lines l WHERE l.household_id=$1 AND l.review_id=$2 AND `+predicate+` ORDER BY l.position,l.id LIMIT 101`, append([]any{id.HouseholdID, rid}, lineArgs...), 101)
		if lineErr != nil {
			return nil, lineErr
		}
		if len(lineRows) > 100 {
			return nil, Invalid{"Gespeicherter Beleg überschreitet100Positionen; es wird nichts gekürzt"}
		}
		for _, line := range lineRows {
			if line["amount_minor"] != nil {
				line["amount_minor"] = String(line["amount_minor"])
			}
			if line["confidence"] != nil {
				line["confidence"] = String(line["confidence"])
			}
		}
		review["lines"] = lineRows
	}
	detail["reviews"] = reviews
	return Record{"rows": []Record{}, "detail": detail, "warnings": []string{"OCR schlägt nur Werte vor; vollständige Positionen und Gesamtsumme bleiben ausdrücklich am Original zu prüfen."}}, nil
}

func (s *Store) FinanceSaveReceiptSuggestions(ctx context.Context, id auth.Identity, key, requestID string, source FinanceReceiptSource, input FinanceReceiptRecognizeInput, result FinanceReceiptSuggestions) (Record, error) {
	if !value.ValidUUID(key) || !value.ValidUUID(requestID) || !id.CanWrite() || input.Generation != source.Generation || input.SourceVersion != source.SourceVersion || input.SourceHash != source.SHA256 ||
		len(input.Pages) < 1 || len(input.Pages) > 3 || len(result.Lines) > 100 || len(result.TotalCandidates) > 20 || len(result.RecognizedPages) != len(input.Pages) {
		return nil, Invalid{"Vollständige begrenzte Belegerkennung mit aktueller Quelle erforderlich"}
	}
	if exponent, ok := finance.CurrencyExponent(input.Currency); !ok || exponent != input.Exponent {
		return nil, finance.ErrMoney
	}
	seen := map[int]bool{}
	for _, page := range input.Pages {
		if page < 1 || page > 20 || seen[page] || source.MediaType != "application/pdf" && page != 1 {
			return nil, Invalid{"Höchstens drei eindeutige Belegseiten auswählen"}
		}
		seen[page] = true
	}
	for _, page := range result.RecognizedPages {
		if !seen[page] {
			return nil, Invalid{"Erkennung gehört nicht zur geprüften Seitenauswahl"}
		}
	}
	hash := hex.EncodeToString(financeValueHash([]any{"receipt-recognition-v1", source.AttachmentID, input}))
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "expenses", input.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	current, err := financeReceiptSourceTx(ctx, tx, id, scope, source.AttachmentID, "edit")
	if err != nil {
		return nil, err
	}
	if current.SHA256 != source.SHA256 || current.SourceVersion != source.SourceVersion || current.AccessVersion != source.AccessVersion || current.Generation != source.Generation {
		return nil, ErrConflict
	}
	capture := finance.StableID(key, id.HouseholdID+"/"+id.UserID+"/receipt-recognition")
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "finance-receipt/"+capture); err != nil {
		return nil, err
	}
	var prior []byte
	err = tx.QueryRow(ctx, `SELECT extracted_values FROM core.capture_jobs WHERE household_id=$1 AND id=$2 AND provider=$3`, id.HouseholdID, capture, financeReceiptProvider).Scan(&prior)
	if err == nil {
		if _, err = financeResourceAccess(ctx, tx, id, scope, "capture", capture, "view"); err != nil {
			return nil, err
		}
		var stored financeStoredReceiptSuggestions
		if decodeFinancePayload(prior, &stored) != nil || stored.RequestHash != hash {
			return nil, ErrConflict
		}
		return s.financeReceiptView(ctx, tx, id, scope, source.AttachmentID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	result.RecognitionComplete = false
	stored := financeStoredReceiptSuggestions{RequestHash: hash, SourceHash: source.SHA256, SourceVersion: source.SourceVersion, Input: input, Result: result}
	raw, err := json.Marshal(stored)
	if err != nil || len(raw) > 128<<10 {
		return nil, Invalid{"Belegerkennung enthält zu viele Quellenangaben"}
	}
	op, err := prepareFinanceArtifact(ctx, tx, id, scope, "capture", capture, []FinanceResourceRef{{Kind: "attachment", ID: source.AttachmentID}})
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema,provider,state,extracted_values)
 VALUES($1,$2,'expenses',$3,$4,'receipt_reviews',$5,'review',$6)`, capture, id.HouseholdID, scope.generation, source.AttachmentID, financeReceiptProvider, raw)
	if err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	out, err := s.financeReceiptView(ctx, tx, id, scope, source.AttachmentID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, financeAuthorityError(err)
	}
	return out, nil
}
