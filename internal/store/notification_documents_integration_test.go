//go:build integration

package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/value"
)

func notificationDocumentSettings(t *testing.T, f *recipeExpansionFixture, deadline, member string, enabled bool) DocumentCommand {
	t.Helper()
	out, err := f.store.DocumentNotificationSettings(f.ctx, f.identity, deadline)
	if err != nil {
		t.Fatal(err)
	}
	n := out["settings"].(Record)
	return documentNativeCommand(t, f, "notification", String(n["document_id"]), Int(n["document_version"]), Record{
		"deadline_id": deadline, "deadline_version": n["deadline_version"], "settings_version": n["version"],
		"responsible_member_id": member, "enabled": enabled})
}

func notificationDocumentScan(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2`, f.identity.HouseholdID, DocumentNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		n, err := f.store.StageDocumentNotifications(f.ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("bounded document scan did not finish its three source kinds")
}

func TestNotificationDocumentsSettingsAndCalendarCutoffs(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	reader := financeAuthorityMember(t, f, "reader")
	d := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("SYNTHETIC private-looking deadline title"))
	docID := String(d["id"])
	due := time.Now().UTC().AddDate(0, 0, 3).Format(time.DateOnly)
	x := documentNativeWrite(t, f, "deadline", docID, Int(d["version"]), Record{
		"deadline_id": "", "deadline_version": 0, "kind": "cancellation", "due_on": due,
		"label": "SYNTHETIC confidential label", "remind_days": 0, "source": "Reviewed original", "confirmed": true, "cancelled": false})
	deadline := String(x["deadline_id"])
	view, err := f.store.DocumentNotificationSettings(f.ctx, reader, deadline)
	if err != nil || Int(view["settings"].(Record)["version"]) != 0 {
		t.Fatal("actual reader/default settings", view, err)
	}
	command := notificationDocumentSettings(t, f, deadline, reader.MembershipID, true)
	if _, err = f.store.DocumentWrite(f.ctx, reader, value.UUID(), value.UUID(), command); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader changed settings", err)
	}
	key := value.UUID()
	first, err := f.store.DocumentWrite(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.DocumentWrite(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil || Int(first["notification_settings"].(Record)["version"]) != Int(replay["notification_settings"].(Record)["version"]) {
		t.Fatal("durable exact replay", err)
	}
	if _, err = f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("stale settings overwritten", err)
	}
	missing := notificationDocumentSettings(t, f, deadline, "", true)
	if _, err = f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), missing); err == nil {
		t.Fatal("unassigned notification enabled")
	}
	view, err = f.store.DocumentNotificationSettings(f.ctx, f.identity, deadline)
	if err != nil || Int(view["settings"].(Record)["document_version"]) != command.Version || Int(view["settings"].(Record)["deadline_version"]) != 1 {
		t.Fatal("configuration changed source revision", view, err)
	}
	var zone string
	if err = f.app.QueryRow(f.ctx, `SELECT timezone FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	at, err := documentNotificationStage(due, zone, 0)
	if err != nil {
		t.Fatal(err)
	}
	read := func(sample time.Time) *notification.Intent {
		t.Helper()
		tx, e := f.app.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer rollback(tx)
		intent, e := documentDeadlineIntent(f.ctx, tx, f.identity.HouseholdID, deadline, sample)
		if e != nil {
			t.Fatal(e)
		}
		return intent
	}
	if read(at.Add(-time.Nanosecond)) != nil || read(at.Add(24*time.Hour)) != nil {
		t.Fatal("outside original stage freshness")
	}
	intent := read(at)
	if intent == nil || intent.UserID != reader.UserID || intent.Occurrence != "deadline/1/"+due || intent.Validate() != nil {
		t.Fatal("explicit recipient or civil deadline differs", intent)
	}
	// Ordinary metadata changes do not invent another source event or silence it.
	metadata := documentNativeMetadata("Edited synthetic title")
	documentNativeWrite(t, f, "save", docID, command.Version, metadata)
	if next := read(at); next == nil || next.Occurrence != intent.Occurrence {
		t.Fatal("unrelated metadata changed deadline event", next)
	}
	detail, err := f.store.DocumentDetail(f.ctx, f.identity, docID)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "deadline", docID, Int(detail["version"]), Record{
		"deadline_id": deadline, "deadline_version": 1, "kind": "cancellation", "due_on": due,
		"label": "SYNTHETIC confidential label", "remind_days": 0, "source": "Reviewed original", "confirmed": true, "cancelled": true})
	if read(at) != nil {
		t.Fatal("cancelled deadline remains eligible")
	}
	var count int
	var contentFree bool
	err = f.app.QueryRow(f.ctx, `SELECT count(*),coalesce(bool_and(NOT changes ? 'responsible_member_id'
 AND NOT changes ? 'title' AND NOT changes ? 'label'),false) FROM core.audit_log
 WHERE household_id=$1 AND module_id='documents' AND action LIKE 'notification_settings_%'`, f.identity.HouseholdID).Scan(&count, &contentFree)
	if err != nil || count != 1 || !contentFree {
		t.Fatal("content-free exact settings audit", count, contentFree, err)
	}
}

func TestNotificationDocumentsNoElapsedReplayAndOwnedClocks(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	// Put local time in10:xx so today's09:00 stage is recent but already elapsed.
	now := time.Now().UTC()
	offset := (10-now.Hour()+36)%24 - 12
	zone := fmt.Sprintf("Etc/GMT%+d", -offset)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.households SET timezone=$2 WHERE id=$1`, f.identity.HouseholdID, zone); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	d := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("Elapsed synthetic deadline"))
	x := documentNativeWrite(t, f, "deadline", String(d["id"]), Int(d["version"]), Record{
		"deadline_id": "", "deadline_version": 0, "kind": "expiry", "due_on": now.In(loc).Format(time.DateOnly),
		"label": "Elapsed", "remind_days": 0, "source": "Explicit recent confirmation", "confirmed": true, "cancelled": false})
	deadline := String(x["deadline_id"])
	command := notificationDocumentSettings(t, f, deadline, f.identity.MembershipID, true)
	if _, err = f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), command); err != nil {
		t.Fatal(err)
	}
	notificationChoreConsent(t, f, f.identity, "documents", false)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, e := f.store.StageDocumentNotifications(f.ctx, 1); results <- e })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var jobs int
	var clean bool
	err = f.app.QueryRow(f.ctx, `SELECT count(*),bool_and(last_error IS NULL) FROM core.jobs
 WHERE household_id=$1 AND kind=$2`, f.identity.HouseholdID, DocumentNotificationScanJobKind).Scan(&jobs, &clean)
	if err != nil || jobs != 1 || !clean {
		t.Fatal("concurrent scanners must preserve one healthy durable cursor", jobs, clean, err)
	}
	notificationDocumentScan(t, f)
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE category='documents'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("elapsed stage replayed", count, err)
	}
	tx, err := audit.Begin(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), f.app)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	for _, statement := range []string{
		`UPDATE documents.notification_settings SET responsible_since='2000-01-01',updated_at='2000-01-01' WHERE id=$1`,
		`UPDATE documents.deadlines SET notification_updated_at='2000-01-01' WHERE id=$1`,
		`UPDATE documents.documents SET notification_active_since='2000-01-01' WHERE id=(SELECT document_id FROM documents.deadlines WHERE id=$1)`,
	} {
		if _, err = tx.Exec(f.ctx, statement, deadline); err != nil {
			t.Fatal(err)
		}
	}
	var safe bool
	err = tx.QueryRow(f.ctx, `SELECT n.responsible_since>clock_timestamp()-interval '5 minutes'
 AND x.notification_updated_at>clock_timestamp()-interval '5 minutes' AND d.notification_active_since>clock_timestamp()-interval '5 minutes'
 FROM documents.notification_settings n JOIN documents.deadlines x ON x.id=n.id JOIN documents.documents d ON d.id=x.document_id WHERE n.id=$1`, deadline).Scan(&safe)
	if err != nil || !safe {
		t.Fatal("source history could be forged", safe, err)
	}
}

func TestNotificationDocumentsFailureEpochsAndFairScan(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	notificationChoreConsent(t, f, f.identity, "documents", false)
	d := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("SYNTHETIC sensitive import"))
	docID := String(d["id"])
	// Historical dates remain valid document facts, but cannot poison or starve
	// the current notification scan when opted in.
	historical := documentNativeWrite(t, f, "deadline", docID, Int(d["version"]), Record{
		"deadline_id": "", "deadline_version": 0, "kind": "custom", "due_on": "1900-01-01",
		"label": "Historical fact", "remind_days": 0, "source": "Reviewed historical record", "confirmed": true, "cancelled": false})
	settings := notificationDocumentSettings(t, f, String(historical["deadline_id"]), f.identity.MembershipID, true)
	if _, err := f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), settings); err != nil {
		t.Fatal(err)
	}
	generation := f.store.States()["documents"].Generation
	pid, jid := value.UUID(), value.UUID()
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	tx, err := audit.Begin(ctx, f.app)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key)
 VALUES($1,$2,'documents',$3,'document_processing','{}',$4)`, jid, f.identity.HouseholdID, generation, "notification-doc-test/"+pid)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO documents.processing(id,household_id,document_id,operation,input,job_id,module_generation,requested_by)
 VALUES($1,$2,$3,'scan','{}',$4,$5,$6)`, pid, f.identity.HouseholdID, docID, jid, generation, f.identity.UserID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		rollback(tx)
	}
	if err != nil {
		t.Fatal(err)
	}
	fail := func() {
		t.Helper()
		if _, e := audit.Exec(ctx, f.app, `UPDATE documents.processing SET state='failed',status_code='local_processing_failed',updated_at=clock_timestamp() WHERE id=$1`, pid); e != nil {
			t.Fatal(e)
		}
	}
	fail()
	source := documentNativeWrite(t, f, "integration", "", 0, Record{"adapter": "watch", "enabled": true})
	if _, err = audit.Exec(ctx, f.app, `UPDATE documents.integration_sources SET failures=1,status_code='input_failed' WHERE id=$1`, source["id"]); err != nil {
		t.Fatal(err)
	}
	notificationDocumentScan(t, f)
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE category='documents' AND user_id=$1`, f.identity.UserID).Scan(&count); err != nil || count != 2 {
		t.Fatal("personal bounded source staging", count, err)
	}
	var eventAt time.Time
	if err = f.app.QueryRow(f.ctx, `SELECT occurred_at FROM documents.notification_processing_events WHERE id=$1`, pid).Scan(&eventAt); err != nil {
		t.Fatal(err)
	}
	fail()
	if _, err = audit.Exec(ctx, f.app, `UPDATE documents.integration_sources SET failures=1000,next_poll_at=clock_timestamp() WHERE id=$1`, source["id"]); err != nil {
		t.Fatal(err)
	}
	notificationDocumentScan(t, f)
	var repeated time.Time
	var epoch int64
	if err = f.app.QueryRow(f.ctx, `SELECT occurred_at,epoch FROM documents.notification_processing_events WHERE id=$1`, pid).Scan(&repeated, &epoch); err != nil || !repeated.Equal(eventAt) || epoch != 1 {
		t.Fatal("same failure refreshed its event", repeated, epoch, err)
	}
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE category='documents'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("repeated failure duplicated delivery intent", count, err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE documents.notification_processing_events SET occurred_at='2000-01-01' WHERE id=$1`, pid); err == nil {
		t.Fatal("app forged operational event")
	}
	// The real retry command retains original requester and invalidates old work.
	detail, err := f.store.DocumentDetail(f.ctx, f.identity, docID)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "retry", docID, Int(detail["version"]), Record{"processing_id": pid})
	var active bool
	if err = f.app.QueryRow(f.ctx, `SELECT active FROM documents.notification_processing_events WHERE id=$1`, pid).Scan(&active); err != nil || active {
		t.Fatal("retry preserved old failure", active, err)
	}
	fail()
	notificationDocumentScan(t, f)
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE category='documents' AND resource_id=$1`, pid).Scan(&count); err != nil || count != 2 {
		t.Fatal("new retry failure missing distinct epoch", count, err)
	}
	if _, err = audit.Exec(ctx, f.app, `UPDATE documents.integration_sources SET failures=0,status_code='ready' WHERE id=$1`, source["id"]); err != nil {
		t.Fatal(err)
	}
	tx, err = f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if intent, e := documentFailureIntent(f.ctx, tx, f.identity.HouseholdID, "import", String(source["id"]), time.Now()); e != nil || intent != nil {
		t.Fatal("recovered import remains eligible", intent, e)
	}
}
