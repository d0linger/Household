//go:build integration

package web

import (
	"errors"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
)

func TestNotificationFoundationConsentIsolationAndDedupe(t *testing.T) {
	f := newIntegrationFixture(t)
	preferences, err := f.app.Store.NotificationPreferences(f.ctx, f.identity)
	if err != nil || len(preferences) != 13 {
		t.Fatalf("read explicit defaults: %v, %d", err, len(preferences))
	}
	for _, p := range preferences {
		if p.Enabled || p.Version != 0 || p.EnabledSince != nil {
			t.Fatal("migration silently opted an account in")
		}
	}
	p := store.NotificationPreference{Category: "test", Enabled: true, QuietHours: notification.QuietHours{Timezone: "Europe/Vienna"}}
	p, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p)
	if err != nil || p.Version != 1 || p.EnabledSince == nil {
		t.Fatalf("first consent: %v %+v", err, p)
	}
	stale := p
	stale.Version = 0
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale preference accepted", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.NotificationPreferences(f.ctx, foreign); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("foreign household accepted", err)
	}
	forged := f.identity
	forged.UserID = value.UUID()
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, forged, p); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("forged account accepted", err)
	}
	var now time.Time
	if err = f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	intent := notification.Intent{Schema: 1, HouseholdID: f.identity.HouseholdID, UserID: f.identity.UserID,
		ResourceType: "test", ResourceID: value.UUID(), Revision: 1, Occurrence: "explicit-test", Category: "test", EventAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute)}
	enqueue := func(i notification.Intent) (string, error) {
		tx, e := audit.Begin(audit.WithSystem(f.ctx, "worker"), f.pool)
		if e != nil {
			return "", e
		}
		defer func() { _ = tx.Rollback(f.ctx) }()
		id, e := store.EnqueueNotificationIntent(f.ctx, tx, i)
		if e != nil {
			return "", e
		}
		return id, tx.Commit(f.ctx)
	}
	old := intent
	old.EventAt = p.EnabledSince.Add(-time.Second)
	old.Occurrence = "old-event"
	if id, e := enqueue(old); e != nil || id != "" {
		t.Fatal("pre-consent history replayed", id, e)
	}
	var wg sync.WaitGroup
	results := make(chan string, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() { id, e := enqueue(intent); results <- id; failures <- e })
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal("concurrent intent", e)
		}
	}
	created := 0
	for id := range results {
		if id != "" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent insert created %d intents", created)
	}
	intent.Revision = 2
	if id, e := enqueue(intent); e != nil || id == "" {
		t.Fatal("new revision not inserted", e)
	}
	tx, err := audit.Begin(audit.WithSystem(f.ctx, "worker"), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CancelNotificationSource(f.ctx, tx, intent.HouseholdID, "", intent.ResourceType, intent.ResourceID, 2); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var pending, cancelled int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='cancelled') FROM core.notification_intents`).Scan(&pending, &cancelled); err != nil || pending != 1 || cancelled != 1 {
		t.Fatal("source revision cancellation", pending, cancelled, err)
	}
	// Two writers with one version: exactly one succeeds, without a lock upgrade deadlock.
	writeResults := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, e := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p); writeResults <- e })
	}
	wg.Wait()
	close(writeResults)
	succeeded, conflicted := 0, 0
	for e := range writeResults {
		if e == nil {
			succeeded++
		} else if errors.Is(e, store.ErrConflict) {
			conflicted++
		} else {
			t.Fatal("concurrent preference", e)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal("lost optimistic update", succeeded, conflicted)
	}
	p.Version++
	p.Enabled = false
	p, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p)
	if err != nil || p.EnabledSince != nil {
		t.Fatal("revoke consent", err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE state='pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("revocation retained pending work", pending, err)
	}
	intent.Revision = 3
	if id, e := enqueue(intent); e != nil || id != "" {
		t.Fatal("disabled account queued", id, e)
	}
	p.Enabled = true
	p, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p)
	if err != nil {
		t.Fatal(err)
	}
	if id, e := enqueue(intent); e != nil || id != "" {
		t.Fatal("reenabling replayed old event", id, e)
	}
	var auditCount int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE resource_type='notification_preferences'`).Scan(&auditCount); err != nil || auditCount < 1 {
		t.Fatal("consent audit missing", auditCount, err)
	}
	// Hold an authorized enqueue uncommitted while consent is revoked. The
	// producer's consent lock makes the revocation see its committed work.
	if err = f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	intent.EventAt = now
	intent.NotBefore = now
	intent.ExpiresAt = now.Add(time.Minute)
	intent.Revision = 4
	tx, err = audit.Begin(audit.WithSystem(f.ctx, "worker"), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if id, e := store.EnqueueNotificationIntent(f.ctx, tx, intent); e != nil || id == "" {
		t.Fatal("prepare concurrent enqueue", e)
	}
	p.Enabled = false
	revoked := make(chan error, 1)
	go func() { _, e := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p); revoked <- e }()
	select {
	case e := <-revoked:
		t.Fatal("revocation bypassed in-flight producer consent lock", e)
	case <-time.After(50 * time.Millisecond):
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-revoked:
		if e != nil {
			t.Fatal("concurrent revocation", e)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("revocation did not complete")
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE state='pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("producer committed pending work after revocation", pending, err)
	}
	for _, state := range []struct{ name, apply, restore string }{
		{"disabled user", `UPDATE core.users SET disabled_at=clock_timestamp() WHERE id=$1`, `UPDATE core.users SET disabled_at=NULL WHERE id=$1`},
		{"forced password change", `UPDATE core.users SET must_change_password=true WHERE id=$1`, `UPDATE core.users SET must_change_password=false WHERE id=$1`},
	} {
		t.Run(state.name, func(t *testing.T) {
			if _, e := f.owner.Exec(f.ctx, state.apply, f.identity.UserID); e != nil {
				t.Fatal(e)
			}
			defer func() {
				if _, e := f.owner.Exec(f.ctx, state.restore, f.identity.UserID); e != nil {
					t.Error(e)
				}
			}()
			if _, e := f.app.Store.NotificationPreferences(f.ctx, f.identity); !errors.Is(e, store.ErrForbidden) {
				t.Fatal("ineligible identity read preferences", e)
			}
			if _, e := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p); !errors.Is(e, store.ErrForbidden) {
				t.Fatal("ineligible identity changed preferences", e)
			}
		})
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1`, f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.NotificationPreferences(f.ctx, f.identity); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session read preferences", err)
	}
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, p); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session changed preferences", err)
	}
}
