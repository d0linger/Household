package energy

import (
	"math/big"
	"testing"
	"time"
)

func energyInstant(t *testing.T, text string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestDecimalRejectsLossyOrUnboundedMeasurements(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"zero", "0", "0"}, {"fraction", "0.000001", "1/1000000"},
		{"maximum", "999999999999.999999", "999999999999999999/1000000"},
		{"negative", "-1", ""}, {"exponent", "1e3", ""}, {"comma", "1,2", ""},
		{"leading zero", "01", ""}, {"fraction overflow", "1.0000001", ""},
		{"integer overflow", "1000000000000", ""}, {"whitespace", " 1", ""}, {"nan", "NaN", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decimal(tc.text)
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid decimal accepted")
				}
				return
			}
			if err != nil || got.RatString() != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestBetweenPreservesMeasurementMeaningAndMissingEvidence(t *testing.T) {
	from := energyInstant(t, "2026-01-01T00:00:00Z")
	for _, tc := range []struct {
		name, kind, unit, before, after, device, rollover, quality, quantity string
		confirmed                                                            bool
		gap                                                                  time.Duration
	}{
		{"exact cumulative", "cumulative", "kWh", "10.1", "10.3", "a", "", "actual", "1/5", false, time.Hour},
		{"known zero", "cumulative", "m3", "10", "10", "a", "", "actual", "0", false, time.Hour},
		{"replacement gap", "cumulative", "kWh", "900", "2", "b", "", "gap", "", false, time.Hour},
		{"reset unknown", "cumulative", "kWh", "900", "2", "a", "1000", "reset_unresolved", "", false, time.Hour},
		{"confirmed rollover", "cumulative", "l", "900", "2", "a", "1000", "actual", "102", true, time.Hour},
		{"sparse count", "cumulative", "kWh", "10", "20", "a", "", "estimated", "10", false, time.Minute},
		{"power integration", "power", "W", "1000", "3000", "a", "", "estimated", "2", false, time.Hour},
		{"power gap", "power", "kW", "1", "3", "a", "", "gap", "", false, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := Point{At: from, DeviceID: "a", Value: tc.before, Kind: "actual"}
			next := Point{At: from.Add(time.Hour), DeviceID: tc.device, Value: tc.after, Kind: "actual", RolloverConfirmed: tc.confirmed}
			got, err := Between(tc.kind, tc.unit, previous, next, tc.rollover, tc.gap)
			if err != nil || got.Quality != tc.quality {
				t.Fatal(got, err)
			}
			if tc.quantity == "" {
				if got.Quantity != nil {
					t.Fatal("missing evidence became quantity", got)
				}
				return
			}
			if got.Quantity == nil || got.Quantity.RatString() != tc.quantity {
				t.Fatal(got)
			}
			if tc.kind == "power" && got.Unit != "kWh" {
				t.Fatal("power was not integrated to energy")
			}
		})
	}
	start := from.Add(15 * time.Minute)
	got, err := Between("interval", "kWh", Point{}, Point{At: from.Add(time.Hour), StartedAt: &start, Value: "5", Kind: "actual"}, "", time.Hour)
	if err != nil || got.Quantity.RatString() != "5" || !got.From.Equal(start) {
		t.Fatal("interval quantity was differenced", got, err)
	}
	if _, err = Between("power", "kWh", Point{}, Point{}, "", time.Hour); err == nil {
		t.Fatal("kWh accepted as instantaneous power")
	}
}

func TestBucketsUseRealCivilHoursAndNeverFillGapsWithZero(t *testing.T) {
	for _, tc := range []struct {
		name, from, through string
		hours               int
	}{
		{"spring", "2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z", 23},
		{"autumn", "2026-10-24T22:00:00Z", "2026-10-25T23:00:00Z", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, through := energyInstant(t, tc.from), energyInstant(t, tc.through)
			interval := Interval{From: from, Through: through, Quantity: big.NewRat(int64(tc.hours), 1), Quality: "actual"}
			buckets, err := Buckets([]Interval{interval}, "Europe/Vienna", "hour", from, through)
			if err != nil || len(buckets) != tc.hours {
				t.Fatal(len(buckets), err)
			}
			total := new(big.Rat)
			for _, bucket := range buckets {
				total.Add(total, bucket.Quantity)
				if bucket.CoveredSeconds != 3600 || bucket.EstimatedSeconds != 3600 {
					t.Fatal(bucket)
				}
			}
			if total.Cmp(interval.Quantity) != 0 {
				t.Fatal("split changed exact quantity")
			}
			days, err := Buckets(nil, "Europe/Vienna", "day", from, through)
			if err != nil || len(days) != 1 || days[0].Quantity != nil || days[0].CoveredSeconds != 0 {
				t.Fatal("gap reported as zero", days, err)
			}
		})
	}
	for _, text := range []string{"2026-04-04T14:45:00Z", "2026-04-04T15:15:00Z", "2026-10-03T15:45:00Z"} {
		at := energyInstant(t, text)
		start, end, err := BucketSpan(at, "Australia/Lord_Howe", "hour")
		if err != nil || at.Before(start) || !at.Before(end) || end.Sub(start) > time.Hour {
			t.Fatal("half-hour transition", start, end, err)
		}
	}
}

func FuzzDecimalRemainsExact(f *testing.F) {
	for _, seed := range []string{"0", "0.000001", "999999999999.999999", "1e5", "-1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		number, err := Decimal(raw)
		if err == nil && (number.Sign() < 0 || number.FloatString(6) == "") {
			t.Fatal("invalid accepted measurement")
		}
	})
}
