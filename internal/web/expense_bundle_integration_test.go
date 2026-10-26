//go:build integration

package web

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"household/internal/expenseedit"
	"household/internal/store"
	"household/internal/value"
)

func TestExpenseBundleSharedReadIsCompleteExactAndSideEffectFree(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	auditCreate(t, f, "expenses", "entry_lines", store.Record{"entry_id": entry["id"], "category_id": category["id"], "amount_minor": "-1.00", "note": "Signed discount", "position": 1})
	snapshot := func() string {
		t.Helper()
		var raw string
		if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('audit',(SELECT count(*) FROM core.audit_log),
 'proofs',(SELECT count(*) FROM core.idempotency_records),'outbox',(SELECT count(*) FROM core.outbox_events),
 'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM expenses.entries e),
 'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l))::text`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil || !bundle.Editable || bundle.Header.Version != 3 || len(bundle.Lines) != 2 || len(bundle.Baseline) != 2 ||
		bundle.Header.AmountMinor != "1000" || bundle.SumMinor != "900" || bundle.RemainingMinor == nil || *bundle.RemainingMinor != "100" ||
		bundle.Lines[1].AmountMinor != "-100" || bundle.Header.PaidByMembershipID != nil || bundle.Header.PayerActive || bundle.Header.PayerLabel != "" {
		t.Fatal("complete exact shared bundle lost saved facts", bundle, err)
	}
	for index, line := range bundle.Lines {
		if bundle.Baseline[index].ID != line.ID || bundle.Baseline[index].Version != line.Version || line.Key != line.ID || line.Note == "" {
			t.Fatal("baseline and saved text do not share one complete source", bundle)
		}
	}
	fresh, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, "")
	if err != nil || fresh.Header.ID != "" || fresh.Header.Version != 0 || len(fresh.Lines) != 0 || fresh.Baseline == nil || fresh.Header.OccurredOn != fresh.Today || fresh.RemainingMinor != nil {
		t.Fatal("new form fabricated a saved identity or amount", fresh, err)
	}
	if before != snapshot() {
		t.Fatal("reading an existing/new form wrote domain/audit/proof state")
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if got, err := f.app.Store.ExpenseBundle(ctx, f.identity, store.String(entry["id"])); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
		t.Fatal("cancelled reader returned partial usable data", got, err)
	}
	if err = f.pool.Ping(f.ctx); err != nil {
		t.Fatal("cancelled read retained an unusable connection", err)
	}
}

func TestExpenseBundleLegacyReferencesTextBytesAndCompleteRowBounds(t *testing.T) {
	f, category, entry, line := expenseIntegrityFixture(t)
	payer := shoppingTestMember(t, f, "legacy-expense-payer", "editor")
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET paid_by_membership_id=$2 WHERE id=$1", entry["id"], payer.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=clock_timestamp() WHERE id=$1", payer.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.categories SET archived_at=clock_timestamp() WHERE id=$1", category["id"]); err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{expenseedit.NoteLimit, expenseedit.NoteLimit + 1} {
		if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET note=$2 WHERE id=$1", line["id"], strings.Repeat("界", length)); err != nil {
			t.Fatal(err)
		}
		bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
		if err != nil || bundle.Header.PaidByMembershipID == nil || *bundle.Header.PaidByMembershipID != payer.MembershipID || bundle.Header.PayerActive ||
			bundle.Header.PayerLabel == "" || !bundle.Lines[0].CategoryArchived || len([]rune(bundle.Lines[0].Note)) != length || bundle.Editable != (length == expenseedit.NoteLimit) {
			t.Fatal("reader dropped inactive references, changed text or disagreed with writer limits", length, bundle, err)
		}
	}
	const byteLimit = 1 << 20
	for _, size := range []int{byteLimit, byteLimit + 1} {
		if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET note=repeat('x',$2) WHERE id=$1", line["id"], size); err != nil {
			t.Fatal(err)
		}
		bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
		if size == byteLimit {
			if err != nil || len(bundle.Lines[0].Note) != size || bundle.Editable {
				t.Fatal("exact byte boundary did not preserve raw read-only history", err)
			}
		} else if err == nil || !reflect.DeepEqual(bundle, store.ExpenseBundle{}) {
			t.Fatal("oversized note produced a partial bundle", bundle, err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET note=repeat('x',11000) WHERE id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by)
 SELECT $1,$2,$3,1,repeat('y',11000),n,$4 FROM generate_series(1,99) n`, f.identity.HouseholdID, entry["id"], category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"])); err == nil || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
		t.Fatal("combined text budget was not enforced before text fetch", got, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entry_lines SET note='bounded' WHERE entry_id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil || len(bundle.Lines) != 100 || len(bundle.Baseline) != 100 || bundle.SumMinor != "1099" || *bundle.RemainingMinor != "-99" {
		t.Fatal("full100-row boundary was truncated or rounded", bundle, err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,created_by)
 VALUES($1,$2,$3,1,'Unseen101st line',$4)`, f.identity.HouseholdID, entry["id"], category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"])); err == nil || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
		t.Fatal("legacy101st row was silently omitted", got, err)
	}
}

func TestExpenseBundleCurrentReaderCalendarAndModuleAuthority(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	reader := shoppingTestMember(t, f, "expense-bundle-reader", "reader")
	_, actualReader, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "Expense bundle reader")
	if err != nil {
		t.Fatal(err)
	}
	zoneName := "Etc/GMT+12"
	if time.Now().UTC().Hour() >= 10 {
		zoneName = "Pacific/Kiritimati"
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.households SET timezone=$2 WHERE id=$1", f.identity.HouseholdID, zoneName); err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"recipes", "pantry"} {
		state := f.app.Store.States()[module]
		if err = f.app.Store.SetModule(f.ctx, f.identity, module, false, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := f.app.Store.ExpenseBundle(f.ctx, actualReader, store.String(entry["id"]))
	zone, zoneErr := time.LoadLocation(zoneName)
	if err != nil || zoneErr != nil || bundle.Editable || bundle.LockReason == "" || bundle.Today != bundle.ReadAt.In(zone).Format(time.DateOnly) || bundle.Today == bundle.ReadAt.UTC().Format(time.DateOnly) {
		t.Fatal("reader authority/calendar depended on unrelated modules or UTC day", bundle, err, zoneErr)
	}
	for _, state := range []struct{ name, block, restore string }{
		{"forced password change", "UPDATE core.users SET must_change_password=true WHERE id=$1", "UPDATE core.users SET must_change_password=false WHERE id=$1"},
		{"disabled account", "UPDATE core.users SET disabled_at=clock_timestamp() WHERE id=$1", "UPDATE core.users SET disabled_at=NULL WHERE id=$1"},
	} {
		t.Run(state.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, state.block, actualReader.UserID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, state.restore, actualReader.UserID); err != nil {
					t.Error(err)
				}
			})
			if got, err := f.app.Store.ExpenseBundle(f.ctx, actualReader, ""); !errors.Is(err, store.ErrForbidden) || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
				t.Fatal("current account restriction did not precede empty-form metadata", got, err)
			}
		})
	}
	forged := actualReader
	forged.Role = "admin"
	if got, err := f.app.Store.ExpenseBundle(f.ctx, forged, store.String(entry["id"])); !errors.Is(err, store.ErrForbidden) || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
		t.Fatal("forged current role was accepted", got, err)
	}
	forged = actualReader
	forged.HouseholdID = value.UUID()
	if _, err = f.app.Store.ExpenseBundle(f.ctx, forged, store.String(entry["id"])); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("foreign household identity accepted", err)
	}
	if _, err = f.app.Store.ExpenseBundle(f.ctx, actualReader, value.UUID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing entry was not private not-found", err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", actualReader.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ExpenseBundle(f.ctx, actualReader, ""); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session obtained even empty form metadata", err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='expenses'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, ""); !errors.Is(err, store.ErrNotFound) || !reflect.DeepEqual(got, store.ExpenseBundle{}) {
		t.Fatal("reader trusted cached activation over current Expenses state", got, err)
	}
}

func TestExpenseBundlePostedReversalAndPayeeByteBoundaryStayUnmodified(t *testing.T) {
	f, category, entry, line := expenseIntegrityFixture(t)
	const byteLimit = 1 << 20
	for _, size := range []int{byteLimit - len(store.String(line["note"])), byteLimit - len(store.String(line["note"])) + 1, byteLimit + 1} {
		if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET payee=repeat('p',$2) WHERE id=$1", entry["id"], size); err != nil {
			t.Fatal(err)
		}
		bundle, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
		if size+len(store.String(line["note"])) == byteLimit {
			if err != nil || len(bundle.Header.Payee) != size || bundle.Editable {
				t.Fatal("exact combined payee/note boundary did not preserve raw content", err)
			}
		} else if err == nil || !reflect.DeepEqual(bundle, store.ExpenseBundle{}) {
			t.Fatal("combined/individual payee overflow returned usable content", err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE expenses.entries SET payee='Preserved payee' WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	if err := expenseIntegrityAppSQL(t, f, "UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1 WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	posted, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil || posted.Editable || posted.Header.State != "posted" || posted.Header.PostedAt == nil || posted.Header.Payee != "Preserved payee" || posted.SumMinor != "1000" {
		t.Fatal("reader normalized or rejected historical posted facts", posted, err)
	}
	reversal := value.UUID()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,description,amount_minor,kind,state,posted_at,reverses_entry_id,created_by)
 VALUES($1,$2,'2026-10-26','Historical reversal',-1000,'reversal','posted',clock_timestamp(),$3,$4)`, reversal, f.identity.HouseholdID, entry["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,created_by)
 VALUES($1,$2,$3,-1000,'Exact historic opposite',$4)`, f.identity.HouseholdID, reversal, category["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	historical, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, reversal)
	if err != nil || historical.Editable || historical.Header.Kind != "reversal" || historical.Header.AmountMinor != "-1000" || historical.SumMinor != "-1000" ||
		historical.Header.ReversesEntryID == nil || *historical.Header.ReversesEntryID != store.String(entry["id"]) || historical.RemainingMinor == nil || *historical.RemainingMinor != "0" {
		t.Fatal("reversal was passed through a draft-only validator", historical, err)
	}
}

func TestExpenseBundleKeepsOneSnapshotAcrossBlockedContentRead(t *testing.T) {
	f, _, entry, line := expenseIntegrityFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	writer, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(f.ctx) }()
	if _, err = writer.Exec(ctx, "LOCK TABLE expenses.entries IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	var writerPID int32
	if err = writer.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		bundle store.ExpenseBundle
		err    error
	}
	returned := make(chan result, 1)
	finished := false
	go func() {
		bundle, err := f.app.Store.ExpenseBundle(ctx, f.identity, store.String(entry["id"]))
		returned <- result{bundle: bundle, err: err}
	}()
	defer func() {
		_ = writer.Rollback(f.ctx)
		cancel()
		if !finished {
			select {
			case <-returned:
			case <-time.After(5 * time.Second):
				t.Error("bundle reader did not stop after its blocking writer released")
			}
		}
	}()
	blocked := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case early := <-returned:
			finished = true
			t.Fatal("reader returned before blocked metadata observation", early)
		default:
		}
		// Observe through the reader's role; pg_stat_activity hides another
		// role's query/wait details from the non-superuser migration owner.
		if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE a.datname=current_database()
 AND $1::int=ANY(pg_blocking_pids(a.pid)) AND a.wait_event_type='Lock' AND a.query LIKE '%e.paid_by_membership_id%')`, writerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		var observed string
		if err = f.pool.QueryRow(ctx, `SELECT coalesce(string_agg(a.state||':'||coalesce(a.wait_event_type,'')||':'||left(a.query,1200),E'\n'),'none')
 FROM pg_stat_activity a WHERE a.datname=current_database() AND $1::int=ANY(pg_blocking_pids(a.pid))`, writerPID).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		t.Fatal("reader did not establish its scope snapshot before content blocking; actual blocked statement:", observed)
	}
	if _, err = writer.Exec(ctx, "UPDATE expenses.entries SET payee='Changed after snapshot',version=version+1 WHERE id=$1", entry["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(ctx, "UPDATE expenses.entry_lines SET note='Changed after snapshot',version=version+1 WHERE id=$1", line["id"]); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	old := <-returned
	finished = true
	if old.err != nil || old.bundle.Header.Payee != "" || old.bundle.Header.Version != 2 || len(old.bundle.Lines) != 1 || old.bundle.Lines[0].Note != store.String(line["note"]) {
		t.Fatal("one reader mixed its scope/content/text snapshots", old)
	}
	fresh, err := f.app.Store.ExpenseBundle(f.ctx, f.identity, store.String(entry["id"]))
	if err != nil || fresh.Header.Payee != "Changed after snapshot" || fresh.Header.Version != 3 || fresh.Lines[0].Note != "Changed after snapshot" {
		t.Fatal("subsequent request did not observe committed current content", fresh, err)
	}
}
