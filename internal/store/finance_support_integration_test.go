//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func financeSupportDetail(t *testing.T, f *recipeExpansionFixture, section, rid, month string) Record {
	t.Helper()
	view, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "planning", Section: section, ID: rid, Month: month})
	if err != nil {
		t.Fatal("current support detail failed", section, err)
	}
	detail, ok := view["detail"].(Record)
	if !ok {
		t.Fatal("missing typed support detail", section)
	}
	row, ok := detail["record"].(Record)
	if !ok {
		t.Fatal("missing typed support record", section)
	}
	return row
}

func TestFinanceSupportBudgetCarryUsesActualAuthorizedMonthConsumption(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	for _, fact := range []struct{ on, minor string }{{"2026-09-15", "1600"}, {"2026-10-15", "400"}} {
		entry := financeNativeEntry(category, fact.minor)
		entry.OccurredOn = fact.on
		financeNativeApply(t, f, "entries", "post", value.UUID(), 0, entry)
	}
	for _, tc := range []struct{ rule, octoberCarry, octoberAvailable, novemberAvailable string }{
		{"all", "-500", "100", "1100"},
		{"positive_only", "0", "600", "1600"},
		{"none", "0", "600", "1000"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			rid := value.UUID()
			initial := "100"
			if tc.rule == "none" {
				initial = "0"
			}
			financeNativeApply(t, f, "budgets", "save", rid, 0, FinanceBudgetInput{Name: "Reviewed carry " + tc.rule, CategoryID: category, StartsOn: "2026-09-01", MonthlyMinor: "1000", CarryRule: tc.rule, InitialCarryMinor: initial})
			october := financeSupportDetail(t, f, "budgets", rid, "2026-10")
			if october["spent_minor"] != "400" || october["carry_minor"] != tc.octoberCarry || october["available_minor"] != tc.octoberAvailable || october["scope_complete"] != true {
				t.Fatal("actual signed carry/consumption mismatch", october)
			}
			november := financeSupportDetail(t, f, "budgets", rid, "2026-11")
			if november["available_minor"] != tc.novemberAvailable || november["spent_minor"] != "0" {
				t.Fatal("next month did not carry the actual preceding remainder", november)
			}
		})
	}
}

func TestFinanceSupportGoalSeparatesPlanActualFutureAndAppendedReversal(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	goal := value.UUID()
	financeNativeApply(t, f, "goals", "save", goal, 0, FinanceGoalInput{Name: "Actual savings versus plan", Currency: "EUR", Exponent: 2, TargetMinor: "1000", State: "active"})
	row := financeSupportDetail(t, f, "goals", goal, "")
	today, err := time.Parse(time.DateOnly, String(row["progress_as_of"]))
	if err != nil {
		t.Fatal(err)
	}
	past, future := today.AddDate(0, 0, -7).Format(time.DateOnly), today.AddDate(0, 0, 7).Format(time.DateOnly)
	actual := value.UUID()
	var actualReceipt Record
	for _, fact := range []struct{ rid, on, minor, kind string }{
		{value.UUID(), past, "500", "planned"},
		{actual, past, "300", "manual_recorded"},
		{value.UUID(), future, "700", "manual_recorded"},
	} {
		row = financeSupportDetail(t, f, "goals", goal, "")
		receipt := financeNativeApply(t, f, "contributions", "save", fact.rid, 0, FinanceContributionInput{GoalID: goal, GoalVersion: Int(row["version"]), OccurredOn: fact.on, AmountMinor: fact.minor, ContributionKind: fact.kind, Note: "Explicitly disclosed manual savings fact"})
		if fact.rid == actual {
			actualReceipt = receipt
		}
	}
	row = financeSupportDetail(t, f, "goals", goal, "")
	if row["recorded_minor"] != "300" || row["planned_minor"] != "500" || row["remaining_minor"] != "700" {
		t.Fatal("future/planned contribution was counted as current actual savings", row)
	}
	financeNativeApply(t, f, "contributions", "reverse", value.UUID(), 0, FinanceContributionReverse{OriginalID: actual, OriginalVersion: Int(actualReceipt["saved_version"]), OccurredOn: past, Note: "Append exact manual correction"})
	row = financeSupportDetail(t, f, "goals", goal, "")
	if row["recorded_minor"] != "0" || row["planned_minor"] != "500" || row["remaining_minor"] != "1000" {
		t.Fatal("appended reversal rewrote plan or failed to reverse current actual", row)
	}
	var original, cash int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT amount_minor FROM expenses.goal_contributions WHERE id=$1),(SELECT count(*) FROM expenses.cash_events WHERE household_id=$2)`, actual, f.identity.HouseholdID).Scan(&original, &cash); err != nil || original != 300 || cash != 0 {
		t.Fatal("manual savings rewrote its source or fabricated an account payment", original, cash, err)
	}
}

func TestFinanceSupportRulePriorityExplainHasNoFinancialEffects(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	first, second := "70000000-0000-4000-8000-000000000001", "70000000-0000-4000-8000-000000000002"
	for _, rid := range []string{second, first} {
		financeNativeApply(t, f, "rules", "save", rid, 0, FinanceRuleInput{Name: "Literal rule " + rid, Priority: 5, Active: true, MatchKind: "prefix", PayeePattern: "Market_", CategoryID: category})
	}
	var before, after string
	const census = `SELECT jsonb_build_array((SELECT count(*) FROM core.finance_operations),(SELECT count(*) FROM core.finance_origin_operations),(SELECT count(*) FROM core.audit_log),(SELECT count(*) FROM expenses.entries))::text`
	if err := f.owner.QueryRow(f.ctx, census).Scan(&before); err != nil {
		t.Fatal(err)
	}
	command, key, preview := financeNativePreview(t, f, "rules", "explain", value.UUID(), 0, FinanceRuleExplainInput{Payee: "Market_ reviewed shop", Currency: "EUR", AmountMinor: "123"})
	impact := preview["impact"].(Record)
	winner := impact["winner"].(Record)
	if winner["id"] != first || len(impact["matches"].([]Record)) != 2 || preview["read_only"] != true {
		t.Fatal("priority tie did not use UUID order or explanation is not read-only", preview)
	}
	if _, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err == nil {
		t.Fatal("read-only explanation was accepted as a financial command")
	}
	if err := f.owner.QueryRow(f.ctx, census).Scan(&after); err != nil || before != after {
		t.Fatal("rule explanation persisted a proof, origin, audit or booking", before, after, err)
	}
}

func financeSupportInitialRecurrence(t *testing.T, f *recipeExpansionFixture, rid string, input FinanceRecurrenceInput) Record {
	t.Helper()
	gen, stamp := financeNativeScope(t, f, "recurrences", rid, 0)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	member := f.identity.MembershipID
	c := FinanceCommand{Resource: "recurrences", Action: "save", ID: rid, Generation: gen, AccessVersion: stamp, Payload: raw, InitialAccess: &FinanceInitialAccess{AccessMode: "household", ResponsibleMembershipID: &member, Grants: []FinanceInitialGrant{}}}
	key := value.UUID()
	preview, err := f.store.FinancePreview(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.ReviewHash = String(preview["impact_hash"])
	receipt, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), c)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestFinanceSupportRecurringExpenseIncomeCatchupConsentAndCadence(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	for _, kind := range []string{"expense", "income"} {
		t.Run(kind, func(t *testing.T) {
			rid := value.UUID()
			var template any = financeNativeEntry(category, "100")
			if kind == "income" {
				template = FinanceIncomeInput{OccurredOn: "2026-10-01", Description: "Salary draft without invented account", Payee: "Actual recurring counterparty", Money: finance.MoneyInput{Currency: "USD", Exponent: 2, Minor: "200"}, State: "draft"}
			}
			raw, err := json.Marshal(template)
			if err != nil {
				t.Fatal(err)
			}
			input := FinanceRecurrenceInput{TemplateKind: kind, Name: "Reviewed recurring " + kind, AnchorOn: "2026-10-01", RepeatUnit: "day", RepeatInterval: 1, MonthdayPolicy: "clamp", Template: raw, Active: true, Automatic: true}
			created := financeSupportInitialRecurrence(t, f, rid, input)
			row := financeSupportDetail(t, f, "recurrences", rid, "")
			if row["automatic"] != true || row["automation_reason"] != "enabled" {
				t.Fatal("explicit current responsible-user consent is not effective", row)
			}
			through, expected := "2026-10-03", 3
			if kind == "expense" {
				through, expected = "2026-10-15", 15
			}
			command, key, preview := financeNativePreview(t, f, "recurrences", "generate", rid, Int(created["saved_version"]), FinanceGenerateInput{Through: through})
			firstBatch := preview["impact"].(Record)
			if kind == "expense" && (len(firstBatch["drafts"].([]Record)) != 12 || firstBatch["backlog"] != true) {
				t.Fatal("catchup did not expose the exact twelve-occurrence bound", firstBatch)
			}
			generated, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command)
			if err != nil {
				t.Fatal(err)
			}
			if generated["resource"] != "recurring_occurrences" || !value.ValidUUID(String(generated["resource_id"])) {
				t.Fatal("generation receipt is not bound to an actual occurrence effect", generated)
			}
			if replay, err := f.store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command); err != nil || replay["replayed"] != true {
				t.Fatal("recurrence retry did not resolve its original receipt", replay, err)
			}
			if kind == "expense" {
				financeNativeApply(t, f, "recurrences", "generate", rid, Int(row["version"]), FinanceGenerateInput{Through: through})
			}
			generation, stamp := financeNativeScope(t, f, "recurrences", rid, Int(row["version"]))
			noDue, err := json.Marshal(FinanceGenerateInput{Through: through})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.FinancePreview(f.ctx, f.identity, value.UUID(), value.UUID(), FinanceCommand{Resource: "recurrences", Action: "generate", ID: rid, Generation: generation, Version: Int(row["version"]), AccessVersion: stamp, Payload: noDue}); err == nil {
				t.Fatal("fresh zero-due generation fabricated an accepted effect")
			}
			var occurrences, drafts, posted, native int
			if err = f.owner.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER(WHERE e.state='draft' OR c.state='draft'),count(*) FILTER(WHERE e.state='posted' OR c.state='posted'),(SELECT count(*) FROM expenses.account_movements WHERE household_id=$1) FROM expenses.recurring_occurrences o LEFT JOIN expenses.entries e ON e.household_id=o.household_id AND e.id=o.entry_id LEFT JOIN expenses.cash_events c ON c.household_id=o.household_id AND c.id=o.event_id WHERE o.household_id=$1 AND o.template_id=$2`, f.identity.HouseholdID, rid).Scan(&occurrences, &drafts, &posted, &native); err != nil || occurrences != expected || drafts != expected || posted != 0 || native != 0 {
				t.Fatal("catchup/dedup created missing, duplicate or unreviewed posted effects", occurrences, drafts, posted, native, err)
			}
			financeSupportImmutableOccurrence(t, f, String(generated["resource_id"]), rid)
			input.RepeatInterval = 2
			generation, stamp = financeNativeScope(t, f, "recurrences", rid, Int(row["version"]))
			changed, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.FinancePreview(f.ctx, f.identity, value.UUID(), value.UUID(), FinanceCommand{Resource: "recurrences", Action: "save", ID: rid, Generation: generation, Version: Int(row["version"]), AccessVersion: stamp, Payload: changed}); err == nil {
				t.Fatal("materialized recurrence calendar was reinterpreted")
			}
			financeNativeApply(t, f, "recurrences", "pause", rid, Int(row["version"]), struct{}{})
			row = financeSupportDetail(t, f, "recurrences", rid, "")
			if row["active"] != false || row["automatic"] != false {
				t.Fatal("pause retained automatic generation authority", row)
			}
		})
	}
}

func financeSupportImmutableOccurrence(t *testing.T, f *recipeExpansionFixture, occurrence, template string) {
	t.Helper()
	const snapshot = `SELECT to_jsonb(o)::text FROM expenses.recurring_occurrences o WHERE household_id=$1 AND id=$2`
	var before string
	if err := f.owner.QueryRow(f.ctx, snapshot, f.identity.HouseholdID, occurrence).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, query, message string }{
		{"rewrite_template_snapshot", `UPDATE expenses.recurring_occurrences SET template_version=template_version+1 WHERE household_id=$1 AND id=$2`, "materialized recurrence is immutable"},
		{"unbind_output", `UPDATE expenses.recurring_occurrences SET entry_id=NULL,event_id=NULL,state='proposed' WHERE household_id=$1 AND id=$2`, "financial child parent is immutable"},
		{"delete_occurrence", `DELETE FROM expenses.recurring_occurrences WHERE household_id=$1 AND id=$2`, "materialized recurrence is immutable"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
			tx, err := f.store.BeginMutation(ctx, f.identity, "expenses", f.store.States()["expenses"].Generation)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(tx)
			scope, err := readFinancialScope(ctx, tx, f.identity)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = prepareFinanceOrigin(ctx, tx, f.identity, scope, "recurring_occurrences", occurrence, "recurrence", []FinanceResourceRef{{Kind: "recurring_templates", ID: template}}); err != nil {
				t.Fatal("prepare actual current occurrence operation:", err)
			}
			_, err = tx.Exec(ctx, mutation.query, f.identity.HouseholdID, occurrence)
			if problem, ok := errors.AsType[*pgconn.PgError](err); !ok || problem.Code != "23514" || problem.Message != mutation.message {
				t.Fatal("materialized occurrence did not reach its exact immutability guard", err)
			}
		})
	}
	var after string
	if err := f.owner.QueryRow(f.ctx, snapshot, f.identity.HouseholdID, occurrence).Scan(&after); err != nil || after != before {
		t.Fatal("denied recurrence rewrites changed the original snapshot or output", err)
	}
}

func TestFinanceSupportBankAllRowReviewDraftsDurableDuplicateAndReplay(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	account, _ := financeNativeAccount(t, f, "EUR", 2)
	profile := value.UUID()
	financeNativeApply(t, f, "bank_profiles", "save", profile, 0, FinanceBankProfileInput{Name: "Actual local CSV profile", AccountID: account, Delimiter: ",", Encoding: "utf-8", DateLayout: "iso", DecimalSeparator: ".", AmountMode: "signed", FieldMap: FinanceBankFields{Date: "Date", Amount: "Amount", Currency: "Currency", Payee: "Payee", Description: "Description", ExternalID: "ID"}})
	raw := []byte("Date,Amount,Currency,Payee,Description,ID\n2026-10-01,-1.25,EUR,Market,Groceries,bank-one\n2026-10-02,20.00,EUR,Employer,Salary,bank-two\nnot-a-date,10.00,EUR,Unknown,Invalid date must be skipped,bank-bad\n")
	view, err := f.store.FinanceBankPreview(f.ctx, f.identity, value.UUID(), value.UUID(), account, profile, f.store.States()["expenses"].Generation, raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := view["rows"].([]FinanceBankRow)
	if len(rows) != 3 || rows[0].AmountMinor != "-125" || rows[1].AmountMinor != "2000" || rows[2].Error == "" {
		t.Fatal("bank source rows were guessed, dropped or rounded", rows)
	}
	input := FinanceBankConfirmInput{Generation: Int(view["generation"]), Version: Int(view["version"]), AccessVersion: String(view["access_version"])}
	for _, row := range rows {
		decision := FinanceBankDecision{RowNumber: row.RowNumber, Fingerprint: row.Fingerprint, Decision: "import"}
		if row.RowNumber == 1 {
			decision.CategoryID = &category
		}
		if row.Error != "" {
			decision.Decision = "skip"
		}
		input.Decisions = append(input.Decisions, decision)
	}
	batch := String(view["id"])
	partial := input
	partial.Decisions = partial.Decisions[:2]
	if _, err = f.store.FinanceBankConfirm(f.ctx, f.identity, batch, value.UUID(), value.UUID(), partial); err == nil {
		t.Fatal("unreviewed third bank row was silently ignored")
	}
	key := value.UUID()
	accepted, err := f.store.FinanceBankConfirm(f.ctx, f.identity, batch, key, value.UUID(), input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.store.FinanceBankConfirm(f.ctx, f.identity, batch, key, value.UUID(), input)
	if err != nil || replayed["replayed"] != true || replayed["id"] != accepted["id"] {
		t.Fatal("bank acceptance did not retain its durable receipt", replayed, err)
	}
	var expenses, incomes, posted, movements, claims int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND state='draft'),(SELECT count(*) FROM expenses.cash_events WHERE household_id=$1 AND event_kind='income' AND state='draft'),(SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND state='posted')+(SELECT count(*) FROM expenses.cash_events WHERE household_id=$1 AND state='posted'),(SELECT count(*) FROM expenses.account_movements WHERE household_id=$1),(SELECT count(*) FROM core.finance_ingest_claims WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&expenses, &incomes, &posted, &movements, &claims); err != nil || expenses != 1 || incomes != 1 || posted != 0 || movements != 0 || claims != 2 {
		t.Fatal("bank review posted unapproved money or lost durable source claims", expenses, incomes, posted, movements, claims, err)
	}
	again, err := f.store.FinanceBankPreview(f.ctx, f.identity, value.UUID(), value.UUID(), account, profile, input.Generation, raw)
	if err != nil {
		t.Fatal(err)
	}
	duplicates := again["rows"].([]FinanceBankRow)
	if !duplicates[0].Duplicate || !duplicates[1].Duplicate {
		t.Fatal("new preview lost the permanent source duplicate claims", duplicates)
	}
}

func TestFinanceSupportAutomaticRecurrenceUsesRealLeaseAndWorkerAudit(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	view, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "planning", Section: "recurrences"})
	if err != nil {
		t.Fatal(err)
	}
	today, err := time.Parse(time.DateOnly, String(view["today"]))
	if err != nil {
		t.Fatal(err)
	}
	on, ends := today.AddDate(0, 0, -1).Format(time.DateOnly), today.Format(time.DateOnly)
	template := value.UUID()
	raw, err := json.Marshal(FinanceIncomeInput{OccurredOn: on, Description: "Real worker salary draft", Payee: "Consented recurring counterparty", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "100"}, State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	created := financeSupportInitialRecurrence(t, f, template, FinanceRecurrenceInput{TemplateKind: "income", Name: "Real automatic worker authority", AnchorOn: on, EndsOn: &ends, RepeatUnit: "day", RepeatInterval: 1, MonthdayPolicy: "clamp", Template: raw, Active: true, Automatic: true})
	if err = f.store.ScheduleFinanceRecurrences(f.ctx); err != nil {
		t.Fatal(err)
	}
	// Claim only the job actually scheduled for this exact template, with the
	// ordinary App-role state/availability/attempt/module-generation lease gates.
	// This avoids importing worker (which imports store) or running unrelated jobs.
	lease := value.UUID()
	ctx := audit.WithRequest(audit.WithSystem(f.ctx, "worker"), value.UUID())
	tx, err := audit.Begin(ctx, f.app)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	var job string
	err = tx.QueryRow(ctx, `WITH candidate AS(SELECT j.id FROM core.jobs j WHERE j.household_id=$1 AND j.kind=$2 AND j.payload->>'template_id'=$3 AND j.state='pending' AND j.available_at<=now() AND j.attempts<j.max_attempts AND EXISTS(SELECT 1 FROM core.module_states s WHERE s.household_id=j.household_id AND s.module_id=j.module_id AND s.state='enabled' AND s.data_generation=j.module_generation) ORDER BY j.available_at,j.id LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE core.jobs j SET state='running',lease_token=$4,lease_until=now()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id`, f.identity.HouseholdID, FinanceRecurrenceJobKind, template, lease).Scan(&job)
	if err != nil {
		t.Fatal("actual scheduler did not produce a claimable consented job", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteFinanceRecurrenceJob(ctx, job, lease); err != nil {
		t.Fatal("actual validated transaction worker authority failed", err)
	}
	var occurrences, drafts, movements, workerAudits, falseUserAudits int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.recurring_occurrences WHERE household_id=$1 AND template_id=$2),(SELECT count(*) FROM expenses.cash_events c JOIN expenses.recurring_occurrences o ON o.household_id=c.household_id AND o.event_id=c.id WHERE o.household_id=$1 AND o.template_id=$2 AND c.state='draft'),(SELECT count(*) FROM expenses.account_movements WHERE household_id=$1),(SELECT count(*) FROM core.audit_log a WHERE a.household_id=$1 AND a.module_id='expenses' AND a.resource_type='cash_events' AND a.action='CREATE' AND a.actor_kind='worker' AND a.actor_user_id IS NULL AND a.db_role='household_app'),(SELECT count(*) FROM core.audit_log a WHERE a.household_id=$1 AND a.module_id='expenses' AND a.resource_type='cash_events' AND a.action='CREATE' AND a.actor_kind='user')`, f.identity.HouseholdID, template).Scan(&occurrences, &drafts, &movements, &workerAudits, &falseUserAudits); err != nil || occurrences != 2 || drafts != 2 || movements != 0 || workerAudits != 2 || falseUserAudits != 0 {
		t.Fatal("automatic drafts lost real worker provenance or fabricated payments", occurrences, drafts, movements, workerAudits, falseUserAudits, err)
	}
	if err = f.store.CompleteFinanceRecurrenceJob(ctx, job, lease); err != nil {
		t.Fatal("expired completed lease retry should be an effect-free no-op", err)
	}
	financeNativeApply(t, f, "recurrences", "pause", template, Int(created["saved_version"]), struct{}{})
	current := financeSupportDetail(t, f, "recurrences", template, "")
	if current["automatic"] != false || current["active"] != false {
		t.Fatal("pause left automatic transaction authority enabled", current)
	}
}

func TestFinanceSupportReaderPrivateFiltersDoNotGrantFinancialWrites(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	reader := financeAuthorityMember(t, f, "reader")
	personal := *f
	personal.identity = reader
	rid := value.UUID()
	created := financeNativeApply(t, &personal, "filters", "save", rid, 0, FinanceFilterInput{Name: "Reader private view sentinel", Criteria: FinanceFilter{From: "2026-10-01", To: "2026-10-31"}})
	view, err := personal.store.FinanceView(f.ctx, reader, FinanceQuery{Pane: "planning", Section: "filters"})
	if err != nil || len(view["rows"].([]Record)) != 1 || view["rows"].([]Record)[0]["id"] != rid {
		t.Fatal("Reader cannot maintain their actual personal preference", view, err)
	}
	adminView, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "planning", Section: "filters"})
	if err != nil || len(adminView["rows"].([]Record)) != 0 {
		t.Fatal("another household member saw private filter identity or label", adminView, err)
	}
	gen, stamp := financeNativeScope(t, &personal, "cash", value.UUID(), 0)
	raw, err := json.Marshal(FinanceIncomeInput{OccurredOn: "2026-10-30", Description: "Reader must not create a finance draft", Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "100"}, State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinancePreview(f.ctx, reader, value.UUID(), value.UUID(), FinanceCommand{Resource: "cash", Action: "income", ID: value.UUID(), Generation: gen, AccessVersion: stamp, Payload: raw}); !errors.Is(err, ErrForbidden) {
		t.Fatal("personal-preference capability granted a Reader financial write", err)
	}
	financeNativeApply(t, &personal, "filters", "delete", rid, Int(created["saved_version"]), struct{}{})
}

// This source corruption is representable without removing protected subjects.
// A genuine pending reservation must not excuse another incomplete sibling.
func TestFinanceSupportGoalReversalDoesNotExcuseUnboundSibling(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	goal := value.UUID()
	financeNativeApply(t, f, "goals", "save", goal, 0, FinanceGoalInput{Name: "Strict reversal source closure", Currency: "EUR", Exponent: 2, TargetMinor: "1000", State: "active"})
	original, sibling := value.UUID(), value.UUID()
	var originalReceipt Record
	var today string
	for _, rid := range []string{original, sibling} {
		row := financeSupportDetail(t, f, "goals", goal, "")
		today = String(row["progress_as_of"])
		receipt := financeNativeApply(t, f, "contributions", "save", rid, 0, FinanceContributionInput{GoalID: goal, GoalVersion: Int(row["version"]), OccurredOn: today, AmountMinor: "100", ContributionKind: "manual_recorded", Note: "Actual source fixture"})
		if rid == original {
			originalReceipt = receipt
		}
	}
	if _, err := f.owner.Exec(f.ctx, "DELETE FROM core.finance_origin_resources WHERE household_id=$1 AND resource_type='goal_contributions' AND resource_id=$2", f.identity.HouseholdID, sibling); err != nil {
		t.Fatal(err)
	}
	var before, after string
	const census = "SELECT jsonb_build_array((SELECT count(*) FROM expenses.goal_contributions),(SELECT count(*) FROM core.finance_operations),(SELECT count(*) FROM core.finance_origin_operations),(SELECT count(*) FROM core.audit_log))::text"
	if err := f.owner.QueryRow(f.ctx, census).Scan(&before); err != nil {
		t.Fatal(err)
	}
	target := value.UUID()
	generation, stamp := financeNativeScope(t, f, "contributions", target, 0)
	payload, err := json.Marshal(FinanceContributionReverse{OriginalID: original, OriginalVersion: Int(originalReceipt["saved_version"]), OccurredOn: today, Note: "Never excuse a preexisting missing source"})
	if err != nil {
		t.Fatal(err)
	}
	command := FinanceCommand{Resource: "contributions", Action: "reverse", ID: target, Generation: generation, AccessVersion: stamp, Payload: payload}
	if result, err := f.store.FinancePreview(f.ctx, f.identity, value.UUID(), value.UUID(), command); !errors.Is(err, ErrNotFound) || result != nil {
		t.Fatal("reversal accepted an incomplete existing goal", result, err)
	}
	tx, err := f.store.BeginMutation(f.ctx, f.identity, "expenses", generation)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	scope, err := readFinancialScope(f.ctx, tx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareFinanceOrigin(f.ctx, tx, f.identity, scope, "goal_contributions", target, "reversal", nil); err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct{ kind, id string }{{"savings_goals", goal}, {"goal_contributions", original}} {
		var visible bool
		err = tx.QueryRow(f.ctx, "SELECT core.finance_resource_visible($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,'view')", f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID, f.identity.SessionID, f.identity.AuthVersion, generation, source.kind, source.id).Scan(&visible)
		if err != nil || visible {
			t.Fatal("pending reservation excused a different missing head", source.kind, visible, err)
		}
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.QueryRow(f.ctx, census).Scan(&after); err != nil || before != after {
		t.Fatal("denied reversal retained an effect or reservation", before, after, err)
	}
}

func financeCashflowReviewRows(t *testing.T, f *recipeExpansionFixture, query FinanceQuery) map[string]Record {
	t.Helper()
	view, err := f.store.FinanceView(f.ctx, f.identity, query)
	if err != nil {
		t.Fatal("actual cashflow view", err)
	}
	rows, ok := view["rows"].([]Record)
	if !ok {
		t.Fatal("cashflow rows are not typed records", view)
	}
	out := make(map[string]Record, len(rows))
	for _, row := range rows {
		key := String(row["account_id"]) + "/" + String(row["date"])
		if out[key] != nil {
			t.Fatal("cashflow repeats an account civil day", key)
		}
		out[key] = row
	}
	return out
}

func TestFinanceSupportCashflowSeparatesConfirmedDraftsRecurrenceAndUnknown(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	account, version := financeNativeAccount(t, f, "EUR", 2)
	unknown, unknownVersion := financeNativeAccount(t, f, "EUR", 2)
	financeNativeApply(t, f, "cash", "opening", value.UUID(), 0, Record{
		"occurred_on": "2026-09-30", "description": "Explicit prior opening", "account_id": account,
		"account_version": version, "native_minor": "10000", "fx": nil,
	})
	financeNativeApply(t, f, "cash", "income", value.UUID(), 0, FinanceIncomeInput{
		OccurredOn: "2026-10-01", Description: "Confirmed actual income", Payee: "Actual source", State: "posted",
		Money:   finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "200"},
		Payment: &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "200"},
	})
	draft := financeNativeEntry(category, "300")
	draft.OccurredOn = "2026-10-02"
	draft.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "300"}
	financeNativeApply(t, f, "entries", "save", value.UUID(), 0, draft)
	financeNativeApply(t, f, "cash", "income", value.UUID(), 0, FinanceIncomeInput{
		OccurredOn: "2026-10-01", Description: "Draft on account without opening", Payee: "Unknown opening source", State: "draft",
		Money:   finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "700"},
		Payment: &FinancePaymentInput{AccountID: unknown, AccountVersion: unknownVersion, NativeMinor: "700"},
	})
	template, err := json.Marshal(FinanceIncomeInput{
		OccurredOn: "2026-10-01", Description: "Reviewed recurring income", Payee: "Recurring source", State: "draft",
		Money:   finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "500"},
		Payment: &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "500"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recurrence := value.UUID()
	created := financeNativeApply(t, f, "recurrences", "save", recurrence, 0, FinanceRecurrenceInput{
		TemplateKind: "income", Name: "Cashflow explicit daily income", AnchorOn: "2026-10-01",
		RepeatUnit: "day", RepeatInterval: 1, MonthdayPolicy: "clamp", Template: template,
		AccountID: &account, Active: true,
	})
	financeNativeApply(t, f, "recurrences", "generate", recurrence, Int(created["saved_version"]), FinanceGenerateInput{Through: "2026-10-01"})
	query := FinanceQuery{Pane: "planning", Section: "cashflow", From: "2026-10-01", To: "2026-10-03"}
	const census = `SELECT jsonb_build_array((SELECT count(*) FROM expenses.cash_events),(SELECT count(*) FROM expenses.entries),
 (SELECT count(*) FROM expenses.recurring_occurrences),(SELECT count(*) FROM core.finance_operations),(SELECT count(*) FROM core.audit_log))::text`
	var before, after string
	if err = f.owner.QueryRow(f.ctx, census).Scan(&before); err != nil {
		t.Fatal(err)
	}
	rows := financeCashflowReviewRows(t, f, query)
	if len(rows) != 6 {
		t.Fatal("cashflow omitted an account/day", len(rows))
	}
	for _, expected := range []struct{ date, confirmed, estimated, projected string }{
		{"2026-10-01", "200", "500", "10700"},
		{"2026-10-02", "0", "200", "10900"},
		{"2026-10-03", "0", "500", "11400"},
	} {
		row := rows[account+"/"+expected.date]
		if row["confirmed_minor"] != expected.confirmed || row["estimated_minor"] != expected.estimated ||
			row["projected_minor"] != expected.projected || row["opening_known"] != true {
			t.Fatal("confirmed/draft/template sums or generated-occurrence deduplication differ", expected, row)
		}
		unknownRow := rows[unknown+"/"+expected.date]
		if unknownRow["projected_minor"] != nil || unknownRow["opening_known"] != false || unknownRow["confirmed_minor"] != "0" {
			t.Fatal("cashflow invented an account opening", unknownRow)
		}
	}
	if rows[unknown+"/2026-10-01"]["estimated_minor"] != "700" {
		t.Fatal("unknown opening concealed the explicit draft amount")
	}
	selected := query
	selected.Account = account
	selectedRows := financeCashflowReviewRows(t, f, selected)
	if len(selectedRows) != 3 || selectedRows[account+"/2026-10-03"]["projected_minor"] != "11400" {
		t.Fatal("explicit account selection changed the complete forecast", selectedRows)
	}
	invalid := query
	invalid.Category = category
	var invalidError Invalid
	if result, err := f.store.FinanceView(f.ctx, f.identity, invalid); result != nil || !errors.As(err, &invalidError) {
		t.Fatal("cashflow silently ignored an unsupported category filter", result, err)
	}
	invalid = query
	invalid.To = "2027-10-01"
	if result, err := f.store.FinanceView(f.ctx, f.identity, invalid); result != nil || !errors.As(err, &invalidError) {
		t.Fatal("cashflow silently truncated an excessive horizon", result, err)
	}
	if err = f.owner.QueryRow(f.ctx, census).Scan(&after); err != nil || before != after {
		t.Fatal("read-only forecast changed financial facts or audit", before, after, err)
	}
	current := financeSupportDetail(t, f, "recurrences", recurrence, "")
	financeNativeApply(t, f, "recurrences", "pause", recurrence, Int(current["version"]), struct{}{})
	paused := financeCashflowReviewRows(t, f, selected)
	if paused[account+"/2026-10-01"]["estimated_minor"] != "500" ||
		paused[account+"/2026-10-02"]["estimated_minor"] != "-300" ||
		paused[account+"/2026-10-03"]["projected_minor"] != "10400" {
		t.Fatal("paused template erased an actual draft or retained ungenerated estimates", paused)
	}
}

func TestFinanceSupportCashflowOpeningGuardAndWindowInvariant(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	account, version := financeNativeAccount(t, f, "EUR", 2)
	financeNativeApply(t, f, "cash", "income", value.UUID(), 0, FinanceIncomeInput{
		OccurredOn: "2026-10-01", Description: "Retained actual income", Payee: "Actual source", State: "posted",
		Money:   finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: "200"},
		Payment: &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "200"},
	})
	// The real database already forbids an opening after posted movements.
	// Verify that boundary; do not manufacture an impossible ledger history.
	const census = "SELECT jsonb_build_array((SELECT count(*) FROM expenses.cash_events),(SELECT count(*) FROM expenses.account_movements),(SELECT count(*) FROM core.finance_operations),(SELECT count(*) FROM core.finance_origin_operations),(SELECT count(*) FROM core.finance_origin_effects),(SELECT count(*) FROM core.finance_key_tombstones),(SELECT count(*) FROM core.audit_log))::text"
	var before, after string
	if err := f.owner.QueryRow(f.ctx, census).Scan(&before); err != nil {
		t.Fatal(err)
	}
	target := value.UUID()
	generation, stamp := financeNativeScope(t, f, "cash", target, 0)
	payload, err := json.Marshal(Record{"occurred_on": "2026-10-02", "description": "Denied late opening", "account_id": account, "account_version": version, "native_minor": "10000", "fx": nil})
	if err != nil {
		t.Fatal(err)
	}
	var sqlError *pgconn.PgError
	result, err := f.store.FinancePreview(f.ctx, f.identity, value.UUID(), value.UUID(), FinanceCommand{Resource: "cash", Action: "opening", ID: target, Generation: generation, AccessVersion: stamp, Payload: payload})
	if result != nil || !errors.As(err, &sqlError) || sqlError.Code != "23514" {
		t.Fatal("late opening bypassed the actual existing-movement boundary", result, err)
	}
	if err = f.owner.QueryRow(f.ctx, census).Scan(&after); err != nil || before != after {
		t.Fatal("denied late opening retained financial or audit effects", before, after, err)
	}
	financeNativeApply(t, f, "cash", "opening", value.UUID(), 0, Record{
		"occurred_on": "2026-09-30", "description": "Explicit valid prior opening", "account_id": account,
		"account_version": version, "native_minor": "10000", "fx": nil,
	})
	wide := financeCashflowReviewRows(t, f, FinanceQuery{Pane: "planning", Section: "cashflow", Account: account, From: "2026-09-29", To: "2026-10-03"})
	narrow := financeCashflowReviewRows(t, f, FinanceQuery{Pane: "planning", Section: "cashflow", Account: account, From: "2026-10-03", To: "2026-10-03"})
	view, err := f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "accounts", ID: account, To: "2026-10-03"})
	if err != nil {
		t.Fatal(err)
	}
	accounts, ok := view["rows"].([]Record)
	if !ok || len(accounts) != 1 {
		t.Fatal("same-date account view omitted the known ledger", view)
	}
	actual := accounts[0]["visible_balance_minor"]
	if wide[account+"/2026-09-29"]["projected_minor"] != nil {
		t.Fatal("a future opening made an earlier unknown balance known", wide)
	}
	for _, row := range []Record{wide[account+"/2026-10-01"], wide[account+"/2026-10-03"], narrow[account+"/2026-10-03"]} {
		if row["projected_minor"] != "10200" || row["projected_minor"] != actual || row["opening_known"] != true {
			t.Fatal("confirmed-only forecast depends on window start or contradicts the same-date visible ledger", row, actual)
		}
	}
}
