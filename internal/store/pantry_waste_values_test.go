package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPantryWasteCivilFilters(t *testing.T) {
	for _, tc := range []struct {
		from, to, today string
		valid           bool
	}{
		{"2024-01-01", "2024-12-31", "2026-10-28", true},
		{"2024-01-01", "2025-01-01", "2026-10-28", false},
		{"2026-03-28", "2026-03-30", "2026-10-28", true},
		{"2026-10-25", "2026-10-27", "2026-10-28", true},
		{"2026-02-30", "2026-03-01", "2026-10-28", false},
		{"2026-10-29", "2026-10-29", "2026-10-28", false},
		{"2026-10-28", "2026-10-27", "2026-10-28", false},
	} {
		t.Run(tc.from+"/"+tc.to, func(t *testing.T) {
			_, err := NormalizePantryWasteFilter(PantryWasteFilter{From: tc.from, To: tc.to}, tc.today)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	got, err := NormalizePantryWasteFilter(PantryWasteFilter{}, "1900-01-02")
	if err != nil || got.From != "1900-01-01" || got.To != "1900-01-02" {
		t.Fatal(got, err)
	}
	for _, filter := range []PantryWasteFilter{{ProductID: "' OR true"}, {LocationID: "invalid"}, {Reason: "fabricated"}} {
		if _, err = NormalizePantryWasteFilter(filter, "2026-10-28"); err == nil {
			t.Fatal("invalid filter", filter)
		}
	}
}

func wasteRow(n int, unit, kind, amount, reason string) PantryWasteRow {
	return PantryWasteRow{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), CreatedAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Microsecond), Day: "2026-10-28", Unit: unit, Kind: kind, Quantity: amount, Reason: reason}
}

func TestPantryWasteExactAggregatesUnitsUnknownAndNegativePeriod(t *testing.T) {
	rows := []PantryWasteRow{wasteRow(1, "kg", "waste", "999999999999.999999", "unknown"), wasteRow(2, "kg", "waste", "999999999999.999999", "unknown"), wasteRow(3, "g", "reversal", "5.000001", "spoiled")}
	out, err := summarizePantryWaste(rows, PantryWastePoint{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Totals) != 2 || out.Movements != 3 || out.UnknownReasons != 2 || out.EstimatedCostMinor != nil || out.CostStatus != "no_valuation_source" {
		t.Fatal(out)
	}
	for _, v := range out.Totals {
		if v.EstimatedCostMinor != nil {
			t.Fatal("invented cost")
		}
		switch v.Unit {
		case "kg":
			if v.Net != "1999999999999.999998" || v.Waste != v.Net || v.Reversals != "0.000000" {
				t.Fatal(v)
			}
		case "g":
			if v.Net != "-5.000001" || v.Waste != "0.000000" || v.Reversals != "5.000001" {
				t.Fatal(v)
			}
		}
	}
	if rows[0].ID != wasteRow(1, "", "", "", "").ID {
		t.Fatal("mutated caller slice")
	}
}

func TestPantryWasteCompleteTotalsStablePagesAndChangedScope(t *testing.T) {
	rows := make([]PantryWasteRow, 101)
	for i := range rows {
		rows[i] = wasteRow(i+1, "piece", "waste", "1.000000", "other")
	}
	first, err := summarizePantryWaste(rows, PantryWastePoint{}, "")
	if err != nil || len(first.Records) != 50 || first.Next == nil || first.Totals[0].Waste != "101.000000" {
		t.Fatal(first, err)
	}
	second, err := summarizePantryWaste(rows, *first.Next, "")
	if err != nil || len(second.Records) != 50 || second.Next == nil || second.Records[0].ID == first.Records[0].ID || second.Totals[0].Waste != "101.000000" {
		t.Fatal(second, err)
	}
	last, err := summarizePantryWaste(rows, *second.Next, "")
	if err != nil || len(last.Records) != 1 || last.Next != nil {
		t.Fatal(last, err)
	}
	rows[100].ProductVersion++
	if _, err = summarizePantryWaste(rows, *first.Next, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("changed complete scope accepted", err)
	}
	if _, err = summarizePantryWaste(nil, PantryWastePoint{Fingerprint: "wrong"}, "newhash"); !errors.Is(err, ErrConflict) {
		t.Fatal("filtered-out scope drift accepted", err)
	}
}

func TestPantryWasteLimitsInvalidRowsAndEmptyZero(t *testing.T) {
	if _, err := summarizePantryWaste(make([]PantryWasteRow, 10001), PantryWastePoint{}, ""); err == nil {
		t.Fatal("row cap")
	}
	for _, r := range []PantryWasteRow{wasteRow(1, "kg", "waste", "0", "other"), wasteRow(1, "EUR", "waste", "1", "other"), wasteRow(1, "kg", "adjust", "1", "other"), wasteRow(1, "kg", "waste", "1", "fiction"), wasteRow(1, "kg", "waste", "1.0000001", "other")} {
		if _, err := summarizePantryWaste([]PantryWasteRow{r}, PantryWastePoint{}, ""); err == nil {
			t.Fatal("invalid row", r)
		}
	}
	out, err := summarizePantryWaste(nil, PantryWastePoint{}, "")
	if err != nil || out.Movements != 0 || out.Totals == nil || out.EstimatedCostMinor != nil {
		t.Fatal(out, err)
	}
}

func TestPantryWasteInputCanonicalAndIntentSeparated(t *testing.T) {
	in := PantryWasteInput{Generation: 1, Action: "waste", LotVersion: 1, ProductVersion: 1, LocationVersion: 1, MovementCount: 2, Balance: "3", Quantity: "1.25", Reason: "spoiled"}
	got, err := normalizePantryWasteInput(in)
	if err != nil || got.Quantity != "1.250000" || got.Balance != "3.000000" {
		t.Fatal(got, err)
	}
	for _, alter := range []func(*PantryWasteInput){func(i *PantryWasteInput) { i.Quantity = "0" }, func(i *PantryWasteInput) { i.Reason = "" }, func(i *PantryWasteInput) { i.MovementCount = 1<<63 - 1 }, func(i *PantryWasteInput) { i.Action = "undo" }, func(i *PantryWasteInput) { i.MovementID = "00000000-0000-4000-8000-000000000001" }} {
		bad := in
		alter(&bad)
		if _, err = normalizePantryWasteInput(bad); err == nil {
			t.Fatal("ambiguous input", bad)
		}
	}
	unknown := in
	unknown.Reason = "unknown"
	if _, err = normalizePantryWasteInput(unknown); err == nil {
		t.Fatal("new loss without explicit known reason")
	}
}

func TestPantryWasteProjectionIsNarrowAndPreservesOtherSQL(t *testing.T) {
	want := "to_jsonb(t)-ARRAY['waste_operation_key','waste_generation','waste_request_hash','waste_result_hash']"
	if got := pantryMovementRecordJSON("pantry", "stock_movements"); got != want {
		t.Fatal(got)
	}
	for _, pair := range [][2]string{{"pantry", "lots"}, {"expenses", "entries"}, {"fuel", "fillups"}, {"shopping", "items"}, {"core", "users"}} {
		if got := pantryMovementRecordJSON(pair[0], pair[1]); got != "to_jsonb(t)" {
			t.Fatal(pair, got)
		}
	}
}
