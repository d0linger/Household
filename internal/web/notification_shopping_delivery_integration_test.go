//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
)

func TestNotificationShoppingChannelOwnershipAndFinalLegacyGuard(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	type episode struct{ list, job, notice, legacy, core string }
	watch := func(t *testing.T, id auth.Identity, list string, mute int64) {
		t.Helper()
		gen := f.app.Store.States()["shopping"].Generation
		p, e := f.app.Store.ShoppingNotificationPreferences(f.ctx, id, list, gen)
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.app.Store.SetShoppingNotificationPreferences(f.ctx, id, list, value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: p.Revision, Enabled: true, PushEnabled: true, MuteForSeconds: mute})
		if e != nil {
			t.Fatal(e)
		}
	}
	produce := func(t *testing.T, id auth.Identity) episode {
		t.Helper()
		list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Private central-channel list"})
		lid := store.String(list["id"])
		watch(t, id, lid, 0)
		shoppingPushChange(t, f, lid)
		return episode{list: lid}
	}
	complete := func(t *testing.T, id auth.Identity, out episode) episode {
		t.Helper()
		shoppingExtensionStage(t, f)
		lease := value.UUID()
		if e := f.owner.QueryRow(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$4,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE kind=$1 AND state='pending' AND payload->>'recipient_id'=$2 AND payload->>'list_id'=$3 RETURNING id::text`, store.ShoppingDigestJobKind, id.UserID, out.list, lease).Scan(&out.job); e != nil {
			t.Fatal(e)
		}
		if e := f.app.Store.CompleteShoppingNotificationJob(f.ctx, out.job, lease); e != nil {
			t.Fatal(e)
		}
		if e := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1' AND dedupe_key LIKE $2`, id.UserID, "%/"+out.list+"/%").Scan(&out.notice); e != nil {
			t.Fatal(e)
		}
		var legacy, core []string
		if e := f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.jobs WHERE kind=$1 AND payload->>'notification_id'=$2),ARRAY(SELECT id::text FROM core.notification_intents WHERE category='shopping' AND resource_id=$3)`, store.ShoppingPushJobKind, out.notice, out.job).Scan(&legacy, &core); e != nil {
			t.Fatal(e)
		}
		if len(legacy) > 1 || len(core) > 1 || len(legacy)+len(core) > 1 {
			t.Fatal("one digest used multiple outbound branches", len(legacy), len(core))
		}
		if len(legacy) == 1 {
			out.legacy = legacy[0]
		}
		if len(core) == 1 {
			out.core = core[0]
		}
		return out
	}
	register := func(t *testing.T, id auth.Identity) string {
		t.Helper()
		c, e := service.Register(f.ctx, id, shoppingPushInput(t, service))
		if e != nil {
			t.Fatal(e)
		}
		return c.ID
	}
	claimLegacy := func(t *testing.T, job string) string {
		t.Helper()
		lease := value.UUID()
		tag, e := f.owner.Exec(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '30 seconds',attempts=attempts+1 WHERE id=$1 AND kind=$3 AND state='pending'`, job, lease, store.ShoppingPushJobKind)
		if e != nil || tag.RowsAffected() != 1 {
			t.Fatal("claim actual legacy job", e)
		}
		return lease
	}
	var coreReader auth.Identity
	var coreConnection string
	var corePreference store.NotificationPreference
	var ready episode
	t.Run("row presence selects channel and never falls back", func(t *testing.T) {
		coreReader = shoppingTestMember(t, f, "central-shopping-reader", "reader")
		coreConnection = register(t, coreReader)
		legacy := complete(t, coreReader, produce(t, coreReader))
		if legacy.legacy == "" || legacy.core != "" {
			t.Fatal("missing central row did not preserve legacy")
		}
		var calls atomic.Int32
		if e := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("centrally owned legacy job reached provider")
		})); e != nil {
			t.Fatal(e)
		}
		var e error
		corePreference, e = f.app.Store.SaveNotificationPreference(f.ctx, coreReader, store.NotificationPreference{Category: "shopping", Enabled: false, QuietHours: notification.QuietHours{Timezone: "UTC"}})
		if e != nil {
			t.Fatal(e)
		}
		if e = service.SendJob(f.ctx, legacy.legacy, claimLegacy(t, legacy.legacy)); e != nil {
			t.Fatal(e)
		}
		if calls.Load() != 0 {
			t.Fatal("pending legacy survived first central save")
		}
		disabled := complete(t, coreReader, produce(t, coreReader))
		if disabled.legacy != "" || disabled.core != "" {
			t.Fatal("central opt-out fell back")
		}
		beforeConsent := produce(t, coreReader)
		corePreference.Enabled = true
		corePreference, e = f.app.Store.SaveNotificationPreference(f.ctx, coreReader, corePreference)
		if e != nil {
			t.Fatal(e)
		}
		old := complete(t, coreReader, beforeConsent)
		if old.legacy != "" || old.core != "" {
			t.Fatal("central enable replayed historical change")
		}
		withoutDevice := complete(t, coreReader, produce(t, coreReader))
		if withoutDevice.core == "" || withoutDevice.legacy != "" {
			t.Fatal("absent device category re-enabled legacy")
		}
		if _, e = service.SaveDevicePreference(f.ctx, coreReader, coreConnection, webpush.DevicePreference{Category: "shopping", Enabled: true}); e != nil {
			t.Fatal(e)
		}
		ready = complete(t, coreReader, produce(t, coreReader))
		if ready.core == "" || ready.legacy != "" {
			t.Fatal("central branch not selected")
		}
	})
	blockers := []auth.Identity{f.identity, shoppingTestMember(t, f, "shopping-slot-blocker", "reader")}
	connections := []string{register(t, blockers[0]), register(t, blockers[1])}
	for _, action := range []string{"first-central-save", "mute", "logout"} {
		t.Run(action+" while legacy send waits for transport slot", func(t *testing.T) {
			reader := shoppingTestMember(t, f, "shopping-slot-"+action, "reader")
			register(t, reader)
			source := complete(t, reader, produce(t, reader))
			if source.legacy == "" {
				t.Fatal("legacy job missing")
			}
			legacyLease := claimLegacy(t, source.legacy)
			type dispatch struct{ job, lease string }
			occupied := make([]dispatch, 0, 2)
			for n, id := range blockers {
				v, e := service.QueueTest(f.ctx, id, connections[n])
				if e != nil {
					t.Fatal(e)
				}
				job, lease := notificationClaimFixture(t, f, v.ID)
				occupied = append(occupied, dispatch{job, lease})
			}
			entered := make(chan struct{}, 3)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var calls atomic.Int32
			if e := service.SetHTTPClientForTests(shoppingControlledPushClient(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				entered <- struct{}{}
				select {
				case <-release:
					return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			})); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			var wg sync.WaitGroup
			results := make(chan error, 3)
			defer func() { unblock(); cancel(); wg.Wait() }()
			for _, d := range occupied {
				wg.Go(func() { results <- service.SendNotificationJob(ctx, d.job, d.lease) })
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("controlled generic send failed to occupy slot")
				}
			}
			wg.Go(func() { results <- service.SendJob(ctx, source.legacy, legacyLease) })
			// The unique user's budget row becomes visible only after legacy
			// preflight commits. No production timing hook is needed.
			hash := sha256.Sum256([]byte(reader.UserID))
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
			limit := time.NewTimer(time.Second)
			defer limit.Stop()
			for {
				var committed bool
				if e := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.rate_limit_buckets WHERE scope='webpush-send-user-v1' AND subject_hash=$1 AND attempt_count>0)`, hash[:]).Scan(&committed); e != nil {
					t.Fatal(e)
				}
				if committed {
					break
				}
				select {
				case <-poll.C:
				case <-limit.C:
					t.Fatal("legacy preflight did not commit before final guard")
				}
			}
			switch action {
			case "first-central-save":
				if _, e := f.app.Store.SaveNotificationPreference(f.ctx, reader, store.NotificationPreference{Category: "shopping", Enabled: false, QuietHours: notification.QuietHours{Timezone: "UTC"}}); e != nil {
					t.Fatal(e)
				}
			case "mute":
				watch(t, reader, source.list, 3600)
			case "logout":
				if e := f.app.Auth.Logout(f.ctx, reader); e != nil {
					t.Fatal(e)
				}
			}
			unblock()
			for range 3 {
				if e := <-results; e != nil {
					t.Fatal(e)
				}
			}
			var state, code string
			if e := f.pool.QueryRow(f.ctx, `SELECT state,coalesce(last_error,'') FROM core.jobs WHERE id=$1`, source.legacy).Scan(&state, &code); e != nil || calls.Load() != 2 || state != "done" || code == "gateway_accepted" {
				t.Fatal("stale legacy crossed final guard", state, code, calls.Load(), e)
			}
		})
	}
	t.Run("operational final guard timeout preserves retryable legacy job", func(t *testing.T) {
		reader := shoppingTestMember(t, f, "shopping-guard-timeout", "reader")
		register(t, reader)
		source := complete(t, reader, produce(t, reader))
		if source.legacy == "" {
			t.Fatal("legacy job missing")
		}
		lease := claimLegacy(t, source.legacy)
		lock, e := f.owner.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = lock.Rollback(f.ctx) }()
		if _, e = lock.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/notification-preferences/'||$1::text||'/'||$2::text,0))`, reader.HouseholdID, reader.UserID); e != nil {
			t.Fatal(e)
		}
		var calls atomic.Int32
		if e = service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		})); e != nil {
			t.Fatal(e)
		}
		if e = service.SendJob(f.ctx, source.legacy, lease); e == nil {
			t.Fatal("operational guard timeout was swallowed")
		}
		var state string
		if e = f.pool.QueryRow(f.ctx, `SELECT state FROM core.jobs WHERE id=$1`, source.legacy).Scan(&state); e != nil || state != "running" || calls.Load() != 0 {
			t.Fatal("guard error permanently consumed legacy job", state, calls.Load(), e)
		}
		if e = lock.Rollback(f.ctx); e != nil {
			t.Fatal(e)
		}
		if e = service.SendJob(f.ctx, source.legacy, lease); e != nil {
			t.Fatal("retry after guard recovery", e)
		}
		var code string
		if e = f.pool.QueryRow(f.ctx, `SELECT state,coalesce(last_error,'') FROM core.jobs WHERE id=$1`, source.legacy).Scan(&state, &code); e != nil || state != "done" || code != "gateway_accepted" || calls.Load() != 1 {
			t.Fatal("recovered guard did not dispatch exactly once", state, code, calls.Load(), e)
		}
	})
	t.Run("central episode reaches encrypted provider after real digest boundary", func(t *testing.T) {
		if ready.core == "" {
			t.Fatal("central episode setup failed")
		}
		var due time.Time
		if e := f.pool.QueryRow(f.ctx, `SELECT not_before FROM core.notification_intents WHERE id=$1`, ready.core).Scan(&due); e != nil {
			t.Fatal(e)
		}
		wait := time.Until(due) + 20*time.Millisecond
		if wait > 65*time.Second {
			t.Fatal("digest deadline exceeded one minute")
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
		}
		if e := service.ScheduleNotifications(f.ctx); e != nil {
			t.Fatal(e)
		}
		var delivery string
		if e := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_deliveries WHERE intent_id=$1 AND subscription_id=$2 AND state='pending'`, ready.core, coreConnection).Scan(&delivery); e != nil {
			t.Fatal(e)
		}
		var calls atomic.Int32
		if e := service.SetHTTPClientForTests(shoppingControlledPushClient(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body, e := io.ReadAll(io.LimitReader(r.Body, 8193))
			if e != nil || len(body) < 100 || len(body) > 8192 || bytes.Contains(body, []byte("Private central-channel")) || bytes.Contains(body, []byte("audience_scope")) || r.Header.Get("Urgency") != "low" {
				return nil, errors.New("invalid closed shopping transport")
			}
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		})); e != nil {
			t.Fatal(e)
		}
		job, lease := notificationClaimFixture(t, f, delivery)
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		v, e := service.Delivery(f.ctx, coreReader, delivery)
		if e != nil || v.State != "accepted" || calls.Load() != 1 {
			t.Fatal("central shopping did not deliver", v, e)
		}
		if e = service.ScheduleNotifications(f.ctx); e != nil {
			t.Fatal(e)
		}
		var copies int
		if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_deliveries WHERE intent_id=$1`, ready.core).Scan(&copies); e != nil || copies != 1 {
			t.Fatal("one core episode delivered more than once", copies, e)
		}
	})
}
