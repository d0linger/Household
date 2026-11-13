//go:build integration

package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/value"
)

func notificationFuelApply(t *testing.T, f *recipeExpansionFixture, action, vehicle, rid string, version int64, data Record) FuelModuleResult {
	t.Helper()
	view, err := f.store.FuelModuleWorkspace(f.ctx, f.identity, FuelModuleQuery{Pane: "overview", Limit: 1})
	if err != nil {
		t.Fatal("current Fuel command authority", err)
	}
	raw := make(map[string]json.RawMessage, len(data))
	for key, val := range data {
		encoded, err := json.Marshal(val)
		if err != nil {
			t.Fatal(err)
		}
		raw[key] = encoded
	}
	command := FuelModuleCommand{Action: action, Generation: view.Context.Generation, VehicleID: vehicle, RecordID: rid,
		Version: version, SourceStamp: view.Context.Stamp, Data: raw}
	for range 4 {
		preview, err := f.store.FuelModulePreview(f.ctx, f.identity, command)
		if err != nil {
			t.Fatal("review real Fuel operation", action, err)
		}
		var acknowledged []string
		if raw := preview.Command.Data["acknowledged_warnings"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &acknowledged); err != nil {
				t.Fatal(err)
			}
		}
		missing := false
		for _, warning := range preview.Warnings {
			if warning.Code != "" && !slices.Contains(acknowledged, warning.Code) {
				acknowledged = append(acknowledged, warning.Code)
				missing = true
			}
		}
		command = preview.Command
		if !missing {
			result, err := f.store.FuelModuleExecute(f.ctx, f.identity, value.UUID(), value.UUID(), command)
			if err != nil {
				t.Fatal("execute real Fuel operation", action, err)
			}
			return result
		}
		slices.Sort(acknowledged)
		command.Data["acknowledged_warnings"], err = json.Marshal(slices.Compact(acknowledged))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("Fuel warning review failed to stabilize")
	return FuelModuleResult{}
}

func notificationFuelVehicle(t *testing.T, f *recipeExpansionFixture, member string, extra Record) FuelModuleResult {
	t.Helper()
	data := Record{"name": "SYNTHETIC notification " + value.UUID(), "fuel_type": "diesel"}
	if member != "" {
		data["responsible_member_id"] = member
	}
	for key, val := range extra {
		data[key] = val
	}
	return notificationFuelApply(t, f, "vehicle.save", "", "", 0, data)
}

func notificationFuelEditVehicle(t *testing.T, f *recipeExpansionFixture, vehicle string, changes Record) FuelModuleResult {
	t.Helper()
	detail, err := f.store.FuelModuleRecord(f.ctx, f.identity, "vehicles", vehicle)
	if err != nil {
		t.Fatal(err)
	}
	data := Record{}
	for _, field := range []string{"name", "fuel_type", "registration", "notes", "responsible_member_id", "next_service_on", "next_inspection_on", "insurance_renewal_on", "tank_capacity", "battery_capacity"} {
		if val, ok := detail.Record[field]; ok {
			data[field] = val
		}
	}
	for key, val := range changes {
		data[key] = val
	}
	return notificationFuelApply(t, f, "vehicle.save", vehicle, vehicle, Int(detail.Record["version"]), data)
}

func notificationFuelScan(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	for range 8 {
		if _, err := f.store.StageFuelNotifications(f.ctx, 50); err != nil {
			t.Fatal("advance actual bounded Fuel scan", err)
		}
	}
}

func notificationFuelOne(t *testing.T, f *recipeExpansionFixture, resource string, recipient auth.Identity) string {
	t.Helper()
	ids := notificationChorePending(t, f, resource, recipient.UserID, "fuel")
	if len(ids) != 1 {
		t.Fatal("expected one current Fuel intent", len(ids))
	}
	return ids[0]
}

func notificationFuelCalendar(t *testing.T, f *recipeExpansionFixture) time.Time {
	t.Helper()
	var now time.Time
	if err := f.app.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	zone := fmt.Sprintf("Etc/GMT%+d", now.UTC().Hour()-12)
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.households SET timezone=$2 WHERE id=$1", f.identity.HouseholdID, zone); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET changed_at=clock_timestamp()-interval '7 days' WHERE household_id=$1 AND module_id='fuel'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, location)
}

func notificationFuelHistoricalVehicle(t *testing.T, f *recipeExpansionFixture, vehicle string, before time.Time) {
	t.Helper()
	// Only this leased fixture supplies a source/assignment that predates its
	// calendar stage. The application never backdates source or consent clocks.
	if _, err := f.owner.Exec(f.ctx, `UPDATE fuel.vehicles SET updated_at=$3::timestamptz,responsible_since=CASE WHEN responsible_member_id IS NULL THEN NULL ELSE $3::timestamptz END
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, vehicle, before); err != nil {
		t.Fatal("set explicit historical date source", err)
	}
}

func TestNotificationFuelDateStagesConsentAndCurrentAuthority(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	due := notificationFuelCalendar(t, f)
	notificationChoreConsent(t, f, f.identity, "fuel", true)
	late := financeAuthorityMember(t, f, "reader")
	notificationChoreConsent(t, f, late, "fuel", false)
	for _, field := range []string{"next_service_on", "next_inspection_on", "insurance_renewal_on"} {
		for _, stage := range []int{7, 0} {
			t.Run(fmt.Sprintf("%s stage %d", field, stage), func(t *testing.T) {
				day := due.AddDate(0, 0, stage).Format(time.DateOnly)
				vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{field: day})
				notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
				notificationFuelScan(t, f)
				intentID := notificationFuelOne(t, f, vehicle.RecordID, f.identity)
				i := notificationChoreAudience(t, f, intentID, true)
				want := fmt.Sprintf("date/%s/%s/stage-%d/cycle-0", field, day, stage)
				if i.ResourceType != "vehicles" || i.Revision != vehicle.Version || i.Occurrence != want || !i.EventAt.Equal(due) || !i.ExpiresAt.Equal(due.Add(24*time.Hour)) {
					t.Fatal("Fuel date-stage contract changed", i.ResourceType, i.Revision, i.Occurrence)
				}
				notificationFuelScan(t, f)
				if notificationFuelOne(t, f, vehicle.RecordID, f.identity) != intentID {
					t.Fatal("repeated scan replaced an unchanged deadline")
				}
			})
		}
	}
	t.Run("unassigned and late-consenting members never receive historical dates", func(t *testing.T) {
		for _, member := range []string{"", late.MembershipID} {
			vehicle := notificationFuelVehicle(t, f, member, Record{"next_service_on": due.Format(time.DateOnly)})
			notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
			notificationFuelScan(t, f)
			var count int
			if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE resource_id=$1 AND category='fuel'", vehicle.RecordID).Scan(&count); err != nil || count != 0 {
				t.Fatal("Fuel inferred recipient or replayed pre-consent deadline", count, err)
			}
		}
	})
	t.Run("new retroactive date is suppressed without historical source", func(t *testing.T) {
		vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{"next_service_on": due.Format(time.DateOnly)})
		notificationFuelScan(t, f)
		if len(notificationChorePending(t, f, vehicle.RecordID, f.identity.UserID, "fuel")) != 0 {
			t.Fatal("newly entered past date was treated as an old scheduled event")
		}
	})
	for _, change := range []string{"archive", "reassign", "move-date", "clear-date", "edit-notes"} {
		t.Run("current audience rejects "+change, func(t *testing.T) {
			vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{"next_service_on": due.Format(time.DateOnly)})
			notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
			notificationFuelScan(t, f)
			intent := notificationFuelOne(t, f, vehicle.RecordID, f.identity)
			switch change {
			case "archive":
				notificationFuelApply(t, f, "vehicle.archive", vehicle.RecordID, vehicle.RecordID, vehicle.Version, Record{"archived": true})
			case "reassign":
				notificationFuelEditVehicle(t, f, vehicle.RecordID, Record{"responsible_member_id": late.MembershipID})
			case "move-date":
				notificationFuelEditVehicle(t, f, vehicle.RecordID, Record{"next_service_on": due.AddDate(0, 0, 2).Format(time.DateOnly)})
			case "clear-date":
				notificationFuelEditVehicle(t, f, vehicle.RecordID, Record{"next_service_on": nil})
			case "edit-notes":
				notificationFuelEditVehicle(t, f, vehicle.RecordID, Record{"notes": "SYNTHETIC new revision"})
			}
			notificationChoreAudience(t, f, intent, false)
		})
	}
	t.Run("accepted occurrence does not repeat across unrelated source revisions", func(t *testing.T) {
		vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{"next_inspection_on": due.Format(time.DateOnly)})
		notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
		notificationFuelScan(t, f)
		intent := notificationFuelOne(t, f, vehicle.RecordID, f.identity)
		if _, err := f.owner.Exec(f.ctx, "UPDATE core.notification_intents SET state='accepted' WHERE id=$1", intent); err != nil {
			t.Fatal(err)
		}
		notificationFuelEditVehicle(t, f, vehicle.RecordID, Record{"notes": "SYNTHETIC accepted source revision"})
		// Isolate logical-episode dedup from the independently tested updated_at
		// no-history cutoff by supplying an older revised source in this fixture.
		notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Minute))
		notificationFuelScan(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE category='fuel' AND resource_id=$1", vehicle.RecordID).Scan(&count); err != nil || count != 1 {
			t.Fatal("accepted date episode repeated across source version", count, err)
		}
	})
	t.Run("maintenance dates use their own assignee and completion cycle", func(t *testing.T) {
		reader := financeAuthorityMember(t, f, "reader")
		notificationChoreConsent(t, f, reader, "fuel", true)
		vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, nil)
		day := due.AddDate(0, 0, 7).Format(time.DateOnly)
		plan := notificationFuelApply(t, f, "maintenance.save", vehicle.RecordID, "", 0,
			Record{"title": "SYNTHETIC dated service", "due_on": day, "assignee_id": reader.MembershipID, "active": true, "task_opt_in": false})
		if _, err := f.owner.Exec(f.ctx, `UPDATE fuel.maintenance_plans SET updated_at=$3::timestamptz,assignee_since=$3::timestamptz
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, plan.RecordID, due.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		notificationFuelScan(t, f)
		intent := notificationFuelOne(t, f, plan.RecordID, reader)
		i := notificationChoreAudience(t, f, intent, true)
		if i.ResourceType != "maintenance_plans" || i.Occurrence != "date/due_on/"+day+"/stage-7/cycle-1" || i.Revision != plan.Version {
			t.Fatal("maintenance date did not retain cycle and current source", i.ResourceType, i.Occurrence)
		}
		if len(notificationChorePending(t, f, plan.RecordID, f.identity.UserID, "fuel")) != 0 {
			t.Fatal("vehicle responsibility overrode maintenance assignment")
		}
		notificationFuelApply(t, f, "maintenance.complete", vehicle.RecordID, plan.RecordID, plan.Version,
			Record{"completed_on": due.Format(time.DateOnly)})
		notificationChoreAudience(t, f, intent, false)
	})
	t.Run("durable scan continues beyond fifty vehicles", func(t *testing.T) {
		ids := make([]string, 0, 51)
		for range 51 {
			vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{"next_service_on": due.Format(time.DateOnly)})
			notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
			ids = append(ids, vehicle.RecordID)
		}
		notificationFuelScan(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, "SELECT count(DISTINCT resource_id) FROM core.notification_intents WHERE category='fuel' AND resource_id=ANY($1::uuid[])", ids).Scan(&count); err != nil || count != len(ids) {
			t.Fatal("bounded scan stranded vehicle deadlines", count, err)
		}
	})
	t.Run("module deactivation and reactivation do not replay date history", func(t *testing.T) {
		vehicle := notificationFuelVehicle(t, f, f.identity.MembershipID, Record{"insurance_renewal_on": due.Format(time.DateOnly)})
		notificationFuelHistoricalVehicle(t, f, vehicle.RecordID, due.Add(-time.Hour))
		notificationFuelScan(t, f)
		intent := notificationFuelOne(t, f, vehicle.RecordID, f.identity)
		state := f.store.States()["fuel"]
		if err := f.store.SetModule(f.ctx, f.identity, "fuel", false, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, intent, false)
		state = f.store.States()["fuel"]
		if err := f.store.SetModule(f.ctx, f.identity, "fuel", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, intent, false)
	})
}

func notificationFuelSeries(t *testing.T, f *recipeExpansionFixture, vehicle, day, initial string) string {
	t.Helper()
	series := notificationFuelApply(t, f, "series.save", vehicle, "", 0, Record{"started_on": day, "initial_reading": initial, "reason": "SYNTHETIC measured counter"})
	detail, err := f.store.FuelModuleRecord(f.ctx, f.identity, "vehicles", vehicle)
	if err != nil {
		t.Fatal(err)
	}
	notificationFuelApply(t, f, "vehicle.select_series", vehicle, vehicle, Int(detail.Record["version"]), Record{"odometer_series_id": series.RecordID, "series_version": series.Version})
	return series.RecordID
}

func notificationFuelPlan(t *testing.T, f *recipeExpansionFixture, vehicle, series, member, threshold string) FuelModuleResult {
	t.Helper()
	data := Record{"title": "SYNTHETIC private service", "due_odometer": threshold, "odometer_series_id": series, "active": true, "task_opt_in": false}
	if member != "" {
		data["assignee_id"] = member
	}
	return notificationFuelApply(t, f, "maintenance.save", vehicle, "", 0, data)
}

func notificationFuelEditPlan(t *testing.T, f *recipeExpansionFixture, vehicle, plan string, changes Record) FuelModuleResult {
	t.Helper()
	detail, err := f.store.FuelModuleRecord(f.ctx, f.identity, "maintenance_plans", plan)
	if err != nil {
		t.Fatal(err)
	}
	data := Record{}
	for _, field := range []string{"title", "notes", "due_on", "due_odometer", "odometer_series_id", "repeat_days", "repeat_distance", "active", "task_opt_in", "assignee_id"} {
		if val, ok := detail.Record[field]; ok {
			data[field] = val
		}
	}
	for key, val := range changes {
		data[key] = val
	}
	return notificationFuelApply(t, f, "maintenance.save", vehicle, plan, Int(detail.Record["version"]), data)
}

func notificationFuelReading(t *testing.T, f *recipeExpansionFixture, vehicle, series, day, reading string) {
	t.Helper()
	notificationFuelApply(t, f, "observation.save", vehicle, "", 0, Record{"odometer_series_id": series, "observed_on": day, "reading": reading, "notes": "SYNTHETIC private reading"})
}

func TestNotificationFuelKilometreEpisodesFreshnessAndMutations(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	notificationChoreConsent(t, f, f.identity, "fuel", false)
	late := financeAuthorityMember(t, f, "reader")
	var today string
	if err := f.app.QueryRow(f.ctx, "SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&today); err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse(time.DateOnly, today)
	if err != nil {
		t.Fatal(err)
	}
	newPlan := func(t *testing.T, assignee, start, initial string) (string, string, FuelModuleResult) {
		t.Helper()
		v := notificationFuelVehicle(t, f, f.identity.MembershipID, nil)
		s := notificationFuelSeries(t, f, v.RecordID, start, initial)
		return v.RecordID, s, notificationFuelPlan(t, f, v.RecordID, s, assignee, "100")
	}
	state := func(t *testing.T, plan, want string) {
		t.Helper()
		var actual string
		if err := f.app.QueryRow(f.ctx, "SELECT state FROM fuel.notification_maintenance_observations WHERE household_id=$1 AND plan_id=$2", f.identity.HouseholdID, plan).Scan(&actual); err != nil || actual != want {
			t.Fatal("kilometre observation state", actual, want, err)
		}
	}
	t.Run("first already due reading is a silent baseline", func(t *testing.T) {
		_, _, plan := newPlan(t, f.identity.MembershipID, today, "100")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "baseline")
		if len(notificationChorePending(t, f, plan.RecordID, f.identity.UserID, "fuel")) != 0 {
			t.Fatal("first already-due reading replayed mileage history")
		}
	})
	t.Run("fresh exact crossing survives repeated and concurrent scans once", func(t *testing.T) {
		vehicle, series, plan := newPlan(t, f.identity.MembershipID, today, "99.999999")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "healthy")
		notificationFuelReading(t, f, vehicle, series, today, "100")
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Go(func() { _, err := f.store.StageFuelNotifications(f.ctx, 50); results <- err })
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal("concurrent Fuel scan", err)
			}
		}
		notificationFuelScan(t, f)
		intent := notificationFuelOne(t, f, plan.RecordID, f.identity)
		i := notificationChoreAudience(t, f, intent, true)
		if i.ResourceType != "maintenance_plans" || i.Revision != plan.Version || i.Occurrence != "distance/cycle-1/episode-1" || !i.ExpiresAt.Equal(i.EventAt.Add(24*time.Hour)) {
			t.Fatal("kilometre episode contract changed", i.ResourceType, i.Revision, i.Occurrence)
		}
		notificationFuelScan(t, f)
		if notificationFuelOne(t, f, plan.RecordID, f.identity) != intent {
			t.Fatal("repeated due mileage created another occurrence")
		}
		next := notificationFuelApply(t, f, "maintenance.complete", vehicle, plan.RecordID, plan.Version,
			Record{"completed_on": today, "odometer": "100", "odometer_series_id": series, "next_due_odometer": "200"})
		notificationChoreAudience(t, f, intent, false)
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "healthy")
		notificationFuelReading(t, f, vehicle, series, today, "200")
		notificationFuelScan(t, f)
		var ids []string
		if err := f.app.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_intents WHERE resource_id=$1 AND category='fuel' AND source_revision=$2 AND occurrence LIKE 'distance/cycle-2/%')`, plan.RecordID, next.Version).Scan(&ids); err != nil || len(ids) != 1 {
			t.Fatal("completion did not establish independent next-cycle notification", len(ids), err)
		}
		notificationChoreAudience(t, f, ids[0], true)
	})
	t.Run("late consent cannot claim a crossing already in progress", func(t *testing.T) {
		vehicle, series, plan := newPlan(t, late.MembershipID, today, "99")
		notificationFuelScan(t, f)
		notificationChoreConsent(t, f, late, "fuel", false)
		notificationFuelReading(t, f, vehicle, series, today, "100")
		notificationFuelScan(t, f)
		if len(notificationChorePending(t, f, plan.RecordID, late.UserID, "fuel")) != 0 {
			t.Fatal("late consent replayed an unobserved kilometre crossing")
		}
	})
	t.Run("unassigned plan does not fall back to vehicle responsibility", func(t *testing.T) {
		vehicle, series, plan := newPlan(t, "", today, "99")
		notificationFuelScan(t, f)
		notificationFuelReading(t, f, vehicle, series, today, "100")
		notificationFuelScan(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE category='fuel' AND resource_id=$1", plan.RecordID).Scan(&count); err != nil || count != 0 {
			t.Fatal("unassigned maintenance plan inferred another recipient", count, err)
		}
	})
	t.Run("stale and future readings never establish current mileage", func(t *testing.T) {
		vehicle, series, plan := newPlan(t, f.identity.MembershipID, day.AddDate(0, 0, -31).Format(time.DateOnly), "99")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "ineligible")
		notificationFuelReading(t, f, vehicle, series, day.AddDate(0, 0, 1).Format(time.DateOnly), "200")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "ineligible")
		notificationFuelReading(t, f, vehicle, series, today, "99")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "healthy")
		notificationFuelReading(t, f, vehicle, series, today, "100")
		notificationFuelScan(t, f)
		notificationChoreAudience(t, f, notificationFuelOne(t, f, plan.RecordID, f.identity), true)
	})
	t.Run("thirty civil days remains the inclusive freshness boundary", func(t *testing.T) {
		vehicle, series, plan := newPlan(t, f.identity.MembershipID, day.AddDate(0, 0, -30).Format(time.DateOnly), "99")
		notificationFuelScan(t, f)
		state(t, plan.RecordID, "healthy")
		notificationFuelReading(t, f, vehicle, series, today, "100")
		notificationFuelScan(t, f)
		notificationChoreAudience(t, f, notificationFuelOne(t, f, plan.RecordID, f.identity), true)
	})
	for _, change := range []string{"reassign", "inactive-plan", "archive-vehicle", "counter-change", "backwards-reading", "stale-reading"} {
		t.Run("pending kilometre audience rejects "+change, func(t *testing.T) {
			vehicle, series, plan := newPlan(t, f.identity.MembershipID, day.AddDate(0, 0, -40).Format(time.DateOnly), "0")
			notificationFuelReading(t, f, vehicle, series, today, "99")
			notificationFuelScan(t, f)
			notificationFuelReading(t, f, vehicle, series, today, "100")
			notificationFuelScan(t, f)
			intent := notificationFuelOne(t, f, plan.RecordID, f.identity)
			switch change {
			case "reassign":
				notificationFuelEditPlan(t, f, vehicle, plan.RecordID, Record{"assignee_id": late.MembershipID})
			case "inactive-plan":
				notificationFuelEditPlan(t, f, vehicle, plan.RecordID, Record{"active": false})
			case "archive-vehicle":
				detail, err := f.store.FuelModuleRecord(f.ctx, f.identity, "vehicles", vehicle)
				if err != nil {
					t.Fatal(err)
				}
				notificationFuelApply(t, f, "vehicle.archive", vehicle, vehicle, Int(detail.Record["version"]), Record{"archived": true})
			case "counter-change":
				notificationFuelSeries(t, f, vehicle, today, "0")
			case "backwards-reading":
				// Probe a restored legacy inconsistency through the isolated owner;
				// normal typed writes already reject backwards physical facts.
				if _, err := f.owner.Exec(f.ctx, `UPDATE fuel.odometer_observations SET observed_on=$3::date-1
 WHERE household_id=$1 AND vehicle_id=$2 AND reading=100`, f.identity.HouseholdID, vehicle, today); err != nil {
					t.Fatal(err)
				}
			case "stale-reading":
				if _, err := f.owner.Exec(f.ctx, `UPDATE fuel.odometer_observations SET observed_on=$3::date-31
 WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vehicle, today); err != nil {
					t.Fatal(err)
				}
			}
			notificationChoreAudience(t, f, intent, false)
		})
	}
}
