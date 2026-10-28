package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRepurchaseCivilDatesMedianAndBoundedEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		dates    []string
		today    string
		interval int
		next     string
		eligible bool
	}{
		{"Vienna spring DST", []string{"2026-03-22", "2026-03-29", "2026-04-05"}, "2026-04-12", 7, "2026-04-12", true},
		{"Vienna autumn DST", []string{"2026-10-18", "2026-10-25", "2026-11-01"}, "2026-11-07", 7, "2026-11-08", true},
		{"median rejects one outlier", []string{"2026-01-01", "2026-01-08", "2026-01-15", "2026-03-01"}, "2026-03-02", 7, "2026-03-08", true},
		{"two intervals round up", []string{"2026-01-01", "2026-01-08", "2026-01-16"}, "2026-01-23", 8, "2026-01-24", true},
		{"same-day repeats insufficient", []string{"2026-01-01", "2026-01-01", "2026-01-08"}, "2026-02-01", 0, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dates := map[string]bool{}
			for _, date := range test.dates {
				dates[date] = true
			}
			candidate := RepurchaseSuggestion{ProductID: "00000000-0000-4000-8000-000000000001", ProductVersion: 1}
			if got := calculateRepurchase(&candidate, dates, test.today); got != test.eligible || candidate.IntervalDays != test.interval || candidate.NextOn != test.next {
				t.Fatalf("wrong calendar interval: %+v", candidate)
			}
			if test.eligible && (candidate.Due != (test.next <= test.today) || len(candidate.Fingerprint) != 64) {
				t.Fatal("wrong due/fingerprint")
			}
		})
	}
	var candidate RepurchaseSuggestion
	dates := map[string]bool{}
	for _, day := range []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10"} {
		dates["2026-01-"+day] = true
	}
	if !calculateRepurchase(&candidate, dates, "2026-02-01") || len(candidate.Dates) != 8 || candidate.Dates[7] != "2026-01-03" {
		t.Fatal("did not bound newest distinct dates", candidate.Dates)
	}
}

func TestRepurchaseEvidenceUsesTypedEffectivePackAndCompletePairing(t *testing.T) {
	const item = "00000000-0000-4000-8000-000000000001"
	const product = "00000000-0000-4000-8000-000000000002"
	fixture := `{"day":"2026-03-29","items":[{"id":"` + item + `","product":"` + product + `","frozen_product":"` + product + `","pack":"500.000000","unit":"g"}],"lines":[{"id":"` + item + `","decision":"no_stock"}]}`
	var history repurchaseHistory
	if err := json.Unmarshal([]byte(fixture), &history); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]map[string]bool{}
	if err := addRepurchaseEvidence(evidence, history, "2026-04-01"); err != nil {
		t.Fatal(err)
	}
	key, _ := repurchasePack(product, "500", "g")
	if !evidence[key]["2026-03-29"] {
		t.Fatal("exact pack purchase missing")
	}
	before := map[string]map[string]bool{key: {"2026-03-29": true}}
	if err := addRepurchaseEvidence(evidence, history, "2026-04-01"); err != nil || !reflect.DeepEqual(evidence, before) {
		t.Fatal("same-day evidence counted twice")
	}
	for _, damage := range []string{"duplicate item", "duplicate purchase", "wrong pairing", "wrong effective product", "future", "outside supported calendar"} {
		t.Run(damage, func(t *testing.T) {
			var h repurchaseHistory
			if err := json.Unmarshal([]byte(fixture), &h); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "duplicate item":
				h.Items = append(h.Items, h.Items[0])
				h.Lines = append(h.Lines, h.Lines[0])
			case "duplicate purchase":
				h.Lines = append(h.Lines, h.Lines[0])
			case "wrong pairing":
				h.Lines[0].ID = product
			case "wrong effective product":
				h.Items[0].FrozenProduct = item
			case "future":
				h.Day = "2026-04-02"
			case "outside supported calendar":
				h.Day = "0001-01-01"
			}
			if err := addRepurchaseEvidence(map[string]map[string]bool{}, h, "2026-04-01"); err == nil {
				t.Fatal("ambiguous history accepted")
			}
		})
	}
	other, _ := repurchasePack(product, "750", "g")
	if other == key {
		t.Fatal("changed pack was merged")
	}
	history.Lines[0].Decision = "skip"
	empty := map[string]map[string]bool{}
	if err := addRepurchaseEvidence(empty, history, "2026-04-01"); err != nil || len(empty) != 0 {
		t.Fatal("skipped item manufactured a purchase")
	}
}

func TestRepurchaseQuantityNormalizationBoundaries(t *testing.T) {
	for _, test := range []struct{ input, want string }{{"1", "1.000000"}, {" 2.25 ", "2.250000"}, {"999999999999.999999", "999999999999.999999"}, {"1000000000000", ""}, {"0", ""}, {"-1", ""}, {"1.0000001", ""}} {
		t.Run(test.input, func(t *testing.T) {
			got, err := normalizeRepurchaseQuantity(test.input)
			if test.want == "" {
				if err == nil {
					t.Fatal("out-of-range quantity accepted", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatal("valid reviewed quantity failed", got, err)
			}
		})
	}
}
