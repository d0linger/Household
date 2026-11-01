package store

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func financeWriteEntry(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, entryID string, expectedVersion int64, action string, in FinanceEntryInput) (Record, error) {
	if !value.ValidUUID(entryID) || expectedVersion < 0 || action != "save" && action != "post" ||
		!finance.CivilDate(in.OccurredOn) || !financeText(in.Description, 1, 300) || !financeText(in.Payee, 0, 200) ||
		in.Kind != "expense" && in.Kind != "refund" || len(in.Lines) > 100 || len(in.Baseline) > 100 || len(in.Removed) > 100 {
		return nil, Invalid{"Vollständige begrenzte Buchung mit Datum und Aktion prüfen"}
	}
	money, err := finance.NormalizeMoney(in.Money, false, action == "save")
	if err != nil || in.Kind == "expense" && money.Minor < 0 || in.Kind == "refund" && money.Minor > 0 {
		return nil, Invalid{"Positive Ausgabe oder negative Erstattung mit Originalwährung prüfen"}
	}
	refs := []FinanceResourceRef{}
	if expectedVersion > 0 {
		refs = append(refs, FinanceResourceRef{Kind: "entries", ID: entryID})
	}
	if in.Payment != nil {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: in.Payment.AccountID})
	}
	if _, err = financeProspectiveAccess(ctx, tx, id, scope, refs, "edit"); err != nil {
		return nil, err
	}
	accounts := []string{}
	if in.Payment != nil {
		accounts = append(accounts, in.Payment.AccountID)
	}
	if expectedVersion > 0 {
		var oldAccount *string
		if err = tx.QueryRow(ctx, `SELECT account_id FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, entryID).Scan(&oldAccount); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if oldAccount != nil {
			accounts = append(accounts, *oldAccount)
		}
	}
	if err = financeLockAccounts(ctx, tx, id, accounts); err != nil {
		return nil, err
	}
	current := Record{}
	if expectedVersion > 0 {
		current, err = financeLedgerRecord(ctx, tx, id.HouseholdID, "entries", entryID, true)
		if err != nil {
			return nil, err
		}
		if Int(current["version"]) != expectedVersion || current["state"] != "draft" {
			return nil, ErrConflict
		}
	}
	var paymentMoney finance.Money
	if in.Payment != nil {
		_, paymentMoney, err = financeAccountForPayment(ctx, tx, id, scope, in.Payment, action == "save")
		if err != nil {
			return nil, err
		}
		if money.BaseMinor != nil && paymentMoney.BaseMinor != nil && absFinance(*money.BaseMinor) != *paymentMoney.BaseMinor && !in.Payment.DifferenceReviewed {
			return nil, Invalid{"Abweichende Zahlungsbewertung ausdrücklich bestätigen"}
		}
	}
	if in.PaidByMembershipID != nil {
		if err = financeMemberSelection(ctx, tx, id, *in.PaidByMembershipID, action == "save" && String(current["paid_by_membership_id"]) == *in.PaidByMembershipID); err != nil {
			return nil, err
		}
	}
	lineAmounts := make([]finance.LineAmount, 0, len(in.Lines))
	draft := expenseedit.Draft{Lines: make([]expenseedit.Line, 0, len(in.Lines))}
	if expectedVersion > 0 {
		draft.EntryID = entryID
	}
	var originalSum int64
	for position, line := range in.Lines {
		amount, parseErr := finance.ParseMinor(line.OriginalMinor)
		if parseErr != nil || amount == 0 || !value.ValidUUID(line.ID) || strings.ToLower(line.ID) != line.ID || !value.ValidUUID(line.CategoryID) || !financeText(line.Note, 0, 300) || line.Version < 0 {
			return nil, Invalid{"Eindeutige Kategoriezeilen mit exakten Originalbeträgen prüfen"}
		}
		var active bool
		err = tx.QueryRow(ctx, `SELECT archived_at IS NULL FROM expenses.categories WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT`, id.HouseholdID, line.CategoryID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, financeAuthorityError(err)
		}
		if !active {
			return nil, Invalid{"Aktive Kategorie ausdrücklich auswählen"}
		}
		lineAmounts = append(lineAmounts, finance.LineAmount{ID: line.ID, Minor: amount})
		oldID := line.ID
		if line.Version == 0 {
			oldID = ""
		}
		draft.Lines = append(draft.Lines, expenseedit.Line{ID: oldID, Key: line.ID, Version: line.Version, Position: position})
		originalSum += amount
	}
	currentLines := []expenseedit.LineVersion{}
	rows, err := tx.Query(ctx, `SELECT id,version FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 ORDER BY id LIMIT 101 FOR UPDATE NOWAIT`, id.HouseholdID, entryID)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	for rows.Next() {
		var line expenseedit.LineVersion
		if err = rows.Scan(&line.ID, &line.Version); err != nil {
			rows.Close()
			return nil, err
		}
		currentLines = append(currentLines, line)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err = expenseedit.ReconcileLines(draft, in.Baseline, in.Removed, currentLines); err != nil {
		if errors.Is(err, expenseedit.ErrStaleLines) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if action == "post" && (len(in.Lines) == 0 || originalSum != money.Minor) {
		return nil, Invalid{"Vor dem Buchen die Originalsumme vollständig aufteilen"}
	}
	if action == "post" {
		if err = financeLockDebts(ctx, tx, id.HouseholdID); err != nil {
			return nil, err
		}
	}
	var bases []int64
	if money.BaseMinor != nil {
		bases, err = finance.ConvertLines(lineAmounts, money.Currency, money.Exponent, money.FX)
		if err != nil {
			return nil, err
		}
	}
	var shares []finance.Share
	if in.Split != nil {
		shares, err = finance.Split(*in.Split, money.Minor)
		if err != nil {
			return nil, err
		}
		for _, participant := range in.Split.Participants {
			if err = financeMemberSelection(ctx, tx, id, participant.MembershipID, false); err != nil {
				return nil, err
			}
		}
	}
	origin, sources := financeOriginFor(ctx, refs)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, refs)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "entries", entryID, origin, sources)
	if err != nil {
		return nil, err
	}
	mode := "legacy_eur"
	if money.Currency != "EUR" {
		mode = "foreign_original"
	}
	if expectedVersion == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,description,payee,amount_minor,kind,paid_by_membership_id,money_mode,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, entryID, id.HouseholdID, in.OccurredOn, in.Description, in.Payee, money.BaseMinor, in.Kind, in.PaidByMembershipID, mode, id.UserID)
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE expenses.entries SET occurred_on=$4,description=$5,payee=$6,amount_minor=$7,kind=$8,paid_by_membership_id=$9,money_mode=$10,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 AND state='draft'`, id.HouseholdID, entryID, expectedVersion, in.OccurredOn, in.Description, in.Payee, money.BaseMinor, in.Kind, in.PaidByMembershipID, mode)
		err = updateErr
		if err == nil && tag.RowsAffected() != 1 {
			return nil, ErrConflict
		}
	}
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	for _, removed := range in.Removed {
		if _, err = tx.Exec(ctx, `DELETE FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 AND id=$3 AND version=$4`, id.HouseholdID, entryID, removed.ID, removed.Version); err != nil {
			return nil, err
		}
	}
	if len(in.Removed) > 0 {
		deletedIDs := make([]string, 0, len(in.Removed))
		for _, removed := range in.Removed {
			deletedIDs = append(deletedIDs, removed.ID)
		}
		if err = cleanupExpenseEditLines(ctx, tx, id, deletedIDs); err != nil {
			return nil, err
		}
	}
	for position, line := range in.Lines {
		var base any
		if bases != nil {
			base = bases[position]
		}
		if line.Version == 0 {
			_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(id,household_id,entry_id,category_id,amount_minor,note,position,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, line.ID, id.HouseholdID, entryID, line.CategoryID, base, line.Note, position, id.UserID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE expenses.entry_lines SET category_id=$4,amount_minor=$5,note=$6,position=$7,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND entry_id=$2 AND id=$3`, id.HouseholdID, entryID, line.ID, line.CategoryID, base, line.Note, position)
		}
		if err != nil {
			return nil, financeAuthorityError(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_original_lines(id,household_id,line_id,entry_id,original_minor,created_by) VALUES($1,$2,$1,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET original_minor=excluded.original_minor WHERE expenses.entry_original_lines.household_id=excluded.household_id AND expenses.entry_original_lines.entry_id=excluded.entry_id`, line.ID, id.HouseholdID, entryID, lineAmounts[position].Minor, id.UserID)
		if err != nil {
			return nil, err
		}
	}
	if err = financeSaveEntryContext(ctx, tx, id, entryID, money, in.Payment, paymentMoney); err != nil {
		return nil, err
	}
	if err = financeSaveEntryShares(ctx, tx, id, entryID, expectedVersion, money, in, shares); err != nil {
		return nil, err
	}
	if action == "post" {
		if err = financePostEntryCash(ctx, tx, id, entryID, in, money, paymentMoney); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID); err != nil {
			return nil, err
		}
	}
	if err = financeSaveRelation(ctx, tx, id, entryID); err != nil {
		return nil, err
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "entries", entryID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM expenses.entries WHERE household_id=$1 AND id=$2`, id.HouseholdID, entryID).Scan(&version); err != nil {
		return nil, err
	}
	impact := Record{"entry_id": entryID, "action": action, "previous_version": expectedVersion, "source_access_version": sourceStamp, "input": in, "base_minor": financeMinorValue(money.BaseMinor), "line_base_minor": bases, "shares": shares, "remaining_original_minor": strconv.FormatInt(money.Minor-originalSum, 10)}
	// Impact money is always exact text, even before the HTTP projection.
	baseStrings := make([]any, len(in.Lines))
	for i, base := range bases {
		baseStrings[i] = strconv.FormatInt(base, 10)
	}
	impact["line_base_minor"] = baseStrings
	return financeLedgerResult("entries", entryID, version, impact), nil
}

func absFinance(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func financeMemberSelection(ctx context.Context, tx pgx.Tx, id auth.Identity, membership string, allowInactive bool) error {
	if !value.ValidUUID(membership) {
		return Invalid{"Gültiges Haushaltsmitglied auswählen"}
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT m.inactive_at IS NULL AND u.disabled_at IS NULL FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.id=$2 FOR SHARE OF m,u NOWAIT`, id.HouseholdID, membership).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return financeAuthorityError(err)
	}
	if !active && !allowInactive {
		return Invalid{"Aktives Haushaltsmitglied auswählen"}
	}
	return nil
}
