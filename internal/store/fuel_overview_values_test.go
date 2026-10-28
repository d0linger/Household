package store

import (
	"testing"

	"household/internal/value"
)

func fuelOverviewTestSeries() FuelOverviewSeries {
	return FuelOverviewSeries{ID: "00000000-0000-4000-8000-000000000001", VehicleID: "00000000-0000-4000-8000-000000000002", Started: "2026-01-01", Initial: "0.000000"}
}
func fuelOverviewTestFill(id, date, odo, quantity string, full, missing bool) FuelOverviewFill {
	s := fuelOverviewTestSeries()
	return FuelOverviewFill{ID: id, VehicleID: s.VehicleID, SeriesID: s.ID, Date: date, Odometer: odo, Quantity: quantity, Fuel: "diesel", Unit: "l", Full: full, Missing: missing}
}

func TestFuelOverviewExactIntervalBoundariesAndPartialFills(t *testing.T) {
	s := fuelOverviewTestSeries()
	rows := []FuelOverviewFill{fuelOverviewTestFill("a", "2026-01-01", "9007199254.000001", "50", true, false), fuelOverviewTestFill("b", "2026-01-02", "9007199254.100001", "0.000001", false, false), fuelOverviewTestFill("c", "2026-01-03", "9007199254.500001", "0.024999", true, false)}
	out, state, err := fuelExplainedConsumption(rows, s)
	if err != nil || state != "available" || out == nil || out.Distance != "0.500000" || out.Quantity != "0.025000" || out.Per100 != "5.00" || out.Historical || out.FromID != "a" || out.ToID != "c" {
		t.Fatal(out, state, err)
	}
	if rows[0].ID != "a" {
		t.Fatal("caller input mutated")
	}
	tiny := []FuelOverviewFill{fuelOverviewTestFill("a", "2026-01-01", "0", "30", true, false), fuelOverviewTestFill("b", "2026-01-02", "999999999999", "0.000001", true, false)}
	got, _, err := fuelExplainedConsumption(tiny, s)
	if err != nil || got == nil || !got.BelowPrecision || got.Quantity != "0.000001" {
		t.Fatal("positive tiny rate became an asserted zero", got, err)
	}
}

func TestFuelOverviewReasonsKeepHistoricalIntervalTruthful(t *testing.T) {
	s := fuelOverviewTestSeries()
	anchor := fuelOverviewTestFill("a", "2026-01-01", "1000", "30", true, false)
	full := fuelOverviewTestFill("b", "2026-01-02", "1500", "25", true, false)
	for _, tc := range []struct {
		name       string
		rows       []FuelOverviewFill
		state      string
		historical bool
		has        bool
	}{
		{"empty", nil, "no_fillups", false, false},
		{"first partial", []FuelOverviewFill{fuelOverviewTestFill("p", "2026-01-01", "1000", "20", false, false)}, "no_full_anchor", false, false},
		{"one full", []FuelOverviewFill{anchor}, "needs_next_full", false, false},
		{"new partial", []FuelOverviewFill{anchor, full, fuelOverviewTestFill("c", "2026-01-03", "1600", "5", false, false)}, "needs_next_full", true, true},
		{"missing", []FuelOverviewFill{anchor, full, fuelOverviewTestFill("c", "2026-01-03", "1600", "5", false, true)}, "missing_fill", true, true},
		{"zero distance", []FuelOverviewFill{anchor, fuelOverviewTestFill("c", "2026-01-03", "1000", "5", true, false)}, "zero_distance", false, false},
		{"tied", []FuelOverviewFill{anchor, full, fuelOverviewTestFill("c", "2026-01-03", "1600", "5", false, false), fuelOverviewTestFill("d", "2026-01-03", "1600", "6", true, false)}, "ambiguous_order", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, state, err := fuelExplainedConsumption(tc.rows, s)
			if err != nil || state != tc.state || (got != nil) != tc.has || got != nil && got.Historical != tc.historical {
				t.Fatal(got, state, err)
			}
		})
	}
}

func TestFuelOverviewFuelUnitsResetAndInvalidHistory(t *testing.T) {
	s := fuelOverviewTestSeries()
	a := fuelOverviewTestFill("a", "2026-01-01", "1000", "30", true, false)
	b := fuelOverviewTestFill("b", "2026-01-02", "1500", "25", true, false)
	a.Fuel, a.Unit, b.Fuel, b.Unit = "cng", "kg", "cng", "kg"
	out, state, err := fuelExplainedConsumption([]FuelOverviewFill{a, b}, s)
	if err != nil || state != "available" || out.Unit != "kg/100 km" || out.Per100 != "5.00" {
		t.Fatal(out, state, err)
	}
	b.Fuel, b.Unit = "diesel", "l"
	out, state, err = fuelExplainedConsumption([]FuelOverviewFill{a, b}, s)
	if err != nil || out != nil || state != "fuel_changed" {
		t.Fatal(out, state, err)
	}
	b.Unit = "kg"
	out, state, err = fuelExplainedConsumption([]FuelOverviewFill{b}, s)
	if err != nil || out != nil || state != "unsupported_unit" {
		t.Fatal(out, state, err)
	}
	for _, alter := range []func(*FuelOverviewFill){func(r *FuelOverviewFill) { r.Quantity = "-1" }, func(r *FuelOverviewFill) { r.SeriesID = value.UUID() }, func(r *FuelOverviewFill) { r.Odometer = "999" }, func(r *FuelOverviewFill) { r.Date = "2025-12-31" }} {
		bad := fuelOverviewTestFill("b", "2026-01-02", "1500", "25", true, false)
		alter(&bad)
		if _, _, err := fuelExplainedConsumption([]FuelOverviewFill{fuelOverviewTestFill("a", "2026-01-01", "1000", "30", true, false), bad}, s); err == nil {
			t.Fatal("invalid history accepted", bad)
		}
	}
	if out, state, err = fuelExplainedConsumption(make([]FuelOverviewFill, 10001), s); err != nil || out != nil || state != "history_limit" {
		t.Fatal(out, state, err)
	}
}

func TestFuelOverviewCurrentSeriesNeverUsesVehicleMaximum(t *testing.T) {
	old := fuelOverviewTestSeries()
	ended := "2026-06-30"
	old.Ended = &ended
	replacement := old
	replacement.ID = value.UUID()
	replacement.Started = "2026-07-01"
	replacement.Ended = nil
	replacement.Initial = "12.000000"
	got, state, err := fuelCurrentSeries([]FuelOverviewSeries{old, replacement}, "2026-10-28")
	if err != nil || state != "current" || got.ID != replacement.ID || got.Initial != "12.000000" {
		t.Fatal(got, state, err)
	}
	overlap := replacement
	overlap.ID = value.UUID()
	if got, state, err = fuelCurrentSeries([]FuelOverviewSeries{replacement, overlap}, "2026-10-28"); err != nil || got != nil || state != "overlapping_series" {
		t.Fatal(got, state, err)
	}
	replacement.Started = "2027-01-01"
	if got, state, err = fuelCurrentSeries([]FuelOverviewSeries{old, replacement}, "2026-10-28"); err != nil || got != nil || state != "no_current_series" {
		t.Fatal(got, state, err)
	}
}

func TestFuelOverviewLatestTieAndOdometerZeroAreDistinct(t *testing.T) {
	a := fuelOverviewTestFill("a", "2026-01-01", "0", "5", true, false)
	b := a
	b.ID = "b"
	got, state, day, err := fuelLatestFill([]FuelOverviewFill{a, b})
	if err != nil || got != nil || state != "ambiguous_latest" || day != "2026-01-01" {
		t.Fatal(got, state, day, err)
	}
	got, state, _, err = fuelLatestFill([]FuelOverviewFill{a})
	if err != nil || got == nil || state != "available" || got.Odometer != "0" {
		t.Fatal(got, state, err)
	}
}

func TestFuelOverviewManualCalendarDatesAcrossDSTAndUnknown(t *testing.T) {
	before, today, after := "2026-03-28", "2026-03-29", "2026-03-30"
	v := FuelOverviewVehicle{NextService: &before, NextInspection: &today, InsuranceRenewal: &after}
	if err := fuelOverviewDates(&v, today); err != nil || len(v.Dates) != 3 || v.Dates[0].Days != -1 || v.Dates[1].State != "today" || v.Dates[2].Days != 1 {
		t.Fatal(v, err)
	}
	v = FuelOverviewVehicle{}
	if err := fuelOverviewDates(&v, "2026-10-25"); err != nil || v.Dates == nil || len(v.Dates) != 0 || v.Odometer != nil || v.Consumption != nil {
		t.Fatal(v, err)
	}
	bad := "2026-02-30"
	v.NextService = &bad
	if err := fuelOverviewDates(&v, today); err == nil {
		t.Fatal("invalid date")
	}
}
