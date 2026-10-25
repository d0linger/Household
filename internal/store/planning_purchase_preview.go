package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"time"
)

func planningPurchaseFingerprint(data any) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// The comparison amount supports signed differences. All actual demand,
// coverage and posted quantities continue to use nonnegative amounts.
func planningPurchaseSignedAmount(amount *big.Rat) (IngredientAmount, error) {
	if amount == nil {
		return IngredientAmount{}, Invalid{"Exakte Mengendifferenz erforderlich"}
	}
	out, err := ingredientAmount(new(big.Rat).Abs(amount))
	if err == nil && amount.Sign() < 0 {
		out.Value, out.Rational = "-"+out.Value, amount.RatString()
	}
	return out, err
}

func sealPlanningPurchasePreview(out *PlanningPurchasesPreview) error {
	copy := *out
	copy.ReadAt, copy.Demand.ReadAt, copy.Fingerprint = time.Time{}, time.Time{}, ""
	hash, err := planningPurchaseFingerprint(copy)
	if err != nil {
		return err
	}
	out.Fingerprint = hash
	return nil
}

func finishPlanningPurchaseScope(scope *planningPurchaseScope) error {
	out := &scope.view
	out.Issues = append(out.Issues, out.Demand.Issues...)
	seen := make(map[string]bool, len(out.Demand.Lines))
	for _, line := range out.Demand.Lines {
		seen[line.ComponentID+"/"+line.IngredientID] = true
		if line.Reason != "" {
			out.Issues = append(out.Issues, Record{"meal_id": line.MealID, "component_id": line.ComponentID, "ingredient_id": line.IngredientID, "reason": line.Reason})
		}
	}
	for _, requirement := range out.Requirements {
		reason := requirement.ReviewReason
		if requirement.Active && !seen[requirement.ComponentID+"/"+requirement.IngredientID] {
			reason = "source_missing"
		}
		if reason != "" {
			out.Issues = append(out.Issues, Record{"requirement_id": requirement.ID, "meal_id": requirement.MealID, "reason": reason})
		}
	}
	if len(out.Issues) > 0 || out.Demand.Outcome == "incomplete" {
		return sealPlanningPurchasePreview(out)
	}
	products := make(map[string]*PlanningDemandProduct, len(out.Demand.Products))
	available := make(map[string]string, len(out.Demand.Products))
	for _, product := range out.Demand.Products {
		products[product.ID], available[product.ID] = product, product.AvailableForSelection
	}
	meals := make(map[string]PlanningDemandMeal, len(out.Demand.Meals))
	for _, meal := range out.Demand.Meals {
		meals[meal.ID] = meal
	}
	sources := make([]planningPurchaseSource, 0, len(out.Demand.Lines))
	lines := make(map[string]PlanningDemandLine, len(out.Demand.Lines))
	for _, line := range out.Demand.Lines {
		product, meal := products[line.ProductID], meals[line.MealID]
		if product == nil || line.Demand == nil {
			return Invalid{"Vollständigen Bedarf des gesamten aktiven Umfangs erneut prüfen"}
		}
		quantity, ok := new(big.Rat).SetString(line.Demand.Rational)
		if !ok {
			return Invalid{"Exakter Zutatenbedarf fehlt"}
		}
		sources = append(sources, planningPurchaseSource{MealID: line.MealID, ComponentID: line.ComponentID, IngredientID: line.IngredientID,
			ProductID: line.ProductID, Date: meal.Date, Slot: meal.Slot, Unit: product.Unit, ComponentPosition: line.ComponentPosition,
			IngredientPosition: line.IngredientPosition, Quantity: quantity, IntegerQuantities: product.IntegerQuantities})
		lines[line.ComponentID+"/"+line.IngredientID] = line
	}
	allocations, err := allocatePlanningPurchases(sources, available)
	if err != nil {
		return err
	}
	out.Ready = true
	for _, allocation := range allocations {
		key := allocation.ComponentID + "/" + allocation.IngredientID
		line := lines[key]
		meal := meals[line.MealID]
		basis, basisErr := planningPurchaseFingerprint(struct {
			Meal    PlanningDemandMeal
			Source  PlanningDemandLine
			Unit    string
			Integer bool
		}{meal, line, products[line.ProductID].Unit, products[line.ProductID].IntegerQuantities})
		if basisErr != nil {
			return basisErr
		}
		row, rowErr := planningPurchasePreviewLine(scope, line, allocation, basis)
		if rowErr != nil {
			return rowErr
		}
		if row.Reason != "" {
			out.Ready = false
			row.Action = "blocked"
		}
		out.Lines = append(out.Lines, row)
	}
	return sealPlanningPurchasePreview(out)
}

func planningPurchasePreviewLine(scope *planningPurchaseScope, source PlanningDemandLine, allocation planningPurchaseAllocation, basis string) (PlanningPurchaseLine, error) {
	out := PlanningPurchaseLine{ComponentID: source.ComponentID, IngredientID: source.IngredientID, Basis: basis, Bookable: allocation.Bookable, Action: "none"}
	requirement := scope.requirements[source.ComponentID+"/"+source.IngredientID]
	current := new(big.Rat)
	if requirement != nil {
		out.RequirementID = String(requirement["id"])
		var err error
		current, err = stockDecimal(String(requirement["pending_quantity"]))
		if err != nil || current.Sign() < 0 {
			return out, Invalid{"Bisherige Einkaufsmenge ist ungültig"}
		}
	}
	delta, additional, err := planningPurchaseDifference(allocation.Net, current)
	if err != nil {
		return out, err
	}
	if out.StockCoverage, err = ingredientAmount(allocation.StockCoverage); err != nil {
		return out, err
	}
	if out.Desired, err = ingredientAmount(allocation.Net); err != nil {
		return out, err
	}
	if out.Delta, err = planningPurchaseSignedAmount(delta); err != nil {
		return out, err
	}
	if out.Additional, err = ingredientAmount(additional); err != nil {
		return out, err
	}
	if !out.Bookable {
		out.Reason = "quantity_decision_required"
	}
	if allocation.Net.Sign() > 0 {
		out.Action = "create"
	}
	if requirement == nil {
		return out, nil
	}
	if requirement["active"] == true && String(requirement["reviewed_basis_hash"]) != basis {
		out.Reason = "source_changed"
	}
	if String(requirement["binding_id"]) != String(source.Binding["id"]) || Int(requirement["binding_version"]) != Int(source.Binding["version"]) ||
		String(requirement["product_id"]) != source.ProductID || Int(requirement["pantry_generation"]) != scope.view.PantryGeneration ||
		String(requirement["unit_code"]) != String(source.Binding["unit"]) {
		out.Reason = "mapping_changed"
	}
	disposition := String(requirement["procurement_disposition"])
	if disposition != "managed" {
		if disposition != "none" && allocation.Net.Sign() > 0 || disposition == "unresolved" || disposition == "missing" {
			out.Reason = "renewal_required"
		}
		return out, nil
	}
	item := scope.items[String(requirement["item_id"])]
	out.CurrentItem = item
	switch {
	case item == nil:
		out.Reason = "item_missing"
	case Int(requirement["shopping_generation"]) != scope.view.ShoppingGeneration || String(item["list_id"]) != String(requirement["list_id"]):
		out.Reason = "destination_changed"
	case Int(item["version"]) != Int(requirement["item_version"]) || String(item["fingerprint"]) != String(requirement["item_fingerprint"]):
		out.Reason = "item_changed"
	case item["checked"] == true:
		out.Reason = "checkout_required"
	case item["list_open"] != true:
		out.Reason = "destination_closed"
	}
	switch {
	case allocation.Net.Sign() == 0:
		out.Action = "remove"
	case delta.Sign() != 0:
		out.Action = "update"
	default:
		out.Action = "none"
	}
	return out, nil
}
