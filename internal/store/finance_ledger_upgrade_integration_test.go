//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/expenseedit"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgxpool"
)

func financeMigratePreviousRelease(t *testing.T, ctx context.Context, ownerURL string, owner *pgxpool.Pool) {
	t.Helper()
	// Build the real complete Core26 release, then the accepted staged Core27/28
	// chain. Do not install current Finance and force its version backwards.
	recipeExpansionMigratePredecessor(t, ctx, ownerURL, owner)
	for _, stage := range []struct {
		schema  string
		version uint
	}{{"core", 27}, {"recipes", 5}, {"fuel", 6}, {"mealplan", 8}, {"core", 28}, {"fuel", 7}, {"pantry", 13}} {
		recipeExpansionMigrateTo(t, ownerURL, stage.schema, stage.version)
	}
	if _, err := owner.Exec(ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal("install previous-release audit contracts", err)
	}
	var policies, coreVersion, expensesVersion int
	var audited, fuelReady, pantryReady, planningReady bool
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.audit_policies),(SELECT version FROM core.schema_migrations),(SELECT version FROM expenses.schema_migrations),core.audit_ready(),fuel.module_ready(),pantry.prepared_capture_ready(),core.planning_extensions_ready()`).Scan(&policies, &coreVersion, &expensesVersion, &audited, &fuelReady, &pantryReady, &planningReady); err != nil || policies != 100 || coreVersion != 28 || expensesVersion != 4 || !audited || !fuelReady || !pantryReady || !planningReady {
		t.Fatal("not the genuine complete previous release", policies, coreVersion, expensesVersion, audited, fuelReady, pantryReady, planningReady, err)
	}
}

func TestFinancePopulatedCore28Expenses4UpgradeRetainsProofAndUnknowns(t *testing.T) {
	f := newRecipeExpansionFixtureForMigration(t, financeMigratePreviousRelease)
	category := financeNativeCategory(t, f)
	proved, line, proof, key := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	input := ExpenseEditInput{Input: expenseedit.Input{
		Generation: f.store.States()["expenses"].Generation, Action: "post",
		Header: expenseedit.HeaderInput{OccurredOn: "2026-10-30", Description: "Proved old atomic editor posting", Payee: "Original reviewed shop", AmountEUR: "1.23", Kind: "expense"},
		Lines:  []expenseedit.LineInput{{Key: line, CategoryID: category, AmountEUR: "1.23", Note: "Exact old allocation"}},
	}}
	draft, err := expenseedit.Normalize(input.Input)
	if err != nil {
		t.Fatal(err)
	}
	input.Baseline, err = canonicalExpenseBasis(input.Baseline)
	if err != nil {
		t.Fatal(err)
	}
	input.Removed, err = canonicalExpenseBasis(input.Removed)
	if err != nil {
		t.Fatal(err)
	}
	// This is the unchanged EX02 canonical envelope, not the advanced Finance
	// purpose. Its original hash must replay after the forward cutover.
	canonical, err := json.Marshal(struct {
		Draft    expenseedit.Draft
		Baseline []expenseedit.LineVersion
		Removed  []expenseedit.LineVersion
	}{draft, input.Baseline, input.Removed})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := audit.Begin(ctx, f.app)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,description,payee,amount_minor,kind,created_by) VALUES($1,$2,'2026-10-30','Proved old atomic editor posting','Original reviewed shop',123,'expense',$3)`, proved, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(id,household_id,entry_id,category_id,amount_minor,note,created_by) VALUES($1,$2,$3,$4,123,'Exact old allocation',$5)`, line, f.identity.HouseholdID, proved, category, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	var savedVersion int64
	if err = tx.QueryRow(ctx, `UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2 RETURNING version`, f.identity.HouseholdID, proved).Scan(&savedVersion); err != nil {
		t.Fatal(err)
	}
	// The real App-role Core24 deferred guard proves a matching same-transaction
	// user audit/effect. No Owner proof insertion or constraint bypass is used.
	if _, err = tx.Exec(ctx, `INSERT INTO core.expense_edit_operations(id,household_id,created_by,request_key,request_hash,expenses_generation,operation,entry_id,result_version) VALUES($1,$2,$3,$4,$5,$6,'post',$7,$8)`, proof, f.identity.HouseholdID, f.identity.UserID, key, digest[:], input.Generation, proved, savedVersion); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("actual previous-release editor proof/effect did not commit", err)
	}
	unknown, unknownLine := value.UUID(), value.UUID()
	seed, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(seed)
	if _, err = seed.Exec(f.ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,description,payee,amount_minor,kind,state,posted_at,created_by,version) VALUES($1,$2,'2026-10-29','Historical source is genuinely unknown','Unproved old counterparty',456,'expense','posted',clock_timestamp(),$3,9)`, unknown, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(f.ctx, `INSERT INTO expenses.entry_lines(id,household_id,entry_id,category_id,amount_minor,note,created_by,version) VALUES($1,$2,$3,$4,456,'Preserved unknown history',$5,4)`, unknownLine, f.identity.HouseholdID, unknown, category, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = seed.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	const oldFacts = `SELECT jsonb_build_object('entries',(SELECT jsonb_agg((to_jsonb(e)-'money_mode') ORDER BY id) FROM expenses.entries e WHERE household_id=$1),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l WHERE household_id=$1),'proof',(SELECT to_jsonb(p) FROM core.expense_edit_operations p WHERE id=$2))::text`
	var before, after string
	if err = f.owner.QueryRow(f.ctx, oldFacts, f.identity.HouseholdID, proof).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, f.ownerURL); err != nil {
			t.Fatal("populated Finance forward migration/repeat", err)
		}
	}
	if err = database.Ready(f.ctx, f.app); err != nil {
		t.Fatal("current upgraded runtime/catalog readiness", err)
	}
	if err = f.owner.QueryRow(f.ctx, oldFacts, f.identity.HouseholdID, proof).Scan(&after); err != nil || after != before {
		t.Fatal("forward Finance migration changed historical facts, versions or proof bytes", err)
	}
	var modes, invented int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.entries WHERE household_id=$1 AND money_mode='legacy_eur'),(SELECT count(*) FROM expenses.entry_finance WHERE household_id=$1)+(SELECT count(*) FROM expenses.accounts WHERE household_id=$1)+(SELECT count(*) FROM expenses.cash_events WHERE household_id=$1)+(SELECT count(*) FROM expenses.entry_split_sets WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&modes, &invented); err != nil || modes != 2 || invented != 0 {
		t.Fatal("upgrade invented original money, native accounts, cash or person facts", modes, invented, err)
	}
	var provedOrigin, unknownOrigin, unknownClassification string
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT o.origin FROM core.finance_origin_resources r JOIN core.finance_origin_operations o ON o.id=coalesce(r.provenance_root_id,r.root_id) WHERE r.household_id=$1 AND r.resource_type='entries' AND r.resource_id=$2),(SELECT o.origin FROM core.finance_origin_resources r JOIN core.finance_origin_operations o ON o.id=coalesce(r.provenance_root_id,r.root_id) WHERE r.household_id=$1 AND r.resource_type='entries' AND r.resource_id=$3),(SELECT s.classification FROM core.finance_origin_resources r JOIN core.financial_requirement_sets s ON s.id=r.set_id WHERE r.household_id=$1 AND r.resource_type='entries' AND r.resource_id=$3)`, f.identity.HouseholdID, proved, unknown).Scan(&provedOrigin, &unknownOrigin, &unknownClassification); err != nil || provedOrigin != "manual" || unknownOrigin != "legacy_unresolved" || unknownClassification != "unresolved" {
		t.Fatal("proven old editor and unproved source were conflated", provedOrigin, unknownOrigin, unknownClassification, err)
	}
	replayed, err := f.store.SaveExpense(f.ctx, f.identity, key, value.UUID(), input)
	if err != nil || !replayed.Replayed || replayed.ID != proof || replayed.EntryID != proved || replayed.SavedVersion != savedVersion || !replayed.CurrentExists || replayed.CurrentState != "posted" {
		t.Fatal("old EX02 canonical receipt did not survive forward cutover", replayed, err)
	}
	if _, err = f.store.FinanceView(f.ctx, f.identity, FinanceQuery{Pane: "entries", ID: unknown}); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
		t.Fatal("unproved legacy source became an implicitly authorized manual record", err)
	}
}
