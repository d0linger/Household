package store

import (
	"math/big"
	"sort"

	"household/internal/value"
)

type planningAutoplanEvaluation struct {
	Candidate       planningAutoplanCandidate
	Score           int
	Reasons         []Record
	Coverage        string
	CostMinor       string
	UnknownCost     int
	Quantities      []planningDemandQuantity
	UrgentOn        string
	DateReview      bool
	DateReasons     []suggestionDateReason
	DateReasonCount int
}

func simulatePlanningAutoplan(in PlanningAutoplanInput, data planningAutoplanData) (Record, []planningAutoplanChoice, error) {
	pool := make(map[string]*big.Rat, len(data.Lots))
	for _, lot := range data.Lots {
		pool[lot.ID] = planningAutoplanRat(lot.Free)
	}
	if err := planningAutoplanSubtractExisting(data, pool); err != nil {
		return nil, nil, err
	}
	repetitions := map[string]int{}
	for _, meal := range data.Week.Meals {
		if meal.Status == "cancelled" {
			continue
		}
		for _, component := range meal.Components {
			repetitions[component.SourceRecipeID]++
		}
	}
	candidates := []Record{}
	for _, candidate := range data.Candidates {
		candidates = append(candidates, Record{"source_version_id": candidate.SourceVersionID,
			"recipe_id": candidate.RecipeID, "title": candidate.Title, "source_generation": in.RecipesGeneration})
	}
	results := []Record{}
	choices := []planningAutoplanChoice{}
	var selectedBytes int64
	for _, slot := range planningAutoplanSortedSlots(in.Slots) {
		if slot.LeaveEmpty {
			results = append(results, Record{"planned_on": slot.PlannedOn, "slot": slot.Slot,
				"servings": slot.Servings, "leave_empty": true, "alternatives": []Record{}, "reasons": []Record{}})
			continue
		}
		alternatives := []planningAutoplanEvaluation{}
		for _, candidate := range data.Candidates {
			evaluation, err := evaluatePlanningAutoplanCandidate(candidate, slot, in.Rules, data, pool, repetitions)
			if err != nil {
				return nil, nil, err
			}
			alternatives = append(alternatives, evaluation)
		}
		sort.Slice(alternatives, func(i, j int) bool {
			if alternatives[i].Score != alternatives[j].Score {
				return alternatives[i].Score > alternatives[j].Score
			}
			return alternatives[i].Candidate.SourceVersionID < alternatives[j].Candidate.SourceVersionID
		})
		if len(alternatives) == 0 {
			return nil, nil, Invalid{"Ausgewählte Rezeptstände sind nicht bewertbar"}
		}
		selected := alternatives[0]
		if slot.SourceVersionID != "" {
			found := false
			for _, candidate := range alternatives {
				if candidate.Candidate.SourceVersionID == slot.SourceVersionID {
					selected, found = candidate, true
					break
				}
			}
			if !found {
				return nil, nil, Invalid{"Ausgewählte Alternative ist kein geprüfter Rezeptstand"}
			}
		}
		selectedBytes += selected.Candidate.SnapshotBytes
		if selectedBytes > planningCopyBytes {
			return nil, nil, Invalid{"Gemeinsam geplante Rezeptstände überschreiten 16 MiB. Plätze verkleinern."}
		}
		if err := consumePlanningAutoplanQuantities(selected.Quantities, data.Lots, pool, slot.PlannedOn); err != nil {
			return nil, nil, err
		}
		repetitions[selected.Candidate.RecipeID]++
		rows := []Record{}
		for _, alternative := range alternatives {
			rows = append(rows, planningAutoplanEvaluationRecord(alternative))
		}
		row := planningAutoplanEvaluationRecord(selected)
		row["planned_on"], row["slot"], row["servings"] = slot.PlannedOn, slot.Slot, slot.Servings
		row["alternatives"], row["leave_empty"] = rows, false
		results = append(results, row)
		choices = append(choices, planningAutoplanChoice{Slot: slot, SourceVersionID: selected.Candidate.SourceVersionID,
			Title: selected.Candidate.Title, PrepMinutes: selected.Candidate.PrepMinutes, CookMinutes: selected.Candidate.CookMinutes})
	}
	notes := []string{"Alle Vorschläge verwenden denselben simulierten Restbestand; es wurde nichts reserviert oder verbraucht.",
		"Bestehender aktiver Bedarf wurde vor neuen Vorschlägen berücksichtigt; eigene Reservierungen werden nicht doppelt abgezogen."}
	if data.UnknownLines > 0 {
		notes = append(notes, "Bestehender Bedarf enthält unbekannte Mengen oder Zuordnungen. Gemeinsame vollständige Vorratsdeckung ist deshalb nicht belegt.")
	}
	return Record{"candidates": candidates, "slots": results, "rules": in.Rules,
		"stock_review": Record{"mode": "joint_simulation", "existing_demand_complete": data.UnknownLines == 0,
			"unknown_existing_lines": data.UnknownLines, "lot_count": len(data.Lots), "notes": notes}}, choices, nil
}

func planningAutoplanEvaluationRecord(e planningAutoplanEvaluation) Record {
	return Record{"source_version_id": e.Candidate.SourceVersionID, "recipe_id": e.Candidate.RecipeID,
		"title": e.Candidate.Title, "score": e.Score, "reasons": e.Reasons, "coverage": e.Coverage,
		"cost_minor": e.CostMinor, "unknown_cost_count": e.UnknownCost, "urgent_on": e.UrgentOn,
		"date_review": e.DateReview, "date_reasons": e.DateReasons, "date_reason_count": e.DateReasonCount}
}

func evaluatePlanningAutoplanCandidate(candidate planningAutoplanCandidate, slot PlanningAutoplanSlotInput, rules PlanningAutoplanRules,
	data planningAutoplanData, pool map[string]*big.Rat, repetitions map[string]int) (planningAutoplanEvaluation, error) {
	out := planningAutoplanEvaluation{Candidate: candidate, Reasons: []Record{}}
	quantities, err := planningAutoplanQuantities(candidate, slot.Servings, data)
	if err != nil {
		return out, err
	}
	out.Quantities = quantities
	lots := planningAutoplanPoolLots(data.Lots, pool)
	stock, err := evaluateSuggestionLots(quantities, lots, slot.PlannedOn)
	if err != nil {
		return out, err
	}
	out.Coverage = stock.Coverage.Outcome
	out.UrgentOn, out.DateReview, out.DateReasonCount = stock.UrgentOn, stock.DateReview, len(stock.Reasons)
	out.DateReasons = suggestionDateEvidence(stock.Reasons)
	if len(out.DateReasons) > 5 {
		out.DateReasons = out.DateReasons[:5]
	}
	stockKnown := data.Scope.PantryEnabled && data.UnknownLines == 0 && !stock.DateReview && out.Coverage != "incomplete" && out.Coverage != "empty"
	stockPoints := 0
	stockDetail := "Keine vollständige, prüfungsfreie Vorratsdeckung belegt"
	switch {
	case !data.Scope.PantryEnabled:
		stockDetail = "Vorratsmodul ist deaktiviert; aktuelle Deckung bleibt unbekannt"
	case data.UnknownLines > 0:
		stockDetail = "Bestehender Bedarf ist unvollständig oder Reservierungen sind unterdeckt; kein Deckungsbonus"
	case stock.DateReview:
		stockDetail = "Mindestens eine zu berücksichtigende Charge benötigt eine Datumsprüfung; kein Deckungsbonus"
	case out.Coverage == "incomplete" || out.Coverage == "empty":
		stockDetail = "Mindestens eine Menge oder Produktzuordnung ist unbekannt; kein Deckungsbonus"
	}
	if stockKnown {
		stockPoints = 100
		if out.Coverage != "sufficient" {
			stockPoints = 0
			stockDetail = "Mindestens eine bestätigte Zutat fehlt im verbleibenden simulierten Vorrat"
		} else {
			stockDetail = "Alle bestätigten Zutaten passen gemeinsam in den verbleibenden simulierten Vorrat"
		}
	} else {
		out.Coverage = "incomplete"
	}
	addPlanningAutoplanReason(&out, "stock", stockPoints, rules.Stock, stockKnown, stockDetail)
	urgent := stockKnown && stock.UrgentOn != "" && !stock.DateReview
	urgency := 0
	if urgent {
		urgency = 100
	}
	addPlanningAutoplanReason(&out, "urgency", urgency, rules.Urgency, stockKnown,
		"Bonus nur für tatsächlich nutzbare bald fällige Mengen; kein Bonus für abgelaufenes Verbrauchsdatum oder prüfpflichtige Datumsangaben")
	effortKnown := candidate.PrepMinutes != nil && candidate.CookMinutes != nil
	effortPoints := 0
	if effortKnown {
		minutes := *candidate.PrepMinutes + *candidate.CookMinutes
		if minutes < 100 {
			effortPoints = 100 - int(minutes)
		}
	}
	addPlanningAutoplanReason(&out, "effort", effortPoints, rules.Effort, effortKnown,
		"100 minus bekannte Vorbereitungs- und Kochminuten, mindestens 0; fehlende Zeit erhält keinen Bonus")
	_, cost, unknown, err := planningBundleCost(candidate.Bundle, slot.Servings, data.Scope.RecipesGeneration, data.Scope, data.Catalogs)
	if err != nil {
		return out, err
	}
	out.CostMinor, _ = recipeMoneyMinor(cost)
	out.UnknownCost = unknown
	costPoints := 0
	if unknown == 0 {
		wholeEuro := new(big.Int).Quo(cost.Num(), new(big.Int).Mul(cost.Denom(), big.NewInt(100)))
		if wholeEuro.IsInt64() && wholeEuro.Int64() < 100 {
			costPoints = 100 - int(wholeEuro.Int64())
		}
	}
	addPlanningAutoplanReason(&out, "cost", costPoints, rules.Cost, unknown == 0,
		"100 minus bekannte volle Euro der Produktionsmenge, mindestens 0; unbekannte Preise erhalten keinen Bonus")
	repeatCount := repetitions[candidate.RecipeID]
	repeatPenalty := repeatCount * 25
	if repeatPenalty > 100 {
		repeatPenalty = 100
	}
	addPlanningAutoplanReason(&out, "repetition", -repeatPenalty, rules.Repetition, true,
		"Je bereits geplante Komponente dieses Rezepts 25 Minuspunkte, höchstens 100; eigene Vorschläge zählen sofort mit")
	return out, nil
}

func addPlanningAutoplanReason(out *planningAutoplanEvaluation, criterion string, points, weight int, known bool, detail string) {
	contribution := points * weight
	out.Score += contribution
	out.Reasons = append(out.Reasons, Record{"criterion": criterion, "contribution": contribution,
		"points": points, "weight": weight, "known": known, "detail": detail})
}

func planningAutoplanPoolLots(lots []suggestionDateLot, pool map[string]*big.Rat) []suggestionDateLot {
	out := append([]suggestionDateLot(nil), lots...)
	for i := range out {
		out[i].Free = planningAutoplanFloor(pool[out[i].ID])
	}
	return out
}

func consumePlanningAutoplanQuantities(lines []planningDemandQuantity, lots []suggestionDateLot, pool map[string]*big.Rat, date string) error {
	groups, _, err := groupPlanningDemand(lines)
	if err != nil {
		return err
	}
	ordered := append([]suggestionDateLot(nil), lots...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.UseBy != b.UseBy {
			if a.UseBy == "" || b.UseBy == "" {
				return a.UseBy != ""
			}
			return a.UseBy < b.UseBy
		}
		if a.BestBefore != b.BestBefore {
			if a.BestBefore == "" || b.BestBefore == "" {
				return a.BestBefore != ""
			}
			return a.BestBefore < b.BestBefore
		}
		return a.ID < b.ID
	})
	for pid, group := range groups {
		remaining := new(big.Rat).Set(group.Quantity)
		for _, lot := range ordered {
			if remaining.Sign() <= 0 {
				break
			}
			free := pool[lot.ID]
			if lot.ProductID != pid || free == nil || free.Sign() <= 0 || lot.UseBy != "" && lot.UseBy < date {
				continue
			}
			used := new(big.Rat).Set(free)
			if used.Cmp(remaining) > 0 {
				used.Set(remaining)
			}
			free.Sub(free, used)
			remaining.Sub(remaining, used)
		}
	}
	return nil
}

func planningAutoplanSubtractExisting(data planningAutoplanData, pool map[string]*big.Rat) error {
	byMeal := map[string][]planningDemandQuantity{}
	for _, line := range data.Existing.Lines {
		if line.Demand == nil || line.Reason != "" || !value.ValidUUID(line.ProductID) {
			continue
		}
		quantity, ok := new(big.Rat).SetString(line.Demand.Rational)
		if !ok || quantity.Sign() < 0 {
			return Invalid{"Bestehender bestätigter Bedarf enthält keine exakte Menge"}
		}
		unit := ""
		for _, product := range data.Existing.Products {
			if product.ID == line.ProductID {
				unit = product.Unit
				break
			}
		}
		byMeal[line.MealID] = append(byMeal[line.MealID], planningDemandQuantity{ProductID: line.ProductID, Unit: unit, Quantity: quantity})
	}
	meals := append([]PlanningDemandMeal(nil), data.Existing.Meals...)
	sort.Slice(meals, func(i, j int) bool {
		if meals[i].Date != meals[j].Date {
			return meals[i].Date < meals[j].Date
		}
		if meals[i].Slot != meals[j].Slot {
			return planningAutoplanSlotRank(meals[i].Slot) < planningAutoplanSlotRank(meals[j].Slot)
		}
		return meals[i].ID < meals[j].ID
	})
	for _, meal := range meals {
		groups, _, err := groupPlanningDemand(byMeal[meal.ID])
		if err != nil {
			return err
		}
		lines := []planningDemandQuantity{}
		for pid, group := range groups {
			unreserved := new(big.Rat).Sub(group.Quantity, planningAutoplanRat(data.OwnClaims[meal.ID+"/"+pid]))
			if unreserved.Sign() > 0 {
				lines = append(lines, planningDemandQuantity{ProductID: pid, Unit: group.Unit, Quantity: unreserved})
			}
		}
		if err = consumePlanningAutoplanQuantities(lines, data.Lots, pool, meal.Date); err != nil {
			return err
		}
	}
	return nil
}
