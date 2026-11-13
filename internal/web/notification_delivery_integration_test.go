//go:build integration && testtools

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
	"household/internal/worker"
)

func TestNotificationSchedulerQuietHoursFairnessAndConcurrentDedupe(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	active := shoppingTestMember(t, f, "notification-active-recipient", "reader")
	var now time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	minute := now.UTC().Hour()*60 + now.UTC().Minute()
	start, end := (minute+1439)%1440, (minute+10)%1440
	for index, id := range []auth.Identity{f.identity, active} {
		connection, err := service.Register(f.ctx, id, shoppingPushInput(t, service))
		if err != nil {
			t.Fatal(err)
		}
		quiet := notification.QuietHours{Timezone: "UTC"}
		count := 1
		if index == 0 {
			quiet.Start, quiet.End = &start, &end
			count = 24 // Occupy a complete urgent scheduling page.
		}
		if _, err = f.app.Store.SaveNotificationPreference(f.ctx, id, store.NotificationPreference{Category: "test", Enabled: true, QuietHours: quiet}); err != nil {
			t.Fatal(err)
		}
		if _, err = service.SaveDevicePreference(f.ctx, id, connection.ID, webpush.DevicePreference{Category: "test", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		for range count {
			tx, e := audit.Begin(audit.WithSystem(f.ctx, "worker"), f.pool)
			if e != nil {
				t.Fatal(e)
			}
			if e = tx.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); e == nil {
				_, e = store.EnqueueNotificationIntent(f.ctx, tx, notification.Intent{Schema: 1, HouseholdID: id.HouseholdID,
					UserID: id.UserID, ResourceType: "push_subscriptions", ResourceID: connection.ID, Revision: 1,
					Occurrence: value.UUID(), Category: "test", EventAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute)})
			}
			if e != nil {
				_ = tx.Rollback(f.ctx)
				t.Fatal(e)
			}
			if e = tx.Commit(f.ctx); e != nil {
				t.Fatal(e)
			}
		}
	}
	// Concurrent schedulers must converge without duplicated jobs. Repeated
	// passes must make progress past an entire page of quiet recipients.
	for range 2 {
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		for range 2 {
			wg.Go(func() { failures <- service.ScheduleNotifications(f.ctx) })
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var deliveries, delayed, unchanged int
	err := f.pool.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER(WHERE j.available_at=i.expires_at),
 count(*) FILTER(WHERE i.not_before=i.event_at AND i.expires_at=i.event_at+interval '60 seconds')
 FROM core.notification_deliveries d JOIN core.jobs j ON j.id=d.job_id JOIN core.notification_intents i ON i.id=d.intent_id`).Scan(&deliveries, &delayed, &unchanged)
	if err != nil || deliveries != 25 || delayed != 24 || unchanged != 25 {
		t.Fatal("quiet recipients blocked the queue or changed source deadlines", deliveries, delayed, unchanged, err)
	}
	calls := 0
	if err = service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: cfg, Push: service, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer func() { _ = w.Close() }()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var accepted int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_deliveries WHERE user_id=$1 AND state='accepted'`, active.UserID).Scan(&accepted); err != nil || accepted != 1 || calls != 1 {
		t.Fatal("eligible recipient starved behind quiet recipients", accepted, calls, err)
	}
}

func notificationClaimFixture(t *testing.T, f *integrationFixture, delivery string) (string, string) {
	t.Helper()
	lease := value.UUID()
	var job string
	err := f.owner.QueryRow(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '30 seconds',attempts=attempts+1
 WHERE id=(SELECT job_id FROM core.notification_deliveries WHERE id=$1) RETURNING id::text`, delivery, lease).Scan(&job)
	if err != nil {
		t.Fatal(err)
	}
	return job, lease
}

func TestNotificationDevicesControlledDeliveryAndHTTP(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	f.app.Push = service
	input := shoppingPushInput(t, service)
	connection, err := service.Register(f.ctx, f.identity, input)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := service.DevicePreferences(f.ctx, f.identity)
	if err != nil || len(devices) != 1 || len(devices[0].Categories) != 13 {
		t.Fatal("device defaults", err, devices)
	}
	for _, p := range devices[0].Categories {
		if p.Enabled || p.Version != 0 {
			t.Fatal("device was implicitly subscribed")
		}
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	call := func(method, path, token, csrf, body string, want int) []byte {
		t.Helper()
		response, raw, e := f.request(method, path, token, csrf, "application/json", strings.NewReader(body), nil)
		if e != nil || response == nil || response.StatusCode != want {
			t.Fatalf("%s %s: %v %v %s", method, path, response, e, raw)
		}
		return raw
	}
	call("POST", "/api/v1/core/push/subscriptions/"+connection.ID+"/test", f.token, "", "{}", 403)
	call("POST", "/api/v1/core/push/subscriptions/"+connection.ID+"/test", f.token, csrf, `{"url":"https://elsewhere.invalid"}`, 422)
	call("POST", "/api/v1/core/notification-preferences", f.token, csrf, `{"category":"unknown_notification_category","enabled":true,"timezone":"UTC","quiet_start":null,"quiet_end":null,"version":0}`, 422)
	var queued webpush.DeliveryView
	if err = json.Unmarshal(call("POST", "/api/v1/core/push/subscriptions/"+connection.ID+"/test", f.token, csrf, "{}", 202), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.ID == "" || queued.State != "pending" {
		t.Fatal("test was not durably queued", queued)
	}
	calls := 0
	if err = service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls++
		wire, e := io.ReadAll(io.LimitReader(req.Body, 8193))
		if e != nil || len(wire) < 100 || bytes.Contains(wire, []byte("audience_scope")) {
			return nil, errors.New("unencrypted test wire")
		}
		probe, e := f.owner.Begin(f.ctx)
		if e != nil {
			return nil, e
		}
		defer func() { _ = probe.Rollback(f.ctx) }()
		if _, e = probe.Exec(f.ctx, `SELECT id FROM core.households WHERE id=$1 FOR UPDATE NOWAIT`, f.identity.HouseholdID); e != nil {
			return nil, e
		}
		if _, e = probe.Exec(f.ctx, `SELECT id FROM core.sessions WHERE id=$1 FOR UPDATE NOWAIT`, f.identity.SessionID); e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: cfg, Push: service, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer func() { _ = w.Close() }()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	view, err := service.Delivery(f.ctx, f.identity, queued.ID)
	if err != nil || view.State != "accepted" || view.SafeCode != "gateway_accepted" || view.AcceptedAt == nil || calls != 1 {
		t.Fatal("controlled delivery state", calls, view, err)
	}
	contextBody := call("GET", "/api/v1/core/push/deliveries/"+queued.ID+"/context", f.token, "", "", 200)
	var ackContext map[string]string
	if err = json.Unmarshal(contextBody, &ackContext); err != nil || len(ackContext) != 3 || ackContext["csrf"] != csrf {
		t.Fatal("minimal display context", err)
	}
	call("POST", "/api/v1/core/push/deliveries/"+queued.ID+"/displayed", f.token, csrf, "{}", 200)
	view, err = service.Delivery(f.ctx, f.identity, queued.ID)
	if err != nil || view.SafeCode != "display_requested" {
		t.Fatal("display request is not explicit", view, err)
	}
	call("POST", "/api/v1/core/push/deliveries/"+queued.ID+"/seen", f.token, csrf, "{}", 200)
	view, err = service.Delivery(f.ctx, f.identity, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := view.UpdatedAt
	call("POST", "/api/v1/core/push/deliveries/"+queued.ID+"/displayed", f.token, csrf, "{}", 200)
	view, err = service.Delivery(f.ctx, f.identity, queued.ID)
	if err != nil || view.SafeCode != "user_confirmed" || !view.UpdatedAt.Equal(confirmed) {
		t.Fatal("late display ack downgraded user confirmation", view, err)
	}
	other := shoppingTestMember(t, f, "notification-other-user", "reader")
	if _, err = service.Delivery(f.ctx, other, queued.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign user read delivery", err)
	}
	if _, err = service.SaveDevicePreference(f.ctx, other, connection.ID, webpush.DevicePreference{Category: "test", Enabled: true}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign user changed device", err)
	}
	if _, err = service.QueueTest(f.ctx, other, connection.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign user tested device", err)
	}
	if err = service.AcknowledgeDelivery(f.ctx, other, queued.ID, true); !errors.Is(err, store.ErrConflict) {
		t.Fatal("foreign user acknowledged delivery", err)
	}
	if target, e := service.DeliveryTarget(f.ctx, f.identity, queued.ID); e != nil || target != "/settings/notifications" {
		t.Fatal("unsafe test navigation", target, e)
	}
	for range 2 {
		if _, err = service.QueueTest(f.ctx, f.identity, connection.ID); err != nil {
			t.Fatal("allowed explicit test", err)
		}
	}
	if _, err = service.QueueTest(f.ctx, f.identity, connection.ID); !errors.Is(err, webpush.ErrLimited) {
		t.Fatal("persistent test limit missing", err)
	}
	var modules int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_preferences WHERE category<>'test'`).Scan(&modules); err != nil || modules != 0 {
		t.Fatal("test changed module consent", modules, err)
	}
}

func TestNotificationDeliveryRevocationExpiryRetryAndLease(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	input := shoppingPushInput(t, service)
	connection, err := service.Register(f.ctx, f.identity, input)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	status := 201
	if err = service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("controlled"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	queue := func() webpush.DeliveryView {
		t.Helper()
		// Only this disposable fixture's management budget is reset between independent cases.
		if _, e := f.owner.Exec(f.ctx, `DELETE FROM core.rate_limit_buckets WHERE scope='notification-device-test-v1'`); e != nil {
			t.Fatal(e)
		}
		v, e := service.QueueTest(f.ctx, f.identity, connection.ID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	assertEnded := func(t *testing.T, delivery, wantState, wantCode string) {
		t.Helper()
		view, err := service.Delivery(f.ctx, f.identity, delivery)
		if err != nil || view.State != wantState || view.SafeCode != wantCode || calls != 0 {
			t.Fatal("terminal notification classification or forbidden transport", view, calls, err)
		}
		var finished bool
		if err = f.pool.QueryRow(f.ctx, `SELECT j.state='done' AND j.finished_at IS NOT NULL AND j.lease_token IS NULL AND j.lease_until IS NULL
 FROM core.jobs j JOIN core.notification_deliveries d ON d.job_id=j.id WHERE d.id=$1`, delivery).Scan(&finished); err != nil || !finished {
			t.Fatal("terminal notification retained an unfinished job or lease", finished, err)
		}
	}
	withdrawAccount := func(t *testing.T) {
		t.Helper()
		prefs, err := f.app.Store.NotificationPreferences(f.ctx, f.identity)
		if err != nil {
			t.Fatal(err)
		}
		for _, pref := range prefs {
			if pref.Category != "test" {
				continue
			}
			pref.Enabled = false
			saved, err := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, pref)
			if err != nil || saved.Enabled || saved.Version != pref.Version+1 || saved.EnabledSince != nil {
				t.Fatal("account withdrawal did not commit the current preference", saved, err)
			}
			return
		}
		t.Fatal("test account preference missing")
	}
	expire := func(t *testing.T, delivery string) {
		t.Helper()
		// Fault-inject expiry of a genuinely queued test notification. One stable
		// statement clock preserves the exact one-minute category freshness bound.
		if _, err := f.owner.Exec(f.ctx, `UPDATE core.notification_intents SET event_at=statement_timestamp()-interval '2 minutes',
 not_before=statement_timestamp()-interval '2 minutes',expires_at=statement_timestamp()-interval '1 minute'
 WHERE id=(SELECT intent_id FROM core.notification_deliveries WHERE id=$1)`, delivery); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("withdraw account before send", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		withdrawAccount(t)
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		assertEnded(t, v.ID, "cancelled", "intent_or_job_ended")
	})
	t.Run("device pause", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		devices, e := service.DevicePreferences(f.ctx, f.identity)
		if e != nil {
			t.Fatal(e)
		}
		for _, p := range devices[0].Categories {
			if p.Category == "test" {
				p.Paused = true
				if _, e = service.SaveDevicePreference(f.ctx, f.identity, connection.ID, p); e != nil {
					t.Fatal(e)
				}
			}
		}
		if e = service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		if calls != 0 {
			t.Fatal("paused device sent")
		}
	})
	t.Run("expiry", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		expire(t, v.ID)
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		assertEnded(t, v.ID, "expired", "notification_expired")
	})
	t.Run("reconcile withdrawn account", func(t *testing.T) {
		v := queue()
		withdrawAccount(t)
		if err := service.ScheduleNotifications(f.ctx); err != nil {
			t.Fatal(err)
		}
		assertEnded(t, v.ID, "cancelled", "intent_or_job_ended")
	})
	t.Run("reconcile actual expiry", func(t *testing.T) {
		v := queue()
		expire(t, v.ID)
		if err := service.ScheduleNotifications(f.ctx); err != nil {
			t.Fatal(err)
		}
		assertEnded(t, v.ID, "expired", "notification_expired")
	})
	t.Run("transient retry within freshness", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		status = 503
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		var state string
		var attempts int
		var bounded bool
		if e := f.pool.QueryRow(f.ctx, `SELECT j.state,j.attempts,j.available_at<i.expires_at FROM core.jobs j JOIN core.notification_deliveries d ON d.job_id=j.id JOIN core.notification_intents i ON i.id=d.intent_id WHERE j.id=$1`, job).Scan(&state, &attempts, &bounded); e != nil || state != "pending" || attempts != 1 || !bounded {
			t.Fatal("retry window", state, attempts, bounded, e)
		}
		job, lease = notificationClaimFixture(t, f, v.ID)
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		got, e := service.Delivery(f.ctx, f.identity, v.ID)
		if e != nil || got.State != "expired" || calls != 2 {
			t.Fatal("retry count did not terminate", got, calls, e)
		}
		status = 201
	})
	t.Run("lost lease", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		replacement := value.UUID()
		if _, e := f.owner.Exec(f.ctx, `UPDATE core.jobs SET lease_token=$2 WHERE id=$1`, job, replacement); e != nil {
			t.Fatal(e)
		}
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		if calls != 2 {
			t.Fatal("lost lease sent")
		}
	})
	t.Run("expired registering session", func(t *testing.T) {
		v := queue()
		job, lease := notificationClaimFixture(t, f, v.ID)
		if _, e := f.owner.Exec(f.ctx, `UPDATE core.sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.identity.SessionID); e != nil {
			t.Fatal(e)
		}
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		if calls != 2 {
			t.Fatal("expired session sent")
		}
	})
}
