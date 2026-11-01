package store

import (
	"context"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

func financeSaveEntryContext(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string, money finance.Money, payment *FinancePaymentInput, paid finance.Money) error {
	n, d, on, source := financeRateValues(money.FX)
	var account, paymentCurrency, paymentExponent, paymentMinor, paymentBase any
	var pn, pd, pon, ps any
	reviewed := false
	if payment != nil {
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM expenses.entries WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID).Scan(&kind); err != nil {
			return err
		}
		direction := int64(-1)
		if kind == "refund" {
			direction = 1
		}
		account, paymentCurrency, paymentExponent, paymentMinor = payment.AccountID, paid.Currency, paid.Exponent, paid.Minor*direction
		if paid.BaseMinor != nil {
			paymentBase = *paid.BaseMinor * direction
		}
		pn, pd, pon, ps = financeRateValues(paid.FX)
		reviewed = payment.DifferenceReviewed
	}
	_, err := tx.Exec(ctx, `INSERT INTO expenses.entry_finance(id,household_id,entry_id,original_state,original_currency,original_exponent,original_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,fx_reviewed_by,fx_reviewed_at,account_id,payment_currency,payment_exponent,payment_minor,payment_fx_numerator,payment_fx_denominator,payment_fx_on,payment_fx_source,payment_base_minor,payment_difference_reviewed,created_by)
 VALUES($1,$2,$1,'known',$3,$4,$5,$6,$7,$8,$9,CASE WHEN $6::numeric IS NOT NULL THEN $21::uuid ELSE NULL END,CASE WHEN $6::numeric IS NOT NULL THEN clock_timestamp() ELSE NULL END,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
 ON CONFLICT(id) DO UPDATE SET original_state='known',original_currency=excluded.original_currency,original_exponent=excluded.original_exponent,original_minor=excluded.original_minor,fx_numerator=excluded.fx_numerator,fx_denominator=excluded.fx_denominator,fx_effective_on=excluded.fx_effective_on,fx_source=excluded.fx_source,fx_reviewed_by=excluded.fx_reviewed_by,fx_reviewed_at=excluded.fx_reviewed_at,account_id=excluded.account_id,payment_currency=excluded.payment_currency,payment_exponent=excluded.payment_exponent,payment_minor=excluded.payment_minor,payment_fx_numerator=excluded.payment_fx_numerator,payment_fx_denominator=excluded.payment_fx_denominator,payment_fx_on=excluded.payment_fx_on,payment_fx_source=excluded.payment_fx_source,payment_base_minor=excluded.payment_base_minor,payment_difference_reviewed=excluded.payment_difference_reviewed`, entryID, id.HouseholdID, money.Currency, money.Exponent, money.Minor, n, d, on, source, account, paymentCurrency, paymentExponent, paymentMinor, pn, pd, pon, ps, paymentBase, reviewed, id.UserID, id.UserID)
	return err
}

func financeSaveEntryShares(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string, parentVersion int64, money finance.Money, in FinanceEntryInput, shares []finance.Share) error {
	if _, err := tx.Exec(ctx, `DELETE FROM expenses.entry_person_shares WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, entryID); err != nil {
		return err
	}
	if in.Split == nil {
		_, err := tx.Exec(ctx, `UPDATE expenses.entry_split_sets SET active=false WHERE household_id=$1 AND entry_id=$2 AND active`, id.HouseholdID, entryID)
		return err
	}
	setID := finance.StableID(entryID, "split")
	basis, err := financeRaw(in.Split.Participants)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_split_sets(id,household_id,entry_id,method,currency_code,currency_exponent,total_minor,payer_membership_id,basis,reviewed_by,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) ON CONFLICT(id) DO UPDATE SET method=excluded.method,currency_code=excluded.currency_code,currency_exponent=excluded.currency_exponent,total_minor=excluded.total_minor,payer_membership_id=excluded.payer_membership_id,basis=excluded.basis,reviewed_by=excluded.reviewed_by,reviewed_at=clock_timestamp(),active=true`, setID, id.HouseholdID, entryID, in.Split.Method, money.Currency, money.Exponent, money.Minor, in.PaidByMembershipID, basis, id.UserID)
	if err != nil {
		return err
	}
	nonzero := []finance.LineAmount{}
	for _, share := range shares {
		minor, parseErr := finance.ParseMinor(share.Minor)
		if parseErr != nil {
			return parseErr
		}
		if minor != 0 {
			nonzero = append(nonzero, finance.LineAmount{ID: share.MembershipID, Minor: minor})
		}
	}
	bases := map[string]int64{}
	if money.BaseMinor != nil {
		converted, convertErr := finance.ConvertLines(nonzero, money.Currency, money.Exponent, money.FX)
		if convertErr != nil {
			return convertErr
		}
		for i, line := range nonzero {
			bases[line.ID] = converted[i]
		}
	}
	for i, share := range shares {
		var base any
		if money.BaseMinor != nil {
			base = bases[share.MembershipID]
		}
		shareID := finance.StableID(entryID, "share/"+share.MembershipID+"/"+strconv.FormatInt(parentVersion, 10))
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_person_shares(id,household_id,split_set_id,entry_id,membership_id,share_minor,base_minor,position,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, shareID, id.HouseholdID, setID, entryID, share.MembershipID, share.Minor, base, i, id.UserID)
		if err != nil {
			return err
		}
	}
	return nil
}

func financePostEntryCash(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string, in FinanceEntryInput, money, paid finance.Money) error {
	eventID := finance.StableID(entryID, "cash")
	n, d, on, source := financeRateValues(money.FX)
	var account any
	var paymentJSON any
	if in.Payment != nil {
		account = in.Payment.AccountID
		encoded, err := financeRaw(in.Payment)
		if err != nil {
			return err
		}
		paymentJSON = encoded
	}
	_, err := tx.Exec(ctx, `INSERT INTO expenses.cash_events(id,household_id,event_kind,occurred_on,description,entry_id,original_currency,original_exponent,original_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,account_id,payment_context,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, eventID, id.HouseholdID, in.Kind, in.OccurredOn, in.Description, entryID, money.Currency, money.Exponent, money.Minor, money.BaseMinor, n, d, on, source, account, paymentJSON, id.UserID)
	if err != nil {
		return err
	}
	if in.Payment != nil {
		direction := int64(-1)
		if in.Kind == "refund" {
			direction = 1
		}
		if err = financeInsertMovement(ctx, tx, id, eventID, 0, in.Payment.AccountID, paid, direction); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE expenses.cash_events SET state='posted',posted_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, eventID)
	return err
}
