//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
	"household/internal/worker"
)

func TestNotificationCookingControlledDeliveryRecoveryAndLatency(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	f.app.Push = service
	connection, err := service.Register(f.ctx, f.identity, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveDevicePreference(f.ctx, f.identity, connection.ID, webpush.DevicePreference{Category: "cooking_timer", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, store.NotificationPreference{Category: "cooking_timer", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	rid := recipeSave(t, f, "", recipeInput(t, f))
	var mu sync.Mutex
	calls := 0
	var measuredTimers []string
	latencies := []time.Duration{}
	client := shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		began := time.Now()
		wire, e := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, parseErr := strconv.Atoi(req.Header.Get("TTL"))
		if e != nil || parseErr != nil || ttl < 1 || ttl > 60 || req.Header.Get("Urgency") != "high" || len(wire) < 100 || bytes.Contains(wire, []byte("audience_scope")) || bytes.Contains(wire, []byte("PRIVATE-TIMER")) {
			return nil, errors.New("timer transport lost encryption, urgency or freshness bound")
		}
		var measured *time.Duration
		if len(measuredTimers) > 0 {
			rows, queryErr := f.pool.Query(req.Context(), `SELECT d.id::text,i.not_before FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_id=ANY($1::uuid[]) AND d.subscription_id=$2`, measuredTimers, connection.ID)
			if queryErr != nil {
				return nil, queryErr
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				var deadline time.Time
				if queryErr = rows.Scan(&id, &deadline); queryErr != nil {
					return nil, queryErr
				}
				hash := sha256.Sum256([]byte(id))
				if hex.EncodeToString(hash[:])[:32] == req.Header.Get("Topic") {
					elapsed := began.Sub(deadline)
					measured = &elapsed
				}
			}
			if queryErr = rows.Err(); queryErr != nil {
				return nil, queryErr
			}
		}
		mu.Lock()
		calls++
		if measured != nil {
			latencies = append(latencies, *measured)
		}
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})
	if err = service.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	waitDue := func(t *testing.T, deadline time.Time) {
		t.Helper()
		ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
		defer cancel()
		poll := time.NewTicker(20 * time.Millisecond)
		defer poll.Stop()
		for {
			var due bool
			if e := f.pool.QueryRow(ctx, `SELECT clock_timestamp()>=$1`, deadline).Scan(&due); e != nil {
				t.Fatal(e)
			}
			if due {
				return
			}
			select {
			case <-poll.C:
			case <-ctx.Done():
				t.Fatal("timer did not reach its persisted deadline", ctx.Err())
			}
		}
	}
	delivery := func(t *testing.T, timer string) string {
		t.Helper()
		var id string
		if e := f.pool.QueryRow(f.ctx, `SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_id=$1 AND i.category='cooking_timer' AND d.subscription_id=$2 AND d.state='pending'`, timer, connection.ID).Scan(&id); e != nil {
			t.Fatal("real timer intent was not staged", e)
		}
		return id
	}
	queue := func(t *testing.T) (store.CookingSession, string) {
		t.Helper()
		s := cookingChange(t, f, cookingStart(t, f, rid), store.CookingInput{Action: "timer_add", TimerName: "PRIVATE-TIMER", Seconds: 1})
		waitDue(t, *s.Timers[0].Deadline)
		if e := service.ScheduleCookingTimers(f.ctx); e != nil {
			t.Fatal(e)
		}
		return s, delivery(t, s.Timers[0].ID)
	}
	request := func(t *testing.T, path, token string, want int) []byte {
		t.Helper()
		r, raw, e := f.request(http.MethodGet, path, token, "", "", nil, nil)
		if e != nil || r == nil || r.StatusCode != want {
			t.Fatalf("timer HTTP status want=%d response=%v error=%v", want, r, e)
		}
		if strings.HasSuffix(path, "/cooking-context") && r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("timer context may be cached")
		}
		return raw
	}
	t.Run("foreground context is private current and never acknowledges timer", func(t *testing.T) {
		s, id := queue(t)
		path := "/api/v1/core/push/deliveries/" + id + "/cooking-context"
		var view webpush.CookingDeliveryContext
		if e := json.Unmarshal(request(t, path, f.token, 200), &view); e != nil || view.TimerID != s.Timers[0].ID || view.SessionID != s.ID || view.Revision != s.Timers[0].Version || !view.Deadline.Equal(*s.Timers[0].Deadline) || len(view.AuthScope) != 64 {
			t.Fatal("invalid foreground authority context", e)
		}
		request(t, path, "", 401)
		other := shoppingTestMember(t, f, "other-notification-cook", "editor")
		otherToken, _, e := f.app.Auth.Login(f.ctx, other.Username, integrationPassword, "", "127.0.0.1", "other-cooking-context")
		if e != nil {
			t.Fatal(e)
		}
		request(t, path, otherToken, 404)
		anotherToken, _, e := f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.1", "same-user-other-session")
		if e != nil {
			t.Fatal(e)
		}
		request(t, path, anotherToken, 404)
		route := "/m/recipes/cooking/timer/" + s.Timers[0].ID
		request(t, route, otherToken, 404)
		r, _, e := f.request(http.MethodGet, route, f.token, "", "", nil, nil)
		if e != nil || r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/m/recipes/cooking/"+s.ID {
			t.Fatal("owner timer route did not resolve safely", e)
		}
		if current := cookingRead(t, f, s.ID); current.Timers[0].State != "running" || current.Version != s.Version {
			t.Fatal("context GET acknowledged or changed timer")
		}
		job, lease := notificationClaimFixture(t, f, id)
		if e = service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		request(t, path, f.token, 200)
		s = cookingChange(t, f, s, store.CookingInput{Action: "restart", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, Seconds: 120})
		request(t, path, f.token, 404)
		cookingChange(t, f, s, store.CookingInput{Action: "abandon"})
	})
	t.Run("brief cooldown lock contention preserves a fresh timer", func(t *testing.T) {
		s, id := queue(t)
		job, lease := notificationClaimFixture(t, f, id)
		lock, e := f.owner.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = lock.Rollback(f.ctx) }()
		if _, e = lock.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "webpush-cooldown-v1/fcm.googleapis.com"); e != nil {
			t.Fatal(e)
		}
		before := count()
		result := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			result <- service.SendNotificationJob(f.ctx, job, lease)
		}()
		defer func() {
			_ = lock.Rollback(f.ctx)
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Error("contended timer send did not stop")
			}
		}()
		observe, stop := context.WithTimeout(f.ctx, 2*time.Second)
		defer stop()
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
		for {
			var waiting bool
			if e = f.owner.QueryRow(observe, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&waiting); e != nil {
				t.Fatal("observe bounded cooldown lock wait", e)
			}
			if waiting {
				break
			}
			select {
			case e = <-result:
				t.Fatal("brief lock contention ended a fresh timer instead of waiting", e)
			case <-poll.C:
			case <-observe.Done():
				t.Fatal("timer did not reach cooldown lock", observe.Err())
			}
		}
		if e = lock.Rollback(f.ctx); e != nil {
			t.Fatal(e)
		}
		select {
		case e = <-result:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timer did not resume after short cooldown lock")
		}
		view, e := service.Delivery(f.ctx, f.identity, id)
		if e != nil || view.State != "accepted" || count() != before+1 {
			t.Fatal("short lock contention exhausted the fresh timer", view, e)
		}
		cookingChange(t, f, s, store.CookingInput{Action: "abandon"})
	})
	for _, action := range []string{"acknowledge", "finish", "abandon", "expiry"} {
		t.Run(action+" before final send refuses stale alarm", func(t *testing.T) {
			s, id := queue(t)
			if action == "expiry" {
				// Simulate elapsed freshness only in this isolated fixture. Source
				// mutation and intent creation above use the real atomic API.
				if _, e := f.owner.Exec(f.ctx, `UPDATE core.notification_intents SET expires_at=not_before+interval '1 microsecond' WHERE id=(SELECT intent_id FROM core.notification_deliveries WHERE id=$1)`, id); e != nil {
					t.Fatal(e)
				}
			} else {
				s = cookingChange(t, f, s, store.CookingInput{Action: action, TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version, ActualServings: "2", CookedOn: s.Today})
			}
			request(t, "/api/v1/core/push/deliveries/"+id+"/cooking-context", f.token, 404)
			before := count()
			job, lease := notificationClaimFixture(t, f, id)
			if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
				t.Fatal(e)
			}
			v, e := service.Delivery(f.ctx, f.identity, id)
			if e != nil || count() != before || v.State != "cancelled" && v.State != "expired" {
				t.Fatal("stale timer reached provider", v, e)
			}
			if s.State == "active" {
				cookingChange(t, f, s, store.CookingInput{Action: "abandon"})
			}
		})
	}
	t.Run("real worker recovers restart and expired lease within bounded deadline latency", func(t *testing.T) {
		service.Close()
		service, err = webpush.New(f.pool, f.app.Auth, f.app.Store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(service.Close)
		f.app.Push = service
		if err = service.SetHTTPClientForTests(client); err != nil {
			t.Fatal(err)
		}
		// A bounded synthetic backlog exceeds both the ordinary 256-row sweep
		// and one fast-loop batch. None may take priority over fresh alarms.
		if _, err = f.owner.Exec(f.ctx, `WITH seed AS MATERIALIZED (
 SELECT n,gen_random_uuid() AS intent,gen_random_uuid() AS job,gen_random_uuid() AS delivery FROM generate_series(1,600) n
), intents AS (
 INSERT INTO core.notification_intents(id,household_id,user_id,module_id,module_generation,resource_type,resource_id,source_revision,occurrence,category,event_at,not_before,expires_at,dedupe_key)
 SELECT intent,$1,$2,'recipes',$3,'cooking_timers',gen_random_uuid(),1,'backlog/'||n::text,'cooking_timer',
 statement_timestamp()-interval '2 hours',statement_timestamp()-interval '2 hours',statement_timestamp()-interval '119 minutes',
 'notification-v1/'||lpad(to_hex(n),64,'0') FROM seed RETURNING id
), jobs AS (
 INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key,available_at,max_attempts)
 SELECT seed.job,$1,'recipes',$3,'notification_push_v1',jsonb_build_object('schema',1,'delivery_id',seed.delivery),
 'notification-delivery/'||seed.delivery::text,clock_timestamp()-interval '2 hours',2 FROM seed JOIN intents ON intents.id=seed.intent RETURNING id
)
 INSERT INTO core.notification_deliveries(id,household_id,user_id,intent_id,subscription_id,job_id,preference_version,device_version)
 SELECT seed.delivery,$1,$2,seed.intent,$4,seed.job,1,1 FROM seed JOIN jobs ON jobs.id=seed.job`, f.identity.HouseholdID, f.identity.UserID, f.app.Store.States()["recipes"].Generation, connection.ID); err != nil {
			t.Fatal("seed bounded stale timer backlog", err)
		}
		s := cookingChange(t, f, cookingStart(t, f, rid), store.CookingInput{Action: "timer_add", TimerName: "PRIVATE-TIMER recovery", Seconds: 1})
		waitDue(t, *s.Timers[0].Deadline)
		if err = service.ScheduleCookingTimers(f.ctx); err != nil {
			t.Fatal(err)
		}
		recoveryID := delivery(t, s.Timers[0].ID)
		// A process died after claiming one persisted job. Restart must reclaim
		// the expired lease rather than require another cooking action.
		if _, err = f.owner.Exec(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()-interval '1 second',attempts=1 WHERE id=(SELECT job_id FROM core.notification_deliveries WHERE id=$1)`, recoveryID, value.UUID()); err != nil {
			t.Fatal(err)
		}
		// These deadlines are still in the future when Run starts. Their intents
		// must be staged by the real one-second scheduler, not this fixture.
		for range 3 {
			s = cookingChange(t, f, s, store.CookingInput{Action: "timer_add", TimerName: "PRIVATE-TIMER scheduled", Seconds: 1})
		}
		for _, timer := range s.Timers {
			measuredTimers = append(measuredTimers, timer.ID)
		}
		var future bool
		if err = f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()<$1`, *s.Timers[1].Deadline).Scan(&future); err != nil || !future {
			t.Fatal("fresh timers were already due before the worker could start", err)
		}
		ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
		done := make(chan struct{})
		var workerLog bytes.Buffer
		w := worker.Worker{Pool: f.pool, Push: service, Cfg: cfg, Log: slog.New(slog.NewTextHandler(&workerLog, nil))}
		go func() { defer close(done); w.Run(ctx) }()
		defer func() {
			cancel()
			select {
			case <-done:
				if t.Failed() {
					t.Logf("controlled worker errors: %s", workerLog.String())
					inspect, stop := context.WithTimeout(f.ctx, 2*time.Second)
					defer stop()
					var states []byte
					inspectErr := f.pool.QueryRow(inspect, `SELECT coalesce(jsonb_agg(jsonb_build_object(
 'intent_state',i.state,'delivery_state',d.state,'safe_code',d.safe_code,'delivery_attempts',d.attempts,
 'job_state',j.state,'job_error',j.last_error,'job_attempts',j.attempts,
 'due_age_seconds',extract(epoch FROM clock_timestamp()-i.not_before),
 'job_available_seconds',extract(epoch FROM j.available_at-clock_timestamp())) ORDER BY i.not_before),'[]'::jsonb)
 FROM core.notification_intents i LEFT JOIN core.notification_deliveries d ON d.intent_id=i.id
 LEFT JOIN core.jobs j ON j.id=d.job_id WHERE i.resource_id=ANY($1::uuid[]) AND i.category='cooking_timer'`, measuredTimers).Scan(&states)
					t.Logf("bounded timer states=%s inspection_error=%v", states, inspectErr)
					mu.Lock()
					t.Logf("measured provider starts=%d latencies=%v", len(latencies), latencies)
					mu.Unlock()
				}
			case <-time.After(5 * time.Second):
				t.Error("timer worker did not release its goroutines")
			}
		}()
		poll := time.NewTicker(20 * time.Millisecond)
		defer poll.Stop()
		for {
			var accepted int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_id=ANY($1::uuid[]) AND d.subscription_id=$2 AND d.state='accepted'`, measuredTimers, connection.ID).Scan(&accepted); err != nil {
				t.Fatal(err)
			}
			if accepted == len(measuredTimers) {
				break
			}
			select {
			case <-poll.C:
			case <-ctx.Done():
				t.Fatal("timer worker did not accept entire bounded batch", ctx.Err())
			}
		}
		cancel()
		<-done
		mu.Lock()
		measured := slices.Clone(latencies)
		mu.Unlock()
		if len(measured) != len(measuredTimers) {
			t.Fatalf("expected one measured provider request per deadline: %d/%d", len(measured), len(measuredTimers))
		}
		slices.Sort(measured)
		p95 := measured[(95*len(measured)+99)/100-1]
		t.Logf("controlled provider deadline-to-request latency samples=%d p95=%s; this does not measure device display", len(measured), p95)
		if measured[0] < 0 || p95 > 3*time.Second {
			t.Fatalf("timer provider-start latency outside 0..3s: %v", measured)
		}
		var attempts int
		if err = f.pool.QueryRow(f.ctx, `SELECT attempts FROM core.jobs WHERE id=(SELECT job_id FROM core.notification_deliveries WHERE id=$1)`, recoveryID).Scan(&attempts); err != nil || attempts != 2 {
			t.Fatal("expired lease not reclaimed exactly once", attempts, err)
		}
	})
}
