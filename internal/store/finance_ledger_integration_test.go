//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/expenseedit"
	"household/internal/finance"
	"household/internal/value"
)

func financeNativeScope(t *testing.T, f *recipeExpansionFixture, resource, rid string, version int64) (int64, string) {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	scope, err := readFinancialScope(f.ctx, tx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := financeProspectiveAccess(f.ctx, tx, f.identity, scope, nil, "view")
	if version > 0 {
		intent := "edit"
		if resource == "filters" {
			intent = "preference"
		}
		stamp, err = financeResourceAccess(f.ctx, tx, f.identity, scope, financeResourceKind(resource), rid, intent)
	}
	if err != nil {
		t.Fatal(err)
	}
	return scope.generation, stamp
}

func financeNativePreview(t *testing.T, f *recipeExpansionFixture, resource, action, rid string, version int64, input any) (FinanceCommand, string, Record) {
	t.Helper()
	generation, stamp := financeNativeScope(t, f, resource, rid, version)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	c := FinanceCommand{Resource: resource, Action: action, ID: rid, Version: version, Generation: generation, AccessVersion: stamp, Payload: raw}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal("real preview failed", resource, action, err)
	}
	c.ReviewHash = String(preview["impact_hash"])
	return c, key, preview
}

func financeNativeApply(t *testing.T, f *recipeExpansionFixture, resource, action, rid string, version int64, input any) Record {
	t.Helper()
	c, key, _ := financeNativePreview(t, f, resource, action, rid, version, input)
	receipt, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal("real apply failed", resource, action, err)
	}
	return receipt
}

func financeNativeCategory(t *testing.T, f *recipeExpansionFixture) string {
	t.Helper()
	id := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO expenses.categories(id,household_id,name,created_by) VALUES($1,$2,$3,$4)`, id, f.identity.HouseholdID, "Finance fixture "+id, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	return id
}

func financeNativeAccount(t *testing.T, f *recipeExpansionFixture, currency string, exponent int) (string, int64) {
	t.Helper()
	id := value.UUID()
	r := financeNativeApply(t, f, "accounts", "save", id, 0, Record{"name": "Testkonto " + id, "account_type": "bank", "currency": currency, "exponent": exponent, "notes": "synthetic native fixture"})
	return id, Int(r["saved_version"])
}

func financeNativeEntry(category, amount string) FinanceEntryInput {
	return FinanceEntryInput{OccurredOn: "2026-10-30", Description: "Native Finanzprüfung", Payee: "Lokale Testquelle", Kind: "expense", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: amount}, Lines: []FinanceEntryLineInput{{ID: value.UUID(), CategoryID: category, OriginalMinor: amount, Note: "Geprüfte Kategorie"}}, Baseline: []expenseedit.LineVersion{}, Removed: []expenseedit.LineVersion{}}
}

func financeNativeMember(t *testing.T, f *recipeExpansionFixture) string {
	t.Helper()
	member, user := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.users(id,username,display_name,password_hash) SELECT $1,$2,'Zweites Testmitglied',password_hash FROM core.users WHERE id=$3`, user, "finance-"+user, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.memberships(id,household_id,user_id,role) VALUES($1,$2,$3,'reader')`, member, f.identity.HouseholdID, user); err != nil {
		t.Fatal(err)
	}
	return member
}

func TestFinanceForwardCompatibilityReadinessAndDriftRefusal(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	var coreReady, auditReady, expensesReady, fuelReady, pantryReady, mealplanReady, planningReady bool
	if err := f.app.QueryRow(f.ctx, `SELECT core.finance_ready(),core.audit_ready(),expenses.finance_ready(),fuel.module_ready(),pantry.prepared_capture_ready(),mealplan.expansion_ready(),core.planning_extensions_ready()`).Scan(&coreReady, &auditReady, &expensesReady, &fuelReady, &pantryReady, &mealplanReady, &planningReady); err != nil || !coreReady || !auditReady || !expensesReady || !fuelReady || !pantryReady || !mealplanReady || !planningReady {
		t.Fatal("current forward compatibility or authority readiness is incomplete", coreReady, auditReady, expensesReady, fuelReady, pantryReady, mealplanReady, planningReady, err)
	}
	for _, tc := range []struct{ name, change, check string }{
		{"expense_guard_disabled", `ALTER TABLE expenses.cash_events DISABLE TRIGGER finance_domain_change`, `SELECT expenses.finance_ready()`},
		{"pantry_guard_disabled", `ALTER TABLE pantry.prepared_lots DISABLE TRIGGER prepared_lot_guard`, `SELECT pantry.prepared_capture_ready()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(tx)
			if _, err = tx.Exec(f.ctx, tc.change); err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, tc.check).Scan(&ready); err != nil || ready {
				t.Fatal("captured trigger drift was accepted", ready, err)
			}
		})
	}
	t.Run("fuel_authority_future_core", func(t *testing.T) {
		tx, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(tx)
		var definition string
		if err = tx.QueryRow(f.ctx, `SELECT pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		fence := fmt.Sprintf("bool_and(version=%d AND NOT dirty)", database.ExpectedVersion("core"))
		future := fmt.Sprintf("bool_and(version=%d AND NOT dirty)", database.ExpectedVersion("core")+1)
		if strings.Count(definition, fence) != 1 {
			t.Fatal("reviewed Fuel fence is not unique")
		}
		if _, err = tx.Exec(f.ctx, strings.Replace(definition, fence, future, 1)); err != nil {
			t.Fatal(err)
		}
		var ready bool
		if err = tx.QueryRow(f.ctx, `SELECT fuel.module_ready()`).Scan(&ready); err != nil || ready {
			t.Fatal("future unreviewed Fuel authority escaped its captured fingerprint", ready, err)
		}
	})
}

func TestFinanceLedgerForeignUnknownZeroAndDurableReplay(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	id := value.UUID()
	in := financeNativeEntry(category, "1")
	in.Money.Currency, in.Money.Exponent = "KWD", 3
	beneficiary, payer := financeNativeMember(t, f), f.identity.MembershipID
	in.PaidByMembershipID = &payer
	in.Split = &finance.SplitInput{Method: "fixed", Participants: []finance.Participant{{MembershipID: beneficiary, Basis: "1"}}}
	saved := financeNativeApply(t, f, "entries", "save", id, 0, in)
	outside, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "entries", Month: "2026-09"})
	if err != nil || len(outside["rows"].([]Record)) != 0 || Int(outside["totals"].(Record)["unknown_draft_count"]) != 0 {
		t.Fatal("visible month filter did not constrain ledger rows and full totals", outside, err)
	}
	var base, lineBase *int64
	var original int64
	if err := f.owner.QueryRow(f.ctx, `SELECT e.amount_minor,l.amount_minor,o.original_minor FROM expenses.entries e JOIN expenses.entry_lines l ON l.household_id=e.household_id AND l.entry_id=e.id JOIN expenses.entry_original_lines o ON o.household_id=l.household_id AND o.line_id=l.id WHERE e.household_id=$1 AND e.id=$2`, f.identity.HouseholdID, id).Scan(&base, &lineBase, &original); err != nil || base != nil || lineBase != nil || original != 1 {
		t.Fatal("unknown foreign draft lost original or invented base", base, lineBase, original, err)
	}
	var lineVersion int64
	if err := f.owner.QueryRow(f.ctx, `SELECT version FROM expenses.entry_lines WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, in.Lines[0].ID).Scan(&lineVersion); err != nil {
		t.Fatal(err)
	}
	in.Lines[0].Version = lineVersion
	in.Baseline = []expenseedit.LineVersion{{ID: in.Lines[0].ID, Version: lineVersion}}
	in.Money.FX = &finance.FXInput{Numerator: "1", Denominator: "1", EffectiveOn: "2026-10-30", Source: "Explizite native Prüfung"}
	c, key, preview := financeNativePreview(t, f, "entries", "post", id, Int(saved["saved_version"]), in)
	if String(preview["impact_hash"]) == "" {
		t.Fatal("no real reviewed impact")
	}
	receipt, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil || replay["replayed"] != true || replay["resource_id"] != receipt["resource_id"] {
		t.Fatal("durable retry did not resolve original acceptance", replay, err)
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT e.amount_minor,l.amount_minor,o.original_minor,(SELECT count(*) FROM expenses.cash_events WHERE household_id=e.household_id AND entry_id=e.id) FROM expenses.entries e JOIN expenses.entry_lines l ON l.household_id=e.household_id AND l.entry_id=e.id JOIN expenses.entry_original_lines o ON o.household_id=l.household_id AND o.line_id=l.id WHERE e.household_id=$1 AND e.id=$2 AND e.state='posted'`, f.identity.HouseholdID, id).Scan(&base, &lineBase, &original, &count); err != nil || base == nil || *base != 0 || lineBase == nil || *lineBase != 0 || original != 1 || count != 1 {
		t.Fatal("genuine subcent posting or once-only cash fact is wrong", base, lineBase, original, count, err)
	}
	people, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "people"})
	if err != nil {
		t.Fatal(err)
	}
	pairs := people["detail"].(Record)["pairs"].([]Record)
	if len(pairs) != 1 || pairs[0]["currency"] != "KWD" || pairs[0]["debt_minor"] != "1" || pairs[0]["debtor"] != beneficiary {
		t.Fatal("zero EUR valuation erased real original-currency debt", pairs)
	}
}

func TestFinanceLedgerCashTransfersAndCompleteReconciliation(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	unassignedID := value.UUID()
	unassigned := FinanceIncomeInput{OccurredOn: "2026-10-30", Description: "Geplante Einnahme vor Kontozuordnung", Payee: "Aufbewahrte Bankgegenpartei", Money: finance.MoneyInput{Currency: "USD", Exponent: 2, Minor: "100"}, State: "draft"}
	draft := financeNativeApply(t, f, "cash", "income", unassignedID, 0, unassigned)
	listed, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "entries", From: "2026-10-01", To: "2026-10-31"})
	if err != nil {
		t.Fatal(err)
	}
	entryRows := listed["rows"].([]Record)
	if len(entryRows) != 1 || entryRows[0]["id"] != unassignedID || entryRows[0]["resource"] != "cash" || entryRows[0]["payee"] != unassigned.Payee {
		t.Fatal("unassigned income draft is unreachable or loses counterparty", entryRows)
	}
	if _, err = f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "entries", ID: unassignedID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("expense identity fell back to an income with the same untyped UUID", err)
	}
	if _, err = f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "entries", Section: "ledger", ID: unassignedID}); err != nil {
		t.Fatal("typed income identity is unreachable", err)
	}
	from, fromVersion := financeNativeAccount(t, f, "EUR", 2)
	to, toVersion := financeNativeAccount(t, f, "EUR", 2)
	for _, account := range []string{from, to} {
		version := fromVersion
		if account == to {
			version = toVersion
		}
		financeNativeApply(t, f, "cash", "opening", value.UUID(), 0, Record{"occurred_on": "2026-01-01", "description": "Anfangsstand zu Tagesbeginn", "account_id": account, "account_version": version, "native_minor": "0", "fx": nil})
	}
	financeNativeApply(t, f, "cash", "income", value.UUID(), 0, FinanceIncomeInput{OccurredOn: "2026-10-30", Description: "Bestätigte Einnahme", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "1000"}, Payment: &FinancePaymentInput{AccountID: from, AccountVersion: fromVersion, NativeMinor: "1000"}})
	unassigned.State = "posted"
	unassigned.Money.FX = &finance.FXInput{Numerator: "1", Denominator: "1", EffectiveOn: "2026-10-30", Source: "Explizit bestätigter Testkurs"}
	unassigned.Payment = &FinancePaymentInput{AccountID: from, AccountVersion: fromVersion, NativeMinor: "100"}
	financeNativeApply(t, f, "cash", "income", unassignedID, Int(draft["saved_version"]), unassigned)
	financeNativeApply(t, f, "cash", "transfer", value.UUID(), 0, financeCashInput{OccurredOn: "2026-10-30", Description: "Atomare Umbuchung", From: &FinancePaymentInput{AccountID: from, AccountVersion: fromVersion, NativeMinor: "333"}, To: &FinancePaymentInput{AccountID: to, AccountVersion: toVersion, NativeMinor: "333"}})
	for _, amount := range []string{"50", "-20"} {
		financeNativeApply(t, f, "cash", "adjustment", value.UUID(), 0, Record{"occurred_on": "2026-10-30", "description": "Ausdrücklich geprüfte native Anpassung " + amount, "account_id": from, "account_version": fromVersion, "native_minor": amount, "fx": nil})
	}
	jpy, jpyVersion := financeNativeAccount(t, f, "JPY", 0)
	financeNativeApply(t, f, "cash", "opening", value.UUID(), 0, Record{"occurred_on": "2026-01-01", "description": "Expliziter Fremdwährungs-Nullstand ohne erfundenen Kurs", "account_id": jpy, "account_version": jpyVersion, "native_minor": "0", "fx": nil})
	var fromBalance, toBalance, consumed string
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT coalesce(sum(amount_minor),0)::text FROM expenses.account_movements WHERE household_id=$1 AND account_id=$2),(SELECT coalesce(sum(amount_minor),0)::text FROM expenses.account_movements WHERE household_id=$1 AND account_id=$3),(SELECT coalesce(sum(amount_minor),0)::text FROM expenses.entries WHERE household_id=$1 AND state='posted')`, f.identity.HouseholdID, from, to).Scan(&fromBalance, &toBalance, &consumed); err != nil || fromBalance != "797" || toBalance != "333" || consumed != "0" {
		t.Fatal("native cash was counted as consumption or transfer unbalanced", fromBalance, toBalance, consumed, err)
	}
	id := value.UUID()
	confirmed := financeNativeApply(t, f, "reconciliations", "confirm", id, 0, FinanceReconciliationInput{AccountID: from, AccountVersion: fromVersion, Cutoff: "2026-10-30", ExternalMinor: "797", Currency: "EUR", Exponent: 2})
	var difference string
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT difference_minor::text,posting_count FROM expenses.reconciliations WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, id).Scan(&difference, &count); err != nil || difference != "0" || count != 6 {
		t.Fatal("full statement set was not reconciled", difference, count, err)
	}
	periodID, startsOn, openingMinor := value.UUID(), "2026-10-01", "0"
	financeNativeApply(t, f, "reconciliations", "confirm", periodID, 0, FinanceReconciliationInput{AccountID: from, AccountVersion: fromVersion, Cutoff: "2026-10-30", ExternalMinor: "797", Currency: "EUR", Exponent: 2, StartsOn: &startsOn, OpeningMinor: &openingMinor})
	var storedStart, storedOpening string
	if err := f.owner.QueryRow(f.ctx, `SELECT difference_minor::text,posting_count,starts_on::text,opening_minor::text FROM expenses.reconciliations WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, periodID).Scan(&difference, &count, &storedStart, &storedOpening); err != nil || difference != "0" || count != 5 || storedStart != startsOn || storedOpening != openingMinor {
		t.Fatal("external statement period did not exclude historical opening or preserve baseline", difference, count, storedStart, storedOpening, err)
	}
	reopened := financeNativeApply(t, f, "reconciliations", "reopen", id, Int(confirmed["saved_version"]), Record{"reason": "Neue manuelle Prüfung"})
	var originalState, newState string
	if err := f.owner.QueryRow(f.ctx, `SELECT original.state,reopened.state FROM expenses.reconciliations original JOIN expenses.reconciliations reopened ON reopened.household_id=original.household_id AND reopened.reopens_id=original.id WHERE original.household_id=$1 AND original.id=$2 AND reopened.id=$3`, f.identity.HouseholdID, id, reopened["resource_id"]).Scan(&originalState, &newState); err != nil || originalState != "confirmed" || newState != "reopened" {
		t.Fatal("reopening rewrote historical reconciliation", originalState, newState, err)
	}
}

func TestFinanceLedgerPersonsSettlementRefundAndCorrectionPreserveFacts(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	member := financeNativeMember(t, f)
	in := financeNativeEntry(category, "100")
	payer := f.identity.MembershipID
	in.PaidByMembershipID = &payer
	in.Split = &finance.SplitInput{Method: "equal", Participants: []finance.Participant{{MembershipID: payer, Basis: "1"}, {MembershipID: member, Basis: "1"}}}
	originalID := value.UUID()
	posted := financeNativeApply(t, f, "entries", "post", originalID, 0, in)
	view, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "people"})
	if err != nil {
		t.Fatal(err)
	}
	pairs := view["detail"].(Record)["pairs"].([]Record)
	if len(pairs) != 1 || pairs[0]["debtor"] != member || pairs[0]["creditor"] != payer || pairs[0]["debt_minor"] != "50" {
		t.Fatal("reviewed person obligation incorrect", pairs)
	}
	sources := pairs[0]["reviewed_sources"].([]FinanceSourceVersion)
	firstSettlement, secondSettlement := value.UUID(), value.UUID()
	financeNativeApply(t, f, "cash", "settle", firstSettlement, 0, FinanceSettlementInput{OccurredOn: "2026-10-30", Description: "Tatsächlich bestätigter Teilausgleich ohne Kontozuordnung", Debtor: member, Creditor: payer, DebtCurrency: "EUR", DebtExponent: 2, DebtMinor: "20", ReviewedSources: sources})
	view, err = f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "people"})
	if err != nil {
		t.Fatal(err)
	}
	pairs = view["detail"].(Record)["pairs"].([]Record)
	if len(pairs) != 1 || pairs[0]["debt_minor"] != "30" || len(pairs[0]["cash_sources"].([]Record)) != 1 {
		t.Fatal("prior partial settlement is missing from the current reviewed debt basis", pairs)
	}
	financeNativeApply(t, f, "cash", "settle", secondSettlement, 0, FinanceSettlementInput{OccurredOn: "2026-10-30", Description: "Restzahlung mit erhaltener erster Zahlungsherkunft", Debtor: member, Creditor: payer, DebtCurrency: "EUR", DebtExponent: 2, DebtMinor: "30", ReviewedSources: pairs[0]["reviewed_sources"].([]FinanceSourceVersion)})
	var retainedPrior bool
	if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM expenses.settlement_details s CROSS JOIN LATERAL jsonb_array_elements(s.reviewed_sources) source WHERE s.household_id=$1 AND s.event_id=$2 AND source->>'event_id'=$3)`, f.identity.HouseholdID, secondSettlement, firstSettlement).Scan(&retainedPrior); err != nil || !retainedPrior {
		t.Fatal("later settlement lost the earlier cash authority dependency", retainedPrior, err)
	}
	refund := financeNativeEntry(category, "-100")
	refund.Kind, refund.PaidByMembershipID, refund.Split = "refund", &payer, in.Split
	financeNativeApply(t, f, "entries", "refund", value.UUID(), 0, Record{"original_entry_id": originalID, "original_version": posted["saved_version"], "entry": refund})
	replacement := financeNativeEntry(category, "120")
	replacement.PaidByMembershipID, replacement.Split = &payer, in.Split
	financeNativeApply(t, f, "entries", "correct", value.UUID(), 0, Record{"original_entry_id": originalID, "original_version": posted["saved_version"], "reversal_id": value.UUID(), "entry": replacement})
	var originalAmount, consumed string
	var reversals, payments int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT amount_minor::text FROM expenses.entries WHERE household_id=$1 AND id=$2),(SELECT coalesce(sum(amount_minor),0)::text FROM expenses.entries WHERE household_id=$1 AND state='posted'),(SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND reverses_entry_id=$2),(SELECT count(*) FROM expenses.settlement_details WHERE household_id=$1)`, f.identity.HouseholdID, originalID).Scan(&originalAmount, &consumed, &reversals, &payments); err != nil || originalAmount != "100" || consumed != "20" || reversals != 1 || payments != 2 {
		t.Fatal("correction changed original/payment or counted settlement as spend", originalAmount, consumed, reversals, payments, err)
	}
}

func TestFinanceLedgerLateCashFailureRollsBackProofAndAllEffects(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	account, version := financeNativeAccount(t, f, "EUR", 2)
	id := value.UUID()
	in := FinanceIncomeInput{OccurredOn: "2026-10-30", Description: "Bewusst später Fehler", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "500"}, Payment: &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "500"}}
	c, key, _ := financeNativePreview(t, f, "cash", "income", id, 0, in)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION public.finance_native_late_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled late financial fault' USING ERRCODE='23514';END $$; CREATE CONSTRAINT TRIGGER finance_native_late_failure AFTER INSERT ON expenses.account_movements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.finance_native_late_failure()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.owner.Exec(ctx, `DROP TRIGGER IF EXISTS finance_native_late_failure ON expenses.account_movements; DROP FUNCTION IF EXISTS public.finance_native_late_failure()`); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c); err == nil {
		t.Fatal("controlled deferred failure accepted")
	}
	var cash, moves, proofs int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.cash_events WHERE household_id=$1 AND id=$2),(SELECT count(*) FROM expenses.account_movements WHERE household_id=$1 AND event_id=$2),(SELECT count(*) FROM core.finance_operations WHERE household_id=$1 AND request_key=$3)`, f.identity.HouseholdID, id, key).Scan(&cash, &moves, &proofs); err != nil || cash != 0 || moves != 0 || proofs != 0 {
		t.Fatal("late failure leaked a fact or receipt", cash, moves, proofs, err)
	}
	if _, err := f.store.FinanceOperation(f.ctx, f.identity, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed operation has a durable acceptance", err)
	}
}
