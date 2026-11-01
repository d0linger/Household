//go:build integration

package web

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func expenseIntegrityFixture(t *testing.T) (*integrationFixture, store.Record, store.Record, store.Record) {
	t.Helper()
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Editor integrity category"})
	entry, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-26", "description": "Editor integrity draft", "amount_minor": "10.00"})
	line, _ := auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": entry["id"], "category_id": category["id"], "amount_minor": "10.00", "note": "Complete initial line", "position": 0})
	return f, category, entry, line
}

func expenseIntegrityAppSQL(t *testing.T, f *integrationFixture, statement string, args ...any) error {
	t.Helper()
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "expenses", f.app.Store.States()["expenses"].Generation)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, statement, args...); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

// expenseIntegrityPreparedSQL exercises the SQL guards with a real current App
// actor and exact prepared entry root. It intentionally bypasses Go form limits
// to construct coherent historical-reader fixtures, not production form coverage.
func expenseIntegrityPreparedSQL(t *testing.T, f *integrationFixture, entry string, statement string, args ...any) error {
	t.Helper()
	generation := f.app.Store.States()["expenses"].Generation
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "expenses", generation)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	operation, err := expenseIntegrityPrepareEntry(f.ctx, f, tx, entry)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(f.ctx, statement, args...); err != nil {
		return err
	}
	if err = expenseIntegrityFinishEntry(f.ctx, f, tx, operation); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func expenseIntegrityPrepareEntry(ctx context.Context, f *integrationFixture, tx pgx.Tx, entry string) (string, error) {
	var operation string
	err := tx.QueryRow(ctx, `SELECT core.finance_origin_begin($1,$2,$3,$4,$5,$6,'entries',$7,'manual','[]'::jsonb,NULL)`,
		f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID, f.identity.SessionID, f.identity.AuthVersion,
		f.app.Store.States()["expenses"].Generation, entry).Scan(&operation)
	return operation, err
}

func expenseIntegrityFinishEntry(ctx context.Context, f *integrationFixture, tx pgx.Tx, operation string) error {
	_, err := tx.Exec(ctx, `SELECT core.finance_origin_finish($1,$2,$3,$4,$5,$6,$7)`, operation,
		f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID, f.identity.SessionID, f.identity.AuthVersion,
		f.app.Store.States()["expenses"].Generation)
	return err
}

func TestExpenseEditorIntegrityEveryAppChildWriteTracksParent(t *testing.T) {
	f, category, entry, line := expenseIntegrityFixture(t)
	version := func() int64 {
		t.Helper()
		var revision int64
		if err := f.pool.QueryRow(f.ctx, "SELECT version FROM expenses.entries WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, entry["id"]).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	if version() != 2 {
		t.Fatal("generic child creation did not invalidate the original parent")
	}
	request := value.UUID()
	tx, err := f.app.Store.BeginMutation(audit.WithRequest(f.ctx, request), f.identity, "expenses", f.app.Store.States()["expenses"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	operation, err := expenseIntegrityPrepareEntry(f.ctx, f, tx, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE expenses.entry_lines SET note='Direct app change',version=version+1 WHERE id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	if err = expenseIntegrityFinishEntry(f.ctx, f, tx, operation); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil || version() != 3 {
		t.Fatal("direct child update did not invalidate parent", err)
	}
	auditCount(t, f, request, 2)
	var attributed bool
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*)=2 AND bool_and(actor_user_id=$2 AND actor_kind='user'
 AND db_role='household_app' AND household_id=$3 AND module_id='expenses' AND action='UPDATE')
 AND count(*) FILTER(WHERE resource_type='entries' AND resource_id=$4)=1
 AND count(*) FILTER(WHERE resource_type='entry_lines' AND resource_id=$5)=1
 FROM core.audit_log WHERE request_id=$1`, request, f.identity.UserID, f.identity.HouseholdID, entry["id"], line["id"]).Scan(&attributed); err != nil || !attributed {
		t.Fatal("parent/child audit lost actor, household or exact resource attribution", attributed, err)
	}
	if err = expenseIntegrityPreparedSQL(t, f, store.String(entry["id"]), "DELETE FROM expenses.entry_lines WHERE id=$1", line["id"]); err != nil || version() != 4 {
		t.Fatal("direct child delete did not invalidate parent", err)
	}
	if err = expenseIntegrityPreparedSQL(t, f, store.String(entry["id"]), `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,created_by)
 VALUES($1,$2,$3,1000,'Restored complete allocation',$4)`, f.identity.HouseholdID, entry["id"], category["id"], f.identity.UserID); err != nil || version() != 5 {
		t.Fatal("direct child insert did not invalidate parent", err)
	}
	if err = expenseIntegrityPreparedSQL(t, f, store.String(entry["id"]), "UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1 WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, statement string }{
		{"parent", "UPDATE expenses.entries SET description='Forbidden' WHERE id=$1"},
		{"line update", "UPDATE expenses.entry_lines SET note='Forbidden' WHERE entry_id=$1"},
		{"line delete", "DELETE FROM expenses.entry_lines WHERE entry_id=$1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pgerr *pgconn.PgError
			if err := expenseIntegrityAppSQL(t, f, tc.statement, entry["id"]); !errors.As(err, &pgerr) || pgerr.Code != "23514" {
				t.Fatal("posted guard weakened", err)
			}
		})
	}
}

func TestExpenseEditorIntegrityUnprovenOwnerMutationDoesNotBecomeReadable(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	var before, after int64
	if err := f.owner.QueryRow(f.ctx, "SELECT version FROM expenses.entries WHERE id=$1", entry["id"]).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET note='Unproven restored content' WHERE id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT version FROM expenses.entries WHERE id=$1", entry["id"]).Scan(&after); err != nil || before != after {
		t.Fatal("Owner restore changed the historical parent version", before, after, err)
	}
	if _, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unproven Owner content bypassed the retained row proof", err)
	}
}

func TestExpenseEditorIntegrityOppositeLockOrderFailsBeforeParentRelease(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	parent, err := f.app.Store.BeginMutation(ctx, f.identity, "expenses", f.app.Store.States()["expenses"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Rollback(f.ctx) }()
	operation, err := expenseIntegrityPrepareEntry(ctx, f, parent, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parent.Exec(ctx, "SELECT id FROM expenses.entries WHERE id=$1 FOR UPDATE", entry["id"]); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entry_lines")
	_, err = f.app.Store.Write(ctx, f.identity, m, e, store.String(line["id"]), value.UUID(), value.UUID(),
		auditMutation(t, f, "expenses", store.Record{"note": "Competing child-first generic edit"}, store.Int(line["version"])))
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatal("generic child-first path waited for parent instead of fail-fast conflict", err)
	}
	if _, err = parent.Exec(ctx, "UPDATE expenses.entry_lines SET note='Parent-first edit',version=version+1 WHERE id=$1", line["id"]); err != nil {
		t.Fatal("failed generic edit retained child lock", err)
	}
	if err = expenseIntegrityFinishEntry(ctx, f, parent, operation); err != nil {
		t.Fatal(err)
	}
	if err = parent.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestExpenseEditorIntegrityDraftCascadeAndSignedDiscounts(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	line, _ := auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": entry["id"], "category_id": category["id"], "amount_minor": "-1.00", "note": "Explicit signed discount", "position": 1})
	if store.Int(line["amount_minor"]) != -100 {
		t.Fatal("signed discount was not retained exactly", line)
	}
	before := expenseEditSnapshot(t, f)
	var pgerr *pgconn.PgError
	if err := expenseIntegrityAppSQL(t, f, "DELETE FROM expenses.entries WHERE id=$1", entry["id"]); !errors.As(err, &pgerr) || pgerr.Code != "42501" || expenseEditSnapshot(t, f) != before {
		t.Fatal("unprepared draft deletion bypassed its exact financial operation", err)
	}
	current, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	if err := f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(entry["id"]), current.Header.Version,
		f.app.Store.States()["expenses"].Generation, value.UUID()); err != nil {
		t.Fatal("prepared draft deletion failed to clean its complete bundle", err)
	}
	var remaining int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM expenses.entry_lines WHERE entry_id=$1", entry["id"]).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("draft deletion left children or resurrected parent", remaining, err)
	}
}

func TestExpenseEditorIntegrityDraftDeleteRejectsNestedForeignRetirement(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	foreign, _ := auditCreate(t, f, "expenses", "entries", store.Record{
		"occurred_on": "2026-10-26", "description": "Independent retained source", "amount_minor": "10.00",
	})
	auditCreate(t, f, "expenses", "entry_lines", store.Record{
		"entry_id": foreign["id"], "category_id": category["id"], "amount_minor": "10.00", "note": "Independent source allocation",
	})
	var executable bool
	if err := f.owner.QueryRow(f.ctx, `SELECT has_function_privilege('household_app',
 'core.finance_union_sets(uuid,uuid[],uuid)','EXECUTE')`).Scan(&executable); err != nil || executable {
		t.Fatal("private source union became an App capability", executable, err)
	}
	before := expenseEditSnapshot(t, f)
	var rootsBefore int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.finance_origin_operations").Scan(&rootsBefore); err != nil {
		t.Fatal(err)
	}
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "expenses", f.app.Store.States()["expenses"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var operation string
	if err = tx.QueryRow(f.ctx, `SELECT core.finance_origin_begin($1,$2,$3,$4,$5,$6,'entries',$7,'manual',
 jsonb_build_array(jsonb_build_object('kind','entries','id',$8::uuid)),NULL)`,
		f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID, f.identity.SessionID, f.identity.AuthVersion,
		f.app.Store.States()["expenses"].Generation, entry["id"], foreign["id"]).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE expenses.entries SET payee='Current proven foreign source',version=version+1 WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	var foreignRequirements int
	if err = tx.QueryRow(f.ctx, `SELECT count(*) FROM core.finance_origin_resources h
 JOIN core.financial_requirement_members m ON m.household_id=h.household_id AND m.set_id=h.set_id
 JOIN core.financial_subjects source ON source.household_id=m.household_id AND source.id=m.subject_id
 WHERE h.household_id=$1 AND h.resource_type='entries' AND h.resource_id=$2
 AND source.policy_id=(SELECT policy_id FROM core.financial_subjects WHERE household_id=$1 AND resource_type='entries' AND resource_id=$3)`,
		f.identity.HouseholdID, entry["id"], foreign["id"]).Scan(&foreignRequirements); err != nil || foreignRequirements != 2 {
		t.Fatal("new source and its complete children were omitted from an existing target", foreignRequirements, err)
	}
	nested, err := expenseIntegrityPrepareEntry(f.ctx, f, tx, store.String(foreign["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "DELETE FROM expenses.entries WHERE id=$1", foreign["id"]); err != nil {
		t.Fatal(err)
	}
	if err = expenseIntegrityFinishEntry(f.ctx, f, tx, nested); err != nil {
		t.Fatal("valid nested source deletion did not finish its own exact bundle", err)
	}
	if _, err = tx.Exec(f.ctx, "DELETE FROM expenses.entries WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	var pgerr *pgconn.PgError
	if err = expenseIntegrityFinishEntry(f.ctx, f, tx, operation); !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatal("own-bundle retirement bypassed the separately retired foreign source", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	var rootsAfter int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.finance_origin_operations").Scan(&rootsAfter); err != nil || rootsAfter != rootsBefore || expenseEditSnapshot(t, f) != before {
		t.Fatal("refused foreign retirement left partial rows or source evidence", rootsBefore, rootsAfter, err)
	}
}

func TestExpenseEditorIntegrityReadinessChecksEveryPrivilegeAndTrigger(t *testing.T) {
	f, _, _, _ := expenseIntegrityFixture(t)
	ready := func(t *testing.T, want bool) {
		t.Helper()
		var got bool
		if err := f.pool.QueryRow(f.ctx, "SELECT expenses.editing_ready()").Scan(&got); err != nil || got != want {
			t.Fatal("expense readiness mismatch", got, want, err)
		}
		if err := database.Ready(f.ctx, f.pool); (err == nil) != want {
			t.Fatal("application readiness ignored expense guard", want, err)
		}
	}
	ready(t, true)
	for _, table := range []string{"entries", "entry_lines"} {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			t.Run(table+"/"+privilege, func(t *testing.T) {
				// All identifiers are fixed test constants, never request input.
				if _, err := f.owner.Exec(f.ctx, "REVOKE "+privilege+" ON expenses."+table+" FROM household_app"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := f.owner.Exec(f.ctx, "GRANT "+privilege+" ON expenses."+table+" TO household_app"); err != nil {
						t.Error("restore privilege:", err)
					}
				})
				ready(t, false)
			})
		}
	}
	for _, guard := range []struct{ table, trigger string }{
		{"entries", "entries_immutable"}, {"entries", "entries_total"},
		{"entry_lines", "lines_immutable"}, {"entry_lines", "lines_touch_entry"}, {"entry_lines", "lines_total"},
	} {
		t.Run(guard.trigger, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "ALTER TABLE expenses."+guard.table+" DISABLE TRIGGER "+guard.trigger); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "ALTER TABLE expenses."+guard.table+" ENABLE TRIGGER "+guard.trigger); err != nil {
					t.Error("restore guard:", err)
				}
			})
			ready(t, false)
		})
	}
	ready(t, true)
	for _, tc := range []struct{ name, trigger string }{
		{"conditional guard", "BEFORE UPDATE OR DELETE ON expenses.entries FOR EACH ROW WHEN (false)"},
		{"restricted update columns", "BEFORE UPDATE OF description OR DELETE ON expenses.entries FOR EACH ROW"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER entries_immutable ON expenses.entries; CREATE TRIGGER entries_immutable "+tc.trigger+" EXECUTE FUNCTION expenses.protect_posted()"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER entries_immutable ON expenses.entries; CREATE TRIGGER entries_immutable BEFORE UPDATE OR DELETE ON expenses.entries FOR EACH ROW EXECUTE FUNCTION expenses.protect_posted()"); err != nil {
					t.Error("restore exact guard:", err)
				}
			})
			ready(t, false)
		})
	}
	ready(t, true)
	for _, tc := range []struct{ name, columns string }{
		{"wrong ordered index", "household_id,entry_id,id,position"},
		{"mixed index direction", "household_id,entry_id,position ASC,id DESC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Column definitions are fixed test constants, never request input.
			if _, err := f.owner.Exec(f.ctx, "DROP INDEX expenses.lines_editor_order; CREATE INDEX lines_editor_order ON expenses.entry_lines("+tc.columns+")"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "DROP INDEX expenses.lines_editor_order; CREATE INDEX lines_editor_order ON expenses.entry_lines(household_id,entry_id,position,id)"); err != nil {
					t.Error(err)
				}
			})
			ready(t, false)
		})
	}
	ready(t, true)
}

func TestExpenseEditorIntegrityPopulatedExpenses2AndLegacyWriterUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "meters" {
			version = 1 // Meters2 requires the later complete Core32 predecessor.
		}
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		switch schema {
		case "core":
			version = 26
		case "fuel":
			version = 5
		case "recipes":
			version = 4
		case "pantry":
			version = 12 // Pantry13 belongs to Core28.
		case "mealplan":
			version = 7 // Keep the genuine Core26 predecessor.
		case "expenses":
			version = 2
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hid, uid, category, draft, posted, oversized, line := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	seed, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seed.Rollback(f.ctx) }()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior Expenses2 household')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-expenses','Prior expense author','fixture-no-login')", []any{uid}},
		{"INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES('expenses','expenses','historical-fixture')", nil},
		{"INSERT INTO core.module_states(household_id,module_id,state,data_generation) VALUES($1,'expenses','enabled',5)", []any{hid}},
		{"INSERT INTO expenses.categories(id,household_id,name,created_by) VALUES($1,$2,'Prior category',$3)", []any{category, hid, uid}},
		{`INSERT INTO expenses.entries(id,household_id,occurred_on,description,amount_minor,state,posted_at,created_by,version)
 VALUES($1,$4,'2026-10-26','Prior draft',1000000000000,'draft',NULL,$5,7),
 ($2,$4,'2026-10-26','Prior posted',1250,'posted',clock_timestamp(),$5,9),
 ($3,$4,'2026-10-26','Legacy101',101,'draft',NULL,$5,11)`, []any{draft, posted, oversized, hid, uid}},
		{`INSERT INTO expenses.entry_lines(id,household_id,entry_id,category_id,amount_minor,note,created_by,version)
 VALUES($1,$2,$3,$4,1000000000000,'Old draft line',$5,3)`, []any{line, hid, draft, category, uid}},
		{`INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,created_by)
 VALUES($1,$2,$3,1250,'Old posted line',$4)`, []any{hid, posted, category, uid}},
		{`INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT $1,$2,$3,1,'Legacy line '||n,n,$4 FROM generate_series(0,100) n`, []any{hid, oversized, category, uid}},
	} {
		if _, err = seed.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = seed.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 25*time.Second)
	defer cancel()
	writer, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(f.ctx) }()
	if _, err = writer.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", hid); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(ctx, "SELECT id FROM expenses.entry_lines WHERE id=$1 FOR UPDATE", line); err != nil {
		t.Fatal(err)
	}
	var writerPID int32
	if err = writer.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	// Keep the short writer-lock contract separate from the full upgrade.
	migrationCtx, stopMigration := context.WithCancel(f.ctx)
	defer stopMigration()
	migrated := make(chan error, 1)
	finished := false
	go func() { migrated <- database.Migrate(migrationCtx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")) }()
	defer func() {
		_ = writer.Rollback(f.ctx)
		if finished {
			return
		}
		stopMigration()
		select {
		case cleanupErr := <-migrated:
			if cleanupErr != nil {
				t.Log("migration cleanup:", cleanupErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("migration did not stop after legacy writer release")
		}
	}()
	blocked := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case migrationErr := <-migrated:
			finished = true
			t.Fatal("migration finished before old writer reached the barrier", migrationErr)
		default:
		}
		if err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE a.datname=current_database()
 AND $1::int=ANY(pg_blocking_pids(a.pid)) AND a.wait_event_type='Lock' AND a.query LIKE '%expense editor household barrier%')`, writerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("Expenses3 migration never waited at its household barrier")
	}
	if _, err = writer.Exec(ctx, "UPDATE expenses.entry_lines SET note='Committed legacy writer',version=version+1 WHERE id=$1", line); err != nil {
		t.Fatal("migration inverted the old child/parent lock order", err)
	}
	const fingerprint = `SELECT jsonb_build_object(
 'entries',(SELECT jsonb_agg((to_jsonb(e)-'money_mode') ORDER BY id) FROM expenses.entries e WHERE household_id=$1),
 'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l WHERE household_id=$1))::text`
	var before, after string
	if err = writer.QueryRow(ctx, fingerprint, hid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-migrated
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.pool.QueryRow(f.ctx, fingerprint, hid).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed financial values/versions/history", err)
	}
	assertLegacyFinanceMoneyMode(t, f)
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM expenses.entry_lines WHERE entry_id=$1", oversized).Scan(&count); err != nil || count != 101 {
		t.Fatal("upgrade truncated legacy allocations", count, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
