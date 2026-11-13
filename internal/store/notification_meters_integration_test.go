//go:build integration

package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/meterfeed"
	"household/internal/notification"
	"household/internal/value"
)

func notificationMeterConfigure(t *testing.T, f *energyNative, member string, reading, anomaly, stale bool) Record {
	t.Helper()
	view, err := f.f.store.MeterNotificationSettings(f.f.ctx, f.f.identity, f.register)
	if err != nil {
		t.Fatal(err)
	}
	settings := view["settings"].(Record)
	var id string
	if Int(settings["version"]) > 0 {
		id = f.register
	}
	return energyNativeWrite(t, f.f, "notification.save", id, Int(settings["version"]), Record{"register_id": f.register,
		"register_version": fmt.Sprint(f.registerVersion(t)), "responsible_member_id": member, "reading_enabled": reading,
		"anomaly_enabled": anomaly, "stale_enabled": stale, "stale_after_seconds": "300"})
}

func notificationMeterScan(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2`, f.identity.HouseholdID, MeterNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		n, err := f.store.StageMeterNotifications(f.ctx, 2)
		if err != nil {
			t.Fatal("meter notification scan", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("meter cursor did not finish")
}

func TestNotificationMetersSettingsAuthorityAndConcurrentScan(t *testing.T) {
	f := newEnergyNative(t, "manual")
	reader := financeAuthorityMember(t, f.f, "reader")
	initial, err := f.f.store.MeterNotificationSettings(f.f.ctx, reader, f.register)
	if err != nil || Int(initial["settings"].(Record)["version"]) != 0 {
		t.Fatal(initial, err)
	}
	configured := notificationMeterConfigure(t, f, reader.MembershipID, true, true, false)
	if Int(configured["version"]) != 1 {
		t.Fatal(configured)
	}
	command := EnergyCommand{Action: "notification.save", ID: f.register, Version: 1, Generation: f.f.store.States()["meters"].Generation, Data: energyRaw(Record{
		"register_id": f.register, "register_version": fmt.Sprint(f.registerVersion(t)), "responsible_member_id": reader.MembershipID,
		"reading_enabled": false, "anomaly_enabled": false, "stale_enabled": false, "stale_after_seconds": "300"})}
	t.Run("reader cannot mutate", func(t *testing.T) {
		if _, err := f.f.store.EnergyWrite(f.f.ctx, reader, value.UUID(), value.UUID(), command); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	})
	t.Run("exact replay and version conflict", func(t *testing.T) {
		key := value.UUID()
		first, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command)
		if err != nil {
			t.Fatal(err)
		}
		again, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command)
		if err != nil || Int(first["version"]) != Int(again["version"]) {
			t.Fatal(first, again, err)
		}
		if _, err = f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command); !errors.Is(err, ErrConflict) {
			t.Fatal("stale settings changed", err)
		}
	})
	t.Run("missing assignment cannot enable", func(t *testing.T) {
		command.Version = 2
		command.Data["responsible_member_id"] = []byte(`""`)
		command.Data["reading_enabled"] = []byte(`true`)
		if _, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command); err == nil {
			t.Fatal("unassigned notification enabled")
		}
	})
	t.Run("assignment clock cannot be forged", func(t *testing.T) {
		tx, err := audit.Begin(audit.WithActor(f.f.ctx, f.f.identity.UserID, f.f.identity.HouseholdID), f.f.app)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(tx)
		_, err = tx.Exec(f.f.ctx, `UPDATE meters.notification_settings SET responsible_since=clock_timestamp()-interval '1 year' WHERE id=$1`, f.register)
		if err != nil {
			t.Fatal(err)
		}
		var preserved bool
		if err = tx.QueryRow(f.f.ctx, `SELECT responsible_since>=created_at AND responsible_since>clock_timestamp()-interval '1 minute' FROM meters.notification_settings WHERE id=$1`, f.register).Scan(&preserved); err != nil || !preserved {
			t.Fatal("assignment history could be forged", err)
		}
	})
	t.Run("configuration changes have content-free audit history", func(t *testing.T) {
		var count int
		var safe bool
		err := f.f.app.QueryRow(f.f.ctx, `SELECT count(*),coalesce(bool_and(
 changes ? 'notification_configuration_version' AND NOT changes ? 'responsible_member_id'
 AND resource_id=$2 AND resource_type='registers'),false) FROM core.audit_log
 WHERE household_id=$1 AND module_id='meters' AND resource_id=$2 AND action LIKE 'notification_settings_%'`, f.f.identity.HouseholdID, f.register).Scan(&count, &safe)
		if err != nil || count != 2 || !safe {
			t.Fatal("notification configuration audit differs", count, safe, err)
		}
	})
	t.Run("bounded concurrent scans", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := f.f.store.StageMeterNotifications(f.f.ctx, 1); results <- err }()
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		notificationMeterScan(t, f.f)
		var count int
		if err := f.f.app.QueryRow(f.f.ctx, `SELECT count(*) FROM meters.notification_observations WHERE register_id=$1`, f.register).Scan(&count); err != nil || count != 3 {
			t.Fatal(count, err)
		}
	})
}

func newNotificationConsumption(t *testing.T) *energyNative {
	t.Helper()
	f := newRecipeExpansionFixture(t)
	n := &energyNative{f: f, start: time.Now().UTC().Truncate(time.Second).Add(-10 * 24 * time.Hour)}
	meter := energyNativeWrite(t, f, "meter.save", "", 0, Record{"name": "SYNTHETIC consumption", "unit_code": "kWh"})
	n.meter = String(meter["id"])
	if err := f.owner.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE meter_id=$1 AND is_default`, n.meter).Scan(&n.register); err != nil {
		t.Fatal(err)
	}
	energyNativeWrite(t, f, "register.save", n.register, 1, Record{"meter_id": n.meter, "name": "Measured consumption", "measurement_kind": "cumulative", "unit_code": "kWh",
		"direction": "import", "timezone": "UTC", "input_mode": "manual", "max_gap_seconds": "172800", "min_history": "7", "daily_warning": "20"})
	device := energyNativeWrite(t, f, "device.save", "", 0, Record{"meter_id": n.meter, "register_id": n.register, "register_version": fmt.Sprint(n.registerVersion(t)),
		"serial_number": "SYNTHETIC actual readings", "installed_at": n.start.Format(time.RFC3339), "initial_value": "0"})
	n.device = String(device["id"])
	return n
}

func TestNotificationMetersConsumptionRequiresCurrentActualData(t *testing.T) {
	f := newNotificationConsumption(t)
	notificationChoreConsent(t, f.f, f.f.identity, "meters", false)
	notificationMeterConfigure(t, f, f.f.identity.MembershipID, false, true, false)
	for n := 1; n <= 9; n++ {
		energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(time.Duration(n)*24*time.Hour), fmt.Sprint(n*10), "actual"))
	}
	energyNativeProject(t, f.f)
	notificationMeterScan(t, f.f)
	if len(notificationChorePending(t, f.f, f.register, f.f.identity.UserID, "meters")) != 0 {
		t.Fatal("healthy baseline sent")
	}
	spike := energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(10*24*time.Hour), "200", "actual"))
	t.Run("dirty calculations do not warn", func(t *testing.T) {
		notificationMeterScan(t, f.f)
		if len(notificationChorePending(t, f.f, f.register, f.f.identity.UserID, "meters")) != 0 {
			t.Fatal("dirty calculation notified")
		}
	})
	energyNativeProject(t, f.f)
	notificationMeterScan(t, f.f)
	ids := notificationChorePending(t, f.f, f.register, f.f.identity.UserID, "meters")
	if len(ids) != 1 {
		t.Fatal("new measured crossing missing", ids)
	}
	i := notificationChoreAudience(t, f.f, ids[0], true)
	if i.Occurrence != "consumption/episode-1/alert" || i.ResourceType != "meter_observation" {
		t.Fatal(i.Occurrence, i.ResourceType)
	}
	t.Run("repeat scan keeps original event", func(t *testing.T) {
		notificationMeterScan(t, f.f)
		again := notificationChorePending(t, f.f, f.register, f.f.identity.UserID, "meters")
		if len(again) != 1 || again[0] != ids[0] {
			t.Fatal(again)
		}
	})
	t.Run("estimate cannot retain send authority", func(t *testing.T) {
		data := f.readingData(t, f.start.Add(10*24*time.Hour), "200", "estimated")
		data["reason"] = "SYNTHETIC estimated reading correction"
		energyNativeWrite(t, f.f, "reading.save", String(spike["id"]), 1, data)
		energyNativeProject(t, f.f)
		notificationChoreAudience(t, f.f, ids[0], false)
		notificationMeterScan(t, f.f)
		var state string
		if err := f.f.app.QueryRow(f.f.ctx, `SELECT state FROM meters.notification_observations WHERE register_id=$1 AND kind='consumption'`, f.register).Scan(&state); err != nil || state != "alert" {
			t.Fatal("unknown data healed episode", state, err)
		}
	})
	t.Run("actual correction clears eligibility", func(t *testing.T) {
		data := f.readingData(t, f.start.Add(10*24*time.Hour), "100", "actual")
		data["reason"] = "SYNTHETIC photograph confirms correction"
		energyNativeWrite(t, f.f, "reading.save", String(spike["id"]), 2, data)
		energyNativeProject(t, f.f)
		notificationMeterScan(t, f.f)
		notificationChoreAudience(t, f.f, ids[0], false)
		var state string
		if err := f.f.app.QueryRow(f.f.ctx, `SELECT state FROM meters.notification_observations WHERE register_id=$1 AND kind='consumption'`, f.register).Scan(&state); err != nil || state != "healthy" {
			t.Fatal(state, err)
		}
	})
}

func TestNotificationMetersAdapterUsesAcceptedSampleNotConnection(t *testing.T) {
	f := newEnergyNative(t, "telemetry")
	source := energyNativeSource(t, f)
	notificationMeterConfigure(t, f, f.f.identity.MembershipID, false, false, true)
	lease := energyNativeAdapterLease(t, f)
	now := time.Now().UTC().Truncate(time.Second)
	inserted, err := f.f.store.IngestEnergySample(f.f.ctx, *lease, meterfeed.Sample{MeasuredAt: now.Add(-10 * time.Minute), Value: "10", MeasurementKind: "cumulative", Unit: "kWh", Quality: "actual"})
	if err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	if err = f.f.store.FinishEnergyAdapter(f.f.ctx, *lease, "ready", meterfeed.MQTTStats{}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.f.app.Begin(f.f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	s, err := readMeterNotificationSource(f.f.ctx, tx, f.f.identity.HouseholdID, f.register)
	if err != nil {
		t.Fatal(err)
	}
	signal, err := meterNotificationSignal(f.f.ctx, tx, f.f.identity.HouseholdID, s, "adapter", now)
	if err != nil || !signal.known || !signal.alert {
		t.Fatal("successful connection hid stale measurement", signal, err)
	}
	var latest time.Time
	if err = tx.QueryRow(f.f.ctx, `SELECT last_success_at FROM meters.integration_sources WHERE id=$1`, source["id"]).Scan(&latest); err != nil || latest.Before(now) {
		t.Fatal(latest, err)
	}
}

func TestNotificationMetersReadingCompletionUsesCurrentRevision(t *testing.T) {
	f := &energyNative{f: newRecipeExpansionFixture(t), start: time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)}
	meter := energyNativeWrite(t, f.f, "meter.save", "", 0, Record{"name": "SYNTHETIC reading deadlines", "unit_code": "kWh"})
	f.meter = String(meter["id"])
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT id::text FROM meters.registers WHERE meter_id=$1 AND is_default`, f.meter).Scan(&f.register); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	zone := fmt.Sprintf("Etc/GMT%+d", (now.Hour()-6+36)%24-12)
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	day := now.In(location).Format(time.DateOnly)
	energyNativeWrite(t, f.f, "register.save", f.register, f.registerVersion(t), Record{
		"meter_id": f.meter, "name": "Reading notification register", "measurement_kind": "cumulative", "unit_code": "kWh",
		"direction": "import", "timezone": zone, "input_mode": "manual", "max_gap_seconds": "86400", "min_history": "7"})
	device := energyNativeWrite(t, f.f, "device.save", "", 0, Record{"meter_id": f.meter, "register_id": f.register, "register_version": fmt.Sprint(f.registerVersion(t)),
		"serial_number": "SYNTHETIC reading deadline device", "installed_at": f.start.Format(time.RFC3339), "initial_value": "0"})
	f.device = String(device["id"])
	plan := energyNativeWrite(t, f.f, "plan.save", "", 0, Record{"register_id": f.register,
		"register_version": fmt.Sprint(f.registerVersion(t)), "anchor_on": day, "interval_days": "1", "actual_only": true, "active": true})
	notificationMeterConfigure(t, f, f.f.identity.MembershipID, true, false, false)
	// The source clock is real. Only the explicit evaluation time advances to
	// the next stage, so no business/assignment/audit clocks need rewriting.
	at, err := notification.CalendarStage(day, zone, "09:00", 0)
	if err != nil {
		t.Fatal(err)
	}
	read := func(want int) {
		t.Helper()
		tx, err := f.f.app.Begin(f.f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(tx)
		source, err := readMeterNotificationSource(f.f.ctx, tx, f.f.identity.HouseholdID, f.register)
		if err != nil {
			t.Fatal(err)
		}
		intents, err := meterReadingIntents(f.f.ctx, tx, f.f.identity.HouseholdID, source, at)
		if err != nil || len(intents) != want {
			t.Fatalf("reading intents=%d want=%d error=%v", len(intents), want, err)
		}
		if want == 1 && intents[0].Occurrence != "reading/"+String(plan["id"])+"/1/"+day {
			t.Fatal(intents[0].Occurrence)
		}
	}
	read(1)
	reading := energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, now, "10", "actual"))
	read(0)
	correction := f.readingData(t, now, "10", "estimated")
	correction["reason"] = "Review shows this was an estimate"
	energyNativeWrite(t, f.f, "reading.save", String(reading["id"]), 1, correction)
	read(1)
	correction["kind"] = "actual"
	correction["reason"] = "Actual reading verified"
	energyNativeWrite(t, f.f, "reading.save", String(reading["id"]), 2, correction)
	read(0)
}
