//go:build integration

package store

import (
	"fmt"
	"testing"
	"time"

	"household/internal/meterfeed"
)

func TestNotificationMetersFrequentReadingsUseBoundedActualHourlyHistory(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	n := &energyNative{f: f, start: time.Now().UTC().Truncate(time.Hour).Add(-170 * time.Hour)}
	meter := energyNativeWrite(t, f, "meter.save", "", 0, Record{"name": "SYNTHETIC frequent meter", "unit_code": "kWh"})
	n.meter = String(meter["id"])
	if err := f.owner.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE meter_id=$1 AND is_default`, n.meter).Scan(&n.register); err != nil {
		t.Fatal(err)
	}
	energyNativeWrite(t, f, "register.save", n.register, 1, Record{"meter_id": n.meter, "name": "Hourly actual register", "measurement_kind": "cumulative", "unit_code": "kWh",
		"direction": "import", "timezone": "UTC", "input_mode": "manual", "max_gap_seconds": "7200", "min_history": "7", "daily_warning": "30"})
	device := energyNativeWrite(t, f, "device.save", "", 0, Record{"meter_id": n.meter, "register_id": n.register, "register_version": "2",
		"serial_number": "SYNTHETIC hourly device", "installed_at": n.start.Format(time.RFC3339), "initial_value": "0"})
	n.device = String(device["id"])
	notificationChoreConsent(t, f, f.identity, "meters", false)
	notificationMeterConfigure(t, n, f.identity.MembershipID, false, true, false)
	for hour := 1; hour <= 169; hour++ {
		energyNativeWrite(t, f, "reading.save", "", 0, n.readingData(t, n.start.Add(time.Duration(hour)*time.Hour), fmt.Sprint(hour), "actual"))
	}
	energyNativeProject(t, f)
	notificationMeterScan(t, f)
	var state string
	if err := f.app.QueryRow(f.ctx, `SELECT state FROM meters.notification_observations WHERE register_id=$1 AND kind='consumption'`, n.register).Scan(&state); err != nil || state != "healthy" {
		t.Fatal("frequent actual measurements never established healthy history", state, err)
	}
	spike := energyNativeWrite(t, f, "reading.save", "", 0, n.readingData(t, n.start.Add(170*time.Hour), "179", "actual"))
	energyNativeProject(t, f)
	notificationMeterScan(t, f)
	ids := notificationChorePending(t, f, n.register, f.identity.UserID, "meters")
	if len(ids) != 1 {
		t.Fatal("actual hourly threshold crossing missing", ids)
	}
	notificationChoreAudience(t, f, ids[0], true)
	// The raw tail is healthy again, but an older estimated hour within the
	// required six days must invalidate the aggregate proof, not rearm it.
	var oldReading string
	if err := f.app.QueryRow(f.ctx, `SELECT id::text FROM meters.readings WHERE register_id=$1 AND measured_at=$2`, n.register, n.start.Add(40*time.Hour)).Scan(&oldReading); err != nil {
		t.Fatal(err)
	}
	data := n.readingData(t, n.start.Add(40*time.Hour), "40", "estimated")
	data["reason"] = "SYNTHETIC missing actual evidence"
	energyNativeWrite(t, f, "reading.save", oldReading, 1, data)
	data = n.readingData(t, n.start.Add(170*time.Hour), "170", "actual")
	data["reason"] = "SYNTHETIC actual corrected spike"
	energyNativeWrite(t, f, "reading.save", String(spike["id"]), 1, data)
	energyNativeProject(t, f)
	notificationMeterScan(t, f)
	notificationChoreAudience(t, f, ids[0], false)
	if err := f.app.QueryRow(f.ctx, `SELECT state FROM meters.notification_observations WHERE register_id=$1 AND kind='consumption'`, n.register).Scan(&state); err != nil || state != "alert" {
		t.Fatal("estimated history incorrectly healed the measured episode", state, err)
	}
}

func TestNotificationMetersTelemetrySourceVersionsInvalidateEvidence(t *testing.T) {
	f := newEnergyNative(t, "telemetry")
	source := energyNativeSource(t, f)
	notificationMeterConfigure(t, f, f.f.identity.MembershipID, false, true, false)
	lease := energyNativeAdapterLease(t, f)
	observed := time.Now().UTC().Truncate(time.Microsecond)
	inserted, err := f.f.store.IngestEnergySample(f.f.ctx, *lease, meterfeed.Sample{MeasuredAt: observed, Value: "10", MeasurementKind: "cumulative", Unit: "kWh", Quality: "actual"})
	if err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	if err = f.f.store.FinishEnergyAdapter(f.f.ctx, *lease, "ready", meterfeed.MQTTStats{}); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) (string, time.Time) {
		t.Helper()
		tx, err := f.f.app.Begin(f.f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(tx)
		s, err := readMeterNotificationSource(f.f.ctx, tx, f.f.identity.HouseholdID, f.register)
		if err != nil {
			t.Fatal(err)
		}
		if want {
			// Dirty projections suppress evaluation without changing source
			// identity: otherwise every ingestion silently resets the episode.
			s.dirty = true
			dirty, err := meterNotificationSignal(f.f.ctx, tx, f.f.identity.HouseholdID, s, "consumption", time.Now().UTC())
			if err != nil || dirty.known {
				t.Fatal("dirty measurement was evaluated", dirty, err)
			}
			s.dirty = false
			clean, err := meterNotificationSignal(f.f.ctx, tx, f.f.identity.HouseholdID, s, "consumption", time.Now().UTC())
			if err != nil || dirty.stamp != clean.stamp {
				t.Fatal("dirty projection changed source stamp", err)
			}
		}
		stamp, since, current, err := meterTelemetryAuthority(f.f.ctx, tx, f.f.identity.HouseholdID, s, time.Now().UTC(), "fixture-base")
		if err != nil || current != want {
			t.Fatal("current source evidence", current, want, err)
		}
		return stamp, since
	}
	stamp, since := check(true)
	if since.After(observed) || since.IsZero() {
		t.Fatal("configuration boundary does not bound accepted sample")
	}
	data := Record{"register_id": f.register, "register_version": fmt.Sprint(f.registerVersion(t)), "device_id": f.device,
		"adapter": "mqtt", "source_name": "native/energy", "measurement_kind": "cumulative", "unit_code": "kWh", "enabled": false}
	energyNativeWrite(t, f.f, "source.save", String(source["id"]), 1, data)
	disabled, _ := check(false)
	if disabled == stamp {
		t.Fatal("disabled source retained the anomaly stamp")
	}
	data["enabled"] = true
	energyNativeWrite(t, f.f, "source.save", String(source["id"]), 2, data)
	reconfigured, boundary := check(false)
	if reconfigured == stamp || !boundary.After(since) {
		t.Fatal("re-enabled source reused previous version evidence")
	}
	// No fabricated projection or historical clock: the old accepted sample is
	// still retained, but cannot prove current authority after reconfiguration.
}
