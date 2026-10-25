package store

import (
	"sort"

	"household/internal/value"
)

type PlanningPurchaseDecision struct {
	RequirementID string `json:"requirement_id"`
	MealID        string `json:"meal_id"`
	Action        string `json:"action"`
}

func planningPurchaseDecisions(choices []PlanningPurchaseDecision) ([]PlanningPurchaseDecision, map[string]string, error) {
	if len(choices) > planningIngredientLimit {
		return nil, nil, Invalid{"Höchstens 500 ausdrückliche Entscheidungen bestätigen"}
	}
	out, seen := make([]PlanningPurchaseDecision, 0, len(choices)), make(map[string]string, len(choices))
	for _, choice := range choices {
		identifier := choice.RequirementID
		valid := value.ValidUUID(identifier) && choice.MealID == "" && (choice.Action == "accept" || choice.Action == "renew")
		if choice.Action == "reactivate" {
			identifier = "meal/" + choice.MealID
			valid = value.ValidUUID(choice.MealID) && choice.RequirementID == ""
		}
		if !valid || seen[identifier] != "" {
			return nil, nil, Invalid{"Jede gültige Quelle mit genau einer benannten Entscheidung bestätigen"}
		}
		seen[identifier] = choice.Action
		out = append(out, choice)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequirementID+out[i].MealID < out[j].RequirementID+out[j].MealID })
	return out, seen, nil
}
