//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func notificationMeterWrite(t *testing.T, f *integrationFixture, action, id string, version int64, data store.Record) store.Record {
	t.Helper()
	result, err := f.app.Store.EnergyWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.EnergyCommand{
		Generation: f.app.Store.States()["meters"].Generation, Action: action, ID: id, Version: version, Data: energyHTTPData(t, data),
	})
	if err != nil {
		t.Fatal("actual meter command", action, err)
	}
	return result
}

func notificationMeterProject(t *testing.T, f *integrationFixture) {
	t.Helper()
	// The fixture claims bounded actual projection work. No calculated intervals,
	// observation state, notification intent or delivery is written by the test.
	for range 200 {
		var pending bool
		if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM meters.dirty_ranges WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		job, lease := value.UUID(), value.UUID()
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,dedupe_key,state,lease_token,lease_until)
 VALUES($1,$2,'meters',$3,$4,$5,'running',$6,clock_timestamp()+interval '60 seconds')`, job, f.identity.HouseholdID,
			f.app.Store.States()["meters"].Generation, store.EnergyWorkspaceJobKind, "notification-meter-projection/"+job, lease); err != nil {
			t.Fatal(err)
		}
		if err := f.app.Store.CompleteEnergyWorkspaceJob(f.ctx, job, lease); err != nil {
			t.Fatal("actual leased meter projection", err)
		}
	}
	t.Fatal("meter projection exceeded bounded work")
}

func notificationMeterWebScan(t *testing.T, f *integrationFixture) {
	t.Helper()
	// Advance the disposable worker retry clock only, not source/consent clocks.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2`,
		f.identity.HouseholdID, store.MeterNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		count, err := f.app.Store.StageMeterNotifications(f.ctx, 10)
		if err != nil {
			t.Fatal("actual bounded meter notification scan", err)
		}
		if count == 0 {
			return
		}
	}
	t.Fatal("meter cursor did not finish")
}

type notificationMeterDelivery struct {
	actor                                            auth.Identity
	meter, register, device, reading, connection, id string
	at                                               time.Time
	pref                                             store.NotificationPreference
	devicePref                                       webpush.DevicePreference
}

func notificationMeterConfigure(t *testing.T, f *integrationFixture, q notificationMeterDelivery, member string, enabled bool) {
	t.Helper()
	view, err := f.app.Store.MeterNotificationSettings(f.ctx, f.identity, q.register)
	if err != nil {
		t.Fatal(err)
	}
	settings := view["settings"].(store.Record)
	version := store.Int(settings["version"])
	id := ""
	if version > 0 {
		id = q.register
	}
	notificationMeterWrite(t, f, "notification.save", id, version, store.Record{
		"register_id": q.register, "register_version": "2", "responsible_member_id": member,
		"reading_enabled": false, "anomaly_enabled": enabled, "stale_enabled": false, "stale_after_seconds": "86400",
	})
}

func notificationMeterQueue(t *testing.T, f *integrationFixture, service *webpush.Service, suffix string) notificationMeterDelivery {
	t.Helper()
	q := notificationMeterDelivery{actor: shoppingTestMember(t, f, "meter-"+suffix, "reader")}
	var err error
	q.pref, err = f.app.Store.SaveNotificationPreference(f.ctx, q.actor, store.NotificationPreference{
		Category: "meters", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"},
	})
	if err != nil {
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
		q.devicePref, err = service.SaveDevicePreference(f.ctx, q.actor, q.connection, webpush.DevicePreference{Category: "meters", Enabled: true})
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
	meter := notificationMeterWrite(t, f, "meter.save", "", 0, store.Record{"name": "PRIVATE-METER " + suffix, "unit_code": "kWh"})
	q.meter = store.String(meter["id"])
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE household_id=$1 AND meter_id=$2 AND is_default`, f.identity.HouseholdID, q.meter).Scan(&q.register); err != nil {
		t.Fatal(err)
	}
	notificationMeterWrite(t, f, "register.save", q.register, 1, store.Record{
		"meter_id": q.meter, "name": "PRIVATE-METER register", "measurement_kind": "cumulative", "unit_code": "kWh", "direction": "import",
		"timezone": "UTC", "input_mode": "manual", "max_gap_seconds": "172800", "min_history": "7", "daily_warning": "20",
	})
	q.at = time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	start := q.at.Add(-10 * 24 * time.Hour)
	device := notificationMeterWrite(t, f, "device.save", "", 0, store.Record{"meter_id": q.meter, "register_id": q.register, "register_version": "2",
		"serial_number": "PRIVATE-METER physical counter", "installed_at": start.Format(time.RFC3339), "initial_value": "0"})
	q.device = store.String(device["id"])
	notificationMeterConfigure(t, f, q, q.actor.MembershipID, true)
	for day := 1; day <= 9; day++ {
		notificationMeterWrite(t, f, "reading.save", "", 0, store.Record{"register_id": q.register, "register_version": "2", "device_id": q.device,
			"measured_at": start.Add(time.Duration(day) * 24 * time.Hour).Format(time.RFC3339), "value": fmt.Sprint(day * 10), "kind": "actual"})
	}
	notificationMeterProject(t, f)
	notificationMeterWebScan(t, f)
	switch suffix {
	case "late-registration":
		register()
		enable()
	case "late-device":
		enable()
	}
	reading := notificationMeterWrite(t, f, "reading.save", "", 0, store.Record{"register_id": q.register, "register_version": "2", "device_id": q.device,
		"measured_at": q.at.Format(time.RFC3339), "value": "200", "kind": "actual"})
	q.reading = store.String(reading["id"])
	notificationMeterProject(t, f)
	notificationMeterWebScan(t, f)
	var intent string
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_intents WHERE category='meters' AND resource_id=$1
 AND user_id=$2 AND occurrence='consumption/episode-1/alert' AND state='pending'`, q.register, q.actor.UserID).Scan(&intent); err != nil {
		t.Fatal("actual meter crossing did not create intent", err)
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
			t.Fatal("new meter enrollment replayed an earlier healthy baseline", len(deliveries))
		}
		return q
	}
	if len(deliveries) != 1 {
		t.Fatal("expected exactly one meter delivery", len(deliveries))
	}
	q.id = deliveries[0]
	return q
}

func notificationMeterReassign(t *testing.T, f *integrationFixture, q notificationMeterDelivery) {
	t.Helper()
	notificationMeterConfigure(t, f, q, f.identity.MembershipID, true)
}

func notificationMeterCorrect(t *testing.T, f *integrationFixture, q notificationMeterDelivery, kind, reading string) {
	t.Helper()
	notificationMeterWrite(t, f, "reading.save", q.reading, 1, store.Record{"register_id": q.register, "register_version": "2", "device_id": q.device,
		"measured_at": q.at.Format(time.RFC3339), "value": reading, "kind": kind, "reason": "PRIVATE-METER reviewed actual correction"})
	notificationMeterProject(t, f)
}

func TestNotificationMetersControlledDeliveryAndFinalSourceGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["meters"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "meters", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	service, _ := shoppingPushService(t, f, true)
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		wire, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
		if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" || len(wire) < 100 || len(wire) > 8192 ||
			bytes.Contains(wire, []byte("PRIVATE-METER")) || bytes.Contains(wire, []byte("daily_warning")) {
			return nil, errors.New("meter transport lost encryption or bounded generic delivery metadata")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"accepted", "late-device", "late-registration", "corrected", "estimated", "disabled", "reassigned", "archived", "account-withdrawn", "device-withdrawn", "logged-out"} {
		t.Run(action, func(t *testing.T) {
			q := notificationMeterQueue(t, f, service, action)
			if q.id == "" {
				return
			}
			switch action {
			case "corrected":
				notificationMeterCorrect(t, f, q, "actual", "100")
			case "estimated":
				notificationMeterCorrect(t, f, q, "estimated", "200")
			case "disabled":
				notificationMeterConfigure(t, f, q, q.actor.MembershipID, false)
			case "reassigned":
				notificationMeterReassign(t, f, q)
			case "archived":
				notificationMeterWrite(t, f, "meter.save", q.meter, 1, store.Record{"name": "PRIVATE-METER archived", "unit_code": "kWh", "archived": true})
			case "account-withdrawn":
				q.pref.Enabled = false
				if _, err := f.app.Store.SaveNotificationPreference(f.ctx, q.actor, q.pref); err != nil {
					t.Fatal(err)
				}
			case "device-withdrawn":
				q.devicePref.Enabled = false
				if _, err := service.SaveDevicePreference(f.ctx, q.actor, q.connection, q.devicePref); err != nil {
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
				t.Fatal("meter source/authority guard failed", action, calls.Load()-before, want)
			}
			var state string
			if err := f.pool.QueryRow(f.ctx, `SELECT state FROM core.notification_deliveries WHERE id=$1`, q.id).Scan(&state); err != nil || (state == "accepted") != (want == 1) {
				t.Fatal(state, err)
			}
			if action == "corrected" {
				notificationMeterWebScan(t, f)
				var count int
				if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE resource_id=$1 AND occurrence LIKE '%/recovered'`, q.register).Scan(&count); err != nil || count != 0 {
					t.Fatal("unreceived warning produced an all-clear", count, err)
				}
			}
			if action == "accepted" {
				notificationMeterCorrect(t, f, q, "actual", "100")
				notificationMeterWebScan(t, f)
				if err := service.ScheduleNotifications(f.ctx); err != nil {
					t.Fatal(err)
				}
				var recovery string
				if err := f.pool.QueryRow(f.ctx, `SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE i.resource_id=$1 AND i.user_id=$2 AND i.occurrence='consumption/episode-1/recovered' AND d.subscription_id=$3`, q.register, q.actor.UserID, q.connection).Scan(&recovery); err != nil {
					t.Fatal("actual accepted warning did not permit recovery notification", err)
				}
				job, lease = notificationClaimFixture(t, f, recovery)
				if err := service.SendNotificationJob(f.ctx, job, lease); err != nil {
					t.Fatal(err)
				}
				if calls.Load()-before != 2 {
					t.Fatal("recovery did not reach controlled provider")
				}
			}
		})
	}
	t.Run("reassignment while waiting for a transport slot prevents disclosure", func(t *testing.T) {
		q := notificationMeterQueue(t, f, service, "slot-withdrawal")
		type leased struct{ job, token string }
		occupied := make([]leased, 0, 2)
		for _, name := range []string{"meter-slot-first", "meter-slot-second"} {
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
				t.Fatal("Meter sender did not finish initial preflight before slot wait")
			}
			time.Sleep(5 * time.Millisecond)
		}
		notificationMeterReassign(t, f, q)
		unblock()
		for range 3 {
			if err := <-results; err != nil {
				t.Fatal("post-slot guard or occupied transport failed", err)
			}
		}
		if activeCalls.Load() != 2 {
			t.Fatal("withdrawn Meter recipient crossed final post-slot source guard", activeCalls.Load())
		}
		var state string
		if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.id).Scan(&state); err != nil || state != "cancelled" {
			t.Fatal("withdrawn source was not retired", state, err)
		}
	})
}
