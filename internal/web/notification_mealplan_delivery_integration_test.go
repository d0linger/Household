//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
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

type notificationPlanningDelivery struct {
	actor                            auth.Identity
	plan, meal, connection, delivery string
	input                            webpush.RegisterInput
	key                              *ecdh.PrivateKey
	accountPreference                store.NotificationPreference
	devicePreference                 webpush.DevicePreference
}

func notificationPlanningEnroll(t *testing.T, f *integrationFixture, service *webpush.Service, actor auth.Identity) notificationPlanningDelivery {
	t.Helper()
	q := notificationPlanningDelivery{actor: actor}
	var err error
	q.key, err = ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	q.input = webpush.RegisterInput{DeviceID: value.UUID(), VAPIDKeyID: *service.Configuration().VAPIDKeyID,
		Subscription: webpush.Receiver{Endpoint: "https://fcm.googleapis.com/controlled-planning/" + value.UUID(),
			Keys: webpush.ReceiverKeys{P256DH: base64.RawURLEncoding.EncodeToString(q.key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(secret)}}}
	if q.accountPreference, err = f.app.Store.SaveNotificationPreference(f.ctx, actor, store.NotificationPreference{Category: "mealplan", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	connection, err := service.Register(f.ctx, actor, q.input)
	if err != nil {
		t.Fatal(err)
	}
	q.connection = connection.ID
	if q.devicePreference, err = service.SaveDevicePreference(f.ctx, actor, connection.ID, webpush.DevicePreference{Category: "mealplan", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return q
}

func notificationPlanningWait(t *testing.T, f *integrationFixture, at time.Time) {
	t.Helper()
	// Wait once for the actual server-owned due time. Neither source clocks,
	// subscription cutoffs nor event timestamps are rewritten for this test.
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Minute)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var due bool
		if err := f.pool.QueryRow(ctx, `SELECT clock_timestamp()>=$1`, at).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if due {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("real planning preparation deadline not reached", ctx.Err())
		case <-ticker.C:
		}
	}
}

func notificationPlanningStage(t *testing.T, f *integrationFixture) {
	t.Helper()
	// Only the isolated scanner job's retry clock is advanced; real source
	// eligibility is evaluated against the unchanged database clock.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2 AND state='pending'`, f.identity.HouseholdID, store.PlanningNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		n, err := f.app.Store.StagePlanningNotifications(f.ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("planning scanner exceeded bounded work")
}

func notificationPlanningWithdraw(t *testing.T, f *integrationFixture, service *webpush.Service, q notificationPlanningDelivery, action string) {
	t.Helper()
	switch action {
	case "settings disabled", "responsibility reassigned":
		var now time.Time
		if err := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			t.Fatal(err)
		}
		// Reassignment is a new explicit confirmation with a future step. The
		// elapsed step that created the queued intent must not be resubmitted.
		in := notificationPlanningInput(t, f, q.plan, q.meal, q.actor.MembershipID, now.Add(24*time.Hour))
		if action == "settings disabled" {
			in.Enabled, in.Steps = false, nil
		} else {
			in.Responsible = f.identity.MembershipID
		}
		if _, err := f.app.Store.SavePlanningNotificationSettings(f.ctx, f.identity, q.plan, q.meal, value.UUID(), value.UUID(), in); err != nil {
			t.Fatal(err)
		}
	case "meal revised", "meal cancelled", "meal cooked":
		meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, q.plan, q.meal)
		if err != nil {
			t.Fatal(err)
		}
		var version int64
		if err = f.pool.QueryRow(f.ctx, `SELECT version FROM mealplan.plans WHERE id=$1`, q.plan).Scan(&version); err != nil {
			t.Fatal(err)
		}
		in := store.PlanningInput{Generation: f.app.Store.States()["mealplan"].Generation, PlanVersion: version, Action: "save", MealID: q.meal, Version: meal.Version,
			Meal: store.PlanningMealInput{Title: meal.Title, PlannedOn: meal.PlannedOn, Slot: meal.Slot, Kind: meal.Kind, Status: meal.Status, Servings: meal.Servings, Notes: meal.Notes}}
		switch action {
		case "meal revised":
			in.Meal.Notes += " revised"
		case "meal cancelled":
			in.Meal.Status = "cancelled"
		default:
			in.Meal.Status = "cooked"
		}
		planningMutation(t, f, q.plan, in)
	case "template reactivated":
		m, _ := f.app.Store.Module("mealplan")
		e, _ := m.Entity("plans")
		for _, template := range []bool{true, false} {
			plan, err := f.app.Store.Get(f.ctx, f.identity, m, e, q.plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, q.plan, value.UUID(), value.UUID(), auditMutation(t, f, "mealplan", store.Record{"is_template": template}, store.Int(plan["version"]))); err != nil {
				t.Fatal(err)
			}
		}
	case "session revoked":
		if err := f.app.Auth.Logout(f.ctx, q.actor); err != nil {
			t.Fatal(err)
		}
	case "account disabled":
		participantDisable(t, f, q.actor)
	case "device consent withdrawn":
		preference := q.devicePreference
		preference.Enabled = false
		saved, err := service.SaveDevicePreference(f.ctx, q.actor, q.connection, preference)
		if err != nil || saved.Enabled || saved.Version != preference.Version+1 || saved.EnabledSince != nil {
			t.Fatal("device consent withdrawal did not commit the current revision", saved.Version, saved.Enabled, saved.EnabledSince, err)
		}
	case "account consent withdrawn":
		preference := q.accountPreference
		preference.Enabled = false
		saved, err := f.app.Store.SaveNotificationPreference(f.ctx, q.actor, preference)
		if err != nil || saved.Enabled || saved.Version != preference.Version+1 || saved.EnabledSince != nil {
			t.Fatal("account consent withdrawal did not commit the current revision", saved.Version, saved.Enabled, saved.EnabledSince, err)
		}
	case "module paused":
		state := f.app.Store.States()["mealplan"]
		if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown planning withdrawal fixture")
	}
}

func notificationPlanningPostSlot(t *testing.T, f *integrationFixture, service *webpush.Service, q notificationPlanningDelivery, action string) {
	t.Helper()
	type leased struct{ job, token string }
	occupied := make([]leased, 0, 2)
	for _, suffix := range []string{"a", "b"} {
		actor := shoppingTestMember(t, f, "plan-slot-"+strings.ReplaceAll(value.UUID(), "-", "")[:12]+suffix, "reader")
		connection, err := service.Register(f.ctx, actor, shoppingPushInput(t, service))
		if err != nil {
			t.Fatal(err)
		}
		delivery, err := service.QueueTest(f.ctx, actor, connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		job, token := notificationClaimFixture(t, f, delivery.ID)
		occupied = append(occupied, leased{job, token})
	}
	entered, release := make(chan struct{}, 3), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
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
	for _, item := range occupied {
		wg.Go(func() { results <- service.SendNotificationJob(ctx, item.job, item.token) })
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("planning transport slot not occupied")
		}
	}
	job, token := notificationClaimFixture(t, f, q.delivery)
	wg.Go(func() { results <- service.SendNotificationJob(ctx, job, token) })
	until := time.Now().Add(time.Second)
	for {
		var reserved bool
		if err := f.pool.QueryRow(f.ctx, `SELECT last_attempt_at IS NOT NULL FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&reserved); err != nil {
			t.Fatal(err)
		}
		if reserved {
			break
		}
		if time.Now().After(until) {
			t.Fatal("planning initial preflight did not finish before slot wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	notificationPlanningWithdraw(t, f, service, q, action)
	unblock()
	for range 3 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("controlled planning sender or final guard", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("controlled planning sender did not finish")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("withdrawn planning source crossed final transport guard", calls.Load())
	}
	var state string
	if err := f.pool.QueryRow(f.ctx, `SELECT state FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("withdrawn planning delivery not cancelled", state, err)
	}
}

func TestNotificationMealplanControlledDeliveryAndFinalSourceGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationPlanningEnable(t, f)
	service, _ := shoppingPushService(t, f, true)
	// A fully opted-in creator is never an implicit recipient. Each meal also
	// includes this administrator as a participant without assigning responsibility.
	notificationPlanningEnroll(t, f, service, f.identity)
	scenarios := []string{"accepted", "settings disabled", "responsibility reassigned", "meal revised", "meal cancelled", "meal cooked", "template reactivated", "session revoked", "account disabled", "device consent withdrawn", "account consent withdrawn", "module paused"}
	cases := make([]notificationPlanningDelivery, len(scenarios))
	// Account creation and real bcrypt logins occur before choosing the deadline.
	for i := range scenarios {
		cases[i] = notificationPlanningEnroll(t, f, service, shoppingTestMember(t, f, "planning-delivery-"+strconv.Itoa(i), "reader"))
	}
	var now time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	at := now.UTC().Truncate(time.Minute).Add(2 * time.Minute)
	for i, scenario := range scenarios {
		q := &cases[i]
		q.plan, q.meal = notificationPlanningSource(t, f, at, scenario)
		in := notificationPlanningInput(t, f, q.plan, q.meal, q.actor.MembershipID, at)
		if _, err := f.app.Store.SavePlanningNotificationSettings(f.ctx, f.identity, q.plan, q.meal, value.UUID(), value.UUID(), in); err != nil {
			t.Fatal("confirm actual future preparation time", scenario, err)
		}
	}
	notificationPlanningWait(t, f, at)
	notificationPlanningStage(t, f)
	// The production scheduler admits eight ordinary intents per tick.
	for range 2 {
		if err := service.ScheduleNotifications(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := range cases {
		q := &cases[i]
		var deliveries []string
		var recipients int
		if err := f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.category='mealplan' AND i.resource_type='mealplan_preparation' AND i.resource_id=$1 AND i.user_id=$2 AND d.subscription_id=$3),
 (SELECT count(DISTINCT user_id) FROM core.notification_intents WHERE category='mealplan' AND resource_type='mealplan_preparation' AND resource_id=$1)`, q.meal, q.actor.UserID, q.connection).Scan(&deliveries, &recipients); err != nil || len(deliveries) != 1 || recipients != 1 {
			t.Fatal("explicit preparation recipient delivery missing or broadcast", i, len(deliveries), recipients, err)
		}
		q.delivery = deliveries[0]
	}
	for i, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			q := cases[i]
			if scenario != "accepted" {
				notificationPlanningPostSlot(t, f, service, q, scenario)
				return
			}
			var calls atomic.Int32
			if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				raw, err := io.ReadAll(io.LimitReader(req.Body, 8193))
				ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
				if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" || req.Header.Get("Content-Encoding") != "aes128gcm" || req.Header.Get("Authorization") == "" || req.URL.String() != q.input.Subscription.Endpoint {
					return nil, errors.New("planning request protocol or destination invalid")
				}
				clear, err := notificationDocumentClear(raw, q.key, q.input.Subscription.Keys.Auth)
				if err != nil {
					return nil, err
				}
				var message struct {
					Schema  int    `json:"schema"`
					Kind    string `json:"kind"`
					ID      string `json:"id"`
					Expires int64  `json:"expires_at"`
					Scope   string `json:"audience_scope"`
				}
				decoder := json.NewDecoder(bytes.NewReader(clear))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&message) != nil || decoder.Decode(new(any)) != io.EOF || message.Schema != 2 || message.Kind != "mealplan" || message.ID != q.delivery || !notification.ValidMessage(message.Schema, message.Kind, message.ID, message.Scope, message.Expires, time.Now().Unix()) || bytes.Contains(clear, []byte("PRIVATE-PLANNING")) || bytes.Contains(clear, []byte(q.meal)) {
					return nil, errors.New("planning plaintext leaked private content or departed from closed reference contract")
				}
				return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
			})); err != nil {
				t.Fatal(err)
			}
			job, lease := notificationClaimFixture(t, f, q.delivery)
			if err := service.SendNotificationJob(f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := f.pool.QueryRow(f.ctx, `SELECT state FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&state); err != nil || state != "accepted" || calls.Load() != 1 {
				t.Fatal("controlled planning acceptance", state, calls.Load(), err)
			}
		})
	}
}
