//go:build integration

package web

import (
	"encoding/csv"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/store"
)

func fuelModuleReadNativeView(t *testing.T, f *integrationFixture, query store.FuelModuleQuery) store.FuelModuleView {
	t.Helper()
	view, err := f.app.Store.FuelModuleWorkspace(f.ctx, f.identity, query)
	if err != nil {
		t.Fatalf("read actual %s workspace: %v", query.Pane, err)
	}
	return view
}

func fuelModuleReadNativeObject(t *testing.T, input any) store.Record {
	t.Helper()
	switch record := input.(type) {
	case store.Record:
		return record
	default:
		t.Fatalf("expected current scoped object, got %T", input)
		return nil
	}
}

func fuelModuleReadNativeRows(t *testing.T, input any) []store.Record {
	t.Helper()
	switch rows := input.(type) {
	case []store.Record:
		return rows
	case []any:
		result := make([]store.Record, len(rows))
		for i, row := range rows {
			result[i] = fuelModuleReadNativeObject(t, row)
		}
		return result
	default:
		t.Fatalf("expected bounded scoped row collection, got %T", input)
		return nil
	}
}

func fuelModuleReadNativeDay(t *testing.T, f *integrationFixture) time.Time {
	t.Helper()
	view := fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "settings"})
	day, err := time.Parse(time.DateOnly, view.Context.Today)
	if err != nil {
		t.Fatal("actual household date", err)
	}
	return day
}

func fuelModuleReadNativeFill(t *testing.T, f *integrationFixture, vehicle, series string, day time.Time, reading, quantity, total, notes string) store.FuelModuleResult {
	t.Helper()
	result, _, _ := fuelModuleNativeApply(t, f, "fillup.save", vehicle, "", 0, store.Record{
		"odometer_series_id": series, "occurred_on": day.Format(time.DateOnly), "fuel_type": "diesel", "quantity_unit": "l",
		"odometer": reading, "quantity": quantity, "total_minor": total, "currency_code": "EUR", "station": "SYNTHETIC native fixture",
		"full_tank": true, "previous_fill_missing": false, "notes": notes,
	}, nil)
	return result
}

func TestFuelModuleReadLargeHistoryEOFCachesExactTotalsAndKeysetPages(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vehicleID, seriesID := store.String(vehicle["id"]), store.String(series["id"])
	today := fuelModuleReadNativeDay(t, f)
	from := today.AddDate(0, 0, -250).Format(time.DateOnly)
	// This is a bounded owner-only fixture, never application/demo data. Real
	// invalidation triggers still run for every inserted historical fact.
	if _, err := f.owner.Exec(f.ctx, `UPDATE fuel.odometer_series SET started_on=least(started_on,$3::date)
 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, seriesID, from); err != nil {
		t.Fatal(err)
	}
	for start := 1; start <= 20501; start += 500 {
		end := min(start+499, 20501)
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.fillups(id,household_id,vehicle_id,odometer_series_id,occurred_on,
 fuel_type,odometer,quantity,quantity_unit,total_minor,currency_code,full_tank,previous_fill_missing,notes,created_by)
 SELECT ('51000000-0000-4000-8000-'||lpad(g::text,12,'0'))::uuid,$1,$2,$3,$4::date+((g-1)/100),
 'diesel',g*10,0.123456,'l',999999999999,'EUR',true,false,'SYNTHETIC bounded large-history fixture',$5
	FROM generate_series($6::integer,$7::integer) g`, f.identity.HouseholdID, vehicleID, seriesID, from, f.identity.UserID, start, end); err != nil {
			t.Fatalf("seed bounded owner history [%d:%d]: %v", start, end, err)
		}
	}
	query := store.FuelModuleQuery{Pane: "reports", VehicleID: vehicleID, From: from, To: today.Format(time.DateOnly), Unit: "l", Limit: 200}
	pending := fuelModuleReadNativeView(t, f, query)
	if pending.Totals != nil || len(pending.Records) != 0 {
		t.Fatal("large pending history exposed partial totals")
	}
	var target, built, processed int64
	var status string
	if err := f.owner.QueryRow(f.ctx, `SELECT source_version,built_version,status FROM fuel.segment_state
 WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vehicleID).Scan(&target, &built, &status); err != nil || built == target || status == "ready" {
		t.Fatal("fixture unexpectedly has a published cache", target, built, status, err)
	}
	var previous int64
	completed := false
	for chunk := 1; chunk <= 43; chunk++ {
		if err := f.app.Store.FuelModuleHousekeeping(f.ctx, 1); err != nil {
			t.Fatalf("bounded worker chunk %d: %v", chunk, err)
		}
		var source int64
		if err := f.owner.QueryRow(f.ctx, `SELECT source_version,built_version,status,(checkpoint->>'rows')::bigint
 FROM fuel.segment_state WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vehicleID).Scan(&source, &built, &status, &processed); err != nil {
			t.Fatal(err)
		}
		if source != target || processed <= previous || processed-previous > 500 {
			t.Fatalf("worker skipped/expanded a chunk or changed target: chunk=%d source=%d target=%d rows=%d prior=%d", chunk, source, target, processed, previous)
		}
		if status == "ready" {
			if processed != 20501 || built != target || chunk != 42 {
				t.Fatalf("cache published before actual EOF: chunk=%d rows=%d built=%d target=%d", chunk, processed, built, target)
			}
			completed = true
			break
		}
		if built == target || status != "building" {
			t.Fatalf("partial cache became current: chunk=%d rows=%d state=%s built=%d", chunk, processed, status, built)
		}
		if chunk == 1 || chunk == 41 {
			partial := fuelModuleReadNativeView(t, f, query)
			if partial.Totals != nil || len(partial.Records) != 0 {
				t.Fatal("partially built cache exposed public totals", chunk)
			}
		}
		previous = processed
	}
	if !completed {
		t.Fatal("bounded worker did not reach EOF")
	}
	var intervals, jobs int
	var quantity, distance, amount string
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*),sum(quantity)::text,sum(distance)::text,
 sum((costs_minor->>'EUR')::numeric)::text FROM fuel.consumption_segments
 WHERE household_id=$1 AND vehicle_id=$2 AND source_version=$3 AND algorithm_version=1 AND state='known'`,
		f.identity.HouseholdID, vehicleID, target).Scan(&intervals, &quantity, &distance, &amount); err != nil {
		t.Fatal(err)
	}
	wantMoney := new(big.Int).Mul(big.NewInt(999999999999), big.NewInt(20500)).String()
	if intervals != 20500 || quantity != "2530.848000" || distance != "205000.000000" || amount != wantMoney {
		t.Fatalf("whole-history SQL totals lost precision or rows: n=%d q=%s d=%s cost=%s", intervals, quantity, distance, amount)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM fuel.segment_jobs WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vehicleID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("completed job was not retired", jobs, err)
	}
	seen := map[string]bool{}
	lastDay, lastID := "", ""
	for page := 0; page < 104; page++ {
		view := fuelModuleReadNativeView(t, f, query)
		groups := fuelModuleReadNativeRows(t, view.Totals["groups"])
		costs := fuelModuleReadNativeRows(t, view.Totals["costs_per_km"])
		if store.String(view.Options["segments_status"]) != "ready" || store.String(view.Totals["record_count"]) != "20500" ||
			len(groups) != 1 || store.String(groups[0]["quantity"]) != quantity || store.String(groups[0]["distance"]) != distance ||
			store.String(groups[0]["per_100"]) != "1.23" || len(costs) != 1 || store.String(costs[0]["amount_minor"]) != wantMoney {
			t.Fatalf("page %d totals only cover its details or round money: %+v", page, view.Totals)
		}
		for _, row := range view.Records {
			id, day := store.String(row["id"]), store.String(row["to_on"])
			if seen[id] || id == "" || store.String(row["state"]) != "available" ||
				lastDay != "" && (day > lastDay || day == lastDay && id >= lastID) {
				t.Fatalf("keyset repeated or reordered interval on page %d: %+v", page, row)
			}
			seen[id], lastDay, lastID = true, day, id
		}
		if !view.More {
			if view.Next != "" {
				t.Fatal("EOF returned a continuation")
			}
			break
		}
		if view.Next == "" || view.Next == query.Cursor {
			t.Fatal("large indexed history cannot continue")
		}
		query.Cursor = view.Next
	}
	if len(seen) != 20500 {
		t.Fatalf("keyset stopped at %d of 20,500 intervals", len(seen))
	}
}

func TestFuelModuleReadHistoricalBackwardsPrefixRemainsUnknownBeforeAndAfterCache(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	today := fuelModuleReadNativeDay(t, f)
	// These are owner-restored prior facts allowed by the original schema, not
	// a new typed write that pretends a backwards counter is a valid reset.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.fillups(household_id,vehicle_id,odometer_series_id,occurred_on,
 fuel_type,odometer,quantity,quantity_unit,total_minor,currency_code,full_tank,previous_fill_missing,notes,created_by)
 SELECT $1,$2,$3,$4::date+(n-1),'diesel',reading,5,'l',500,'EUR',true,false,'SYNTHETIC prior backwards history',$5
 FROM(VALUES(1,800),(2,1000),(3,900),(4,1100),(5,1200)) prior(n,reading)`,
		f.identity.HouseholdID, vid, sid, today.AddDate(0, 0, -4).Format(time.DateOnly), f.identity.UserID); err != nil {
		t.Fatal("restore bounded prior facts", err)
	}
	query := store.FuelModuleQuery{Pane: "reports", VehicleID: vid, From: today.Format(time.DateOnly), To: today.Format(time.DateOnly), Limit: 10}
	assertUnknown := func(view store.FuelModuleView, phase string) {
		t.Helper()
		if view.Totals == nil || len(fuelModuleReadNativeRows(t, view.Totals["groups"])) != 0 || len(view.Records) != 1 {
			t.Fatalf("%s: narrow window silently restarted a blocked series", phase)
		}
		row := view.Records[0]
		if store.String(row["state"]) != "odometer_backwards" || row["quantity"] != nil || row["distance"] != nil || row["per_100"] != nil {
			t.Fatalf("%s: backwards prefix became a physical consumption claim: %+v", phase, row)
		}
		explained := false
		for _, warning := range view.Warnings {
			explained = explained || warning.Code == "odometer_backwards" && warning.NextAction != ""
		}
		if !explained {
			t.Fatalf("%s: actual backwards reason was hidden by the date window", phase)
		}
	}
	before := fuelModuleReadNativeView(t, f, query)
	if store.String(before.Options["segments_status"]) != "live_complete_window" {
		t.Fatal("bounded prefix did not take the genuine live path")
	}
	assertUnknown(before, "live prefix")
	if err := f.app.Store.FuelModuleHousekeeping(f.ctx, 1); err != nil {
		t.Fatal(err)
	}
	after := fuelModuleReadNativeView(t, f, query)
	if store.String(after.Options["segments_status"]) != "ready" {
		t.Fatal("five prior receipts did not produce a genuine EOF cache")
	}
	assertUnknown(after, "ready cache")
}

func TestFuelModuleReadMutationInvalidatesCursorAndFutureReceiptDoesNotEnterBalance(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	today := fuelModuleReadNativeDay(t, f)
	fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -3), "100", "5", "500", "anchor")
	fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -2), "200", "5", "500", "second full")
	closing := fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -1), "300", "5", "500", "closing full")
	if err := f.app.Store.FuelModuleHousekeeping(f.ctx, 1); err != nil {
		t.Fatal(err)
	}
	query := store.FuelModuleQuery{Pane: "reports", VehicleID: vid, From: today.AddDate(0, 0, -4).Format(time.DateOnly), To: today.Format(time.DateOnly), Limit: 1}
	before := fuelModuleReadNativeView(t, f, query)
	if !before.More || before.Next == "" || store.String(before.Options["segments_status"]) != "ready" {
		t.Fatal("genuine ready report did not provide a continuation")
	}
	_, _, _ = fuelModuleNativeApply(t, f, "fillup.save", vid, closing.RecordID, closing.Version, store.Record{
		"odometer_series_id": sid, "occurred_on": today.AddDate(0, 0, -1).Format(time.DateOnly), "fuel_type": "diesel", "quantity_unit": "l",
		"odometer": "300", "quantity": "6", "total_minor": "600", "currency_code": "EUR", "full_tank": true, "previous_fill_missing": false, "notes": "corrected quantity",
	}, nil)
	query.Cursor = before.Next
	if _, err := f.app.Store.FuelModuleWorkspace(f.ctx, f.identity, query); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("old report cursor crossed the actual write revision: %v", err)
	}
	query.Cursor = ""
	current := fuelModuleReadNativeView(t, f, query)
	groups := fuelModuleReadNativeRows(t, current.Totals["groups"])
	if store.String(current.Options["segments_status"]) != "live_complete_window" || len(groups) != 1 || store.String(groups[0]["quantity"]) != "11.000000" {
		t.Fatal("stale cached quantity survived a current mutation", current.Totals)
	}
	future := fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, 1), "400", "50", "5000", "future must not enter current balance")
	if err := f.app.Store.FuelModuleHousekeeping(f.ctx, 1); err != nil {
		t.Fatal(err)
	}
	after := fuelModuleReadNativeView(t, f, query)
	groups = fuelModuleReadNativeRows(t, after.Totals["groups"])
	if store.String(after.Options["segments_status"]) != "ready" || len(groups) != 1 || store.String(groups[0]["quantity"]) != "11.000000" {
		t.Fatal("future receipt closed today's balance", after.Totals)
	}
	var futureSegments int
	var cutoff string
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM fuel.consumption_segments cache JOIN fuel.segment_state state
 ON state.household_id=cache.household_id AND state.vehicle_id=cache.vehicle_id AND state.source_version=cache.source_version
 WHERE cache.household_id=$1 AND cache.vehicle_id=$2 AND cache.to_fillup_id=$3`, f.identity.HouseholdID, vid, future.RecordID).Scan(&futureSegments); err != nil || futureSegments != 0 {
		t.Fatal("future receipt was persisted in the current cache", futureSegments, err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT checkpoint->>'as_of' FROM fuel.segment_state WHERE household_id=$1 AND vehicle_id=$2`, f.identity.HouseholdID, vid).Scan(&cutoff); err != nil || cutoff != today.Format(time.DateOnly) {
		t.Fatal("actual household cutoff was not retained", cutoff, err)
	}
}

func TestFuelModuleReadMaintenanceUsesFourStreamsCurrentSeriesAndFutureCutoff(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	today := fuelModuleReadNativeDay(t, f)
	plan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", vid, "", 0, store.Record{
		"title": "SYNTHETIC documented distance service", "due_odometer": "450", "odometer_series_id": sid, "active": true, "task_opt_in": false,
	}, nil)
	assertReading := func(source, reading, distanceState string, due bool) {
		t.Helper()
		view := fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "maintenance", VehicleID: vid})
		if len(view.Records) != 1 || store.String(view.Records[0]["id"]) != plan.RecordID {
			t.Fatal("actual maintenance plan not visible")
		}
		row := view.Records[0]
		current := fuelModuleReadNativeObject(t, row["current_reading"])
		if store.String(current["source"]) != source || store.String(current["reading"]) != reading ||
			store.String(row["distance_state"]) != distanceState || row["due"] != due {
			t.Fatalf("current reading/due rule mismatch: row=%+v reading=%+v", row, current)
		}
	}
	fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -4), "100", "5", "500", "first stream")
	assertReading("fillup", "100.000000", "upcoming", false)
	_, _, _ = fuelModuleNativeApply(t, f, "observation.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "observed_on": today.AddDate(0, 0, -3).Format(time.DateOnly), "reading": "200", "notes": "second stream",
	}, nil)
	assertReading("observation", "200.000000", "upcoming", false)
	_, _, _ = fuelModuleNativeApply(t, f, "charge.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "occurred_on": today.AddDate(0, 0, -2).Format(time.DateOnly), "odometer": "300", "energy_kwh": "10",
		"measurement_basis": "wall", "total_minor": "500", "currency_code": "EUR", "location": "SYNTHETIC charging", "charge_kind": "home",
	}, nil)
	assertReading("charging", "300.000000", "upcoming", false)
	_, _, _ = fuelModuleNativeApply(t, f, "trip.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "started_on": today.AddDate(0, 0, -1).Format(time.DateOnly), "ended_on": today.Format(time.DateOnly),
		"start_odometer": "400", "end_odometer": "500", "purpose": "SYNTHETIC fourth stream",
	}, nil)
	assertReading("trip", "500.000000", "due", true)
	_, _, _ = fuelModuleNativeApply(t, f, "observation.save", vid, "", 0, store.Record{
		"odometer_series_id": sid, "observed_on": today.AddDate(0, 0, 1).Format(time.DateOnly), "reading": "900", "notes": "future excluded",
	}, nil)
	assertReading("trip", "500.000000", "due", true)
	other, _, _ := fuelModuleNativeApply(t, f, "series.save", vid, "", 0, store.Record{
		"started_on": today.AddDate(0, 0, -10).Format(time.DateOnly), "initial_reading": "0", "reason": "SYNTHETIC separate counter",
	}, nil)
	_, _, _ = fuelModuleNativeApply(t, f, "observation.save", vid, "", 0, store.Record{
		"odometer_series_id": other.RecordID, "observed_on": today.Format(time.DateOnly), "reading": "600", "notes": "separate actual counter",
	}, nil)
	assertReading("trip", "500.000000", "due", true)
	detail, err := f.app.Store.FuelModuleRecord(f.ctx, f.identity, "vehicles", vid)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = fuelModuleNativeApply(t, f, "vehicle.select_series", vid, vid, store.Int(detail.Record["version"]), store.Record{
		"odometer_series_id": other.RecordID, "series_version": other.Version,
	}, nil)
	assertReading("observation", "600.000000", "unknown_series_or_reading", false)
	// A restored legacy vehicle without an explicit selection remains ambiguous;
	// a high value in another counter must not turn into a false maintenance due.
	if _, err = f.owner.Exec(f.ctx, `UPDATE fuel.vehicles SET current_series_id=NULL,version=version+1 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, vid); err != nil {
		t.Fatal(err)
	}
	view := fuelModuleReadNativeView(t, f, store.FuelModuleQuery{Pane: "maintenance", VehicleID: vid})
	current := fuelModuleReadNativeObject(t, view.Records[0]["current_reading"])
	if current["reading"] != nil || store.String(current["reason"]) != "overlapping_series" || view.Records[0]["due"] != false ||
		store.String(view.Records[0]["distance_state"]) != "unknown_series_or_reading" {
		t.Fatal("legacy ambiguity became a current kilometre claim", view.Records[0])
	}
}

func TestFuelModuleReadExportPreservesFullNotesWindowAndCurrentActor(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	vid, sid := store.String(vehicle["id"]), store.String(series["id"])
	today := fuelModuleReadNativeDay(t, f)
	notes := "=SUM(1,2)\n\"SYNTHETIC full note\", " + strings.Repeat("Ä", 5000) + " END-OF-ORIGINAL"
	fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -8), "100", "5", "500", "outside older bound")
	first := fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -2), "200", "5", "500", notes)
	second := fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, -1), "300", "5", "500", "second in window")
	fuelModuleReadNativeFill(t, f, vid, sid, today.AddDate(0, 0, 1), "400", "5", "500", "outside future bound")
	query := store.FuelModuleQuery{Pane: "history", VehicleID: vid, From: today.AddDate(0, 0, -2).Format(time.DateOnly), To: today.Format(time.DateOnly), Limit: 1}
	page := fuelModuleReadNativeView(t, f, query)
	if !page.More || page.Next == "" {
		t.Fatal("actual two-row window did not page")
	}
	detail, err := f.app.Store.FuelModuleRecord(f.ctx, f.identity, "fillups", first.RecordID)
	if err != nil || store.String(detail.Record["notes"]) != notes {
		t.Fatal("typed detail shortened original notes", err)
	}
	header, rows, err := f.app.Store.FuelModuleExport(f.ctx, f.identity, query)
	if err != nil || len(rows) != 2 {
		t.Fatal("whole selected window export", len(rows), err)
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[name] = i
	}
	for _, required := range []string{"id", "notes", "occurred_on", "quantity", "total_minor", "text_truncated"} {
		if _, ok := columns[required]; !ok {
			t.Fatal("missing explicit CSV field", required)
		}
	}
	exported := map[string][]string{}
	for _, row := range rows {
		exported[row[columns["id"]]] = row
		if row[columns["total_minor"]] != "500" || row[columns["text_truncated"]] != "false" {
			t.Fatal("export changed precise cents or retained a list preview", row)
		}
	}
	if exported[first.RecordID] == nil || exported[second.RecordID] == nil || exported[first.RecordID][columns["notes"]] != notes {
		t.Fatal("export lost full UTF-8/quoted/multiline notes or included out-of-window facts")
	}
	query.Cursor = page.Next
	if h, r, err := f.app.Store.FuelModuleExport(f.ctx, f.identity, query); err == nil || h != nil || r != nil {
		t.Fatal("partial-page export accepted or returned partial output", err)
	}
	query.Cursor = ""
	values := url.Values{"pane": {"history"}, "vehicle": {vid}, "from": {query.From}, "to": {query.To}}
	path := "/api/v1/fuel/module/export?" + values.Encode()
	response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("actual CSV route status/error: %+v %v", response, err)
	}
	csvRows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(csvRows) != 3 {
		t.Fatal("quoted CSV could not be read exactly", len(csvRows), err)
	}
	csvColumns := map[string]int{}
	for i, field := range csvRows[0] {
		csvColumns[field] = i
	}
	matched := false
	for _, row := range csvRows[1:] {
		if row[csvColumns["id"]] == first.RecordID {
			matched = row[csvColumns["notes"]] == "'"+notes && row[csvColumns["total_minor"]] == "500"
		}
	}
	if !matched {
		t.Fatal("download lost original CSV text or failed formula-safe presentation")
	}
	values.Set("cursor", page.Next)
	status, _, _ := fuelModuleNativeHTTP(t, f, http.MethodGet, "/api/v1/fuel/module/export?"+values.Encode(), nil, "")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("HTTP partial export accepted: %d", status)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1`, f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if h, r, err := f.app.Store.FuelModuleExport(f.ctx, f.identity, query); !errors.Is(err, store.ErrForbidden) || h != nil || r != nil {
		t.Fatal("export trusted stale actor or returned partial data", err)
	}
	if _, err = f.app.Store.FuelModuleRecord(f.ctx, f.identity, "fillups", first.RecordID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("full detail trusted stale actor", err)
	}
	status, body, deniedRaw := fuelModuleNativeHTTP(t, f, http.MethodGet, path, nil, "")
	if status == http.StatusOK || strings.Contains(string(deniedRaw), "END-OF-ORIGINAL") {
		t.Fatalf("revoked HTTP actor received CSV facts: status=%d error=%s", status, store.String(body["error"]))
	}
}
