//go:build integration

package store

import (
	"errors"
	"sync"
	"testing"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/securityevent"
	"household/internal/value"
)

func notificationSecurityEvent(t *testing.T, f *recipeExpansionFixture, kind string, commit bool) string {
	t.Helper()
	tx, err := audit.Begin(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), f.app)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if err = securityevent.Record(f.ctx, tx, f.identity.HouseholdID, f.identity.UserID, kind); err != nil {
		t.Fatal(err)
	}
	var event string
	if err = tx.QueryRow(f.ctx, `SELECT id::text FROM core.notifications WHERE household_id=$1 AND user_id=$2
 AND kind=$3 AND created_at>=transaction_timestamp() ORDER BY created_at DESC,id DESC LIMIT 1`, f.identity.HouseholdID, f.identity.UserID, "security_"+kind).Scan(&event); err != nil {
		t.Fatal(err)
	}
	if commit {
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	return event
}

func notificationSecurityIntentFor(t *testing.T, f *recipeExpansionFixture, event string) notification.Intent {
	t.Helper()
	var id string
	if err := f.app.QueryRow(f.ctx, "SELECT id::text FROM core.notification_intents WHERE resource_type='security_event' AND resource_id=$1", event).Scan(&id); err != nil {
		t.Fatal(err)
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	i, err := ReadNotificationIntent(f.ctx, tx, id)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestNotificationSecurityAtomicPersonalSourceConsentAndFinalGuards(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	rolled := notificationSecurityEvent(t, f, "passkey_added", false)
	var count int
	if err := f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.notifications WHERE id=$1)+
 (SELECT count(*) FROM core.jobs WHERE dedupe_key='security-event-job/'||$1::text)`, rolled).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back security change left work", err)
	}
	old := notificationSecurityEvent(t, f, "password_changed", true)
	if _, err := f.store.StageSecurityNotifications(f.ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE resource_id=$1", old).Scan(&count); err != nil || count != 0 {
		t.Fatal("unconsented security event was staged", err)
	}
	notificationChoreConsent(t, f, f.identity, "security", false)
	if _, err := f.store.StageSecurityNotifications(f.ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE resource_id=$1", old).Scan(&count); err != nil || count != 0 {
		t.Fatal("new consent replayed inbox history", err)
	}
	var retained bool
	if err := f.app.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.notifications WHERE id=$1 AND dismissed_at IS NULL)", old).Scan(&retained); err != nil || !retained {
		t.Fatal("missing consent removed personal inbox", err)
	}
	fresh := notificationSecurityEvent(t, f, "passkey_removed", true)
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for range 2 {
		group.Go(func() { _, err := f.store.StageSecurityNotifications(f.ctx, 50); failures <- err })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent source processing failed", err)
		}
	}
	if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE resource_id=$1", fresh).Scan(&count); err != nil || count != 1 {
		t.Fatal("security source was lost or duplicated", err)
	}
	i := notificationSecurityIntentFor(t, f, fresh)
	if !securityNotificationReference(i) || i.UserID != f.identity.UserID || NotificationTarget(i) != "/settings/security" {
		t.Fatal("security source authority changed")
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.store.NotificationAudience(f.ctx, tx, i)
	if err != nil || !allowed {
		t.Fatal("actual current source rejected", err)
	}
	cutoff, err := f.store.NotificationConsentCutoff(f.ctx, tx, i)
	if err != nil || !cutoff.Equal(i.EventAt) {
		t.Fatal("security consent clock shifted", err)
	}
	wrong := i
	wrong.UserID = value.UUID()
	allowed, err = f.store.NotificationAudience(f.ctx, tx, wrong)
	if err != nil || allowed {
		t.Fatal("another account gained source authority", err)
	}
	rollback(tx)
	if _, err = f.app.Exec(f.ctx, "UPDATE core.notifications SET dismissed_at=clock_timestamp() WHERE id=$1 AND user_id=$2", fresh, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	tx, err = f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	allowed, err = f.store.NotificationAudience(f.ctx, tx, i)
	if err != nil || allowed {
		t.Fatal("dismissed source still authorized delivery", err)
	}
	cutoff, err = f.store.NotificationConsentCutoff(f.ctx, tx, i)
	if err != nil || !cutoff.IsZero() {
		t.Fatal("dismissed source retained consent cutoff", err)
	}
}

func TestNotificationSecurityClosedJobBackoffAndExhaustion(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if _, err := f.store.StageSecurityNotifications(f.ctx, 50); err != nil {
		t.Fatal(err)
	}
	job := value.UUID()
	if _, err := audit.Exec(audit.WithSystem(f.ctx, "worker"), f.app, `INSERT INTO core.jobs(id,household_id,kind,payload,dedupe_key,max_attempts)
 VALUES($1,$2,$3,'{"event_id":"not-a-record","extra":"synthetic-private-marker"}',$4,2)`, job, f.identity.HouseholdID, securityevent.JobKind, "security-event-job/"+value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StageSecurityNotifications(f.ctx, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("malformed source not rejected", err)
	}
	var state, code string
	var attempts int
	var future bool
	if err := f.app.QueryRow(f.ctx, "SELECT state,last_error,attempts,available_at>clock_timestamp() FROM core.jobs WHERE id=$1", job).Scan(&state, &code, &attempts, &future); err != nil || state != "pending" || attempts != 1 || !future || code != "security notification source failed" {
		t.Fatal("bounded safe retry missing", err)
	}
	if n, err := f.store.StageSecurityNotifications(f.ctx, 1); err != nil || n != 0 {
		t.Fatal("retry ignored backoff", n, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.jobs SET available_at=clock_timestamp() WHERE id=$1", job); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StageSecurityNotifications(f.ctx, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("second malformed attempt accepted", err)
	}
	if err := f.app.QueryRow(f.ctx, "SELECT state,attempts FROM core.jobs WHERE id=$1", job).Scan(&state, &attempts); err != nil || state != "failed" || attempts != 2 {
		t.Fatal("retry exhaustion missing", err)
	}
}
