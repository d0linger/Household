//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/value"
	"household/internal/webpush"
)

func TestNotificationFinalConsentGuardAfterTransportWait(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	identities := []auth.Identity{f.identity,
		shoppingTestMember(t, f, "notification-slot-second", "reader"),
		shoppingTestMember(t, f, "notification-slot-withdrawn", "reader")}
	type candidate struct{ delivery, connection, job, lease string }
	candidates := make([]candidate, len(identities))
	for index, identity := range identities {
		connection, err := service.Register(f.ctx, identity, shoppingPushInput(t, service))
		if err != nil {
			t.Fatal(err)
		}
		delivery, err := service.QueueTest(f.ctx, identity, connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		job, lease := notificationClaimFixture(t, f, delivery.ID)
		candidates[index] = candidate{delivery.ID, connection.ID, job, lease}
	}
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	var wg sync.WaitGroup
	results := make(chan error, 3)
	defer func() { unblock(); cancel(); wg.Wait() }()
	start := func(c candidate) {
		wg.Go(func() { results <- service.SendNotificationJob(ctx, c.job, c.lease) })
	}
	// Start each occupied slot only after the previous request passed its final
	// guard, avoiding unrelated simultaneous per-host budget reservations.
	for _, c := range candidates[:2] {
		start(c)
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("controlled request did not occupy a transport slot")
		}
	}
	last := candidates[2]
	start(last)
	deadline := time.Now().Add(time.Second)
	for {
		var reserved bool
		if err := f.pool.QueryRow(f.ctx, `SELECT last_attempt_at IS NOT NULL FROM core.notification_deliveries WHERE id=$1`, last.delivery).Scan(&reserved); err != nil {
			t.Fatal(err)
		}
		if reserved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("third delivery did not commit its preflight before slot release")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatal("transport did not enforce two occupied slots", calls.Load())
	}
	if _, err := service.SaveDevicePreference(f.ctx, identities[2], last.connection,
		webpush.DevicePreference{Category: "test", Enabled: true, Paused: true, Version: 1}); err != nil {
		t.Fatal(err)
	}
	unblock()
	for range candidates {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	view, err := service.Delivery(f.ctx, identities[2], last.delivery)
	if err != nil || view.State != "cancelled" || calls.Load() != 2 {
		t.Fatal("revoked consent crossed the final transport guard", view, calls.Load(), err)
	}
}

func TestNotificationPersistentGatewayCooldownAfterServiceRestart(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	connection, err := service.Register(f.ctx, f.identity, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader("limited"))}, nil
	})
	if err = service.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	first, err := service.QueueTest(f.ctx, f.identity, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, lease := notificationClaimFixture(t, f, first.ID)
	if err = service.SendNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	view, err := service.Delivery(f.ctx, f.identity, first.ID)
	if err != nil || view.State != "expired" || view.SafeCode != "retry_window_exhausted" {
		t.Fatal("long cooldown must not replay a stale test", view, err)
	}
	var durable bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.rate_limit_buckets WHERE scope='webpush-cooldown-v1' AND window_end>clock_timestamp()+interval '59 minutes')`).Scan(&durable); err != nil || !durable {
		t.Fatal("provider cooldown was not persisted", err)
	}
	// Keep the same keys/configuration but replace all in-memory transport state.
	restarted, err := webpush.New(f.pool, f.app.Auth, f.app.Store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err = restarted.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	second, err := restarted.QueueTest(f.ctx, f.identity, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, lease = notificationClaimFixture(t, f, second.ID)
	if err = restarted.SendNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	view, err = restarted.Delivery(f.ctx, f.identity, second.ID)
	if err != nil || view.State != "expired" || calls != 1 {
		t.Fatal("service restart bypassed durable provider cooldown", view, calls, err)
	}
}

func TestNotificationGoneResponsePreservesReplacementCipher(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newIntegrationFixture(t)
			service, _ := shoppingPushService(t, f, true)
			input := shoppingPushInput(t, service)
			connection, err := service.Register(f.ctx, f.identity, input)
			if err != nil {
				t.Fatal(err)
			}
			delivery, err := service.QueueTest(f.ctx, f.identity, connection.ID)
			if err != nil {
				t.Fatal(err)
			}
			var original []byte
			if err = f.pool.QueryRow(f.ctx, `SELECT encrypted_subscription FROM core.push_subscriptions WHERE id=$1`, connection.ID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			replacement := input
			replacement.Subscription.Endpoint = "https://fcm.googleapis.com/notification-replacement/" + value.UUID()
			calls := 0
			if err = service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
				calls++
				view, e := service.Register(f.ctx, f.identity, replacement)
				if e != nil || view.ID != connection.ID {
					return nil, errors.New("controlled replacement did not retain device identity")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("gone"))}, nil
			})); err != nil {
				t.Fatal(err)
			}
			job, lease := notificationClaimFixture(t, f, delivery.ID)
			if err = service.SendNotificationJob(f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			var disabled bool
			var hash, cipher []byte
			if err = f.pool.QueryRow(f.ctx, `SELECT disabled_at IS NOT NULL,endpoint_hash,encrypted_subscription FROM core.push_subscriptions WHERE id=$1`, connection.ID).Scan(&disabled, &hash, &cipher); err != nil {
				t.Fatal(err)
			}
			expected := sha256.Sum256([]byte(replacement.Subscription.Endpoint))
			view, err := service.Delivery(f.ctx, f.identity, delivery.ID)
			if err != nil || calls != 1 || disabled || !bytes.Equal(hash, expected[:]) || bytes.Equal(cipher, original) || view.State != "failed" {
				t.Fatal("old gone response retired replacement or left old delivery pending", view, calls, disabled, err)
			}
		})
	}
}

func TestNotificationReconciliationRetentionIsBounded(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, false)
	// Seed historical operational records only in this disposable database.
	// 129 eligible rows prove that each pass deletes at most its 128-row batch.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.notification_intents
 (household_id,user_id,module_generation,resource_type,resource_id,source_revision,occurrence,category,event_at,not_before,expires_at,state,dedupe_key,updated_at)
 SELECT $1,$2,0,'push_subscriptions',gen_random_uuid(),1,n::text,'test',now()-interval '31 days',now()-interval '31 days',now()-interval '31 days'+interval '60 seconds','accepted',
 'notification-v1/'||lpad(to_hex(n),64,'0'),CASE WHEN n=130 THEN now() ELSE now()-interval '31 days' END FROM generate_series(1,130) n`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.notifications(household_id,user_id,kind,title,dedupe_key,created_at)
 SELECT $1,$2,'test','Retention fixture','retention-test/'||n::text,CASE WHEN n=130 THEN now() ELSE now()-interval '91 days' END FROM generate_series(1,130) n`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{2, 1} {
		if err := service.ScheduleNotifications(f.ctx); err != nil {
			t.Fatal(err)
		}
		var intents, notices, recentIntents, recentNotices int
		if err := f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM core.notification_intents),
 (SELECT count(*) FROM core.notifications WHERE dedupe_key LIKE 'retention-test/%'),
 (SELECT count(*) FROM core.notification_intents WHERE updated_at>now()-interval '1 day'),
 (SELECT count(*) FROM core.notifications WHERE dedupe_key='retention-test/130')`).Scan(&intents, &notices, &recentIntents, &recentNotices); err != nil || intents != want || notices != want || recentIntents != 1 || recentNotices != 1 {
			t.Fatal("retention exceeded batch or deleted fresh history", want, intents, notices, recentIntents, recentNotices, err)
		}
	}
}
