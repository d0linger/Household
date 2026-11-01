package store

import (
	"context"
	"errors"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func financeAccountCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	var in struct {
		Name        string `json:"name"`
		AccountType string `json:"account_type"`
		Currency    string `json:"currency"`
		Exponent    int    `json:"exponent"`
		Notes       string `json:"notes"`
	}
	refs := []FinanceResourceRef{}
	if c.Version > 0 {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: c.ID})
		if _, err := financeResourceAccess(ctx, tx, id, scope, "accounts", c.ID, "edit"); err != nil {
			return nil, err
		}
	}
	if c.Action == "archive" {
		if err := decodeFinancePayload(c.Payload, &struct{}{}); err != nil {
			return nil, err
		}
		if c.Version <= 0 {
			return nil, ErrConflict
		}
	} else {
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		exponent, ok := finance.CurrencyExponent(in.Currency)
		if !ok || exponent != in.Exponent || !financeText(in.Name, 1, 100) || !financeText(in.Notes, 0, 1000) ||
			in.AccountType != "cash" && in.AccountType != "bank" && in.AccountType != "card" && in.AccountType != "other" {
			return nil, Invalid{"Kontoname, Typ und feste Währung prüfen"}
		}
	}
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "accounts", c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	var version int64
	if c.Action == "archive" {
		err = tx.QueryRow(ctx, `UPDATE expenses.accounts SET archived_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 AND archived_at IS NULL RETURNING version`, id.HouseholdID, c.ID, c.Version).Scan(&version)
	} else if c.Version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO expenses.accounts(id,household_id,name,account_type,currency_code,currency_exponent,notes,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING version`, c.ID, id.HouseholdID, in.Name, in.AccountType, in.Currency, in.Exponent, in.Notes, id.UserID).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE expenses.accounts SET name=$4,account_type=$5,currency_code=$6,currency_exponent=$7,notes=$8 WHERE household_id=$1 AND id=$2 AND version=$3 AND archived_at IS NULL RETURNING version`, id.HouseholdID, c.ID, c.Version, in.Name, in.AccountType, in.Currency, in.Exponent, in.Notes).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "accounts", c.ID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return financeLedgerResult("accounts", c.ID, version, Record{"account_id": c.ID, "previous_version": c.Version, "source_access_version": sourceStamp, "name": in.Name, "currency": in.Currency, "exponent": in.Exponent, "action": c.Action}), nil
}

// financeWriteIncome creates or posts the same reviewable cash-income identity.
func financeWriteIncome(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, eventID string, expectedVersion int64, in FinanceIncomeInput) (Record, error) {
	if in.State == "" {
		in.State = "posted"
	}
	if !value.ValidUUID(eventID) || expectedVersion < 0 || in.State != "draft" && in.State != "posted" ||
		!finance.CivilDate(in.OccurredOn) || !financeText(in.Description, 1, 300) || !financeText(in.Payee, 0, 200) {
		return nil, Invalid{"Einnahme mit Datum, Beschreibung und Zustand prüfen"}
	}
	money, err := finance.NormalizeMoney(in.Money, false, in.State == "draft")
	if err != nil || money.Minor <= 0 || in.State == "posted" && in.Payment == nil {
		return nil, Invalid{"Positive Einnahme und tatsächliches Zielkonto angeben"}
	}
	refs := []FinanceResourceRef{}
	if in.Payment != nil {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: in.Payment.AccountID})
	}
	if expectedVersion > 0 {
		refs = append(refs, FinanceResourceRef{Kind: "cash_events", ID: eventID})
	}
	if _, err = financeProspectiveAccess(ctx, tx, id, scope, refs, "edit"); err != nil {
		return nil, err
	}
	var paymentMoney finance.Money
	if in.Payment != nil {
		_, paymentMoney, err = financeAccountForPayment(ctx, tx, id, scope, in.Payment, in.State == "draft")
		if err != nil {
			return nil, err
		}
	}
	if in.Payment != nil && money.BaseMinor != nil && paymentMoney.BaseMinor != nil && *money.BaseMinor != *paymentMoney.BaseMinor && !in.Payment.DifferenceReviewed {
		return nil, Invalid{"Abweichung zwischen Einnahmen- und Zahlungsbewertung ausdrücklich prüfen"}
	}
	if expectedVersion > 0 {
		current, readErr := financeLedgerRecord(ctx, tx, id.HouseholdID, "cash_events", eventID, true)
		if readErr != nil {
			return nil, readErr
		}
		if Int(current["version"]) != expectedVersion || current["state"] != "draft" || current["event_kind"] != "income" {
			return nil, ErrConflict
		}
	}
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "cash_events", eventID, origin, sources)
	if err != nil {
		return nil, err
	}
	n, d, on, source := financeRateValues(money.FX)
	var accountArg, paymentArg any
	if in.Payment != nil {
		accountArg = in.Payment.AccountID
		paymentArg, err = financeRaw(in.Payment)
		if err != nil {
			return nil, err
		}
	}
	var version int64
	if expectedVersion == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO expenses.cash_events(id,household_id,event_kind,occurred_on,description,original_currency,original_exponent,original_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,account_id,payment_context,payee,created_by) VALUES($1,$2,'income',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING version`, eventID, id.HouseholdID, in.OccurredOn, in.Description, money.Currency, money.Exponent, money.Minor, money.BaseMinor, n, d, on, source, accountArg, paymentArg, in.Payee, id.UserID).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE expenses.cash_events SET occurred_on=$4,description=$5,payee=$16,original_currency=$6,original_exponent=$7,original_minor=$8,base_minor=$9,fx_numerator=$10,fx_denominator=$11,fx_effective_on=$12,fx_source=$13,account_id=$14,payment_context=$15 WHERE household_id=$1 AND id=$2 AND version=$3 AND state='draft' RETURNING version`, id.HouseholdID, eventID, expectedVersion, in.OccurredOn, in.Description, money.Currency, money.Exponent, money.Minor, money.BaseMinor, n, d, on, source, accountArg, paymentArg, in.Payee).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	if in.State == "posted" {
		if err = financeInsertMovement(ctx, tx, id, eventID, 0, in.Payment.AccountID, paymentMoney, 1); err != nil {
			return nil, err
		}
		if err = tx.QueryRow(ctx, `UPDATE expenses.cash_events SET state='posted',posted_at=clock_timestamp() WHERE household_id=$1 AND id=$2 RETURNING version`, id.HouseholdID, eventID).Scan(&version); err != nil {
			return nil, err
		}
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "cash_events", eventID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return financeLedgerResult("cash", eventID, version, Record{"event_kind": "income", "state": in.State, "payee": in.Payee, "source_access_version": sourceStamp, "money": in.Money, "base_minor": financeMinorValue(money.BaseMinor), "payment": in.Payment, "payment_base_minor": financeMinorValue(paymentMoney.BaseMinor), "previous_version": expectedVersion}), nil
}

func financeInsertMovement(ctx context.Context, tx pgx.Tx, id auth.Identity, eventID string, leg int, accountID string, money finance.Money, direction int64) error {
	if money.BaseMinor == nil {
		return Invalid{"Kontobewegung braucht einen bestätigten Kurs"}
	}
	n, d, on, source := financeRateValues(money.FX)
	_, err := tx.Exec(ctx, `INSERT INTO expenses.account_movements(id,household_id,event_id,account_id,leg,currency_code,currency_exponent,amount_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, finance.StableID(eventID, "movement/"+strconv.Itoa(leg)), id.HouseholdID, eventID, accountID, leg, money.Currency, money.Exponent, money.Minor*direction, *money.BaseMinor*direction, n, d, on, source, id.UserID)
	return financeAuthorityError(err)
}
