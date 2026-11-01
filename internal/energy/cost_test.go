package energy

import (
	"math/big"
	"testing"
	"time"
)

func TestCostsSplitTariffsExactlyAndExposeIncompleteEvidence(t *testing.T) {
	from := energyInstant(t, "2026-01-01T00:00:00Z")
	middle, through := from.Add(12*time.Hour), from.Add(24*time.Hour)
	intervals := []Interval{{From: from, Through: through, Quantity: big.NewRat(3, 1), Quality: "actual"}}
	tariffs := []Tariff{{From: from, Through: middle, PriceMinor: "10.1", DailyMinor: "1", CurrencyCode: "EUR"}, {From: middle, Through: through, PriceMinor: "20.3", DailyMinor: "1", CurrencyCode: "EUR"}}
	got, err := Costs(intervals, tariffs, "UTC", from, through)
	if err != nil || !got.Complete || !got.Estimated || got.Minor.Cmp(big.NewRat(233, 5)) != 0 {
		t.Fatal(got, err)
	}
	missing, err := Costs(intervals, nil, "UTC", from, through)
	if err != nil || missing.Complete || missing.MissingTariffSeconds != 86400 {
		t.Fatal("missing tariff became free", missing, err)
	}
	missing, err = Costs(nil, tariffs, "UTC", from, through)
	if err != nil || missing.Complete || missing.MissingQuantitySeconds != 86400 || missing.Minor.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatal("missing quantity became known consumption", missing, err)
	}
	tariffs[1].From = from
	if _, err = Costs(intervals, tariffs, "UTC", from, through); err == nil {
		t.Fatal("overlapping prices accepted")
	}
}

func TestCostsDailyBaseUsesCivilDayAcrossDST(t *testing.T) {
	from, through := energyInstant(t, "2026-03-28T23:00:00Z"), energyInstant(t, "2026-03-29T22:00:00Z")
	got, err := Costs([]Interval{{From: from, Through: through, Quantity: big.NewRat(0, 1), Quality: "actual"}}, []Tariff{{From: from, Through: through, PriceMinor: "0", DailyMinor: "123", CurrencyCode: "EUR"}}, "Europe/Vienna", from, through)
	if err != nil || !got.Complete || got.Minor.Cmp(big.NewRat(123, 1)) != 0 {
		t.Fatal("23-hour day was charged as part of a civil day", got, err)
	}
}

func TestAnomaliesRequireMeasuredHistoryAndCoveredNights(t *testing.T) {
	from := energyInstant(t, "2026-01-01T00:00:00Z")
	intervals := []Interval{}
	for n := range 7 {
		intervals = append(intervals, Interval{From: from.AddDate(0, 0, n), Through: from.AddDate(0, 0, n+1), Quantity: big.NewRat(20, 1), Quality: "actual"})
	}
	short, err := Anomalies(intervals[:6], "10", 7)
	if err != nil || len(short) != 1 || short[0].Code != "history_insufficient" {
		t.Fatal(short, err)
	}
	got, err := Anomalies(intervals, "10", 7)
	if err != nil || len(got) != 1 || got[0].Code != "daily_limit" {
		t.Fatal(got, err)
	}
	nights := []Night{}
	for n := range 7 {
		nights = append(nights, Night{Day: from.AddDate(0, 0, n).Format(time.DateOnly), QuantityKWh: "6", CoveredSeconds: 21600, ExpectedSeconds: 21600})
	}
	got, err = BaseLoad(nights, "W", "500", 7)
	if err != nil || len(got) != 1 || got[0].Code != "base_load" {
		t.Fatal(got, err)
	}
	nights[0].CoveredSeconds = 1
	got, err = BaseLoad(nights, "W", "500", 7)
	if err != nil || len(got) != 1 || got[0].Code != "base_history_insufficient" {
		t.Fatal("insufficient night treated as full evidence", got, err)
	}
}
