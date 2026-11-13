package database

import (
	"slices"
	"strings"
	"testing"
)

func TestReadinessChecksKeepEveryContractExactlyOnce(t *testing.T) {
	expected := []string{
		"SELECT fuel.overview_ready()",
		"SELECT fuel.module_ready()",
		"SELECT shopping.repurchase_ready()",
		"SELECT shopping.barcode_ready()",
		"SELECT core.audit_ready()",
		"SELECT core.notification_foundation_ready()",
		"SELECT core.crypto_envelopes_ready(),core.security_events_ready()",
		"SELECT pantry.projection_ready()",
		"SELECT recipes.catalog_ready()",
		"SELECT core.recipe_expansion_ready()",
		"SELECT recipes.expansion_ready()",
		"SELECT pantry.cooking_stock_ready()",
		"SELECT pantry.stock_batch_ready()",
		"SELECT pantry.replenishment_ready()",
		"SELECT pantry.waste_ready()",
		"SELECT pantry.prepared_capture_ready()",
		"SELECT pantry.notification_observations_ready()",
		"SELECT fuel.notification_ready()",
		"SELECT core.notification_financial_ready()",
		"SELECT expenses.notification_ready()",
		"SELECT core.meal_reservations_ready()",
		"SELECT core.planning_bindings_ready()",
		"SELECT core.planning_purchases_ready()",
		"SELECT core.planning_participants_ready()",
		"SELECT mealplan.expansion_ready(),core.planning_extensions_ready()",
		"SELECT expenses.editing_ready()",
		"SELECT core.expense_editing_ready()",
		"SELECT expenses.financial_bindings_ready()",
		"SELECT core.financial_requirements_ready()",
		"SELECT expenses.finance_ready(),core.finance_ready()",
		"SELECT chores.expansion_ready()",
		"SELECT meters.expansion_ready()",
		"SELECT meters.notification_ready()",
		"SELECT documents.expansion_ready()",
		"SELECT documents.notification_ready()",
		"SELECT mealplan.notification_ready()",
	}
	checks := readinessChecks()
	actual := make([]string, 0, len(checks))
	for _, check := range checks {
		actual = append(actual, check.query)
		if check.inspect == "" || check.incomplete == "" || check.columns != strings.Count(check.query, ",")+1 {
			t.Fatalf("incomplete readiness assertion: %+v", check)
		}
	}
	prefix := []string{
		"SELECT fuel.module_ready()",
		"SELECT pantry.prepared_capture_ready()",
		"SELECT core.audit_ready()",
		"SELECT expenses.finance_ready(),core.finance_ready()",
		"SELECT core.financial_requirements_ready()",
	}
	if len(actual) < len(prefix) || !slices.Equal(actual[:len(prefix)], prefix) {
		t.Fatal("deep inspectors must start before the short tail")
	}
	slices.Sort(expected)
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		t.Fatalf("readiness inventory changed: %v", actual)
	}
}
