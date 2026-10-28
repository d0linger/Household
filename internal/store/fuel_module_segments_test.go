package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fuelSegmentTestVehicle = "10000000-0000-4000-8000-000000000001"
const fuelSegmentTestSeries = "10000000-0000-4000-8000-000000000002"

func fuelSegmentTestPoint(index int, day, reading, quantity string, full bool) fuelSegmentPoint {
	return fuelSegmentPoint{
		ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", index), VehicleID: fuelSegmentTestVehicle,
		SeriesID: fuelSegmentTestSeries, Date: day, Odometer: reading, Quantity: quantity,
		Fuel: "diesel", Unit: "l", Currency: "EUR", CostMinor: "100", Initial: "0",
		Started: "2026-01-01", Full: full, Version: 1,
	}
}

// Each run consumes the same ordered stream exactly once. Serialization between
// batches models a worker giving up its transaction and resuming in another one.
func fuelSegmentTestStream(t *testing.T, points []fuelSegmentPoint, batch int, resume bool) ([]fuelComputedSegment, fuelSegmentCheckpoint) {
	t.Helper()
	checkpoint := fuelEmptyCheckpoint()
	checkpoint.AsOf = "2026-12-31"
	var result []fuelComputedSegment
	for start := 0; start < len(points); start += batch {
		end := min(start+batch, len(points))
		segments, err := fuelSegmentFeed(context.Background(), &checkpoint, points[start:end], end == len(points))
		if err != nil {
			t.Fatalf("feed [%d:%d]: %v", start, end, err)
		}
		result = append(result, segments...)
		if resume {
			raw, err := fuelSegmentJSON(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err = fuelCheckpointDecode(raw, fuelSegmentTestVehicle)
			if err != nil {
				t.Fatalf("resume after %d receipts: %v", end, err)
			}
		}
	}
	return result, checkpoint
}

func TestFuelModuleSegmentsLargeHistoryCheckpointEquivalence(t *testing.T) {
	const receiptCount = 20051
	points := make([]fuelSegmentPoint, receiptCount)
	firstDay := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range points {
		points[i] = fuelSegmentTestPoint(i+1, firstDay.AddDate(0, 0, i/100).Format(time.DateOnly), fmt.Sprint(i*10), "0.123456", true)
		points[i].CostMinor = "999999999999"
	}
	baseline, checkpoint := fuelSegmentTestStream(t, points, fuelSegmentChunk, false)
	if len(baseline) != receiptCount-1 || checkpoint.Rows != receiptCount || checkpoint.AsOf != "2026-12-31" {
		t.Fatalf("lost history: intervals=%d rows=%d as_of=%s", len(baseline), checkpoint.Rows, checkpoint.AsOf)
	}
	quantity, distance, money := new(big.Rat), new(big.Rat), new(big.Int)
	for i, segment := range baseline {
		if segment.State != "available" || segment.FromID != points[i].ID || segment.ToID != points[i+1].ID ||
			segment.Quantity == nil || segment.Distance == nil || segment.Per100 == nil || *segment.Per100 != "1.23" {
			t.Fatalf("interval %d has wrong boundaries or rate: %+v", i, segment)
		}
		q, qOK := new(big.Rat).SetString(*segment.Quantity)
		d, dOK := new(big.Rat).SetString(*segment.Distance)
		m, mOK := new(big.Int).SetString(segment.Costs["EUR"], 10)
		if !qOK || !dOK || !mOK {
			t.Fatalf("interval %d lost exact values", i)
		}
		quantity.Add(quantity, q)
		distance.Add(distance, d)
		money.Add(money, m)
	}
	intervals := int64(receiptCount - 1)
	wantQuantity := new(big.Rat).Mul(big.NewRat(123456, 1000000), big.NewRat(intervals, 1))
	wantMoney := new(big.Int).Mul(big.NewInt(999999999999), big.NewInt(intervals))
	if quantity.Cmp(wantQuantity) != 0 || distance.Cmp(big.NewRat(intervals*10, 1)) != 0 || money.Cmp(wantMoney) != 0 {
		t.Fatalf("independent totals differ: quantity=%s distance=%s money=%s", quantity.RatString(), distance.RatString(), money)
	}
	for _, batch := range []int{137, 499, 500} {
		t.Run(fmt.Sprintf("resume_every_%d", batch), func(t *testing.T) {
			got, resumed := fuelSegmentTestStream(t, points, batch, true)
			if !reflect.DeepEqual(got, baseline) || !reflect.DeepEqual(resumed, checkpoint) {
				t.Fatalf("checkpoint boundaries changed results for batch %d: intervals=%d rows=%d", batch, len(got), resumed.Rows)
			}
		})
	}
}

func TestFuelModuleSegmentsTieSurvivesCheckpointBeforePublication(t *testing.T) {
	t.Parallel()
	points := []fuelSegmentPoint{
		fuelSegmentTestPoint(1, "2026-01-01", "100", "50", true),
		fuelSegmentTestPoint(2, "2026-01-02", "200", "10", true),
		fuelSegmentTestPoint(3, "2026-01-02", "200.000000", "20", true),
		fuelSegmentTestPoint(4, "2026-01-03", "300", "30", true),
		fuelSegmentTestPoint(5, "2026-01-04", "400", "40", true),
	}
	checkpoint := fuelEmptyCheckpoint()
	checkpoint.AsOf = "2026-01-04"
	first, err := fuelSegmentFeed(context.Background(), &checkpoint, points[:2], false)
	if err != nil || len(first) != 0 || checkpoint.Pending == nil || checkpoint.Pending.ID != points[1].ID {
		t.Fatalf("unconfirmed closing receipt published too early: %+v %v", first, err)
	}
	for _, group := range [][]fuelSegmentPoint{points[2:3], points[3:]} {
		part, err := fuelSegmentFeed(context.Background(), &checkpoint, group, group[0].ID == points[3].ID)
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, part...)
		raw, err := fuelSegmentJSON(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, err = fuelCheckpointDecode(raw, fuelSegmentTestVehicle)
		if err != nil {
			t.Fatal(err)
		}
		if len(group) == 1 && !checkpoint.PendingTied {
			t.Fatal("equivalent numeric reading tie was lost across checkpoint")
		}
	}
	var available, ambiguous int
	for _, segment := range first {
		switch segment.State {
		case "available":
			available++
			if segment.FromID != points[3].ID || segment.ToID != points[4].ID {
				t.Fatalf("ambiguous receipt entered a completed interval: %+v", segment)
			}
		case "ambiguous_order":
			ambiguous++
		}
	}
	if available != 1 || ambiguous != 1 {
		t.Fatalf("known=%d ambiguous=%d, want 1 each: %+v", available, ambiguous, first)
	}
}

func TestFuelModuleSegmentsGapsCannotBridgeUnknownPhysicalHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		alter        func([]fuelSegmentPoint)
		knownFrom    int
		knownTo      int
	}{
		{name: "missing partial", reason: "missing_fill", alter: func(p []fuelSegmentPoint) { p[1].Full, p[1].Missing = false, true }, knownFrom: 2, knownTo: 3},
		{name: "fuel switch", reason: "fuel_changed", alter: func(p []fuelSegmentPoint) {
			for i := 1; i < len(p); i++ {
				p[i].Fuel = "petrol"
			}
		}, knownFrom: 1, knownTo: 2},
		{name: "unsupported unit", reason: "unsupported_unit", alter: func(p []fuelSegmentPoint) { p[1].Unit = "kg" }, knownFrom: 2, knownTo: 3},
		{name: "zero distance", reason: "zero_distance", alter: func(p []fuelSegmentPoint) { p[1].Odometer = p[0].Odometer }, knownFrom: 1, knownTo: 2},
		{name: "counter fell", reason: "odometer_backwards", alter: func(p []fuelSegmentPoint) { p[1].Odometer = "90" }, knownFrom: -1, knownTo: -1},
		{name: "new counter", reason: "needs_next_full", alter: func(p []fuelSegmentPoint) {
			p[1].Full = false
			for i := 2; i < len(p); i++ {
				p[i].SeriesID = "10000000-0000-4000-8000-000000000003"
				p[i].Odometer = fmt.Sprint((i - 2) * 100)
			}
		}, knownFrom: 2, knownTo: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			points := []fuelSegmentPoint{
				fuelSegmentTestPoint(1, "2026-01-01", "100", "10", true),
				fuelSegmentTestPoint(2, "2026-01-02", "200", "10", true),
				fuelSegmentTestPoint(3, "2026-01-03", "300", "10", true),
				fuelSegmentTestPoint(4, "2026-01-04", "400", "10", true),
			}
			tc.alter(points)
			segments, _ := fuelSegmentTestStream(t, points, 1, true)
			foundReason, foundExpected := false, tc.knownFrom < 0
			for _, segment := range segments {
				foundReason = foundReason || segment.State == tc.reason
				if segment.State != "available" {
					if segment.Quantity != nil || segment.Distance != nil || segment.Per100 != nil {
						t.Fatalf("gap masquerades as a physical total: %+v", segment)
					}
					continue
				}
				if tc.knownFrom < 0 || segment.SeriesID != points[tc.knownFrom].SeriesID || segment.FromID == points[0].ID {
					t.Fatalf("interval bridged invalid history: %+v", segment)
				}
				foundExpected = foundExpected || segment.FromID == points[tc.knownFrom].ID && segment.ToID == points[tc.knownTo].ID
			}
			if !foundReason || !foundExpected {
				t.Fatalf("reason=%v recovered interval=%v: %+v", foundReason, foundExpected, segments)
			}
		})
	}
}

func TestFuelModuleSegmentsPartialAmountsAndCurrenciesExcludeAnchor(t *testing.T) {
	t.Parallel()
	points := []fuelSegmentPoint{
		fuelSegmentTestPoint(1, "2026-01-01", "9007199254.000001", "50", true),
		fuelSegmentTestPoint(2, "2026-01-02", "9007199254.100001", "0.000001", false),
		fuelSegmentTestPoint(3, "2026-01-03", "9007199254.500001", "0.024999", true),
	}
	points[0].CostMinor = "999999999999"
	points[1].Currency, points[1].CostMinor = "USD", "999999999999"
	points[2].CostMinor = "7"
	segments, _ := fuelSegmentTestStream(t, points, 2, true)
	if len(segments) != 1 {
		t.Fatalf("want one physical interval: %+v", segments)
	}
	got := segments[0]
	if got.Quantity == nil || *got.Quantity != "0.025000" || got.Distance == nil || *got.Distance != "0.500000" ||
		got.Per100 == nil || *got.Per100 != "5.00" || got.Costs["EUR"] != "7" || got.Costs["USD"] != "999999999999" || len(got.Costs) != 2 {
		t.Fatalf("partial fill or currency lost precision / anchor included: %+v", got)
	}
	encoded, err := json.Marshal(fuelSegmentRecord(got))
	if err != nil || !strings.Contains(string(encoded), `"USD":"999999999999"`) {
		t.Fatalf("money must remain JSON integer strings: %s %v", encoded, err)
	}
}

func TestFuelModuleSegmentsPositiveBelowDisplayPrecisionAndUnknownCost(t *testing.T) {
	t.Parallel()
	points := []fuelSegmentPoint{
		fuelSegmentTestPoint(1, "2026-01-01", "0", "1", true),
		fuelSegmentTestPoint(2, "2026-01-02", "999999999999", "0.000001", true),
	}
	points[1].Currency, points[1].CostMinor = "", ""
	segments, _ := fuelSegmentTestStream(t, points, 1, true)
	if len(segments) != 1 {
		t.Fatalf("want one tiny positive interval, got %+v", segments)
	}
	got := segments[0]
	if !got.BelowPrecision || got.Quantity == nil || *got.Quantity != "0.000001" || got.Per100 == nil || *got.Per100 != "0.00" ||
		!got.CostUnknown || len(got.Costs) != 0 {
		t.Fatalf("tiny known consumption or unknown cost became an asserted zero: %+v", got)
	}
}

func TestFuelModuleSegmentsValidationAndSeriesDateBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		alter func(*fuelSegmentPoint)
	}{
		{"invalid receipt id", func(p *fuelSegmentPoint) { p.ID = "receipt" }},
		{"invalid vehicle", func(p *fuelSegmentPoint) { p.VehicleID = "vehicle" }},
		{"invalid series", func(p *fuelSegmentPoint) { p.SeriesID = "series" }},
		{"before series", func(p *fuelSegmentPoint) { p.Date = "2025-12-31" }},
		{"after series", func(p *fuelSegmentPoint) { end := "2026-01-01"; p.Ended = &end }},
		{"invalid leap date", func(p *fuelSegmentPoint) { p.Date = "2026-02-29" }},
		{"below initial reading", func(p *fuelSegmentPoint) { p.Initial = "101" }},
		{"negative reading", func(p *fuelSegmentPoint) { p.Odometer = "-1" }},
		{"negative initial", func(p *fuelSegmentPoint) { p.Initial = "-1" }},
		{"zero quantity", func(p *fuelSegmentPoint) { p.Quantity = "0" }},
		{"negative quantity", func(p *fuelSegmentPoint) { p.Quantity = "-1" }},
		{"too precise quantity", func(p *fuelSegmentPoint) { p.Quantity = "0.0000001" }},
		{"negative cost", func(p *fuelSegmentPoint) { p.CostMinor = "-1" }},
		{"fractional minor cost", func(p *fuelSegmentPoint) { p.CostMinor = "1.5" }},
		{"lowercase currency", func(p *fuelSegmentPoint) { p.Currency = "eur" }},
		{"unicode currency", func(p *fuelSegmentPoint) { p.Currency = "EÜR" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			point := fuelSegmentTestPoint(1, "2026-01-02", "100", "10", true)
			tc.alter(&point)
			checkpoint := fuelEmptyCheckpoint()
			if _, err := fuelSegmentFeed(context.Background(), &checkpoint, []fuelSegmentPoint{point}, true); err == nil {
				t.Fatal("invalid stored fact was accepted")
			}
		})
	}
	for _, tc := range []struct{ name, fuel, unit string }{
		{"liquid", "diesel", "l"}, {"gas", "cng", "kg"}, {"hydrogen", "hydrogen", "kg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			point := fuelSegmentTestPoint(1, "2026-01-02", "100", "10", true)
			point.Fuel, point.Unit = tc.fuel, tc.unit
			if !fuelSegmentSupported(point) {
				t.Fatal("supported physical dimension rejected")
			}
		})
	}
}

func TestFuelModuleSegmentsRejectInvalidCheckpointAndBoundedWork(t *testing.T) {
	t.Parallel()
	point := fuelSegmentTestPoint(1, "2026-01-02", "100", "10", true)
	base := fuelEmptyCheckpoint()
	base.AsOf, base.Pending = "2026-01-02", &point
	for _, tc := range []struct {
		name string
		raw  func() []byte
	}{
		{"unknown field", func() []byte { return []byte(`{"algorithm":1,"as_of":"2026-01-02","extra":true}`) }},
		{"trailing object", func() []byte { return []byte(`{"algorithm":1,"as_of":"2026-01-02"}{}`) }},
		{"oversized", func() []byte { return []byte(strings.Repeat(" ", (64<<10)+1)) }},
		{"wrong algorithm", func() []byte { c := base; c.Algorithm++; b, _ := json.Marshal(c); return b }},
		{"negative rows", func() []byte { c := base; c.Rows = -1; b, _ := json.Marshal(c); return b }},
		{"invalid cutoff", func() []byte { c := base; c.AsOf = "2026-02-29"; b, _ := json.Marshal(c); return b }},
		{"foreign vehicle", func() []byte {
			c, p := base, point
			p.VehicleID = "10000000-0000-4000-8000-000000000099"
			c.Pending = &p
			b, _ := json.Marshal(c)
			return b
		}},
		{"currency count", func() []byte {
			c := base
			c.Costs = map[string]string{}
			for i := 0; i <= fuelSegmentCurrencyLimit; i++ {
				c.Costs[fmt.Sprintf("%03d", i)] = "1"
			}
			b, _ := json.Marshal(c)
			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fuelCheckpointDecode(tc.raw(), fuelSegmentTestVehicle); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
	t.Run("cutoff survives a worker handoff", func(t *testing.T) {
		raw, err := fuelSegmentJSON(base)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := fuelCheckpointDecode(raw, fuelSegmentTestVehicle)
		if err != nil || decoded.AsOf != base.AsOf || decoded.Pending == nil || decoded.Pending.Date > decoded.AsOf {
			t.Fatalf("cutoff lost: %+v %v", decoded, err)
		}
		decoded.Pending.Quantity = "999"
		if base.Pending.Quantity == decoded.Pending.Quantity {
			t.Fatal("resumed checkpoint aliases previous worker memory")
		}
	})
	t.Run("work bounds", func(t *testing.T) {
		if _, err := fuelSegmentFeed(context.Background(), nil, nil, true); err == nil {
			t.Fatal("nil checkpoint accepted")
		}
		checkpoint := fuelEmptyCheckpoint()
		if _, err := fuelSegmentFeed(context.Background(), &checkpoint, make([]fuelSegmentPoint, fuelSegmentChunk+1), true); err == nil {
			t.Fatal("unbounded work accepted")
		}
		checkpoint.Rows = math.MaxInt64
		if _, err := fuelSegmentFeed(context.Background(), &checkpoint, []fuelSegmentPoint{point}, true); err == nil {
			t.Fatal("row counter overflow accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		checkpoint = fuelEmptyCheckpoint()
		if _, err := fuelSegmentFeed(ctx, &checkpoint, []fuelSegmentPoint{point}, true); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	})
}

func TestFuelModuleSegmentAggregatePrecisionAndStableIdentity(t *testing.T) {
	t.Parallel()
	valid := strings.Repeat("9", 48) + ".123456"
	if got, err := fuelAggregateDecimal(valid); err != nil || got.FloatString(6) != valid {
		t.Fatalf("aggregate inherited the single-input precision cap: %v %v", got, err)
	}
	for _, text := range []string{"", "+1", "1e3", "1,5", "1/2", ".1", "1.", "1.0000001", strings.Repeat("9", 49), "NaN"} {
		t.Run("invalid_"+text, func(t *testing.T) {
			if _, err := fuelAggregateDecimal(text); err == nil {
				t.Fatalf("invalid aggregate %q accepted", text)
			}
		})
	}
	segment := fuelSegmentGap(fuelSegmentTestPoint(1, "2026-01-01", "100", "1", true), "needs_next_full")
	household := "30000000-0000-4000-8000-000000000001"
	first := fuelSegmentID(household, 10, segment)
	if first != fuelSegmentID(household, 10, segment) || first == fuelSegmentID(household, 11, segment) ||
		first == fuelSegmentID("30000000-0000-4000-8000-000000000002", 10, segment) {
		t.Fatal("projection identity is unstable or crosses revision/household scope")
	}
	segment.State = "missing_fill"
	if first == fuelSegmentID(household, 10, segment) {
		t.Fatal("different physical gap states have the same projection identity")
	}
}
