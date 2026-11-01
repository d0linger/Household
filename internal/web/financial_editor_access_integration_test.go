//go:build integration

package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func financialEditorAccessSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var identities string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'policies',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY id),'[]') FROM core.financial_policies p),
 'subjects',(SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY id),'[]') FROM core.financial_subjects s))::text`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	return identities + "\n" + expenseEditSnapshot(t, f)
}

func TestFinancialEditorPolicyContentionFailsBeforeReleaseAndKeyCanRetry(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	in := expenseEditInput(t, f, store.String(entry["id"]))
	in.Header.Description = "Explicit change after policy contention"
	key := value.UUID()
	before := financialEditorAccessSnapshot(t, f)
	held, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(f.ctx) }()
	if _, err = held.Exec(f.ctx, "UPDATE core.financial_policies SET version=version+1 WHERE id=$1", financialPolicyID(t, f, entry)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	got, err := f.app.Store.SaveExpense(ctx, f.identity, key, value.UUID(), in)
	if !errors.Is(err, store.ErrConflict) || got != (store.ExpenseEditReceipt{}) || financialEditorAccessSnapshot(t, f) != before {
		t.Fatal("policy contention waited, leaked a receipt or wrote partial effects", got, err)
	}
	// The failed writer returned while the competing metadata transaction was
	// still open. Its exact key/body can succeed after that lock is released.
	if err = held.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	got, err = f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil || got.Replayed || got.EntryID != store.String(entry["id"]) {
		t.Fatal("failed authorization lock consumed the key", got, err)
	}
	var proofs int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.expense_edit_operations WHERE request_key=$1", key).Scan(&proofs); err != nil || proofs != 1 {
		t.Fatal("policy retry did not leave exactly one proof", proofs, err)
	}
}

func TestFinancialEditorBrokenCurrentAccessDeniesMutationAndOldReceipt(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{"retired policy", "UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE resource_id=$1"},
		{"missing parent origin", "DELETE FROM core.finance_origin_resources WHERE resource_type='entries' AND resource_id=$1"},
		{"missing child origin", "DELETE FROM core.finance_origin_resources WHERE resource_type='entry_lines' AND subject_id IN(SELECT id FROM core.financial_subjects WHERE policy_id IN(SELECT id FROM core.financial_policies WHERE resource_id=$1))"},
		{"old policy generation", "UPDATE core.financial_policies SET module_generation=module_generation+1 WHERE resource_id=$1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Current access category"})
			original := expenseEditNewInput(t, f, store.String(category["id"]))
			key := value.UUID()
			first, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), original)
			if err != nil {
				t.Fatal(err)
			}
			update := expenseEditInput(t, f, first.EntryID)
			update.Header.Description = "Must remain uncommitted"
			if _, err = f.owner.Exec(f.ctx, test.statement, first.EntryID); err != nil {
				t.Fatal(err)
			}
			before := financialEditorAccessSnapshot(t, f)
			for _, attempt := range []struct {
				key   string
				input store.ExpenseEditInput
			}{{value.UUID(), update}, {key, original}} {
				got, err := f.app.Store.SaveExpense(f.ctx, f.identity, attempt.key, value.UUID(), attempt.input)
				if !errors.Is(err, store.ErrNotFound) || got != (store.ExpenseEditReceipt{}) || financialEditorAccessSnapshot(t, f) != before {
					t.Fatal("broken current access exposed old identity or changed financial effects", got, err)
				}
			}
			var proofs int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.expense_edit_operations").Scan(&proofs); err != nil || proofs != 1 {
				t.Fatal("denial erased permanent once-only evidence", proofs, err)
			}
		})
	}
}

func TestFinancialEditorConcurrentDeleteBeforePolicyLockNeverReturnsRetiredReceipt(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Concurrent receipt category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key := value.UUID()
	first, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	// A disposable test-only RLS function holds the SELECT after its statement
	// snapshot, before the outer policy row lock. Production uses explicit SQL
	// authorization and does not install this policy/function.
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION core.test_financial_read_barrier() RETURNS boolean
 LANGUAGE plpgsql VOLATILE SET search_path=pg_catalog AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(9232019); RETURN true; END $$;
 GRANT EXECUTE ON FUNCTION core.test_financial_read_barrier() TO household_app;
 ALTER TABLE core.financial_policies ENABLE ROW LEVEL SECURITY;
 CREATE POLICY test_financial_read_barrier ON core.financial_policies FOR SELECT TO household_app
 USING(core.test_financial_read_barrier());
 CREATE POLICY test_financial_lock_access ON core.financial_policies FOR UPDATE TO household_app
 USING(true) WITH CHECK(true);`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, cleanup := f.owner.Exec(f.ctx, `DROP POLICY test_financial_read_barrier ON core.financial_policies;
 DROP POLICY test_financial_lock_access ON core.financial_policies;
 ALTER TABLE core.financial_policies DISABLE ROW LEVEL SECURITY; DROP FUNCTION core.test_financial_read_barrier()`); cleanup != nil {
			t.Error(cleanup)
		}
	}()
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	barrier, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Rollback(f.ctx) }()
	var barrierPID int32
	if err = barrier.QueryRow(ctx, "SELECT pg_backend_pid() FROM (SELECT pg_advisory_xact_lock(9232019)) held").Scan(&barrierPID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		receipt store.ExpenseEditReceipt
		err     error
	}
	returned := make(chan result, 1)
	go func() {
		receipt, saveErr := f.app.Store.SaveExpense(ctx, f.identity, key, value.UUID(), in)
		returned <- result{receipt: receipt, err: saveErr}
	}()
	finished := false
	defer func() {
		if !finished {
			cancel()
			_ = barrier.Rollback(f.ctx)
			select {
			case <-returned:
			case <-time.After(5 * time.Second):
				t.Error("financial replay did not release its goroutine")
			}
		}
	}()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case early := <-returned:
			finished = true
			t.Fatal("replay returned before its controlled policy snapshot", early, early.err)
		default:
		}
		if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a
 WHERE a.datname=current_database() AND $1::int=ANY(pg_blocking_pids(a.pid))
 AND a.query LIKE '%SELECT p.id,p.resource_id,p.version FROM%')`, barrierPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("did not observe replay blocked before policy lock")
	}
	if _, err = f.owner.Exec(ctx, "DELETE FROM expenses.entries WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, first.EntryID); err != nil {
		t.Fatal(err)
	}
	before := financialEditorAccessSnapshot(t, f)
	if err = barrier.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-returned:
		finished = true
		if !errors.Is(got.err, store.ErrNotFound) || got.receipt != (store.ExpenseEditReceipt{}) || financialEditorAccessSnapshot(t, f) != before {
			t.Fatal("concurrent deletion exposed retired receipt or repeated effects", got, got.err)
		}
	case <-ctx.Done():
		t.Fatal("financial replay did not finish after policy barrier release", ctx.Err())
	}
}
