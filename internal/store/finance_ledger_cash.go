package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financeCashInput struct {
	OccurredOn  string               `json:"occurred_on"`
	Description string               `json:"description"`
	From        *FinancePaymentInput `json:"from"`
	To          *FinancePaymentInput `json:"to"`
}

type FinanceSettlementInput struct {
	OccurredOn      string                 `json:"occurred_on"`
	Description     string                 `json:"description"`
	Debtor          string                 `json:"debtor"`
	Creditor        string                 `json:"creditor"`
	DebtCurrency    string                 `json:"debt_currency"`
	DebtExponent    int                    `json:"debt_exponent"`
	DebtMinor       string                 `json:"debt_minor"`
	DebtFX          *finance.FXInput       `json:"debt_fx"`
	FromPayment     *FinancePaymentInput   `json:"from_payment"`
	ToPayment       *FinancePaymentInput   `json:"to_payment"`
	ReviewedSources []FinanceSourceVersion `json:"reviewed_sources"`
}

func financeCashCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "income" {
		var in FinanceIncomeInput
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		return financeWriteIncome(ctx, tx, id, scope, c.ID, c.Version, in)
	}
	if c.Version != 0 {
		return nil, ErrConflict
	}
	if c.Action == "opening" || c.Action == "adjustment" {
		return financeOpening(ctx, tx, id, scope, c)
	}
	if c.Action == "settle" {
		return financeSettlement(ctx, tx, id, scope, c)
	}
	var in financeCashInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if c.Action != "transfer" || !finance.CivilDate(in.OccurredOn) || !financeText(in.Description, 1, 300) || in.From == nil || in.To == nil || in.From.AccountID == in.To.AccountID {
		return nil, Invalid{"Umbuchung zwischen zwei verschiedenen Konten prüfen"}
	}
	refs := []FinanceResourceRef{{Kind: "accounts", ID: in.From.AccountID}, {Kind: "accounts", ID: in.To.AccountID}}
	if _, err := financeProspectiveAccess(ctx, tx, id, scope, refs, "edit"); err != nil {
		return nil, err
	}
	if err := financeLockAccounts(ctx, tx, id, []string{in.From.AccountID, in.To.AccountID}); err != nil {
		return nil, err
	}
	_, from, err := financeAccountForPayment(ctx, tx, id, scope, in.From, false)
	if err != nil {
		return nil, err
	}
	_, to, err := financeAccountForPayment(ctx, tx, id, scope, in.To, false)
	if err != nil {
		return nil, err
	}
	if from.Currency == to.Currency && from.Minor != to.Minor {
		return nil, Invalid{"Gleichwährungs-Transfer braucht gleiche native Beträge; Gebühren separat erfassen"}
	}
	if *from.BaseMinor != *to.BaseMinor && (from.Currency == to.Currency || !in.From.DifferenceReviewed && !in.To.DifferenceReviewed) {
		return nil, Invalid{"Umbuchungsbewertung weicht ab; Fremdwährungsdifferenz ausdrücklich prüfen"}
	}
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "cash_events", c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	if err = financeCreateCashEvent(ctx, tx, id, c.ID, "transfer", in.OccurredOn, in.Description, from, in.From.AccountID); err != nil {
		return nil, err
	}
	if err = financeInsertMovement(ctx, tx, id, c.ID, 0, in.From.AccountID, from, -1); err != nil {
		return nil, err
	}
	if err = financeInsertMovement(ctx, tx, id, c.ID, 1, in.To.AccountID, to, 1); err != nil {
		return nil, err
	}
	version, err := financeFinishCash(ctx, tx, id, scope, c.ID, op)
	if err != nil {
		return nil, err
	}
	return financeLedgerResult("cash", c.ID, version, Record{"event_kind": "transfer", "source_access_version": sourceStamp, "date": in.OccurredOn, "description": in.Description, "from": in.From, "to": in.To, "from_base_minor": financeMinorValue(from.BaseMinor), "to_base_minor": financeMinorValue(to.BaseMinor), "consumption_minor": "0"}), nil
}

func financeLockAccounts(ctx context.Context, tx pgx.Tx, id auth.Identity, accounts []string) error {
	slices.Sort(accounts)
	for _, account := range slices.Compact(accounts) {
		if !value.ValidUUID(account) {
			return ErrNotFound
		}
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM expenses.accounts WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT`, id.HouseholdID, account).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return financeAuthorityError(err)
		}
	}
	return nil
}

func financeOpening(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var in struct {
		OccurredOn     string           `json:"occurred_on"`
		Description    string           `json:"description"`
		AccountID      string           `json:"account_id"`
		AccountVersion int64            `json:"account_version"`
		NativeMinor    string           `json:"native_minor"`
		FX             *finance.FXInput `json:"fx"`
	}
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if !finance.CivilDate(in.OccurredOn) || !financeText(in.Description, 1, 300) {
		return nil, Invalid{"Anfangsdatum und Erklärung prüfen"}
	}
	if _, err := financeResourceAccess(ctx, tx, id, scope, "accounts", in.AccountID, "edit"); err != nil {
		return nil, err
	}
	account, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "accounts", in.AccountID, true)
	if err != nil {
		return nil, err
	}
	if Int(account["version"]) != in.AccountVersion || account["archived_at"] != nil {
		return nil, ErrConflict
	}
	money, err := finance.NormalizeMoney(finance.MoneyInput{Currency: String(account["currency_code"]), Exponent: int(Int(account["currency_exponent"])), Minor: in.NativeMinor, FX: in.FX}, c.Action == "opening", false)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.cash_events WHERE household_id=$1 AND account_id=$2 AND event_kind='opening' AND state='posted')`, id.HouseholdID, in.AccountID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists && c.Action == "opening" {
		return nil, Invalid{"Ein bestätigter Anfangsstand ist bereits vorhanden"}
	}
	origin, sources := financeOriginFor(ctx, []FinanceResourceRef{{Kind: "accounts", ID: in.AccountID}})
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, []FinanceResourceRef{{Kind: "accounts", ID: in.AccountID}})
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "cash_events", c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	if err = financeCreateCashEvent(ctx, tx, id, c.ID, c.Action, in.OccurredOn, in.Description, money, in.AccountID); err != nil {
		return nil, err
	}
	if err = financeInsertMovement(ctx, tx, id, c.ID, 0, in.AccountID, money, 1); err != nil {
		return nil, err
	}
	version, err := financeFinishCash(ctx, tx, id, scope, c.ID, op)
	if err != nil {
		return nil, err
	}
	return financeLedgerResult("cash", c.ID, version, Record{"event_kind": c.Action, "source_access_version": sourceStamp, "account_id": in.AccountID, "account_version": in.AccountVersion, "date": in.OccurredOn, "native_minor": in.NativeMinor, "fx": in.FX, "base_minor": financeMinorValue(money.BaseMinor), "consumption_minor": "0"}), nil
}

func financeCreateCashEvent(ctx context.Context, tx pgx.Tx, id auth.Identity, eventID, kind, on, description string, money finance.Money, accountID string) error {
	n, d, date, source := financeRateValues(money.FX)
	var account any
	if accountID != "" {
		account = accountID
	}
	_, err := tx.Exec(ctx, `INSERT INTO expenses.cash_events(id,household_id,event_kind,occurred_on,description,original_currency,original_exponent,original_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,account_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, eventID, id.HouseholdID, kind, on, description, money.Currency, money.Exponent, money.Minor, money.BaseMinor, n, d, date, source, account, id.UserID)
	return err
}

func financeFinishCash(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, eventID, op string) (int64, error) {
	var version int64
	if err := tx.QueryRow(ctx, `UPDATE expenses.cash_events SET state='posted',posted_at=clock_timestamp() WHERE household_id=$1 AND id=$2 RETURNING version`, id.HouseholdID, eventID).Scan(&version); err != nil {
		return 0, err
	}
	if err := financeProjectLedger(ctx, tx, id, scope, "cash_events", eventID); err != nil {
		return 0, err
	}
	if err := completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return 0, err
	}
	return version, nil
}

func financeSettlement(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var in FinanceSettlementInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if !finance.CivilDate(in.OccurredOn) || !financeText(in.Description, 1, 300) || in.Debtor == in.Creditor || len(in.ReviewedSources) == 0 || len(in.ReviewedSources) > 1000 {
		return nil, Invalid{"Aktuellen Ausgleich zwischen zwei Personen prüfen"}
	}
	for _, member := range []string{in.Debtor, in.Creditor} {
		if err := financeMemberSelection(ctx, tx, id, member, false); err != nil {
			return nil, err
		}
	}
	money, err := finance.NormalizeMoney(finance.MoneyInput{Currency: in.DebtCurrency, Exponent: in.DebtExponent, Minor: in.DebtMinor, FX: in.DebtFX}, false, false)
	if err != nil || money.Minor <= 0 {
		return nil, Invalid{"Positiven Ausgleich in der Schuldenwährung prüfen"}
	}
	priorPairs, _, err := financeDebtProjection(ctx, tx, id, scope)
	if err != nil {
		return nil, err
	}
	var priorPair Record
	for _, p := range priorPairs {
		if p["debtor"] == in.Debtor && p["creditor"] == in.Creditor && p["currency"] == in.DebtCurrency {
			priorPair = p
		}
	}
	if priorPair == nil {
		return nil, ErrConflict
	}
	refs := make([]FinanceResourceRef, 0, len(in.ReviewedSources)+2)
	for _, source := range in.ReviewedSources {
		refs = append(refs, FinanceResourceRef{Kind: "entries", ID: source.EntryID})
	}
	for _, source := range priorPair["cash_sources"].([]Record) {
		refs = append(refs, FinanceResourceRef{Kind: "cash_events", ID: String(source["event_id"])})
	}
	accountIDs := []string{}
	for _, payment := range []*FinancePaymentInput{in.FromPayment, in.ToPayment} {
		if payment != nil {
			refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: payment.AccountID})
			accountIDs = append(accountIDs, payment.AccountID)
		}
	}
	if len(accountIDs) == 2 && accountIDs[0] == accountIDs[1] {
		return nil, Invalid{"Ausgleichskonten müssen verschieden sein"}
	}
	if _, err = financeProspectiveAccess(ctx, tx, id, scope, refs, "edit"); err != nil {
		return nil, err
	}
	if err = financeLockAccounts(ctx, tx, id, accountIDs); err != nil {
		return nil, err
	}
	// Serialize person-debt acceptance even when no tracked account is assigned.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.HouseholdID+"/finance/debts"); err != nil {
		return nil, err
	}
	pairs, _, err := financeDebtProjection(ctx, tx, id, scope)
	if err != nil {
		return nil, err
	}
	var pair Record
	for _, p := range pairs {
		if p["debtor"] == in.Debtor && p["creditor"] == in.Creditor && p["currency"] == in.DebtCurrency {
			pair = p
		}
	}
	if pair == nil {
		return nil, ErrConflict
	}
	capacity, err := strconv.ParseInt(String(pair["debt_minor"]), 10, 64)
	if err != nil || money.Minor > capacity {
		return nil, ErrConflict
	}
	claimed, err := json.Marshal(in.ReviewedSources)
	if err != nil {
		return nil, err
	}
	actualEntries, err := json.Marshal(pair["reviewed_sources"])
	if err != nil {
		return nil, err
	}
	priorCash, err := json.Marshal(priorPair["cash_sources"])
	if err != nil {
		return nil, err
	}
	currentCash, err := json.Marshal(pair["cash_sources"])
	if err != nil {
		return nil, err
	}
	if string(claimed) != string(actualEntries) || string(priorCash) != string(currentCash) {
		return nil, ErrConflict
	}
	reviewed := make([]any, 0, len(in.ReviewedSources)+len(pair["cash_sources"].([]Record)))
	for _, source := range in.ReviewedSources {
		reviewed = append(reviewed, source)
	}
	for _, source := range pair["cash_sources"].([]Record) {
		reviewed = append(reviewed, source)
	}
	actual, err := json.Marshal(reviewed)
	if err != nil {
		return nil, err
	}
	payments := make([]finance.Money, 2)
	for i, payment := range []*FinancePaymentInput{in.FromPayment, in.ToPayment} {
		if payment != nil {
			_, payments[i], err = financeAccountForPayment(ctx, tx, id, scope, payment, false)
			if err != nil {
				return nil, err
			}
			if money.BaseMinor != nil && payments[i].BaseMinor != nil && *money.BaseMinor != *payments[i].BaseMinor && !payment.DifferenceReviewed {
				return nil, Invalid{"Abweichende Zahlungsbewertung zum Forderungsausgleich ausdrücklich prüfen"}
			}
		}
	}
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "cash_events", c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	if err = financeCreateCashEvent(ctx, tx, id, c.ID, "settlement", in.OccurredOn, in.Description, money, ""); err != nil {
		return nil, err
	}
	for i, payment := range []*FinancePaymentInput{in.FromPayment, in.ToPayment} {
		if payment != nil {
			direction := int64(-1)
			if i == 1 {
				direction = 1
			}
			if err = financeInsertMovement(ctx, tx, id, c.ID, i, payment.AccountID, payments[i], direction); err != nil {
				return nil, err
			}
		}
	}
	hash := sha256.Sum256(actual)
	if _, err = tx.Exec(ctx, `INSERT INTO expenses.settlement_details(id,household_id,event_id,from_membership_id,to_membership_id,currency_code,currency_exponent,amount_minor,reviewed_sources,reviewed_balance_hash,created_by) VALUES($1,$2,$1,$3,$4,$5,$6,$7,$8,$9,$10)`, c.ID, id.HouseholdID, in.Debtor, in.Creditor, in.DebtCurrency, in.DebtExponent, money.Minor, actual, hash[:], id.UserID); err != nil {
		return nil, err
	}
	version, err := financeFinishCash(ctx, tx, id, scope, c.ID, op)
	if err != nil {
		return nil, err
	}
	return financeLedgerResult("cash", c.ID, version, Record{"event_kind": "settlement", "source_access_version": sourceStamp, "input": in, "reviewed_sources": pair["reviewed_sources"], "reviewed_cash_sources": pair["cash_sources"], "debt_before_minor": pair["debt_minor"], "base_minor": financeMinorValue(money.BaseMinor), "consumption_minor": "0"}), nil
}
