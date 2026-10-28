package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fuelReportTestSegment(from, to, quantity, distance string) fuelComputedSegment {
	rate := "untrusted display value"
	return fuelComputedSegment{
		VehicleID: fuelSegmentTestVehicle, SeriesID: fuelSegmentTestSeries,
		FromID: "40000000-0000-4000-8000-000000000001", ToID: "40000000-0000-4000-8000-000000000002",
		From: "2026-01-01", To: "2026-01-02", FromReading: from, ToReading: to,
		Fuel: "diesel", Unit: "l", State: "available", Quantity: &quantity, Distance: &distance,
		Per100: &rate, Costs: map[string]string{"EUR": "1000"},
	}
}

func fuelReportTestRows(t *testing.T, totals Record, key string) []Record {
	t.Helper()
	rows, ok := totals[key].([]Record)
	if !ok {
		t.Fatalf("%s is not a bounded record collection: %#v", key, totals[key])
	}
	return rows
}

func fuelReportTestWarning(warnings []FuelModuleWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestFuelModuleReportPlausibilityUsesExactRatioAndMatchingDimension(t *testing.T) {
	t.Parallel()
	segment := fuelReportTestSegment("0", "100", "1.234567", "100")
	for _, tc := range []struct {
		name     string
		settings Record
		alter    func(*fuelComputedSegment)
		want     string
	}{
		{"minimum before rounding", Record{"plausibility_unit": "l", "minimum_consumption": "1.234568"}, nil, "consumption_low"},
		{"maximum before rounding", Record{"plausibility_unit": "l", "maximum_consumption": "1.234566"}, nil, "consumption_high"},
		{"exact bounds inclusive", Record{"plausibility_unit": "l", "minimum_consumption": "1.234567", "maximum_consumption": "1.234567"}, nil, ""},
		{"different physical dimension", Record{"plausibility_unit": "kg", "maximum_consumption": "0.1"}, nil, ""},
		{"missing dimension", Record{"maximum_consumption": "0.1"}, nil, ""},
		{"disabled maximum", Record{"plausibility_unit": "l", "maximum_consumption": "0"}, nil, ""},
		{"negative threshold", Record{"plausibility_unit": "l", "maximum_consumption": "-1"}, nil, ""},
		{"malformed threshold", Record{"plausibility_unit": "l", "maximum_consumption": "NaN", "minimum_consumption": "1e2"}, nil, ""},
		{"unknown physical interval", Record{"plausibility_unit": "l", "maximum_consumption": "0.1"}, func(s *fuelComputedSegment) { s.State = "missing_fill" }, ""},
		{"zero denominator", Record{"plausibility_unit": "l", "maximum_consumption": "0.1"}, func(s *fuelComputedSegment) { zero := "0"; s.Distance = &zero }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := segment
			if tc.alter != nil {
				tc.alter(&current)
			}
			warnings := fuelSegmentPlausibility(current, tc.settings)
			if tc.want == "" {
				if len(warnings) != 0 {
					t.Fatalf("inapplicable setting changed physical interpretation: %+v", warnings)
				}
				return
			}
			if len(warnings) != 1 || warnings[0].Code != tc.want || warnings[0].RecordID != segment.ToID || warnings[0].NextAction == "" {
				t.Fatalf("want %s tied to actual closing receipt, got %+v", tc.want, warnings)
			}
		})
	}
}

func TestFuelModuleReportWeightedConsumptionExactCurrenciesAndDenominator(t *testing.T) {
	t.Parallel()
	first := fuelReportTestSegment("0", "100", "5", "100")
	first.Costs = map[string]string{"EUR": "9007199254740993", "USD": "2"}
	second := fuelReportTestSegment("100", "300", "20", "200")
	second.From, second.To = "2026-01-02", "2026-01-03"
	second.FromID, second.ToID = first.ToID, "40000000-0000-4000-8000-000000000003"
	second.Costs = map[string]string{"EUR": "7", "USD": "3"}
	input := []fuelComputedSegment{second, first}
	original := append([]fuelComputedSegment(nil), input...)
	totals, warnings, err := fuelReportSummary(context.Background(), input, FuelModuleQuery{From: "2026-01-02", To: "2026-01-02"})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("valid report rejected: %v %+v", err, warnings)
	}
	groups := fuelReportTestRows(t, totals, "groups")
	if len(groups) != 1 || groups[0]["quantity"] != "25.000000" || groups[0]["distance"] != "300.000000" ||
		groups[0]["per_100"] != "8.33" || groups[0]["coverage"] != "completed_full_tank_intervals" || groups[0]["prorated"] != false ||
		groups[0]["from_on"] != "2026-01-01" || groups[0]["to_on"] != "2026-01-03" {
		t.Fatalf("rate averaged or boundary interval prorated: %+v", groups)
	}
	costs := fuelReportTestRows(t, totals, "costs_per_km")
	if len(costs) != 2 || costs[0]["currency"] != "EUR" || costs[0]["amount_minor"] != "9007199254741000" ||
		costs[1]["currency"] != "USD" || costs[1]["amount_minor"] != "5" || costs[1]["cost_per_km"] != "0.0002" {
		t.Fatalf("exact currencies were merged or rounded before aggregation: %+v", costs)
	}
	for _, cost := range costs {
		if cost["distance"] != "300.000000" || cost["partial_currency"] != true || cost["currency_conversion"] != false ||
			cost["other_vehicle_costs_included"] != false || cost["cost_scope"] != "refuels_after_anchor_through_closing_full" {
			t.Fatalf("cost denominator or scope is misleading: %+v", cost)
		}
	}
	if totals["all_vehicle_cost_per_km"] != nil || totals["all_vehicle_cost_reason"] != "no_shared_unambiguous_distance_allocation" ||
		totals["window_prorated"] != false || !reflect.DeepEqual(input, original) {
		t.Fatalf("unrecorded driving invented or caller data reordered: %+v", totals)
	}
}

func TestFuelModuleReportSeparatesCounterFuelAndQuantityDimensions(t *testing.T) {
	t.Parallel()
	liquid := fuelReportTestSegment("0", "100", "5", "100")
	gas := fuelReportTestSegment("0", "50", "2", "50")
	gas.SeriesID, gas.Fuel, gas.Unit = "10000000-0000-4000-8000-000000000003", "hydrogen", "kg"
	otherCounter := fuelReportTestSegment("0", "200", "16", "200")
	otherCounter.SeriesID = "10000000-0000-4000-8000-000000000004"
	totals, _, err := fuelReportSummary(context.Background(), []fuelComputedSegment{otherCounter, gas, liquid}, FuelModuleQuery{})
	if err != nil {
		t.Fatal(err)
	}
	groups := fuelReportTestRows(t, totals, "groups")
	if len(groups) != 3 {
		t.Fatalf("incomparable readings collapsed into %d groups: %+v", len(groups), groups)
	}
	want := map[string]string{fuelSegmentTestSeries + "/l": "5.00", gas.SeriesID + "/kg": "4.00", otherCounter.SeriesID + "/l": "8.00"}
	for _, group := range groups {
		key := String(group["series_id"]) + "/" + String(group["unit"])
		if want[key] != group["per_100"] {
			t.Fatalf("wrong dimension or rate: %+v", group)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing dimensions: %+v", want)
	}
}

func TestFuelModuleReportUnknownCostsAndStatesNeverBecomeZero(t *testing.T) {
	t.Parallel()
	first := fuelReportTestSegment("0", "100", "5", "100")
	second := fuelReportTestSegment("100", "200", "5", "100")
	second.From, second.To, second.Costs = "2026-01-02", "2026-01-03", map[string]string{}
	gap := fuelSegmentGap(fuelSegmentTestPoint(10, "2026-01-04", "250", "1", false), "missing_fill")
	totals, warnings, err := fuelReportSummary(context.Background(), []fuelComputedSegment{first, second, gap, gap}, FuelModuleQuery{})
	if err != nil {
		t.Fatal(err)
	}
	groups := fuelReportTestRows(t, totals, "groups")
	costs := fuelReportTestRows(t, totals, "costs_per_km")
	if len(groups) != 1 || groups[0]["cost_unknown"] != true || groups[0]["quantity"] != "10.000000" ||
		len(costs) != 1 || costs[0]["amount_minor"] != nil || costs[0]["cost_per_km"] != nil {
		t.Fatalf("unknown cost turned into zero or discarded known consumption: %+v", totals)
	}
	if len(warnings) != 2 || !fuelReportTestWarning(warnings, "missing_fill") || !fuelReportTestWarning(warnings, "unknown_cost_currency") {
		t.Fatalf("missing or repeated explanation: %+v", warnings)
	}
	unknown, unknownWarnings, err := fuelReportSummary(context.Background(), []fuelComputedSegment{gap}, FuelModuleQuery{})
	if err != nil || len(fuelReportTestRows(t, unknown, "groups")) != 0 || !fuelReportTestWarning(unknownWarnings, "no_documented_distance") {
		t.Fatalf("gap-only period acquired a denominator: %+v %+v %v", unknown, unknownWarnings, err)
	}
	tiny := fuelReportTestSegment("0", "999999999999", "0.000001", "999999999999")
	tiny.Costs = map[string]string{"EUR": "1"}
	tinyTotals, _, err := fuelReportSummary(context.Background(), []fuelComputedSegment{tiny}, FuelModuleQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if physical, cost := fuelReportTestRows(t, tinyTotals, "groups")[0], fuelReportTestRows(t, tinyTotals, "costs_per_km")[0]; physical["below_precision"] != true || cost["below_precision"] != true || physical["quantity"] != "0.000001" || cost["amount_minor"] != "1" {
		t.Fatalf("tiny positive facts lost: %+v", tinyTotals)
	}
}

func TestFuelModuleReportRejectsConflictingDistanceAndMalformedCosts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		alter func(*fuelComputedSegment)
	}{
		{"overlapping readings", func(s *fuelComputedSegment) { s.FromReading, s.ToReading = "99", "199" }},
		{"overlapping days", func(s *fuelComputedSegment) { s.From = "2026-01-01" }},
		{"contradictory distance", func(s *fuelComputedSegment) { x := "99"; s.Distance = &x }},
		{"zero distance", func(s *fuelComputedSegment) { x := "0"; s.Distance = &x }},
		{"negative quantity", func(s *fuelComputedSegment) { x := "-1"; s.Quantity = &x }},
		{"invalid decimal", func(s *fuelComputedSegment) { x := "NaN"; s.Quantity = &x }},
		{"negative money", func(s *fuelComputedSegment) { s.Costs = map[string]string{"EUR": "-1"} }},
		{"fractional minor units", func(s *fuelComputedSegment) { s.Costs = map[string]string{"EUR": "1.5"} }},
		{"invalid currency length", func(s *fuelComputedSegment) { s.Costs = map[string]string{"EU": "1"} }},
		{"lowercase currency", func(s *fuelComputedSegment) { s.Costs = map[string]string{"eur": "1"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := fuelReportTestSegment("0", "100", "5", "100")
			second := fuelReportTestSegment("100", "200", "5", "100")
			second.From, second.To = "2026-01-02", "2026-01-03"
			tc.alter(&second)
			if totals, _, err := fuelReportSummary(context.Background(), []fuelComputedSegment{first, second}, FuelModuleQuery{}); err == nil || totals != nil {
				t.Fatalf("invalid projection yielded totals: %+v %v", totals, err)
			}
		})
	}
	if _, _, err := fuelReportSummary(context.Background(), make([]fuelComputedSegment, 10001), FuelModuleQuery{}); err == nil {
		t.Fatal("live fallback accepted unbounded materialized history")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := fuelReportSummary(ctx, []fuelComputedSegment{fuelReportTestSegment("0", "100", "5", "100")}, FuelModuleQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("report ignored cancellation: %v", err)
	}
}

func TestFuelModuleQueryBoundsAndExplicitDimensions(t *testing.T) {
	t.Parallel()
	const today = "2026-10-28"
	defaults, err := normalizeFuelModuleQuery(FuelModuleQuery{}, today)
	if err != nil || defaults.Pane != "overview" || defaults.From != "2025-10-28" || defaults.To != today || defaults.Limit != 100 {
		t.Fatalf("default scope is not a bounded civil-date window: %+v %v", defaults, err)
	}
	for _, tc := range []struct {
		name string
		q    FuelModuleQuery
	}{
		{"unknown pane", FuelModuleQuery{Pane: "admin"}},
		{"invalid vehicle", FuelModuleQuery{VehicleID: "vehicle"}},
		{"invalid job", FuelModuleQuery{Pane: "imports", JobID: "job"}},
		{"non UTF8 search", FuelModuleQuery{Search: string([]byte{0xff})}},
		{"oversized search", FuelModuleQuery{Search: strings.Repeat("x", 301)}},
		{"NUL search", FuelModuleQuery{Search: "a\x00b"}},
		{"cursor budget", FuelModuleQuery{Cursor: strings.Repeat("x", 4097)}},
		{"unknown unit", FuelModuleQuery{Unit: "gallons"}},
		{"unknown kind", FuelModuleQuery{Pane: "charging", Kind: "diesel"}},
		{"report text removes needed boundaries", FuelModuleQuery{Pane: "reports", Search: "station"}},
		{"energy versus volume", FuelModuleQuery{Pane: "reports", Kind: "energy", Unit: "l"}},
		{"liquid versus electric", FuelModuleQuery{Pane: "reports", Kind: "petrol", Unit: "kWh"}},
		{"invalid date", FuelModuleQuery{From: "2026-02-29"}},
		{"reversed range", FuelModuleQuery{From: "2026-10-28", To: "2026-10-27"}},
		{"367 civil days", FuelModuleQuery{From: "2025-10-27", To: today}},
		{"future report", FuelModuleQuery{Pane: "reports", From: today, To: "2026-10-29"}},
		{"future cost period", FuelModuleQuery{Pane: "costs", From: today, To: "2026-10-29"}},
		{"unbounded page", FuelModuleQuery{Limit: 201}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeFuelModuleQuery(tc.q, today); err == nil {
				t.Fatal("invalid query accepted")
			}
		})
	}
	for _, tc := range []struct {
		name string
		q    FuelModuleQuery
	}{
		{"366 days", FuelModuleQuery{From: "2025-10-28", To: today, Limit: 200}},
		{"leap boundary", FuelModuleQuery{From: "2024-02-29", To: "2025-02-28"}},
		{"hydrogen mass", FuelModuleQuery{Pane: "reports", Kind: "hydrogen", Unit: "kg"}},
		{"separate energy", FuelModuleQuery{Pane: "reports", Kind: "energy", Unit: "kWh"}},
		{"future original history", FuelModuleQuery{Pane: "history", From: today, To: "2026-10-29"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeFuelModuleQuery(tc.q, today); err != nil {
				t.Fatalf("valid bounded query rejected: %v", err)
			}
		})
	}
	if _, err := normalizeFuelModuleQuery(FuelModuleQuery{}, "not-a-date"); err == nil {
		t.Fatal("unknown household day accepted")
	}
}

func TestFuelModuleCursorBindsCompleteCurrentScopeAndQuery(t *testing.T) {
	t.Parallel()
	scope := FuelModuleContext{Stamp: "authorized-source-1"}
	query := FuelModuleQuery{Pane: "history", VehicleID: fuelSegmentTestVehicle, From: "2026-01-01", To: "2026-10-28", Limit: 100}
	initial, err := fuelModulePageCursor(query, scope)
	if err != nil || initial.Fingerprint == "" {
		t.Fatal(initial, err)
	}
	initial.Day, initial.ID, initial.Dataset = "2026-03-01", "40000000-0000-4000-8000-000000000001", "7/1"
	raw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	query.Cursor = string(raw)
	if got, err := fuelModulePageCursor(query, scope); err != nil || !reflect.DeepEqual(got, initial) {
		t.Fatalf("server-issued cursor did not resume: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name  string
		alter func(*FuelModuleQuery)
	}{
		{"pane", func(q *FuelModuleQuery) { q.Pane = "costs" }},
		{"vehicle", func(q *FuelModuleQuery) { q.VehicleID = "10000000-0000-4000-8000-000000000099" }},
		{"job", func(q *FuelModuleQuery) { q.JobID = "50000000-0000-4000-8000-000000000001" }},
		{"from", func(q *FuelModuleQuery) { q.From = "2026-01-02" }},
		{"to", func(q *FuelModuleQuery) { q.To = "2026-10-27" }},
		{"search", func(q *FuelModuleQuery) { q.Search = "station" }},
		{"archive", func(q *FuelModuleQuery) { q.Archived = true }},
		{"unit", func(q *FuelModuleQuery) { q.Unit = "kg" }},
		{"kind", func(q *FuelModuleQuery) { q.Kind = "diesel" }},
		{"limit", func(q *FuelModuleQuery) { q.Limit = 50 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := query
			tc.alter(&current)
			if _, err := fuelModulePageCursor(current, scope); !errors.Is(err, ErrConflict) {
				t.Fatalf("cursor crossed changed %s: %v", tc.name, err)
			}
		})
	}
	if _, err := fuelModulePageCursor(query, FuelModuleContext{Stamp: "authorized-source-2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cursor crossed a changed authorization/revision stamp: %v", err)
	}
	for _, tc := range []struct {
		name  string
		alter func(*fuelModuleCursor)
		tail  string
	}{
		{name: "negative offset", alter: func(c *fuelModuleCursor) { c.Offset = -1 }},
		{name: "fallback offset limit", alter: func(c *fuelModuleCursor) { c.Offset = 10001 }},
		{name: "invalid UUID", alter: func(c *fuelModuleCursor) { c.ID = "row" }},
		{name: "half key", alter: func(c *fuelModuleCursor) { c.Day = "" }},
		{name: "oversized date key", alter: func(c *fuelModuleCursor) { c.Day = strings.Repeat("x", 41) }},
		{name: "oversized dataset", alter: func(c *fuelModuleCursor) { c.Dataset = strings.Repeat("x", 81) }},
		{name: "oversized source", alter: func(c *fuelModuleCursor) { c.Source = strings.Repeat("x", 33) }},
		{name: "trailing JSON", tail: "{}"},
		{name: "unknown field", tail: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := initial
			if tc.alter != nil {
				tc.alter(&cursor)
			}
			encoded, err := json.Marshal(cursor)
			if err != nil {
				t.Fatal(err)
			}
			current := query
			if tc.tail == "unknown" {
				current.Cursor = strings.TrimSuffix(string(encoded), "}") + `,"extra":true}`
			} else {
				current.Cursor = string(encoded) + tc.tail
			}
			if _, err := fuelModulePageCursor(current, scope); !errors.Is(err, ErrConflict) {
				t.Fatalf("malformed internal cursor accepted: %v", err)
			}
		})
	}
	keyNext, err := fuelModuleKeyNext(fuelModuleCursor{Fingerprint: initial.Fingerprint, Offset: 10000},
		[]Record{{"id": initial.ID, "_cursor_day": initial.Day}}, true)
	if err != nil {
		t.Fatal("large indexed histories must not inherit fallback offset cap:", err)
	}
	keyQuery := query
	keyQuery.Cursor = keyNext
	if resumed, err := fuelModulePageCursor(keyQuery, scope); err != nil || resumed.Offset != 0 || resumed.ID != initial.ID {
		t.Fatalf("keyset continuation lost: %+v %v", resumed, err)
	}
	if _, err := fuelModuleNext(fuelModuleCursor{Offset: 10000}, 1, true); err == nil {
		t.Fatal("bounded live-fallback continuation exceeded its limit")
	}
}

func TestFuelModuleUnknownAndPendingStatusesHaveActionableExplanations(t *testing.T) {
	t.Parallel()
	for _, code := range []string{
		"no_fillups", "no_full_anchor", "needs_next_full", "missing_fill", "fuel_changed", "zero_distance",
		"ambiguous_order", "unsupported_unit", "odometer_backwards", "segments_pending", "segments_stale", "segments_failed",
		"history_window_limit", "no_documented_distance", "overlapping_distance", "unknown_cost_currency", "series_limit",
		"no_current_series", "overlapping_series", "selected_series_unavailable", "reading_outside_series", "invalid_history", "no_completed_interval",
	} {
		t.Run(code, func(t *testing.T) {
			warning := fuelReason(code)
			if warning.Code != code || warning.Message == "" || warning.NextAction == "" || warning.RecordID != "" {
				t.Fatalf("status lacks safe, actionable explanation: %+v", warning)
			}
		})
	}
	unknown := fuelReason("unrecognized-restored-state")
	if unknown.Code != "unrecognized-restored-state" || unknown.Message == "" || unknown.NextAction == "" {
		t.Fatal("unknown restored state lost its neutral explanation")
	}
}
