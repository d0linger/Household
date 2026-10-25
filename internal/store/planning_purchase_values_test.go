package store

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"
)

func purchaseSource(index int, quantity *big.Rat) planningPurchaseSource {
	return planningPurchaseSource{MealID: fmt.Sprintf("10000000-0000-4000-8000-%012d", index+1),
		ComponentID: fmt.Sprintf("20000000-0000-4000-8000-%012d", index+1), IngredientID: fmt.Sprintf("30000000-0000-4000-8000-%012d", index+1),
		ProductID: planningDemandProduct, Date: "2026-10-26", Slot: "dinner", Unit: "kg", Quantity: quantity}
}

func TestAllocatePlanningPurchasesWholeScope(t *testing.T) {
	for _, tc := range []struct {
		name, available string
		quantity        *big.Rat
		want            []string
	}{
		{"disjoint sync includes previous active source", "0.5", big.NewRat(1, 1), []string{"1/2", "1"}},
		{"zero purchase still consumes forecast coverage", "0.5", big.NewRat(2, 5), []string{"0", "3/10"}},
		{"stored purchase keeps planned source active", "1", big.NewRat(1, 1), []string{"0", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := purchaseSource(0, tc.quantity), purchaseSource(1, tc.quantity)
			out, err := allocatePlanningPurchases([]planningPurchaseSource{b, a}, map[string]string{planningDemandProduct: tc.available})
			if err != nil || len(out) != 2 || out[0].ComponentID != a.ComponentID || out[0].Net.RatString() != tc.want[0] || out[1].Net.RatString() != tc.want[1] {
				t.Fatal("shared coverage or canonical order", out, err)
			}
			out[0].Demand.SetInt64(100)
			out[0].Net.SetInt64(200)
			out[0].StockCoverage.SetInt64(300)
			if a.Quantity.Cmp(tc.quantity) != 0 || b.Quantity.Cmp(tc.quantity) != 0 {
				t.Fatal("output mutation changed source demand")
			}
		})
	}
}

func TestAllocatePlanningPurchasesExactTargets(t *testing.T) {
	sources := []planningPurchaseSource{purchaseSource(0, big.NewRat(1, 3)), purchaseSource(1, big.NewRat(1, 3)), purchaseSource(2, big.NewRat(1, 3))}
	out, err := allocatePlanningPurchases(sources, map[string]string{planningDemandProduct: "0"})
	if err != nil {
		t.Fatal(err)
	}
	total := new(big.Rat)
	for _, row := range out {
		total.Add(total, row.Net)
		if row.Bookable || row.Net.RatString() != "1/3" {
			t.Fatal("whole grouped total authorized rounded individual purchases", row)
		}
	}
	if total.RatString() != "1" {
		t.Fatal("lost exact grouped need", total)
	}
	for _, tc := range []struct {
		name, stock string
		quantity    *big.Rat
		integers    bool
		bookable    bool
	}{
		{"below precision", "0", big.NewRat(1, 3000000), false, false},
		{"integer product", "0", big.NewRat(1, 2), true, false},
		{"covered fractional ingredient", "1", big.NewRat(1, 2), true, true},
		{"above single-item range", "0", big.NewRat(1200000000000, 1), false, false},
		{"normal exact target", "0.25", big.NewRat(1, 1), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := purchaseSource(0, tc.quantity)
			source.IntegerQuantities = tc.integers
			got, allocationErr := allocatePlanningPurchases([]planningPurchaseSource{source}, map[string]string{planningDemandProduct: tc.stock})
			if allocationErr != nil || got[0].Bookable != tc.bookable {
				t.Fatal("quantity booking boundary", got, allocationErr)
			}
		})
	}
}

func TestAllocatePlanningPurchasesConservesCoverageAcrossPermutations(t *testing.T) {
	sources := make([]planningPurchaseSource, 100)
	for i := range sources {
		sources[i] = purchaseSource(i, big.NewRat(int64(1+i%13), int64(1+i%7)))
		sources[i].MealID = fmt.Sprintf("10000000-0000-4000-8000-%012d", i/10+1)
		sources[i].ComponentPosition = i % 10
		sources[i].ProductID = fmt.Sprintf("40000000-0000-4000-8000-%012d", i%3+1)
	}
	available := map[string]string{}
	for i := range 3 {
		available[fmt.Sprintf("40000000-0000-4000-8000-%012d", i+1)] = "0.700001"
	}
	baseline, err := allocatePlanningPurchases(sources, available)
	if err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(11, 29))
	for range 20 {
		random.Shuffle(len(sources), func(i, j int) { sources[i], sources[j] = sources[j], sources[i] })
		got, allocationErr := allocatePlanningPurchases(sources, available)
		if allocationErr != nil {
			t.Fatal(allocationErr)
		}
		coverage := make(map[string]*big.Rat)
		for i, row := range got {
			if row.ComponentID != baseline[i].ComponentID || row.Net.Cmp(baseline[i].Net) != 0 || new(big.Rat).Add(row.StockCoverage, row.Net).Cmp(row.Demand) != 0 {
				t.Fatal("permutation changed attribution or lost source quantity", i)
			}
			if coverage[row.ProductID] == nil {
				coverage[row.ProductID] = new(big.Rat)
			}
			coverage[row.ProductID].Add(coverage[row.ProductID], row.StockCoverage)
		}
		for _, total := range coverage {
			if total.Cmp(big.NewRat(700001, 1000000)) != 0 {
				t.Fatal("physical product coverage was reused or lost", total)
			}
		}
	}
}

func TestAllocatePlanningPurchasesRejectsIncompleteScope(t *testing.T) {
	valid := purchaseSource(0, big.NewRat(1, 1))
	for _, tc := range []struct {
		name   string
		change func(*planningPurchaseSource)
	}{
		{"missing meal", func(s *planningPurchaseSource) { s.MealID = "" }},
		{"missing component", func(s *planningPurchaseSource) { s.ComponentID = "" }},
		{"missing ingredient", func(s *planningPurchaseSource) { s.IngredientID = "" }},
		{"missing product", func(s *planningPurchaseSource) { s.ProductID = "" }},
		{"unknown amount", func(s *planningPurchaseSource) { s.Quantity = nil }},
		{"negative demand", func(s *planningPurchaseSource) { s.Quantity = big.NewRat(-1, 1) }},
		{"unknown unit", func(s *planningPurchaseSource) { s.Unit = "cup" }},
		{"invalid date", func(s *planningPurchaseSource) { s.Date = "2026-02-30" }},
		{"unknown slot", func(s *planningPurchaseSource) { s.Slot = "other" }},
		{"invalid component order", func(s *planningPurchaseSource) { s.ComponentPosition = 10 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := valid
			tc.change(&source)
			if _, err := allocatePlanningPurchases([]planningPurchaseSource{source}, map[string]string{planningDemandProduct: "0"}); err == nil {
				t.Fatal("incomplete or invalid scope accepted")
			}
		})
	}
	for _, sources := range [][]planningPurchaseSource{nil, {valid, valid}, make([]planningPurchaseSource, planningIngredientLimit+1)} {
		if _, err := allocatePlanningPurchases(sources, map[string]string{planningDemandProduct: "0"}); err == nil {
			t.Fatal("invalid source set accepted")
		}
	}
	for _, stock := range []map[string]string{nil, {planningDemandProduct: "-1"}, {planningDemandProduct: "NaN"}, {planningDemandProduct: "0", "extra": "0"}} {
		if _, err := allocatePlanningPurchases([]planningPurchaseSource{valid}, stock); err == nil {
			t.Fatal("missing, invalid or unrelated coverage accepted")
		}
	}
}

func TestAllocatePlanningPurchasesRejectsContradictoryOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*planningPurchaseSource)
	}{
		{"component reused in another meal", func(s *planningPurchaseSource) { s.ComponentID = purchaseSource(0, nil).ComponentID }},
		{"component moved between source rows", func(s *planningPurchaseSource) {
			s.MealID, s.ComponentID = purchaseSource(0, nil).MealID, purchaseSource(0, nil).ComponentID
			s.ComponentPosition = 1
		}},
		{"two components at one position", func(s *planningPurchaseSource) { s.MealID = purchaseSource(0, nil).MealID }},
		{"two ingredients at one position", func(s *planningPurchaseSource) {
			s.MealID, s.ComponentID = purchaseSource(0, nil).MealID, purchaseSource(0, nil).ComponentID
		}},
		{"one meal at contradictory times", func(s *planningPurchaseSource) {
			s.MealID, s.ComponentPosition, s.Slot = purchaseSource(0, nil).MealID, 1, "lunch"
		}},
		{"contradictory product quantity policy", func(s *planningPurchaseSource) { s.IntegerQuantities = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := purchaseSource(0, big.NewRat(1, 1)), purchaseSource(1, big.NewRat(1, 1))
			tc.change(&b)
			if _, err := allocatePlanningPurchases([]planningPurchaseSource{a, b}, map[string]string{planningDemandProduct: "1"}); err == nil {
				t.Fatal("contradictory source ordering accepted")
			}
		})
	}
	sources := make([]planningPurchaseSource, planningMealLimit)
	for i := range sources {
		sources[i] = purchaseSource(i, big.NewRat(1, 1))
	}
	if _, err := allocatePlanningPurchases(sources, map[string]string{planningDemandProduct: "1"}); err != nil {
		t.Fatal("bounded complete scope refused", err)
	}
	sources = append(sources, purchaseSource(planningMealLimit, big.NewRat(1, 1)))
	if _, err := allocatePlanningPurchases(sources, map[string]string{planningDemandProduct: "1"}); err == nil {
		t.Fatal("more than 42 whole meals accepted")
	}
}

func TestAllocatePlanningPurchasesUsesMealAndSourceOrder(t *testing.T) {
	sources := make([]planningPurchaseSource, 7)
	for i := range sources {
		sources[i] = purchaseSource(i, big.NewRat(1, 1))
	}
	sources[0].Date = "2026-10-25"
	sources[1].Slot = "breakfast"
	sources[2].Slot = "lunch"
	sources[6].Slot = "snack"
	sources[4].MealID = sources[3].MealID
	sources[4].ComponentPosition = 1
	sources[5].MealID = sources[4].MealID
	sources[5].ComponentID = sources[4].ComponentID
	sources[5].ComponentPosition = 1
	sources[5].IngredientPosition = 1
	ordered := append([]planningPurchaseSource(nil), sources...)
	for i, j := 0, len(sources)-1; i < j; i, j = i+1, j-1 {
		sources[i], sources[j] = sources[j], sources[i]
	}
	out, err := allocatePlanningPurchases(sources, map[string]string{planningDemandProduct: "2.5"})
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range out {
		if row.IngredientID != ordered[i].IngredientID {
			t.Fatal("date/slot/component/ingredient order changed", i)
		}
		want := "1"
		if i < 2 {
			want = "0"
		} else if i == 2 {
			want = "1/2"
		}
		if row.Net.RatString() != want {
			t.Fatal("stock coverage assigned to wrong source", i, row.Net)
		}
	}
}

func TestPlanningPurchaseDifferenceKeepsStableDesiredTarget(t *testing.T) {
	for _, tc := range []struct{ desired, applied, delta, additional string }{
		{"1", "1/2", "1/2", "1/2"}, {"1", "1", "0", "0"}, {"1/2", "1", "-1/2", "0"}, {"0", "1", "-1", "0"},
	} {
		t.Run(tc.desired+" versus "+tc.applied, func(t *testing.T) {
			desired, _ := new(big.Rat).SetString(tc.desired)
			applied, _ := new(big.Rat).SetString(tc.applied)
			delta, additional, err := planningPurchaseDifference(desired, applied)
			if err != nil || delta.RatString() != tc.delta || additional.RatString() != tc.additional || desired.RatString() != tc.desired || applied.RatString() != tc.applied {
				t.Fatal("delta changed desired target or added a negative purchase", delta, additional, err)
			}
		})
	}
	for _, pair := range [][2]*big.Rat{{nil, big.NewRat(1, 1)}, {big.NewRat(-1, 1), new(big.Rat)}, {new(big.Rat), big.NewRat(-1, 1)}} {
		if _, _, err := planningPurchaseDifference(pair[0], pair[1]); err == nil {
			t.Fatal("invalid target/prior purchase accepted")
		}
	}
}
