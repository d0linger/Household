//go:build integration && testtools

package web

import (
	"bytes"
	"context"
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
	"household/internal/webpush"
)

func notificationFuelWebScan(t *testing.T, f *integrationFixture) {
	t.Helper()
	for range 8 {
		if _, err := f.app.Store.StageFuelNotifications(f.ctx, 50); err != nil {
			t.Fatal("scan real Fuel notification sources", err)
		}
	}
}

type notificationFuelDelivery struct {
	actor                                 auth.Identity
	vehicle, series, plan, connection, id string
	planVersion                           int64
	day                                   string
	device                                webpush.DevicePreference
}

func notificationFuelQueue(t *testing.T, f *integrationFixture, service *webpush.Service, suffix string) notificationFuelDelivery {
	t.Helper()
	q := notificationFuelDelivery{actor: shoppingTestMember(t, f, "fuel-"+suffix, "reader")}
	if _, err := f.app.Store.SaveNotificationPreference(f.ctx, q.actor, store.NotificationPreference{Category: "fuel", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	register := func() {
		connection, err := service.Register(f.ctx, q.actor, shoppingPushInput(t, service))
		if err != nil {
			t.Fatal(err)
		}
		q.connection = connection.ID
	}
	enable := func() {
		var err error
		q.device, err = service.SaveDevicePreference(f.ctx, q.actor, q.connection, webpush.DevicePreference{Category: "fuel", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	if suffix != "late-registration" {
		register()
		if suffix != "late-device" {
			enable()
		}
	}
	vehicle, series := fuelModuleNativeVehicle(t, f)
	q.vehicle, q.series = store.String(vehicle["id"]), store.String(series["id"])
	q.day = fuelModuleReadNativeDay(t, f).Format(time.DateOnly)
	plan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", q.vehicle, "", 0, store.Record{
		"title": "PRIVATE-FUEL notification service", "due_odometer": "100", "odometer_series_id": q.series,
		"assignee_id": q.actor.MembershipID, "active": true, "task_opt_in": false,
	}, nil)
	q.plan, q.planVersion = plan.RecordID, plan.Version
	fuelModuleNativeApply(t, f, "observation.save", q.vehicle, "", 0, store.Record{
		"odometer_series_id": q.series, "observed_on": q.day, "reading": "99", "notes": "PRIVATE-FUEL baseline",
	}, nil)
	notificationFuelWebScan(t, f)
	switch suffix {
	case "late-registration":
		register()
		enable()
	case "late-device":
		enable()
	}
	fuelModuleNativeApply(t, f, "observation.save", q.vehicle, "", 0, store.Record{
		"odometer_series_id": q.series, "observed_on": q.day, "reading": "100", "notes": "PRIVATE-FUEL current measured crossing",
	}, nil)
	notificationFuelWebScan(t, f)
	var intent string
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_intents WHERE category='fuel' AND resource_id=$1 AND user_id=$2 AND state='pending'`, q.plan, q.actor.UserID).Scan(&intent); err != nil {
		t.Fatal("actual Fuel intent was not produced", err)
	}
	if err := service.ScheduleNotifications(f.ctx); err != nil {
		t.Fatal(err)
	}
	var deliveries []string
	if err := f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_deliveries WHERE intent_id=$1 AND subscription_id=$2)`, intent, q.connection).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if suffix == "late-device" || suffix == "late-registration" {
		if len(deliveries) != 0 {
			t.Fatal("Fuel mileage replayed history to a newly enrolled device", len(deliveries))
		}
		return q
	}
	if len(deliveries) != 1 {
		t.Fatal("one eligible Fuel recipient/device did not produce one delivery", len(deliveries))
	}
	q.id = deliveries[0]
	return q
}

func notificationFuelReassign(t *testing.T, f *integrationFixture, q notificationFuelDelivery) {
	t.Helper()
	fuelModuleNativeApply(t, f, "maintenance.save", q.vehicle, q.plan, q.planVersion, store.Record{
		"title": "PRIVATE-FUEL reassigned service", "due_odometer": "100", "odometer_series_id": q.series,
		"assignee_id": f.identity.MembershipID, "active": true, "task_opt_in": false,
	}, nil)
}

func TestNotificationFuelControlledDeliveryAndFinalSourceGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	var calls atomic.Int32
	client := shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		wire, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
		if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" || len(wire) < 100 || len(wire) > 8192 ||
			bytes.Contains(wire, []byte("PRIVATE-FUEL")) || bytes.Contains(wire, []byte("audience_scope")) || bytes.Contains(wire, []byte("due_odometer")) {
			return nil, errors.New("Fuel transport lost encrypted generic payload or bounded delivery metadata")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})
	if err := service.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"accepted", "late-device", "late-registration", "completed", "reassigned", "archived", "counter-replaced", "device-withdrawn", "logged-out"} {
		t.Run(action, func(t *testing.T) {
			q := notificationFuelQueue(t, f, service, action)
			if q.id == "" {
				return
			}
			switch action {
			case "completed":
				fuelModuleNativeApply(t, f, "maintenance.complete", q.vehicle, q.plan, q.planVersion, store.Record{"completed_on": q.day}, nil)
			case "reassigned":
				notificationFuelReassign(t, f, q)
			case "archived":
				detail, err := f.app.Store.FuelModuleRecord(f.ctx, f.identity, "vehicles", q.vehicle)
				if err != nil {
					t.Fatal(err)
				}
				fuelModuleNativeApply(t, f, "vehicle.archive", q.vehicle, q.vehicle, store.Int(detail.Record["version"]), store.Record{"archived": true}, nil)
			case "counter-replaced":
				series, _, _ := fuelModuleNativeApply(t, f, "series.save", q.vehicle, "", 0, store.Record{"started_on": q.day, "initial_reading": "0", "reason": "PRIVATE-FUEL replacement"}, nil)
				detail, err := f.app.Store.FuelModuleRecord(f.ctx, f.identity, "vehicles", q.vehicle)
				if err != nil {
					t.Fatal(err)
				}
				fuelModuleNativeApply(t, f, "vehicle.select_series", q.vehicle, q.vehicle, store.Int(detail.Record["version"]), store.Record{"odometer_series_id": series.RecordID, "series_version": series.Version}, nil)
			case "device-withdrawn":
				q.device.Enabled = false
				if _, err := service.SaveDevicePreference(f.ctx, q.actor, q.connection, q.device); err != nil {
					t.Fatal(err)
				}
			case "logged-out":
				if err := f.app.Auth.Logout(f.ctx, q.actor); err != nil {
					t.Fatal(err)
				}
			}
			before := calls.Load()
			job, lease := notificationClaimFixture(t, f, q.id)
			if err := service.SendNotificationJob(f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if action == "accepted" {
				want = 1
			}
			if calls.Load()-before != want {
				t.Fatal("Fuel source/authority changed but transport still ran", action, calls.Load()-before, want)
			}
			var state string
			if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.id).Scan(&state); err != nil || (state == "accepted") != (want == 1) {
				t.Fatal("Fuel delivery state does not match provider acceptance", state, err)
			}
		})
	}
	t.Run("reassignment while waiting for a transport slot prevents disclosure", func(t *testing.T) {
		q := notificationFuelQueue(t, f, service, "slot-withdrawal")
		type leased struct{ job, token string }
		occupied := make([]leased, 0, 2)
		for _, name := range []string{"fuel-slot-first", "fuel-slot-second"} {
			actor := shoppingTestMember(t, f, name, "reader")
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
		entered := make(chan struct{}, 3)
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		var activeCalls atomic.Int32
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
			activeCalls.Add(1)
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
		for _, lease := range occupied {
			wg.Go(func() { results <- service.SendNotificationJob(ctx, lease.job, lease.token) })
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("controlled transport slot not occupied")
			}
		}
		job, token := notificationClaimFixture(t, f, q.id)
		wg.Go(func() { results <- service.SendNotificationJob(ctx, job, token) })
		until := time.Now().Add(time.Second)
		for {
			var reserved bool
			if err := f.pool.QueryRow(f.ctx, "SELECT last_attempt_at IS NOT NULL FROM core.notification_deliveries WHERE id=$1", q.id).Scan(&reserved); err != nil {
				t.Fatal(err)
			}
			if reserved {
				break
			}
			if time.Now().After(until) {
				t.Fatal("Fuel sender did not finish initial preflight before slot wait")
			}
			time.Sleep(5 * time.Millisecond)
		}
		notificationFuelReassign(t, f, q)
		unblock()
		for range 3 {
			if err := <-results; err != nil {
				t.Fatal("post-slot guard or occupied transport failed", err)
			}
		}
		if activeCalls.Load() != 2 {
			t.Fatal("withdrawn Fuel recipient crossed final post-slot source guard", activeCalls.Load())
		}
		var state string
		if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.id).Scan(&state); err != nil || state != "cancelled" {
			t.Fatal("withdrawn source was not retired", state, err)
		}
	})
}
