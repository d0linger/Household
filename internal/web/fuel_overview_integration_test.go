//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func fuelOverviewFixture(t *testing.T, f *integrationFixture, suffix string) (store.Record, store.Record) {
	t.Helper()
	v, _ := auditCreate(t, f, "fuel", "vehicles", store.Record{"name": "Overview " + suffix, "registration": "SYNTHETIC " + suffix, "fuel_type": "diesel"})
	s, _ := auditCreate(t, f, "fuel", "odometer_series", store.Record{"vehicle_id": v["id"], "started_on": "2026-01-01", "initial_reading": "0", "reason": "Original counter"})
	return v, s
}

func fuelOverviewFill(t *testing.T, f *integrationFixture, v, s store.Record, date, odo, quantity string, full, missing bool) store.Record {
	t.Helper()
	// Preserve the original 50.00 EUR fact as an explicit minor-unit string;
	// repeating unit-price decimals require the real reviewed warning/ACK flow.
	r, _, _ := fuelModuleNativeApply(t, f, "fillup.save", store.String(v["id"]), "", 0, store.Record{
		"odometer_series_id": s["id"], "occurred_on": date, "odometer": odo, "quantity": quantity, "total_minor": "5000",
		"currency_code": "EUR", "quantity_unit": "l", "fuel_type": "diesel", "full_tank": full, "previous_fill_missing": missing,
		"station": "PRIVATE STATION", "notes": "PRIVATE RAW NOTE",
	}, nil)
	return r.Data
}

func fuelOverviewEndSeries(t *testing.T, f *integrationFixture, v, series store.Record, ended string) {
	t.Helper()
	_, _, _ = fuelModuleNativeApply(t, f, "series.save", store.String(v["id"]), store.String(series["id"]), store.Int(series["version"]), store.Record{
		"started_on": series["started_on"], "ended_on": ended, "initial_reading": series["initial_reading"], "reason": series["reason"],
	}, nil)
}

func fuelOverviewRead(t *testing.T, f *integrationFixture, q store.FuelOverviewQuery) store.FuelOverviewPage {
	t.Helper()
	scope, err := f.app.Store.FuelOverviewContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.app.Store.FuelOverview(f.ctx, f.identity, scope, q)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFuelOverviewPhysicalFactsAndExplainedHistoricalInterval(t *testing.T) {
	f := newIntegrationFixture(t)
	v, s := fuelOverviewFixture(t, f, "physical")
	a := fuelOverviewFill(t, f, v, s, "2026-01-01", "1000", "30", true, false)
	fuelOverviewFill(t, f, v, s, "2026-01-02", "1200", "10", false, false)
	b := fuelOverviewFill(t, f, v, s, "2026-01-03", "1500", "15", true, false)
	out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "physical"})
	if len(out.Vehicles) != 1 {
		t.Fatal(out)
	}
	row := out.Vehicles[0]
	if row.Odometer == nil || *row.Odometer != "1500.000000" || row.Last == nil || row.Last.ID != b["id"] || row.Consumption == nil || row.Consumption.FromID != a["id"] || row.Consumption.ToID != b["id"] || row.Consumption.Per100 != "5.00" || row.Consumption.Quantity != "25.000000" || row.Consumption.Historical {
		t.Fatal(row)
	}
	fuelOverviewFill(t, f, v, s, "2026-01-04", "1600", "5", false, true)
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "physical"})
	row = out.Vehicles[0]
	if row.Consumption == nil || !row.Consumption.Historical || row.ConsumptionState != "missing_fill" || row.Odometer == nil || *row.Odometer != "1600.000000" {
		t.Fatal(row)
	}
	encoded, _ := json.Marshal(out)
	for _, forbidden := range []string{"total_minor", "unit_price", "PRIVATE STATION", "PRIVATE RAW NOTE", "resource_links", "financial_subject"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("private/unscoped projection", forbidden)
		}
	}
}

func TestFuelOverviewEmptyInitialReplacementOverlapAndFutureFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	empty, _ := auditCreate(t, f, "fuel", "vehicles", store.Record{"name": "Empty overview", "fuel_type": "diesel"})
	out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "Empty overview"})
	row := out.Vehicles[0]
	if row.ID != empty["id"] || row.Last != nil || row.Odometer != nil || row.Consumption != nil || len(row.Dates) != 0 || row.SeriesState != "no_current_series" {
		t.Fatal(row)
	}
	v, s := fuelOverviewFixture(t, f, "replacement")
	fuelOverviewFill(t, f, v, s, "2026-01-01", "90000", "30", true, false)
	fuelOverviewEndSeries(t, f, v, s, "2026-06-30")
	newSeries, _ := auditCreate(t, f, "fuel", "odometer_series", store.Record{"vehicle_id": v["id"], "started_on": "2026-07-01", "initial_reading": "12", "reason": "Replacement"})
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "replacement"})
	row = out.Vehicles[0]
	if row.Series == nil || row.Series.ID != newSeries["id"] || row.Odometer == nil || *row.Odometer != "12.000000" || row.OdometerSource != "initial_reading" || row.Consumption != nil {
		t.Fatal("counter maximum inferred", row)
	}
	scope, err := f.app.Store.FuelOverviewContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	day, _ := time.Parse(time.DateOnly, scope.Today)
	fuelOverviewFill(t, f, v, newSeries, day.AddDate(0, 0, 1).Format(time.DateOnly), "15", "2", true, false)
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "replacement"})
	row = out.Vehicles[0]
	if !row.FutureFills || *row.Odometer != "12.000000" || row.Last == nil || row.Last.Odometer != "90000.000000" {
		t.Fatal("future reading became current", row)
	}
	auditCreate(t, f, "fuel", "odometer_series", store.Record{"vehicle_id": v["id"], "started_on": "2026-07-02", "initial_reading": "0", "reason": "Ambiguous additional series"})
	out = fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "replacement"})
	row = out.Vehicles[0]
	if row.Series != nil || row.Odometer != nil || row.Consumption != nil || row.SeriesState != "overlapping_series" {
		t.Fatal(row)
	}
}

func TestFuelOverviewLatestTiesDoNotChooseAnArbitraryReceipt(t *testing.T) {
	f := newIntegrationFixture(t)
	v, s := fuelOverviewFixture(t, f, "tie")
	fuelOverviewFill(t, f, v, s, "2026-01-01", "1000", "30", true, false)
	fuelOverviewFill(t, f, v, s, "2026-01-01", "1000", "20", false, false)
	out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: "tie"})
	r := out.Vehicles[0]
	if r.Last != nil || r.LastState != "ambiguous_latest" || r.LastDay != "2026-01-01" || r.Odometer == nil || *r.Odometer != "1000.000000" || r.Consumption != nil || r.ConsumptionState != "ambiguous_order" {
		t.Fatal(r)
	}
}

func TestFuelOverviewSameDayAcrossCounterSeriesCannotRankReceipts(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, threeRows := range []bool{false, true} {
		name := "two-row counter day"
		if threeRows {
			name = "three-row counter day"
		}
		v, old := fuelOverviewFixture(t, f, name)
		fuelOverviewFill(t, f, v, old, "2026-06-30", "150000", "30", true, false)
		if threeRows {
			fuelOverviewFill(t, f, v, old, "2026-06-30", "150001", "10", false, false)
		}
		fuelOverviewEndSeries(t, f, v, old, "2026-06-30")
		replacement, _ := auditCreate(t, f, "fuel", "odometer_series", store.Record{"vehicle_id": v["id"], "started_on": "2026-06-30", "initial_reading": "0", "reason": "Replacement counter"})
		fuelOverviewFill(t, f, v, replacement, "2026-06-30", "12", "20", true, false)
		out := fuelOverviewRead(t, f, store.FuelOverviewQuery{Search: name})
		row := out.Vehicles[0]
		if row.Last != nil || row.LastState != "ambiguous_latest_series" || row.LastDay != "2026-06-30" || row.Series == nil || row.Series.ID != replacement["id"] || row.Odometer == nil || *row.Odometer != "12.000000" {
			t.Fatal("counter values ranked unrelated receipt order", row)
		}
	}
}

func TestFuelOverviewSignedPagingSearchArchiveAndIdentity(t *testing.T) {
	f := newIntegrationFixture(t)
	// Bounded owner-only fleet setup exercises actual App reads and keysets.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO fuel.vehicles(household_id,name,fuel_type,created_by) SELECT $1,'Fleet page '||g,'diesel',$2 FROM generate_series(1,23) g`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	var archived string
	if err := f.owner.QueryRow(f.ctx, "SELECT id FROM fuel.vehicles WHERE household_id=$1 AND name='Fleet page 1'", f.identity.HouseholdID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	_, _, _ = fuelModuleNativeApply(t, f, "vehicle.archive", archived, archived, 1, store.Record{"archived": true}, nil)
	type page struct {
		store.FuelOverviewPage
		Next string `json:"next_cursor"`
	}
	load := func(path string, status int) page {
		t.Helper()
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || r.StatusCode != status {
			t.Fatal(r, string(raw), err)
		}
		var got page
		if status == 200 {
			if err = json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	base := "/api/v1/fuel/overview?q=Fleet"
	first := load(base, 200)
	if len(first.Vehicles) != 20 || first.Next == "" {
		t.Fatal(first)
	}
	second := load(base+"&cursor="+url.QueryEscape(first.Next), 200)
	if len(second.Vehicles) != 2 || second.Next != "" {
		t.Fatal(second)
	}
	seen := map[string]bool{}
	for _, p := range []page{first, second} {
		for _, v := range p.Vehicles {
			if seen[v.ID] || v.ID == archived {
				t.Fatal("duplicate/archive leak")
			}
			seen[v.ID] = true
		}
	}
	load(base+"&archived=1&cursor="+url.QueryEscape(first.Next), 422)
	load(base+"&cursor=forged", 422)
	reader := shoppingTestMember(t, f, "overview-page-reader", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "overview reader")
	if err != nil {
		t.Fatal(err)
	}
	r, raw, err := f.request(http.MethodGet, base+"&cursor="+url.QueryEscape(first.Next), token, "", "", nil, nil)
	if err != nil || r.StatusCode != 422 {
		t.Fatal("cross-identity cursor", r, string(raw), err)
	}
	archivedPage := load("/api/v1/fuel/overview?q=Fleet%20page%201&archived=1", 200)
	found := false
	for _, v := range archivedPage.Vehicles {
		found = found || v.ID == archived && v.Archived
	}
	if !found {
		t.Fatal(archivedPage)
	}
}

func TestFuelOverviewCurrentAuthorityAndModuleBarrier(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelOverviewFixture(t, f, "authority")
	scope, err := f.app.Store.FuelOverviewContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	stale := f.identity
	stale.AuthVersion++
	if _, err = f.app.Store.FuelOverviewContext(f.ctx, stale); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("stale read authority", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.FuelOverviewContext(f.ctx, foreign); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("foreign household", err)
	}
	wrong := scope
	wrong.Generation++
	if _, err = f.app.Store.FuelOverview(f.ctx, f.identity, wrong, store.FuelOverviewQuery{}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale generation", err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "fuel", false, f.app.Store.States()["fuel"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	r, raw, err := f.request(http.MethodGet, "/api/v1/fuel/overview", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != 404 {
		t.Fatal("disabled overview", r, string(raw), err)
	}
}
