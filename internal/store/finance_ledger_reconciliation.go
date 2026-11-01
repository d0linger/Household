package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

type FinanceReconciliationInput struct {
	AccountID            string  `json:"account_id"`
	AccountVersion       int64   `json:"account_version"`
	Cutoff               string  `json:"cutoff"`
	ExternalMinor        string  `json:"external_minor"`
	Currency             string  `json:"currency"`
	Exponent             int     `json:"exponent"`
	StatementFingerprint string  `json:"statement_fingerprint"`
	StartsOn             *string `json:"starts_on,omitempty"`
	OpeningMinor         *string `json:"opening_minor,omitempty"`
}

func financeReconciliationCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "reopen" {
		return financeReopenReconciliation(ctx, tx, id, scope, c)
	}
	if c.Action != "confirm" || c.Version != 0 {
		return nil, ErrConflict
	}
	var in FinanceReconciliationInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	external, err := finance.ParseMinor(in.ExternalMinor)
	if err != nil || !finance.CivilDate(in.Cutoff) {
		return nil, Invalid{"Externen Stichtag und exakten nativen Saldo prüfen"}
	}
	var openingMinor *int64
	if (in.StartsOn == nil) != (in.OpeningMinor == nil) {
		return nil, Invalid{"Zeitraumbeginn und externer Anfangsstand gemeinsam angeben"}
	}
	if in.StartsOn != nil {
		parsed, parseErr := finance.ParseMinor(*in.OpeningMinor)
		if parseErr != nil || !finance.CivilDate(*in.StartsOn) || *in.StartsOn > in.Cutoff {
			return nil, Invalid{"Externen Abstimmungszeitraum und Anfangsstand prüfen"}
		}
		openingMinor = &parsed
	}
	if _, err = financeResourceAccess(ctx, tx, id, scope, "accounts", in.AccountID, "edit"); err != nil {
		return nil, err
	}
	account, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "accounts", in.AccountID, true)
	if err != nil {
		return nil, err
	}
	if Int(account["version"]) != in.AccountVersion || String(account["currency_code"]) != in.Currency || Int(account["currency_exponent"]) != int64(in.Exponent) {
		return nil, ErrConflict
	}
	where := "m.household_id=$1 AND m.account_id=$2 AND e.state='posted' AND e.occurred_on<=$3"
	args := []any{id.HouseholdID, in.AccountID, in.Cutoff}
	if in.StartsOn != nil {
		where += " AND e.occurred_on>=$4::date AND e.event_kind<>'opening'"
		args = append(args, *in.StartsOn)
	}
	predicate, auth, err := financeResourcePredicate(scope, "account_movements", "view", "m", len(args)+1)
	if err != nil {
		return nil, err
	}
	var denied bool
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_or((`+predicate+`) IS NOT TRUE),false) FROM expenses.account_movements m JOIN expenses.cash_events e ON e.household_id=m.household_id AND e.id=m.event_id WHERE `+where, append(append([]any{}, args...), auth...)...).Scan(&count, &denied)
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrForbidden
	}
	if count > 10000 {
		return nil, Invalid{"Vollständige Kontoabstimmung überschreitet die Quellgrenze"}
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',m.id,'event_id',e.id,'event_version',e.version,'movement_version',m.version,'occurred_on',e.occurred_on::text,'event_kind',e.event_kind,'amount_minor',m.amount_minor::text) FROM expenses.account_movements m JOIN expenses.cash_events e ON e.household_id=m.household_id AND e.id=m.event_id WHERE `+where+` ORDER BY e.occurred_on,m.id LIMIT 10001`, args, 10000)
	if err != nil {
		return nil, err
	}
	refs := []FinanceResourceRef{{Kind: "accounts", ID: in.AccountID}}
	total := new(big.Int)
	opening := openingMinor != nil
	if openingMinor != nil {
		total.SetInt64(*openingMinor)
	}
	for _, row := range rows {
		amount, ok := new(big.Int).SetString(String(row["amount_minor"]), 10)
		if !ok {
			return nil, finance.ErrMoney
		}
		total.Add(total, amount)
		opening = opening || row["event_kind"] == "opening"
		refs = append(refs, FinanceResourceRef{Kind: "cash_events", ID: String(row["event_id"])})
	}
	if !opening {
		return nil, Invalid{"Bestätigter Anfangsstand fehlt; sichtbare Bewegungen sind kein Banksaldo"}
	}
	encoded, err := json.Marshal(Record{"account_id": in.AccountID, "account_version": in.AccountVersion, "cutoff": in.Cutoff, "starts_on": in.StartsOn, "opening_minor": in.OpeningMinor, "postings": rows})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	if in.StatementFingerprint != "" && in.StatementFingerprint != fingerprint {
		return nil, ErrConflict
	}
	difference := new(big.Int).Sub(big.NewInt(external), total)
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, fmt.Errorf("bind reconciliation source authority: %w", err)
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "reconciliations", c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.reconciliations(id,household_id,account_id,statement_on,statement_minor,currency_code,currency_exponent,computed_minor,difference_minor,scope_hash,account_version,posting_count,state,created_by,starts_on,opening_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'review',$13,$14,$15)`, c.ID, id.HouseholdID, in.AccountID, in.Cutoff, external, in.Currency, in.Exponent, total.String(), difference.String(), digest[:], in.AccountVersion, len(rows), id.UserID, in.StartsOn, openingMinor)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		_, err = tx.Exec(ctx, `INSERT INTO expenses.reconciliation_items(id,household_id,reconciliation_id,movement_id,reviewed_event_version,reviewed_movement_version,amount_minor,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, finance.StableID(c.ID, "posting/"+String(row["id"])), id.HouseholdID, c.ID, row["id"], Int(row["event_version"]), Int(row["movement_version"]), row["amount_minor"], id.UserID)
		if err != nil {
			return nil, err
		}
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE expenses.reconciliations SET state='confirmed',confirmed_by=$3,confirmed_at=clock_timestamp() WHERE household_id=$1 AND id=$2 RETURNING version`, id.HouseholdID, c.ID, id.UserID).Scan(&version)
	if err != nil {
		return nil, err
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "reconciliations", c.ID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return financeLedgerResult("reconciliations", c.ID, version, Record{"account_id": in.AccountID, "source_access_version": sourceStamp, "account_version": in.AccountVersion, "cutoff": in.Cutoff, "starts_on": in.StartsOn, "opening_minor": in.OpeningMinor, "currency": in.Currency, "exponent": in.Exponent, "external_minor": in.ExternalMinor, "computed_minor": total.String(), "difference_minor": difference.String(), "statement_fingerprint": fingerprint, "posting_count": len(rows), "adjustment_created": false}), nil
}

func financeReopenReconciliation(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if !financeText(in.Reason, 1, 1000) || c.Version <= 0 {
		return nil, Invalid{"Aktuellen Abschluss und Grund prüfen"}
	}
	if _, err := financeResourceAccess(ctx, tx, id, scope, "reconciliations", c.ID, "edit"); err != nil {
		return nil, err
	}
	original, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "reconciliations", c.ID, true)
	if err != nil {
		return nil, err
	}
	if Int(original["version"]) != c.Version || original["state"] != "confirmed" {
		return nil, ErrConflict
	}
	var alreadyReopened bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.reconciliations WHERE household_id=$1 AND reopens_id=$2)`, id.HouseholdID, c.ID).Scan(&alreadyReopened); err != nil {
		return nil, err
	}
	if alreadyReopened {
		return nil, ErrConflict
	}
	newID := finance.StableID(c.ID, "reopen/"+String(original["version"]))
	origin, sources := financeOriginFor(ctx, []FinanceResourceRef{{Kind: "reconciliations", ID: c.ID}})
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "reconciliations", newID, origin, sources)
	if err != nil {
		return nil, err
	}
	var version int64
	err = tx.QueryRow(ctx, `INSERT INTO expenses.reconciliations(id,household_id,account_id,statement_on,statement_minor,currency_code,currency_exponent,computed_minor,difference_minor,scope_hash,account_version,posting_count,state,reopens_id,reopen_reason,created_by,starts_on,opening_minor) SELECT $1,household_id,account_id,statement_on,statement_minor,currency_code,currency_exponent,computed_minor,difference_minor,scope_hash,account_version,posting_count,'reopened',id,$4,$5,starts_on,opening_minor FROM expenses.reconciliations WHERE household_id=$2 AND id=$3 RETURNING version`, newID, id.HouseholdID, c.ID, in.Reason, id.UserID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "reconciliations", newID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return financeLedgerResult("reconciliations", newID, version, Record{"original_id": c.ID, "original_version": c.Version, "reopened_id": newID, "reason": in.Reason, "original_fingerprint": original["scope_hash"], "adjustment_created": false}), nil
}
