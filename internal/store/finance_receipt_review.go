package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

var financeReceiptDecimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,11})(?:[.,][0-9]{1,6})?$`)

func (s *Store) financeReceiptCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var in FinanceReceiptInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if len(in.Lines) > 100 || in.State != "draft" && in.State != "confirmed" || (in.EntryID == nil) != (in.EntryVersion == nil) {
		return nil, Invalid{"Belegprüfung, Buchungsbezug und höchstens100Positionen vollständig prüfen"}
	}
	if exponent, ok := finance.CurrencyExponent(in.Currency); !ok || exponent != in.Exponent {
		return nil, finance.ErrMoney
	}
	encoded, err := json.Marshal(in.Lines)
	if err != nil || len(encoded) > 65536 {
		return nil, Invalid{"Belegpositionen auf64KiBbegrenzen"}
	}
	source, err := financeReceiptSourceTx(ctx, tx, id, scope, in.AttachmentID, "edit")
	if err != nil {
		return nil, err
	}
	if source.SourceVersion != in.SourceVersion || source.SHA256 != in.SourceHash {
		return nil, ErrConflict
	}
	refs := []FinanceResourceRef{{Kind: "attachment", ID: in.AttachmentID}}
	var entryAmount any
	var entryVersion any
	var entryCurrency any
	var entryExponent any
	if in.EntryID != nil {
		if !value.ValidUUID(*in.EntryID) || *in.EntryVersion <= 0 {
			return nil, Invalid{"Aktuelle vollständige Buchungsreferenz erforderlich"}
		}
		if _, err = financeResourceAccess(ctx, tx, id, scope, "entries", *in.EntryID, "edit"); err != nil {
			return nil, err
		}
		e, entryErr := financeLedgerRecord(ctx, tx, id.HouseholdID, "entries", *in.EntryID, true)
		if entryErr != nil {
			return nil, entryErr
		}
		if Int(e["version"]) != *in.EntryVersion {
			return nil, ErrConflict
		}
		var original *string
		var currency *string
		var exponent *int
		if err = tx.QueryRow(ctx, `SELECT original_minor::text,original_currency,original_exponent FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, *in.EntryID).Scan(&original, &currency, &exponent); errors.Is(err, pgx.ErrNoRows) {
			captured := String(e["amount_minor"])
			code := "EUR"
			scale := 2
			original, currency, exponent = &captured, &code, &scale
			err = nil
		}
		if err != nil {
			return nil, err
		}
		if original == nil || currency == nil || exponent == nil || *currency != in.Currency || *exponent != in.Exponent {
			return nil, Invalid{"Beleg und geprüfter Buchungsoriginalbetrag benötigen dieselbe bekannte Währung"}
		}
		amount, amountErr := finance.ParseMinor(*original)
		if amountErr != nil {
			return nil, amountErr
		}
		entryAmount, entryVersion, entryCurrency, entryExponent = amount, *in.EntryVersion, *currency, *exponent
		refs = append(refs, FinanceResourceRef{Kind: "entries", ID: *in.EntryID})
	}
	var captured *financeStoredReceiptSuggestions
	if in.CaptureJobID != nil {
		if !value.ValidUUID(*in.CaptureJobID) {
			return nil, Invalid{"Gültigen Erkennungsnachweis auswählen"}
		}
		if _, err = financeResourceAccess(ctx, tx, id, scope, "capture", *in.CaptureJobID, "view"); err != nil {
			return nil, err
		}
		var data []byte
		err = tx.QueryRow(ctx, `SELECT extracted_values FROM core.capture_jobs WHERE household_id=$1 AND id=$2 AND module_id='expenses'
 AND source_attachment_id=$3 AND provider=$4 AND target_schema='receipt_reviews' AND extracted_values IS NOT NULL`, id.HouseholdID, *in.CaptureJobID, in.AttachmentID, financeReceiptProvider).Scan(&data)
		if err != nil {
			return nil, financeNoRows(err)
		}
		var stored financeStoredReceiptSuggestions
		if decodeFinancePayload(data, &stored) != nil || stored.SourceHash != source.SHA256 || stored.SourceVersion != source.SourceVersion || stored.Input.Currency != in.Currency || stored.Input.Exponent != in.Exponent {
			return nil, ErrConflict
		}
		captured = &stored
		refs = append(refs, FinanceResourceRef{Kind: "capture", ID: *in.CaptureJobID})
	}
	if c.Version > 0 {
		old, readErr := financeSupportRecord(ctx, tx, id, scope, "receipt_reviews", c.ID, true)
		if readErr != nil {
			return nil, readErr
		}
		if old["state"] != "draft" || Int(old["version"]) != c.Version {
			return nil, ErrConflict
		}
	}
	known := len(in.Lines) > 0
	sum := new(big.Int)
	seen := map[string]bool{}
	positions := map[int]bool{}
	for _, line := range in.Lines {
		if !value.ValidUUID(line.ID) || seen[line.ID] || positions[line.Position] || line.Position < 0 || line.Position > 99 || !financeText(line.Label, 0, 300) || !financeText(line.SourceText, 0, 2000) ||
			line.SourceKind != "manual" && line.SourceKind != "ocr" || line.PageNumber < 0 || line.PageNumber > 20 || line.SourceLine < 0 || line.SourceLine > 100000 ||
			source.MediaType != "application/pdf" && line.PageNumber > 1 {
			return nil, Invalid{"Eindeutige begrenzte Belegpositionen mit tatsächlicher Quellenart prüfen"}
		}
		seen[line.ID], positions[line.Position] = true, true
		for _, decimal := range []*string{line.Quantity, line.UnitPrice} {
			if decimal != nil && !financeReceiptDecimal.MatchString(*decimal) {
				return nil, Invalid{"Menge und Einzelpreis als begrenzte exakte Dezimalangaben erfassen"}
			}
		}
		if line.AmountMinor == nil {
			known = false
		} else {
			amount, amountErr := finance.ParseMinor(*line.AmountMinor)
			if amountErr != nil {
				return nil, amountErr
			}
			sum.Add(sum, big.NewInt(amount))
		}
		if line.SourceKind == "manual" {
			if line.Confidence != nil {
				return nil, Invalid{"Manuelle Angaben erhalten keine erfundene Erkennungssicherheit"}
			}
		} else {
			if captured == nil || line.PageNumber < 1 || line.SourceLine < 1 || !financeReceiptEvidence(*captured, line) {
				return nil, Invalid{"OCR-Position stimmt nicht mit dem tatsächlichen gespeicherten Quellenbeleg überein"}
			}
		}
		if in.State == "confirmed" && !line.Reviewed {
			return nil, Invalid{"Jede bestätigte Position ausdrücklich am Original prüfen"}
		}
		if line.EntryLineID != nil {
			if in.EntryID == nil || !value.ValidUUID(*line.EntryLineID) {
				return nil, Invalid{"Kategoriezeile benötigt die zugehörige geprüfte Buchung"}
			}
			if _, err = financeResourceAccess(ctx, tx, id, scope, "entry_lines", *line.EntryLineID, "view"); err != nil {
				return nil, err
			}
			var matches bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entry_lines WHERE household_id=$1 AND id=$2 AND entry_id=$3)`, id.HouseholdID, *line.EntryLineID, *in.EntryID).Scan(&matches); err != nil {
				return nil, err
			}
			if !matches {
				return nil, Invalid{"Kategoriezeile gehört zu einer anderen Buchung"}
			}
			refs = append(refs, FinanceResourceRef{Kind: "entry_lines", ID: *line.EntryLineID})
		}
	}
	var receiptAmount any
	var lineTotal any
	var lineDifference any
	var entryDifference any
	if in.ReceiptTotalMinor != nil {
		amount, amountErr := finance.ParseMinor(*in.ReceiptTotalMinor)
		if amountErr != nil {
			return nil, amountErr
		}
		receiptAmount = amount
	}
	if known {
		lineTotal = sum.String()
		if receiptAmount != nil {
			lineDifference = new(big.Int).Sub(sum, big.NewInt(receiptAmount.(int64))).String()
		}
	}
	if receiptAmount != nil && entryAmount != nil {
		entryDifference = new(big.Int).Sub(big.NewInt(receiptAmount.(int64)), big.NewInt(entryAmount.(int64))).String()
	}
	unresolved := !known || receiptAmount == nil || lineDifference != nil && lineDifference != "0" || entryDifference != nil && entryDifference != "0"
	if in.State == "confirmed" && unresolved && !in.DifferenceAcknowledged {
		return nil, Invalid{"Abweichung oder unbekannte Gesamtsumme ausdrücklich prüfen; sie wird nicht zu null ergänzt"}
	}
	checksum, err := hex.DecodeString(source.SHA256)
	if err != nil {
		return nil, err
	}
	columns := []string{"entry_id", "capture_job_id", "attachment_id", "source_version", "source_hash", "currency_code", "currency_exponent", "receipt_total_minor", "line_total_minor", "difference_minor", "state", "difference_acknowledged", "reviewed_entry_version", "reviewed_entry_original_minor", "reviewed_entry_currency", "reviewed_entry_exponent", "entry_difference_minor"}
	values := []any{in.EntryID, in.CaptureJobID, in.AttachmentID, in.SourceVersion, checksum, in.Currency, in.Exponent, receiptAmount, lineTotal, lineDifference, "draft", in.DifferenceAcknowledged, entryVersion, entryAmount, entryCurrency, entryExponent, entryDifference}
	return financeSaveSupport(ctx, tx, id, scope, c, "receipt_reviews", columns, values, refs, in, func(_ Record) error {
		rows, readErr := tx.Query(ctx, `SELECT id FROM expenses.receipt_review_lines WHERE household_id=$1 AND review_id=$2 ORDER BY id LIMIT 101 FOR UPDATE NOWAIT`, id.HouseholdID, c.ID)
		if readErr != nil {
			return readErr
		}
		current := map[string]bool{}
		for rows.Next() {
			var rid string
			if readErr = rows.Scan(&rid); readErr != nil {
				rows.Close()
				return readErr
			}
			current[rid] = true
		}
		readErr = rows.Err()
		rows.Close()
		if readErr != nil {
			return readErr
		}
		if len(current) > 100 {
			return Invalid{"Gespeicherter Prüfentwurf enthält mehr als100Positionen"}
		}
		for rid := range current {
			if !seen[rid] {
				if _, readErr = tx.Exec(ctx, `DELETE FROM expenses.receipt_review_lines WHERE household_id=$1 AND review_id=$2 AND id=$3`, id.HouseholdID, c.ID, rid); readErr != nil {
					return readErr
				}
			}
		}
		for _, line := range in.Lines {
			var amount any
			if line.AmountMinor != nil {
				parsed, parseErr := finance.ParseMinor(*line.AmountMinor)
				if parseErr != nil {
					return parseErr
				}
				amount = parsed
			}
			var page any
			var sourceLine any
			if line.PageNumber > 0 {
				page = line.PageNumber
			}
			if line.SourceLine > 0 {
				sourceLine = line.SourceLine
			}
			args := []any{line.ID, id.HouseholdID, c.ID, line.Position, line.Label, line.Quantity, line.UnitPrice, amount, line.SourceText, line.SourceKind, page, sourceLine, line.Confidence, line.EntryLineID, line.Reviewed, id.UserID}
			statement := `INSERT INTO expenses.receipt_review_lines(id,household_id,review_id,position,label,quantity,unit_price,amount_minor,source_text,source_kind,page_number,source_line,confidence,entry_line_id,reviewed,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
			if current[line.ID] {
				statement = `UPDATE expenses.receipt_review_lines SET position=$4,label=$5,quantity=$6,unit_price=$7,amount_minor=$8,source_text=$9,source_kind=$10,page_number=$11,source_line=$12,confidence=$13,entry_line_id=$14,reviewed=$15,changed_by=$16
 WHERE id=$1 AND household_id=$2 AND review_id=$3`
			}
			if _, readErr = tx.Exec(ctx, statement, args...); readErr != nil {
				return readErr
			}
		}
		if in.State == "confirmed" {
			_, readErr = tx.Exec(ctx, `UPDATE expenses.receipt_reviews SET state='confirmed',confirmed_by=$3,confirmed_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, c.ID, id.UserID)
		}
		return readErr
	})
}

func financeReceiptEvidence(stored financeStoredReceiptSuggestions, line FinanceReceiptLine) bool {
	for _, source := range stored.Result.Lines {
		if source.PageNumber != line.PageNumber || source.SourceLine != line.SourceLine {
			continue
		}
		return source.SourceText == line.SourceText && source.Label == line.Label && financeReceiptOptionalEqual(source.AmountMinor, line.AmountMinor) &&
			financeReceiptOptionalEqual(source.Confidence, line.Confidence) && financeReceiptOptionalEqual(source.Quantity, line.Quantity) && financeReceiptOptionalEqual(source.UnitPrice, line.UnitPrice)
	}
	return false
}

func financeReceiptOptionalEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func financeReceiptHex(value any) string { return strings.TrimPrefix(String(value), "\\x") }
