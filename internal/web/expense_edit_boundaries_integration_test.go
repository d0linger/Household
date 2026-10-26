//go:build integration

package web

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/expenseedit"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestExpenseEditCanonicalReadSetAndKeyCase(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": entry["id"], "category_id": category["id"], "amount_minor": "-1.00", "position": 1})
	in := expenseEditInput(t, f, store.String(entry["id"]))
	key := value.UUID()
	first, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := in
	equivalent.Baseline = append([]expenseedit.LineVersion(nil), in.Baseline...)
	slices.Reverse(equivalent.Baseline)
	for index := range equivalent.Baseline {
		equivalent.Baseline[index].ID = strings.ToUpper(equivalent.Baseline[index].ID)
	}
	equivalent.Removed = nil
	before := expenseEditSnapshot(t, f)
	replay, err := f.app.Store.SaveExpense(f.ctx, f.identity, strings.ToUpper(key), value.UUID(), equivalent)
	if err != nil || !replay.Replayed || replay.ID != first.ID || expenseEditSnapshot(t, f) != before || in.Baseline[0].ID != strings.ToLower(in.Baseline[0].ID) {
		t.Fatal("read-set ordering/case changed request identity or caller data", replay, err)
	}
}

func TestExpenseEditInactiveReferencesAndOversizedLegacyStayExplicit(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	payer := shoppingTestMember(t, f, "editor-retained-payer", "reader")
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET paid_by_membership_id=$2 WHERE id=$1", entry["id"], payer.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.categories SET archived_at=now() WHERE id=$1", category["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=now() WHERE id=$1", payer.MembershipID); err != nil {
		t.Fatal(err)
	}
	in := expenseEditInput(t, f, store.String(entry["id"]))
	saved, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil || saved.CurrentState != "draft" {
		t.Fatal("retained historical inactive references were discarded", saved, err)
	}
	post := expenseEditInput(t, f, saved.EntryID)
	post.Action = "post"
	before := expenseEditSnapshot(t, f)
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post); err == nil || expenseEditSnapshot(t, f) != before {
		t.Fatal("posting silently accepted archived category", err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE expenses.categories SET archived_at=NULL WHERE id=$1", category["id"]); err != nil {
		t.Fatal(err)
	}
	before = expenseEditSnapshot(t, f)
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post); err == nil || expenseEditSnapshot(t, f) != before {
		t.Fatal("posting silently accepted inactive payer", err)
	}
	post.Header.PaidByMembershipID = ""
	posted, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post)
	if err != nil || posted.CurrentState != "posted" {
		t.Fatal("explicit unknown payer could not be posted", posted, err)
	}
	newInput := expenseEditNewInput(t, f, store.String(category["id"]))
	newInput.Header.PaidByMembershipID = payer.MembershipID
	before = expenseEditSnapshot(t, f)
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), newInput); err == nil || expenseEditSnapshot(t, f) != before {
		t.Fatal("new inactive payer selection accepted", err)
	}
	large, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Oversized editor legacy", "amount_minor": "10.00"})
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT $1,$2,$3,1,'Legacy',n,$4 FROM generate_series(0,100) n`, f.identity.HouseholdID, large["id"], category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	newInput.EntryID, newInput.Version, newInput.Header.PaidByMembershipID = store.String(large["id"]), store.Int(large["version"]), ""
	before = expenseEditSnapshot(t, f)
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), newInput); err == nil || expenseEditSnapshot(t, f) != before {
		t.Fatal("oversized unseen rows were replaced", err)
	}
}

func TestExpenseEditProofOnlyMismatchAndReadinessGuards(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	before := expenseEditSnapshot(t, f)
	err := expenseIntegrityAppSQL(t, f, `INSERT INTO core.expense_edit_operations(household_id,created_by,request_key,request_hash,
 expenses_generation,operation,entry_id,result_version) VALUES($1,$2,$3,decode(repeat('ab',32),'hex'),$4,'save',$5,2)`,
		f.identity.HouseholdID, f.identity.UserID, value.UUID(), f.app.Store.States()["expenses"].Generation, entry["id"])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || expenseEditSnapshot(t, f) != before {
		t.Fatal("proof-only operation committed without matching effect", err)
	}
	err = expenseIntegrityAppSQL(t, f, `WITH changed AS(UPDATE expenses.entries SET version=version+1 WHERE household_id=$1 AND id=$5 RETURNING id,version)
 INSERT INTO core.expense_edit_operations(household_id,created_by,request_key,request_hash,expenses_generation,operation,entry_id,result_version)
 SELECT $1,$2,$3,decode(repeat('ab',32),'hex'),$4,'save',changed.id,changed.version+1 FROM changed`,
		f.identity.HouseholdID, f.identity.UserID, value.UUID(), f.app.Store.States()["expenses"].Generation, entry["id"])
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || expenseEditSnapshot(t, f) != before {
		t.Fatal("mismatched proof committed header effect", err)
	}
	ready := func(t *testing.T, want bool) {
		t.Helper()
		var got bool
		if err := f.pool.QueryRow(f.ctx, "SELECT core.expense_editing_ready()").Scan(&got); err != nil || got != want {
			t.Fatal("proof readiness mismatch", got, want, err)
		}
		if err := database.Ready(f.ctx, f.pool); (err == nil) != want {
			t.Fatal("global readiness ignored proof guard", want, err)
		}
	}
	for _, trigger := range []string{"expense_edit_insert", "expense_edit_immutable", "expense_edit_complete"} {
		t.Run(trigger, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "ALTER TABLE core.expense_edit_operations DISABLE TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "ALTER TABLE core.expense_edit_operations ENABLE TRIGGER "+trigger); err != nil {
					t.Error(err)
				}
			})
			ready(t, false)
		})
	}
	for _, privilege := range []string{"SELECT", "INSERT"} {
		t.Run(privilege, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "REVOKE "+privilege+" ON core.expense_edit_operations FROM household_app"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "GRANT "+privilege+" ON core.expense_edit_operations TO household_app"); err != nil {
					t.Error(err)
				}
			})
			ready(t, false)
		})
	}
	ready(t, true)
	input := expenseEditNewInput(t, f, store.String(category["id"]))
	accepted, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE core.expense_edit_operations SET result_version=result_version+1 WHERE id=$1",
		"DELETE FROM core.expense_edit_operations WHERE id=$1",
	} {
		if err := expenseIntegrityAppSQL(t, f, statement, accepted.ID); err == nil {
			t.Fatal("runtime proof mutation accepted", statement)
		}
	}
}

func TestExpenseEditPopulatedCore23UpgradePreservesSharedFinancialFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "core" {
			version = 23
		}
		if schema == "expenses" {
			version = 3
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, category, entry := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `WITH h AS(INSERT INTO core.households(id,name) VALUES($1,'Prior Core23 financial fixture') RETURNING id),
 u AS(INSERT INTO core.users(id,username,display_name,password_hash) VALUES($2,'prior-proof-upgrade','Prior financial author','fixture-no-login') RETURNING id),
 c AS(INSERT INTO expenses.categories(id,household_id,name,created_by) SELECT $3,h.id,'Preserved shared category',u.id FROM h,u RETURNING id,household_id,created_by),
 e AS(INSERT INTO expenses.entries(id,household_id,occurred_on,description,payee,amount_minor,kind,state,posted_at,created_by,version)
 SELECT $4,c.household_id,'2026-10-26','Preserved shared financial fact','Original payee',1000000000000,'expense','posted',clock_timestamp(),c.created_by,19 FROM c RETURNING id,household_id,created_by)
 INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,created_by,version)
 SELECT e.household_id,e.id,c.id,1000000000000,'Original exact allocation',e.created_by,7 FROM e,c`, hh, user, category, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `WITH installation AS(INSERT INTO core.module_installations(module_id,schema_name,last_seen_build)
	 VALUES('expenses','expenses','historical-fixture') RETURNING module_id)
	 INSERT INTO core.module_states(household_id,module_id,state,data_generation) SELECT $1,module_id,'disabled',7 FROM installation`, hh); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object('entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM expenses.entries e),
 'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l),'categories',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM expenses.categories c))::text`
	var before, after string
	if err := f.owner.QueryRow(f.ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.owner.QueryRow(f.ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("Core24 changed historical shared fact or invented values", err)
	}
	var proofs, policies int
	if err := f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.expense_edit_operations),(SELECT count(*) FROM core.audit_policies)").Scan(&proofs, &policies); err != nil || proofs != 0 || policies != 77 {
		t.Fatal("upgrade fabricated editor proofs or lost audit policy", proofs, policies, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
