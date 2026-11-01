//go:build integration

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/meterfeed"
	"household/internal/value"
)

func energyNativeSource(t *testing.T, f *energyNative) Record {
	t.Helper()
	return energyNativeWrite(t, f.f, "source.save", "", 0, Record{"register_id": f.register, "register_version": fmt.Sprint(f.registerVersion(t)), "device_id": f.device, "adapter": "mqtt", "source_name": "native/energy", "measurement_kind": "cumulative", "unit_code": "kWh", "enabled": true})
}
func energyNativeAdapterLease(t *testing.T, f *energyNative) *EnergyAdapterLease {
	t.Helper()
	if err := f.f.store.ScheduleEnergyAdapters(f.f.ctx, false, true); err != nil {
		t.Fatal(err)
	}
	lease, err := f.f.store.ClaimEnergyAdapter(f.f.ctx)
	if err != nil || lease == nil {
		t.Fatal("actual current adapter claim", lease, err)
	}
	return lease
}

func TestEnergyNativeAdapterLeaseDedupePersistentBudgetAndArchive(t *testing.T) {
	f := newEnergyNative(t, "telemetry")
	source := energyNativeSource(t, f)
	lease := energyNativeAdapterLease(t, f)
	sample := meterfeed.Sample{MeasuredAt: f.start.Add(time.Hour), Value: "1", MeasurementKind: "cumulative", Unit: "kWh", Quality: "actual"}
	inserted, err := f.f.store.IngestEnergySample(f.f.ctx, *lease, sample)
	if err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	inserted, err = f.f.store.IngestEnergySample(f.f.ctx, *lease, sample)
	if err != nil || inserted {
		t.Fatal("physical duplicate not deduplicated", inserted, err)
	}
	for n := 2; n <= 120; n++ {
		sample.MeasuredAt = f.start.Add(time.Hour + time.Duration(n)*time.Second)
		sample.Value = fmt.Sprint(n)
		if _, err = f.f.store.IngestEnergySample(f.f.ctx, *lease, sample); err != nil {
			t.Fatal(n, err)
		}
	}
	sample.MeasuredAt = f.start.Add(2 * time.Hour)
	sample.Value = "121"
	if _, err = f.f.store.IngestEnergySample(f.f.ctx, *lease, sample); !errors.Is(err, ErrEnergyFeedRate) {
		t.Fatal("persistent minute budget bypassed", err)
	}
	if _, err = f.f.owner.Exec(f.f.ctx, `UPDATE core.jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.store.IngestEnergySample(f.f.ctx, *lease, sample); err == nil {
		t.Fatal("expired lease ingested")
	}
	energyNativeWrite(t, f.f, "meter.save", f.meter, 1, Record{"name": "Native electricity", "unit_code": "kWh", "archived": true})
	if err = f.f.store.ScheduleEnergyAdapters(f.f.ctx, false, true); err != nil {
		t.Fatal(err)
	}
	if next, err := f.f.store.ClaimEnergyAdapter(f.f.ctx); err != nil || next != nil {
		t.Fatal("archived meter still schedules external traffic", next, err)
	}
	var status, state string
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT source.status_code,j.state FROM meters.integration_sources source JOIN core.jobs j ON j.id=$2 WHERE source.id=$1`, source["id"], lease.JobID).Scan(&status, &state); err != nil || status != "approval_required" || state != "paused" {
		t.Fatal(status, state, err)
	}
}

func TestEnergyNativeLargeTelemetryUsesAggregatesAndLeaseFencedRetention(t *testing.T) {
	f := newEnergyNative(t, "retention")
	source := energyNativeSource(t, f)
	ctx := audit.WithActor(f.f.ctx, f.f.identity.UserID, f.f.identity.HouseholdID)
	// Real typed INSERT guards, revision invalidation and audit remain enabled.
	// Fixed 500-row statements preserve the production statement deadline.
	for start := 1; start <= 10001; start += 500 {
		end := min(start+499, 10001)
		_, err := audit.Exec(ctx, f.f.owner, `INSERT INTO meters.telemetry(household_id,register_id,device_id,source_id,source_version,source_key,measured_at,value,kind,created_by)
 SELECT $1,$2,$3,$4,1,'native/'||n::text,$5::timestamptz+n*interval '1 second',n,'actual',$6 FROM generate_series($7::integer,$8::integer)n`, f.f.identity.HouseholdID, f.register, f.device, source["id"], f.start, f.f.identity.UserID, start, end)
		if err != nil {
			t.Fatal("actual typed bulk telemetry", start, err)
		}
	}
	energyNativeProject(t, f.f)
	before, err := f.f.store.EnergySeries(f.f.ctx, f.f.identity, f.query())
	if err != nil || String(before["total_quantity"]) != "10001.000000" || Int(before["coverage_seconds"]) != 10001 {
		t.Fatal("aggregate still applies old 10000-point bound", before, err)
	}
	history, err := f.f.store.EnergyHistory(f.f.ctx, f.f.identity, f.query())
	if err != nil || len(history["rows"].([]Record)) != 100 || String(history["next_cursor"]) == "" {
		t.Fatal("bounded raw keyset page", history, err)
	}
	if _, err = f.f.app.Exec(f.f.ctx, `DELETE FROM meters.telemetry WHERE register_id=$1`, f.register); err == nil {
		t.Fatal("raw telemetry deletion bypassed retention lease")
	}
	energyNativeWrite(t, f.f, "register.save", f.register, 2, Record{"meter_id": f.meter, "name": "Retained native telemetry", "measurement_kind": "cumulative", "unit_code": "kWh", "direction": "import", "timezone": "UTC", "input_mode": "telemetry", "max_gap_seconds": "86400", "min_history": "7", "retention_days": "7"})
	job, lease := energyNativeLease(t, f.f)
	if _, err = f.f.app.Exec(f.f.ctx, `SELECT meters.prune_telemetry($1,$2,$3,$4,500)`, f.f.identity.HouseholdID, f.register, job, value.UUID()); err == nil {
		t.Fatal("forged retention lease accepted")
	}
	for range 30 {
		if err = f.f.store.CompleteEnergyWorkspaceJob(f.f.ctx, job, lease); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM meters.telemetry WHERE register_id=$1`, f.register).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		job, lease = energyNativeLease(t, f.f)
	}
	var remaining int
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM meters.telemetry WHERE register_id=$1`, f.register).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("retention failed to preserve exactly the predecessor anchor", remaining, err)
	}
	after, err := f.f.store.EnergySeries(f.f.ctx, f.f.identity, f.query())
	if err != nil || String(after["total_quantity"]) != String(before["total_quantity"]) {
		t.Fatal("retention changed observed historical aggregate", after, err)
	}
}
