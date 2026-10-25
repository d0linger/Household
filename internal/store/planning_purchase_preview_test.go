package store

import (
	"math/big"
	"testing"
	"time"

	"household/internal/value"
)

func TestPlanningPurchaseSelections(t *testing.T) {
	pid, mid := value.UUID(), value.UUID()
	many := make([]string, 43)
	for i := range many {
		many[i] = value.UUID()
	}
	for _, tc := range []struct {
		name     string
		selected []PlanningPurchaseSelection
		valid    bool
	}{
		{"existing active scope without additional selection", nil, true},
		{"one whole meal", []PlanningPurchaseSelection{{pid, []string{mid}}}, true},
		{"two plans", []PlanningPurchaseSelection{{pid, []string{mid}}, {value.UUID(), []string{value.UUID()}}}, true},
		{"empty source", []PlanningPurchaseSelection{{pid, nil}}, false},
		{"invalid plan", []PlanningPurchaseSelection{{"invalid", []string{mid}}}, false},
		{"duplicate across plans", []PlanningPurchaseSelection{{pid, []string{mid}}, {value.UUID(), []string{mid}}}, false},
		{"too many whole meals", []PlanningPurchaseSelection{{pid, many}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := planningPurchaseSelections(tc.selected)
			if (err == nil) != tc.valid {
				t.Fatal("selection contract", err)
			}
		})
	}
}

func TestPlanningPurchaseSignedAmount(t *testing.T) {
	for _, tc := range []struct{ amount, exact, display string }{
		{"-1/3", "-1/3", "-0.333333"},
		{"-1/3000000", "-1/3000000", "-0.000000"},
		{"0", "0", "0.000000"},
		{"1/8", "1/8", "0.125000"},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			amount, _ := new(big.Rat).SetString(tc.amount)
			out, err := planningPurchaseSignedAmount(amount)
			if err != nil || out.Rational != tc.exact || out.Value != tc.display || amount.RatString() != tc.exact {
				t.Fatal("exact signed difference or caller quantity changed", out, err)
			}
		})
	}
}

func TestPlanningPurchaseFingerprintExcludesOnlyReadClock(t *testing.T) {
	out := PlanningPurchasesPreview{ReadAt: time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC), Requirements: []PlanningPurchaseRequirement{{ID: value.UUID(), Version: 1}}}
	out.Demand.ReadAt = out.ReadAt
	if err := sealPlanningPurchasePreview(&out); err != nil {
		t.Fatal(err)
	}
	first := out.Fingerprint
	out.ReadAt = out.ReadAt.Add(time.Minute)
	out.Demand.ReadAt = out.ReadAt
	if err := sealPlanningPurchasePreview(&out); err != nil || out.Fingerprint != first {
		t.Fatal("a fresh clock alone invalidated the reviewed state", err)
	}
	out.Requirements[0].Version++
	if err := sealPlanningPurchasePreview(&out); err != nil || out.Fingerprint == first {
		t.Fatal("requirement change did not invalidate review", err)
	}
}
