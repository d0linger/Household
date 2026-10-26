//go:build integration

package web

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"household/internal/expenseedit"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func expenseEditNewInput(t *testing.T, f *integrationFixture, category string) store.ExpenseEditInput {
	t.Helper()
	return store.ExpenseEditInput{Input: expenseedit.Input{Generation: f.app.Store.States()["expenses"].Generation, Action: "save",
		Header: expenseedit.HeaderInput{OccurredOn: "2026-10-26", Description: "Private synthetic editor fixture", Payee: "Synthetic payee", AmountEUR: "10.00", Kind: "expense"},
		Lines:  []expenseedit.LineInput{{Key: value.UUID(), CategoryID: category, AmountEUR: "9.00", Note: "Synthetic allocation"}}},
		Baseline: []expenseedit.LineVersion{}, Removed: []expenseedit.LineVersion{}}
}

func expenseEditInput(t *testing.T, f *integrationFixture, entry string) store.ExpenseEditInput {
	t.Helper()
	bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, entry)
	if err != nil {
		t.Fatal(err)
	}
	euro := func(text string) string {
		t.Helper()
		minor, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		sign := ""
		if minor < 0 {
			sign, minor = "-", -minor
		}
		return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
	}
	in := store.ExpenseEditInput{Input: expenseedit.Input{EntryID: entry, Version: bundle.Header.Version, Generation: bundle.Generation, Action: "save",
		Header: expenseedit.HeaderInput{OccurredOn: bundle.Header.OccurredOn, Description: bundle.Header.Description, Payee: bundle.Header.Payee,
			Kind: bundle.Header.Kind, AmountEUR: euro(bundle.Header.AmountMinor)}}, Baseline: bundle.Baseline, Removed: []expenseedit.LineVersion{}}
	if bundle.Header.PaidByMembershipID != nil {
		in.Header.PaidByMembershipID = *bundle.Header.PaidByMembershipID
	}
	for _, line := range bundle.Lines {
		in.Lines = append(in.Lines, expenseedit.LineInput{Key: line.ID, ID: line.ID, Version: line.Version, CategoryID: line.CategoryID, AmountEUR: euro(line.AmountMinor), Note: line.Note})
	}
	return in
}

func expenseEditSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var out string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'entries',(SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY id),'[]') FROM expenses.entries e),
 'lines',(SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY id),'[]') FROM expenses.entry_lines l),
 'search',(SELECT coalesce(jsonb_agg(to_jsonb(d) ORDER BY resource_type,resource_id),'[]') FROM core.search_documents d WHERE module_id='expenses'),
 'outbox',(SELECT count(*) FROM core.outbox_events),'audit',(SELECT count(*) FROM core.audit_log),
 'proof',(SELECT count(*) FROM core.expense_edit_operations),'files',(SELECT count(*) FROM core.file_deletions))::text`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExpenseEditAtomicDraftAndExplicitPost(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Editor category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key, request := value.UUID(), value.UUID()
	saved, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, request, in)
	if err != nil || saved.Replayed || !saved.CurrentExists || saved.CurrentState != "draft" || saved.SavedVersion != 2 {
		t.Fatal("atomic draft did not include child parent-version propagation", saved, err)
	}
	bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, saved.EntryID)
	if err != nil || bundle.SumMinor != "900" || bundle.RemainingMinor == nil || *bundle.RemainingMinor != "100" || len(bundle.Lines) != 1 {
		t.Fatal("incomplete draft lost exact rest", bundle, err)
	}
	post := expenseEditInput(t, f, saved.EntryID)
	post.Action = "post"
	before := expenseEditSnapshot(t, f)
	if result, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post); err == nil || !reflect.DeepEqual(result, store.ExpenseEditReceipt{}) || expenseEditSnapshot(t, f) != before {
		t.Fatal("incomplete posting wrote state or usable receipt", result, err)
	}
	post.Lines[0].AmountEUR = "11.00"
	post.Lines = append(post.Lines, expenseedit.LineInput{Key: value.UUID(), CategoryID: store.String(category["id"]), AmountEUR: "-1.00", Note: "Explicit signed discount"})
	posted, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post)
	if err != nil || posted.CurrentState != "posted" || posted.SavedVersion != 6 {
		t.Fatal("explicit exact signed posting failed", posted, err)
	}
	bundle, err = f.app.Store.ExpenseBundle(f.ctx, f.identity, saved.EntryID)
	if err != nil || bundle.Editable || bundle.Header.PostedAt == nil || bundle.SumMinor != "1000" || len(bundle.Lines) != 2 || bundle.Lines[1].AmountMinor != "-100" {
		t.Fatal("posted facts were not final and exact", bundle, err)
	}
	var projections, attributed, total int
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.search_documents WHERE module_id='expenses' AND resource_type IN('entries','entry_lines')),
 count(*) FILTER(WHERE actor_user_id=$1 AND actor_kind='user' AND db_role='household_app'),count(*)
 FROM core.audit_log WHERE request_id=$2 AND module_id IN('expenses','core')`, f.identity.UserID, request).Scan(&projections, &attributed, &total)
	if err != nil || projections != 3 || total < 5 || attributed != total {
		t.Fatal("atomic child/header/proof projections or audit actor missing", projections, attributed, total, err)
	}
	before = expenseEditSnapshot(t, f)
	post.Version = posted.SavedVersion
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), post); !errors.Is(err, store.ErrConflict) || expenseEditSnapshot(t, f) != before {
		t.Fatal("posted editor mutation was not rejected atomically", err)
	}
}

func TestExpenseEditCompleteBaselineAndExplicitRemoval(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	second, _ := auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": entry["id"], "category_id": category["id"], "amount_minor": "-1.00", "note": "Second", "position": 1})
	in := expenseEditInput(t, f, store.String(entry["id"]))
	before := expenseEditSnapshot(t, f)
	missing := in
	missing.Baseline = append([]expenseedit.LineVersion(nil), in.Baseline[:1]...)
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), missing); !errors.Is(err, store.ErrConflict) || expenseEditSnapshot(t, f) != before {
		t.Fatal("truncated baseline accepted", err)
	}
	in.Lines = in.Lines[:1]
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), in); err == nil || expenseEditSnapshot(t, f) != before {
		t.Fatal("omitted line became implicit deletion", err)
	}
	in.Removed = []expenseedit.LineVersion{{ID: store.String(second["id"]), Version: store.Int(second["version"])}}
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("explicit complete removal rejected", err)
	}
	var remains, projection int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM expenses.entry_lines WHERE id=$1),
 (SELECT count(*) FROM core.search_documents WHERE module_id='expenses' AND resource_id=$1)`, second["id"]).Scan(&remains, &projection); err != nil || remains != 0 || projection != 0 {
		t.Fatal("removed child or projection survived", remains, projection, err)
	}
	stale := expenseEditInput(t, f, store.String(entry["id"]))
	if err := expenseIntegrityAppSQL(t, f, "UPDATE expenses.entry_lines SET note='Intervening child',version=version+1 WHERE id=$1", stale.Lines[0].ID); err != nil {
		t.Fatal(err)
	}
	// Even if an old client somehow carries the current parent version, its
	// complete child read set still rejects the changed child version.
	fresh := expenseEditInput(t, f, store.String(entry["id"]))
	stale.Version = fresh.Version
	before = expenseEditSnapshot(t, f)
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), stale); !errors.Is(err, store.ErrConflict) || expenseEditSnapshot(t, f) != before {
		t.Fatal("stale child version bypassed complete reconciliation", err)
	}
}

func TestExpenseEditReplayAfterFollowupDeletionAndResponsePruning(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Replay category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key := value.UUID()
	first, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	follow := expenseEditInput(t, f, first.EntryID)
	follow.Header.Description = "Later current description"
	second, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), follow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	before := expenseEditSnapshot(t, f)
	replay, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil || !replay.Replayed || replay.ID != first.ID || replay.SavedVersion != first.SavedVersion || replay.CurrentVersion != second.SavedVersion || expenseEditSnapshot(t, f) != before {
		t.Fatal("replay used stale form basis, lost proof or repeated effects", replay, err)
	}
	different := in
	different.Action = "post"
	different.Lines = append([]expenseedit.LineInput(nil), in.Lines...)
	different.Lines[0].AmountEUR = "10.00"
	if _, err = f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), different); !errors.Is(err, store.ErrConflict) || expenseEditSnapshot(t, f) != before {
		t.Fatal("same key different action/payload was accepted", err)
	}
	m, _ := f.app.Store.Module("expenses")
	e, _ := m.Entity("entries")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, first.EntryID, second.SavedVersion, in.Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	before = expenseEditSnapshot(t, f)
	replay, err = f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in)
	if !errors.Is(err, store.ErrNotFound) || replay != (store.ExpenseEditReceipt{}) || expenseEditSnapshot(t, f) != before {
		t.Fatal("replay after deletion exposed retired identity or repeated effects", replay, err)
	}
}

func TestExpenseEditConcurrentKeysAndVersionContenders(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Concurrent editor category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key := value.UUID()
	type outcome struct {
		receipt store.ExpenseEditReceipt
		err     error
	}
	results := make(chan outcome, 6)
	start := make(chan struct{})
	var writers sync.WaitGroup
	for index := range 6 {
		writers.Go(func() {
			<-start
			requestKey := key
			if index%2 == 1 {
				requestKey = strings.ToUpper(key)
			}
			receipt, err := f.app.Store.SaveExpense(f.ctx, f.identity, requestKey, value.UUID(), in)
			results <- outcome{receipt, err}
		})
	}
	close(start)
	writers.Wait()
	close(results)
	entry, proof := "", ""
	initial := 0
	for result := range results {
		if result.err != nil || entry != "" && entry != result.receipt.EntryID || proof != "" && proof != result.receipt.ID {
			t.Fatal("concurrent same key diverged", result)
		}
		entry, proof = result.receipt.EntryID, result.receipt.ID
		if !result.receipt.Replayed {
			initial++
		}
	}
	if initial != 1 {
		t.Fatal("expected one original acceptance", initial)
	}
	edit := expenseEditInput(t, f, entry)
	start = make(chan struct{})
	results = make(chan outcome, 2)
	for range 2 {
		writers.Go(func() {
			<-start
			receipt, err := f.app.Store.SaveExpense(f.ctx, f.identity, value.UUID(), value.UUID(), edit)
			results <- outcome{receipt, err}
		})
	}
	close(start)
	writers.Wait()
	close(results)
	success, conflict := 0, 0
	for result := range results {
		if result.err == nil {
			success++
		} else if errors.Is(result.err, store.ErrConflict) {
			conflict++
		} else {
			t.Fatal(result.err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("same baseline did not select one writer", success, conflict)
	}
}

func TestExpenseEditLateDeferredFailureRollsBackEverythingAndReusesKey(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	in := expenseEditInput(t, f, store.String(entry["id"]))
	in.Action, in.Header.Description = "post", "Late-failure candidate"
	key := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.expense_edit_fixture_failure() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'synthetic deferred failure' USING ERRCODE='23514'; END $$;
 CREATE CONSTRAINT TRIGGER expense_edit_fixture_failure AFTER INSERT ON core.expense_edit_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.expense_edit_fixture_failure()`); err != nil {
		t.Fatal(err)
	}
	before := expenseEditSnapshot(t, f)
	if result, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); err == nil || !reflect.DeepEqual(result, store.ExpenseEditReceipt{}) || expenseEditSnapshot(t, f) != before {
		t.Fatal("late commit failure leaked partial entry/children/search/audit/outbox/proof", result, err)
	}
	if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER expense_edit_fixture_failure ON core.expense_edit_operations; DROP FUNCTION core.expense_edit_fixture_failure()"); err != nil {
		t.Fatal(err)
	}
	if result, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); err != nil || result.Replayed || result.CurrentState != "posted" {
		t.Fatal("rolled-back key was not reusable", result, err)
	}
}

func TestExpenseEditCurrentAuthorizationPrecedesReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Authorization editor category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key := value.UUID()
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, block, restore string }{
		{"forced password", "UPDATE core.users SET must_change_password=true WHERE id=$1", "UPDATE core.users SET must_change_password=false WHERE id=$1"},
		{"disabled account", "UPDATE core.users SET disabled_at=now() WHERE id=$1", "UPDATE core.users SET disabled_at=NULL WHERE id=$1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.block, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, tc.restore, f.identity.UserID); err != nil {
					t.Error(err)
				}
			})
			before := expenseEditSnapshot(t, f)
			if got, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); !errors.Is(err, store.ErrForbidden) || !reflect.DeepEqual(got, store.ExpenseEditReceipt{}) || expenseEditSnapshot(t, f) != before {
				t.Fatal("current restriction did not precede replay", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := f.app.Store.SaveExpense(ctx, f.identity, key, value.UUID(), in); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled write lost context", err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=now() WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session replayed", err)
	}
}

func TestExpenseEditProofSurvivesActualPurgeAndCannotRecreate(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "Purged editor category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	key := value.UUID()
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["expenses"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "expenses", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	// Synthetic backup metadata authorizes the actual isolated purge. This is
	// not an encrypted-backup or restore test.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'fixture','isolated-test',$2)`, backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'expenses',$3,$4,$5)`, purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	state = f.app.Store.States()["expenses"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "expenses", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old generation replayed after purge", err)
	}
	in.Generation = state.Generation
	if _, err := f.app.Store.SaveExpense(f.ctx, f.identity, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old key created new generation expense", err)
	}
	var proofs, entries int
	if err := f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.expense_edit_operations),(SELECT count(*) FROM expenses.entries)").Scan(&proofs, &entries); err != nil || proofs != 1 || entries != 0 {
		t.Fatal("purge removed proof or recreated expense", proofs, entries, err)
	}
}
