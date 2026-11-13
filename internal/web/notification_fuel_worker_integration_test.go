//go:build integration

package web

import (
	"testing"
	"time"

	"household/internal/notification"
	"household/internal/store"
	"household/internal/worker"
)

func TestNotificationFuelWorkerDurableScanAndEpisode(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, store.NotificationPreference{
		Category: "fuel", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"},
	}); err != nil {
		t.Fatal(err)
	}
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vehicleID, seriesID := store.String(vehicle["id"]), store.String(series["id"])
	day := fuelModuleReadNativeDay(t, f).Format(time.DateOnly)
	plan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", vehicleID, "", 0, store.Record{
		"title": "SYNTHETIC worker service", "due_odometer": "100", "odometer_series_id": seriesID,
		"assignee_id": f.identity.MembershipID, "active": true, "task_opt_in": false,
	}, nil)
	fuelModuleNativeApply(t, f, "observation.save", vehicleID, "", 0, store.Record{
		"odometer_series_id": seriesID, "observed_on": day, "reading": "99",
	}, nil)

	// Each instance starts without an in-memory cursor. The real generic job
	// dispatcher runs in Tick too and must leave the durable scanner pending.
	tick := func(wantPhase string) {
		t.Helper()
		background := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
		defer func() { _ = background.Close() }()
		if err := background.Tick(f.ctx); err != nil {
			t.Fatal("actual Fuel worker tick", err)
		}
		var phase, state string
		var attempts int
		if err := f.pool.QueryRow(f.ctx, `SELECT payload->>'phase',state,attempts FROM core.jobs
 WHERE household_id=$1 AND kind='fuel_notification_scan_v1'`, f.identity.HouseholdID).
			Scan(&phase, &state, &attempts); err != nil || phase != wantPhase || state != "pending" || attempts != 0 {
			t.Fatalf("durable Fuel cursor: phase=%s state=%s attempts=%d err=%v", phase, state, attempts, err)
		}
	}
	check := func(wantState string, wantIntents int) {
		t.Helper()
		var state string
		if err := f.pool.QueryRow(f.ctx, `SELECT state FROM fuel.notification_maintenance_observations
 WHERE household_id=$1 AND plan_id=$2`, f.identity.HouseholdID, plan.RecordID).Scan(&state); err != nil || state != wantState {
			t.Fatal("actual worker mileage observation", state, err)
		}
		var count int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents
 WHERE household_id=$1 AND user_id=$2 AND category='fuel' AND resource_id=$3`,
			f.identity.HouseholdID, f.identity.UserID, plan.RecordID).Scan(&count); err != nil || count != wantIntents {
			t.Fatal("actual worker Fuel episode", count, err)
		}
	}
	tick("maintenance")
	tick("vehicles")
	check("healthy", 0)
	fuelModuleNativeApply(t, f, "observation.save", vehicleID, "", 0, store.Record{
		"odometer_series_id": seriesID, "observed_on": day, "reading": "100",
	}, nil)
	tick("maintenance")
	tick("vehicles")
	check("due", 1)
	tick("maintenance")
	tick("vehicles")
	check("due", 1)
}
