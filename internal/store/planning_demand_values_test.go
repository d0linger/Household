package store

import (
	"math/big"
	"testing"
)

const planningDemandProduct = "10000000-0000-4000-8000-000000000001"

func TestGroupPlanningDemand(t *testing.T) {
	tests := []struct {
		name       string
		quantities []*big.Rat
		complete   bool
		want       string
	}{
		{"duplicate components", []*big.Rat{big.NewRat(3, 5), big.NewRat(3, 5)}, true, "6/5"},
		{"exact repeating fractions", []*big.Rat{big.NewRat(1, 3), big.NewRat(1, 3), big.NewRat(1, 3)}, true, "1"},
		{"unknown keeps known subtotal", []*big.Rat{big.NewRat(3, 5), nil}, false, "3/5"},
		{"all unknown", []*big.Rat{nil, nil}, false, "0"},
		{"explicit zero", []*big.Rat{new(big.Rat)}, true, "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := make([]planningDemandQuantity, 0, len(tc.quantities))
			original := make([]string, len(tc.quantities))
			for i, quantity := range tc.quantities {
				lines = append(lines, planningDemandQuantity{planningDemandProduct, "kg", quantity})
				if quantity != nil {
					original[i] = quantity.RatString()
				}
			}
			groups, complete, err := groupPlanningDemand(lines)
			group := groups[planningDemandProduct]
			if err != nil || group == nil || complete != tc.complete || group.Complete != tc.complete || group.Quantity.RatString() != tc.want {
				t.Fatalf("grouped demand: %+v/%t/%v", group, complete, err)
			}
			group.Quantity.SetInt64(99)
			for i, quantity := range tc.quantities {
				if quantity != nil && quantity.RatString() != original[i] {
					t.Fatal("group aggregation or output mutation changed an input quantity")
				}
			}
		})
	}
}

func TestGroupPlanningDemandBoundaries(t *testing.T) {
	valid := planningDemandQuantity{planningDemandProduct, "kg", big.NewRat(1, 1)}
	tests := []struct {
		name  string
		lines []planningDemandQuantity
	}{
		{"invalid product", []planningDemandQuantity{{"invalid", "kg", big.NewRat(1, 1)}}},
		{"unknown unit", []planningDemandQuantity{{planningDemandProduct, "cup", big.NewRat(1, 1)}}},
		{"negative demand", []planningDemandQuantity{{planningDemandProduct, "kg", big.NewRat(-1, 1)}}},
		{"unconverted units", []planningDemandQuantity{valid, {planningDemandProduct, "g", big.NewRat(1, 1)}}},
		{"too many ingredients", make([]planningDemandQuantity, planningIngredientLimit+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := groupPlanningDemand(tc.lines); err == nil {
				t.Fatal("invalid demand accepted")
			}
		})
	}
	for _, lines := range [][]planningDemandQuantity{nil, {{}}} {
		groups, complete, err := groupPlanningDemand(lines)
		if err != nil || complete || len(groups) != 0 {
			t.Fatal("empty or unmapped demand became complete", groups, complete, err)
		}
	}
	lines := make([]planningDemandQuantity, planningIngredientLimit)
	for i := range lines {
		lines[i] = valid
	}
	groups, complete, err := groupPlanningDemand(lines)
	if err != nil || !complete || groups[planningDemandProduct].Quantity.RatString() != "500" {
		t.Fatal("bounded valid demand rejected", complete, err)
	}
}

func TestPlanningDemandShortage(t *testing.T) {
	tests := []struct {
		name, available, want string
		demand                *big.Rat
		approximate           bool
	}{
		{"shared stock counted once", "1", "1/5", big.NewRat(6, 5), false},
		{"own share capped after consumption", "0.3", "7/10", big.NewRat(1, 1), false},
		{"sufficient stock", "2", "0", big.NewRat(6, 5), false},
		{"fraction remains exact", "0", "1/3", big.NewRat(1, 3), true},
		{"below booking precision remains demand", "0", "1/10000000", big.NewRat(1, 10000000), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group := &planningDemandGroup{"kg", new(big.Rat).Set(tc.demand), true}
			got, err := planningDemandShortage(group, tc.available)
			if err != nil || got == nil || got.Rational != tc.want || got.Approximate != tc.approximate || group.Quantity.Cmp(tc.demand) != 0 {
				t.Fatal("incorrect or mutating shortage", got, err)
			}
		})
	}
	unknown := &planningDemandGroup{"kg", big.NewRat(1, 1), false}
	if got, err := planningDemandShortage(unknown, "1"); got != nil || err != nil {
		t.Fatal("incomplete demand became a zero shortage", got, err)
	}
	for _, available := range []string{"-1", "NaN", "1/3", "0.0000001"} {
		if _, err := planningDemandShortage(unknown, available); err == nil {
			t.Fatal("invalid available stock accepted", available)
		}
	}
}
