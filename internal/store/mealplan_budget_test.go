package store

import (
	"math/big"
	"testing"
)

func TestPlanningBudgetMinorRejectsAmbiguousOrUnboundedMoney(t *testing.T) {
	for _, raw := range []string{"-1", "+1", "01", " 1", "1.0", "1,0", "1000000000001", "9223372036854775808", ""} {
		if _, err := planningMinor(raw); err == nil {
			t.Errorf("accepted ambiguous or excessive minor value %q", raw)
		}
	}
	for _, raw := range []string{"0", "1", "1000000000000"} {
		if _, err := planningMinor(raw); err != nil {
			t.Errorf("rejected exact minor value %q: %v", raw, err)
		}
	}
}

func TestPlanningBudgetRulesRetainKnownZeroAndRejectWeightOverflow(t *testing.T) {
	if err := validatePlanningRules(PlanningAutoplanRules{}); err != nil {
		t.Fatal(err)
	}
	for _, rules := range []PlanningAutoplanRules{{Stock: -1}, {Urgency: 101}, {Effort: 101}, {Cost: -1}, {Repetition: 101}} {
		if err := validatePlanningRules(rules); err == nil {
			t.Fatal("accepted invalid weight", rules)
		}
	}
}

func TestPlanningBudgetFrozenIngredientCostRoundsOnlyAggregate(t *testing.T) {
	bundle, catalogs, scope := planningBudgetCostFixture()
	bundle.Ingredients = []Record{bundle.Ingredients[0], bundle.Ingredients[0], bundle.Ingredients[0]}
	lines, known, unknown, err := planningBundleCost(bundle, "1", 1, scope, catalogs)
	if err != nil || unknown != 0 || len(lines) != 3 || known.Cmp(big.NewRat(6, 5)) != 0 {
		t.Fatal(lines, known, unknown, err)
	}
	minor, approximate := recipeMoneyMinor(known)
	if minor != "1" || !approximate {
		t.Fatal("fractional cents were rounded per line instead of once", minor, approximate)
	}
	// The frozen bundle's base quantity remains1g; production portions scale it.
	_, scaled, _, err := planningBundleCost(bundle, "2", 1, scope, catalogs)
	if err != nil || scaled.Cmp(big.NewRat(12, 5)) != 0 || String(bundle.Ingredients[0]["quantity"]) != "1" {
		t.Fatal("portion evaluation mutated the frozen snapshot", scaled, err)
	}
}

func TestPlanningBudgetUnknownAndStaleMetadataNeverLooksFree(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		change       func(*RecipeBundle, map[string]Record, *planningBudgetScope) int64
	}{
		{"recipe disabled", "recipes_disabled", func(_ *RecipeBundle, _ map[string]Record, scope *planningBudgetScope) int64 {
			scope.RecipesEnabled = false
			return 1
		}},
		{"shopping disabled", "shopping_disabled", func(_ *RecipeBundle, _ map[string]Record, scope *planningBudgetScope) int64 {
			scope.ShoppingEnabled = false
			return 1
		}},
		{"old recipe identity", "source_generation_changed", func(_ *RecipeBundle, _ map[string]Record, _ *planningBudgetScope) int64 { return 2 }},
		{"stale price", "price_stale", func(_ *RecipeBundle, catalogs map[string]Record, _ *planningBudgetScope) int64 {
			catalogs[planningBudgetCatalogFixture]["price_observed_on"] = "2026-01-01"
			return 1
		}},
		{"unknown amount", "unknown_quantity", func(bundle *RecipeBundle, _ map[string]Record, _ *planningBudgetScope) int64 {
			bundle.Ingredients[0]["quantity"] = ""
			return 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle, catalogs, scope := planningBudgetCostFixture()
			generation := test.change(&bundle, catalogs, &scope)
			lines, known, unknown, err := planningBundleCost(bundle, "1", generation, scope, catalogs)
			if err != nil || known.Sign() != 0 || unknown != 1 || len(lines) != 1 || String(lines[0]["status"]) != test.reason {
				t.Fatal(lines, known, unknown, err)
			}
		})
	}
}

const planningBudgetCatalogFixture = "a1111111-1111-4111-8111-111111111111"

func planningBudgetCostFixture() (RecipeBundle, map[string]Record, planningBudgetScope) {
	bundle := RecipeBundle{Recipe: Record{"base_servings": "1", "title": "Frozen quantity"},
		Ingredients: []Record{{"id": "a2222222-2222-4222-8222-222222222222", "catalog_id": planningBudgetCatalogFixture,
			"name": "Ingredient", "quantity": "1", "unit_code": "g"}}}
	catalogs := map[string]Record{planningBudgetCatalogFixture: {"product_id": "a3333333-3333-4333-8333-333333333333",
		"price_minor": "1", "price_quantity": "2.5", "price_unit_code": "g", "price_observed_on": "2026-10-29"}}
	scope := planningBudgetScope{RecipesEnabled: true, ShoppingEnabled: true, RecipesGeneration: 1, ShoppingGeneration: 1, Today: "2026-10-30"}
	return bundle, catalogs, scope
}
