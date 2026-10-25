package store

import (
	"testing"

	"household/internal/value"
)

func TestPlanningPurchaseDecisions(t *testing.T) {
	rid, mid := value.UUID(), value.UUID()
	for _, tc := range []struct {
		name    string
		choices []PlanningPurchaseDecision
		valid   bool
	}{
		{"no implicit decisions", nil, true},
		{"accept current source and item", []PlanningPurchaseDecision{{RequirementID: rid, Action: "accept"}}, true},
		{"explicit new purchase", []PlanningPurchaseDecision{{RequirementID: rid, Action: "renew"}}, true},
		{"whole meal reactivation", []PlanningPurchaseDecision{{MealID: mid, Action: "reactivate"}}, true},
		{"ingredient reactivation forbidden", []PlanningPurchaseDecision{{RequirementID: rid, Action: "reactivate"}}, false},
		{"mixed identities forbidden", []PlanningPurchaseDecision{{RequirementID: rid, MealID: mid, Action: "accept"}}, false},
		{"unknown action", []PlanningPurchaseDecision{{RequirementID: rid, Action: "overwrite"}}, false},
		{"duplicate contradictory decisions", []PlanningPurchaseDecision{{RequirementID: rid, Action: "accept"}, {RequirementID: rid, Action: "renew"}}, false},
		{"duplicate whole meal", []PlanningPurchaseDecision{{MealID: mid, Action: "reactivate"}, {MealID: mid, Action: "reactivate"}}, false},
		{"invalid identity", []PlanningPurchaseDecision{{RequirementID: "invalid", Action: "renew"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := planningPurchaseDecisions(tc.choices)
			if (err == nil) != tc.valid {
				t.Fatal("explicit decision identity contract", err)
			}
		})
	}
}
