package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financeBankStoredProfile struct {
	Profile FinanceBankProfileInput `json:"profile"`
}

func (s *Store) FinanceBankPreview(ctx context.Context, id auth.Identity, key, requestID, account, profile string, generation int64, raw []byte) (Record, error) {
	if !value.ValidUUID(key) || !value.ValidUUID(requestID) || !value.ValidUUID(account) || !value.ValidUUID(profile) || len(raw) > 1<<20 || len(raw) == 0 {
		return nil, Invalid{"Konto, Profil, Vorgang und begrenzte Bankdatei prüfen"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "expenses", generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "finance-key-v1/"+id.HouseholdID+id.UserID+key); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	hash := financeValueHash([]any{"bank-preview-v1", account, profile, generation, hex.EncodeToString(digest[:])})
	prior, priorHash, err := financeReadOperation(ctx, tx, id, scope, key)
	if err == nil {
		if !bytes.Equal(hash, priorHash) || prior["action"] != "bank.preview" {
			return nil, ErrConflict
		}
		out, readErr := financeBankReviewTx(ctx, tx, id, scope, String(prior["resource_id"]), false)
		if readErr == nil {
			delete(out, "stored")
		}
		return out, readErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	p, err := financeSupportRecord(ctx, tx, id, scope, "bank_profiles", profile, true)
	if err != nil {
		return nil, err
	}
	if p["archived_at"] != nil || String(p["account_id"]) != account {
		return nil, ErrNotFound
	}
	if _, err = financeResourceAccess(ctx, tx, id, scope, "accounts", account, "edit"); err != nil {
		return nil, err
	}
	a, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "accounts", account, true)
	if err != nil {
		return nil, err
	}
	if a["archived_at"] != nil {
		return nil, ErrNotFound
	}
	profileInput, err := financeBankProfileFromRecord(p)
	if err != nil {
		return nil, err
	}
	rows, sourceHash, err := financeParseBankCSV(raw, profileInput, account, String(a["currency_code"]), int(Int(a["currency_exponent"])))
	if err != nil {
		return nil, err
	}
	valid, invalid := 0, 0
	for index := range rows {
		row := &rows[index]
		if row.Error == "" && !row.Duplicate {
			duplicate, checkErr := financeIngestStatus(ctx, tx, id, scope, financeBankSourceKind(*row), row.Fingerprint)
			if errors.Is(checkErr, ErrNotFound) || errors.Is(checkErr, ErrForbidden) {
				row.Error = "Diese Quelle ist derzeit nicht verfügbar"
			} else if checkErr != nil {
				return nil, checkErr
			} else {
				row.Duplicate = duplicate
			}
		}
		if row.Error != "" {
			invalid++
		} else {
			valid++
		}
	}
	batch := finance.StableID(key, id.HouseholdID+"/"+id.UserID+"/bank-preview")
	op, err := prepareFinanceArtifact(ctx, tx, id, scope, "import_batch", batch, []FinanceResourceRef{{Kind: "accounts", ID: account}, {Kind: "bank_profiles", ID: profile}})
	if err != nil {
		return nil, err
	}
	stored, err := json.Marshal(financeBankStoredProfile{Profile: profileInput})
	if err != nil {
		return nil, err
	}
	expires := scope.readAt.Add(30 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO core.import_batches(id,household_id,module_id,module_generation,entity,purpose,row_count,valid_count,error_count,
 created_by,version,expires_at,source_hash,profile_id,profile_version,account_id,account_version,review_data)
 VALUES($1,$2,'expenses',$3,'bank','finance_bank',$4,$5,$6,$7,1,$8,$9,$10,$11,$12,$13,$14)`, batch, id.HouseholdID, generation, len(rows), valid, invalid, id.UserID, expires, digest[:], profile, Int(p["version"]), account, Int(a["version"]), stored)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	for _, row := range rows {
		normalized, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return nil, marshalErr
		}
		issues := []string{}
		if row.Error != "" {
			issues = append(issues, row.Error)
		}
		issueBytes, marshalErr := json.Marshal(issues)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO core.import_rows(batch_id,row_number,normalized_values,validation_errors) VALUES($1,$2,$3,$4)`, batch, row.RowNumber, normalized, issueBytes); err != nil {
			return nil, err
		}
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	if _, err = financeStoreOperation(ctx, tx, id, scope, key, "bank.preview", hash, "import_batch", batch, 1); err != nil {
		return nil, err
	}
	out, err := financeBankReviewTx(ctx, tx, id, scope, batch, false)
	if err != nil {
		return nil, err
	}
	out["source_hash"] = sourceHash
	delete(out, "stored")
	if err = tx.Commit(ctx); err != nil {
		return nil, financeAuthorityError(err)
	}
	return out, nil
}

func (s *Store) FinanceBankReview(ctx context.Context, id auth.Identity, batch string) (Record, error) {
	if !value.ValidUUID(batch) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	out, err := financeBankReviewTx(ctx, tx, id, scope, batch, false)
	if err != nil {
		return nil, err
	}
	delete(out, "stored")
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func financeBankReviewTx(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, batch string, lock bool) (Record, error) {
	stamp, err := financeResourceAccess(ctx, tx, id, scope, "import_batch", batch, "view")
	if err != nil {
		return nil, err
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE NOWAIT"
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT to_jsonb(b) FROM core.import_batches b WHERE household_id=$1 AND id=$2
 AND module_id='expenses' AND entity='bank' AND purpose='finance_bank' AND module_generation=$3 AND created_by=$4`+suffix, id.HouseholdID, batch, scope.generation, id.UserID).Scan(&raw)
	if err != nil {
		return nil, financeNoRows(err)
	}
	b, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(b["row_count"]) < 1 || Int(b["row_count"]) > 500 || Int(b["version"]) <= 0 {
		return nil, Invalid{"Gespeicherte Bankvorschau überschreitet die zulässigen Grenzen"}
	}
	if b["state"] != "preview" {
		return nil, Invalid{"Bankvorschau ist bereits abgeschlossen; aktuellen Vorgangsbeleg prüfen"}
	}
	expires, err := time.Parse(time.RFC3339Nano, String(b["expires_at"]))
	if err != nil || !expires.After(scope.readAt) {
		return nil, Invalid{"Bankvorschau ist abgelaufen; Quelldatei neu prüfen"}
	}
	rows, err := tx.Query(ctx, `SELECT row_number,normalized_values FROM core.import_rows WHERE batch_id=$1 ORDER BY row_number LIMIT 501`, batch)
	if err != nil {
		return nil, err
	}
	items := []FinanceBankRow{}
	for rows.Next() {
		var number int
		var normalized []byte
		if err = rows.Scan(&number, &normalized); err != nil {
			rows.Close()
			return nil, err
		}
		var row FinanceBankRow
		if decodeFinancePayload(normalized, &row) != nil || row.RowNumber != number || number != len(items)+1 || len(normalized) > 16384 {
			rows.Close()
			return nil, Invalid{"Gespeicherte Bankzeile ist unvollständig oder widersprüchlich"}
		}
		items = append(items, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) != int(Int(b["row_count"])) || len(items) > 500 {
		return nil, Invalid{"Bankvorschau enthält keine vollständige Zeilenbasis"}
	}
	var sourceHash string
	if err = tx.QueryRow(ctx, "SELECT encode(source_hash,'hex') FROM core.import_batches WHERE id=$1", batch).Scan(&sourceHash); err != nil {
		return nil, err
	}
	return Record{"id": batch, "version": b["version"], "generation": scope.generation, "access_version": stamp, "expires_at": expires,
		"account_id": b["account_id"], "profile_id": b["profile_id"], "source_hash": sourceHash, "rows": items, "warnings": []string{"Alle importierten Zeilen werden prüfbare Entwürfe. Ähnliche Zahlungen sind keine sichere Dublettenidentität."}, "stored": b}, nil
}

func (s *Store) FinanceBankConfirm(ctx context.Context, id auth.Identity, batch, key, requestID string, input FinanceBankConfirmInput) (Record, error) {
	if !value.ValidUUID(batch) || !value.ValidUUID(key) || !value.ValidUUID(requestID) || input.Generation <= 0 || input.Version <= 0 || len(input.AccessVersion) != 64 || len(input.Decisions) < 1 || len(input.Decisions) > 500 {
		return nil, Invalid{"Vollständigen begrenzten Bankprüfauftrag bestätigen"}
	}
	input.Decisions = append([]FinanceBankDecision{}, input.Decisions...)
	slices.SortFunc(input.Decisions, func(a, b FinanceBankDecision) int { return a.RowNumber - b.RowNumber })
	hash := financeValueHash([]any{"bank-confirm-v1", batch, input})
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
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "finance-key-v1/"+id.HouseholdID+id.UserID+key); err != nil {
		return nil, err
	}
	prior, priorHash, err := financeReadOperation(ctx, tx, id, scope, key)
	if err == nil {
		if !bytes.Equal(hash, priorHash) || prior["action"] != "bank.confirm" {
			return nil, ErrConflict
		}
		prior["replayed"] = true
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	view, err := financeBankReviewTx(ctx, tx, id, scope, batch, true)
	if err != nil {
		return nil, err
	}
	if Int(view["version"]) != input.Version || String(view["access_version"]) != input.AccessVersion {
		return nil, ErrConflict
	}
	b, _ := view["stored"].(Record)
	account, profile := String(b["account_id"]), String(b["profile_id"])
	if _, err = financeResourceAccess(ctx, tx, id, scope, "accounts", account, "edit"); err != nil {
		return nil, err
	}
	a, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "accounts", account, true)
	if err != nil {
		return nil, err
	}
	p, err := financeSupportRecord(ctx, tx, id, scope, "bank_profiles", profile, true)
	if err != nil {
		return nil, err
	}
	if Int(a["version"]) != Int(b["account_version"]) || Int(p["version"]) != Int(b["profile_version"]) || a["archived_at"] != nil || p["archived_at"] != nil {
		return nil, ErrConflict
	}
	bankRows, _ := view["rows"].([]FinanceBankRow)
	if len(input.Decisions) != len(bankRows) {
		return nil, Invalid{"Jede Bankzeile ausdrücklich übernehmen oder überspringen"}
	}
	op, err := prepareFinanceArtifact(ctx, tx, id, scope, "import_batch", batch, []FinanceResourceRef{{Kind: "accounts", ID: account}, {Kind: "bank_profiles", ID: profile}})
	if err != nil {
		return nil, err
	}
	for index, decision := range input.Decisions {
		row := bankRows[index]
		if decision.RowNumber != row.RowNumber || decision.Fingerprint != row.Fingerprint || !slices.Contains([]string{"skip", "import"}, decision.Decision) {
			return nil, Invalid{"Bankzeilen wurden verändert oder nicht vollständig entschieden"}
		}
		if decision.Decision == "skip" {
			if decision.CategoryID != nil {
				return nil, Invalid{"Übersprungene Bankzeile darf keine Kategorie übernehmen"}
			}
			if _, err = tx.Exec(ctx, "UPDATE core.import_rows SET state='skipped' WHERE batch_id=$1 AND row_number=$2", batch, row.RowNumber); err != nil {
				return nil, err
			}
			continue
		}
		if row.Error != "" || row.Duplicate {
			return nil, Invalid{"Fehlerhafte oder bereits beanspruchte Bankzeile kann nicht übernommen werden"}
		}
		amount, amountErr := finance.ParseMinor(row.AmountMinor)
		if amountErr != nil || amount == 0 {
			return nil, Invalid{"Gespeicherten Bankbetrag erneut prüfen"}
		}
		target := finance.StableID(batch, "bank-row/"+strconv.Itoa(row.RowNumber))
		kind := "entries"
		if amount > 0 {
			kind = "cash_events"
		}
		if err = financeClaimIngest(ctx, tx, id, scope, financeBankSourceKind(row), row.Fingerprint, kind, target); err != nil {
			return nil, err
		}
		producerCtx := withFinanceOrigin(ctx, "bank", []FinanceResourceRef{{Kind: "import_row", ID: batch, RowNumber: int64(row.RowNumber)}, {Kind: "accounts", ID: account}})
		absolute := amount
		if absolute < 0 {
			absolute = -absolute
		}
		payment := &FinancePaymentInput{AccountID: account, AccountVersion: Int(a["version"]), NativeMinor: strconv.FormatInt(absolute, 10)}
		money := finance.MoneyInput{Currency: row.Currency, Exponent: row.Exponent, Minor: strconv.FormatInt(absolute, 10)}
		if amount < 0 {
			lines := []FinanceEntryLineInput{}
			if decision.CategoryID != nil {
				if err = financeCategory(ctx, tx, id.HouseholdID, *decision.CategoryID); err != nil {
					return nil, err
				}
				lines = append(lines, FinanceEntryLineInput{ID: finance.StableID(target, "bank-category"), CategoryID: *decision.CategoryID, OriginalMinor: money.Minor})
			}
			_, err = financeWriteEntry(producerCtx, tx, id, scope, target, 0, "save", FinanceEntryInput{OccurredOn: row.OccurredOn, Description: row.Description, Payee: row.Payee, Kind: "expense", Money: money, Payment: payment, Lines: lines, Baseline: []expenseedit.LineVersion{}, Removed: []expenseedit.LineVersion{}})
		} else {
			if decision.CategoryID != nil {
				return nil, Invalid{"Einnahmeentwurf wird keiner Verbrauchskategorie zugeordnet"}
			}
			_, err = financeWriteIncome(producerCtx, tx, id, scope, target, 0, FinanceIncomeInput{OccurredOn: row.OccurredOn, Description: row.Description, Payee: row.Payee, Money: money, Payment: payment, State: "draft"})
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "UPDATE core.import_rows SET state='confirmed',result_resource_id=$3 WHERE batch_id=$1 AND row_number=$2", batch, row.RowNumber, target); err != nil {
			return nil, err
		}
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE core.import_batches SET state='confirmed',confirmed_by=$3,confirmed_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2 AND version=$4 RETURNING version`, id.HouseholdID, batch, id.UserID, input.Version).Scan(&version)
	if err != nil {
		return nil, financeNoRows(err)
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	receipt, err := financeStoreOperation(ctx, tx, id, scope, key, "bank.confirm", hash, "import_batch", batch, version)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, financeAuthorityError(err)
	}
	return receipt, nil
}

func financeBankProfileFromRecord(record Record) (FinanceBankProfileInput, error) {
	in := FinanceBankProfileInput{Name: String(record["name"]), AccountID: String(record["account_id"]), Delimiter: String(record["delimiter"]), Encoding: String(record["encoding"]), DateLayout: String(record["date_layout"]), DecimalSeparator: String(record["decimal_separator"]), GroupSeparator: String(record["group_separator"]), AmountMode: String(record["amount_mode"])}
	raw, err := json.Marshal(record["field_map"])
	if err != nil {
		return in, err
	}
	if err = decodeFinancePayload(raw, &in.FieldMap); err != nil {
		return in, err
	}
	return in, validateFinanceBankProfile(in)
}

func financeValueHash(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	hash := sha256.Sum256(raw)
	return hash[:]
}

func financeStoreOperation(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, key, operation string, hash []byte, kind, rid string, version int64) (Record, error) {
	if len(hash) != 32 || !value.ValidUUID(key) || !value.ValidUUID(rid) || version <= 0 {
		return nil, ErrConflict
	}
	op := value.UUID()
	var accepted time.Time
	err := tx.QueryRow(ctx, `INSERT INTO core.finance_operations(id,household_id,created_by,membership_id,request_key,purpose,operation,request_hash,expenses_generation,resource_type,resource_id,result_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at`, op, id.HouseholdID, id.UserID, id.MembershipID, key, financePurpose, operation, hash, scope.generation, kind, rid, version).Scan(&accepted)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return Record{"id": op, "request_key": key, "generation": scope.generation, "resource": kind, "action": operation, "resource_id": rid, "saved_version": version, "accepted_at": accepted, "replayed": false, "current_exists": true, "current_version": version, "current_state": "confirmed"}, nil
}
