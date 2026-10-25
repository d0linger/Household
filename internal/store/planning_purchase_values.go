package store

import (
	"math/big"
	"sort"

	"household/internal/value"
)

// The caller supplies the complete active household scope, including sources
// with zero purchases and stored purchases whose meals are still planned.
type planningPurchaseSource struct {
	MealID, ComponentID, IngredientID, ProductID string
	Date, Slot, Unit                             string
	ComponentPosition, IngredientPosition        int
	Quantity                                     *big.Rat
	IntegerQuantities                            bool
}

type planningPurchaseAllocation struct {
	ComponentID, IngredientID, ProductID string
	Demand, StockCoverage, Net           *big.Rat
	Bookable                             bool
}

// allocatePlanningPurchases allocates physical coverage before considering any
// existing shopping quantity. It does not register a scope or write effects.
func allocatePlanningPurchases(sources []planningPurchaseSource, available map[string]string) ([]planningPurchaseAllocation, error) {
	if len(sources) == 0 || len(sources) > planningIngredientLimit {
		return nil, Invalid{"1 bis 500 bestätigte Zutaten aus dem vollständigen aktiven Planungsumfang erforderlich"}
	}
	slots := map[string]int{"breakfast": 0, "lunch": 1, "dinner": 2, "snack": 3}
	seen := make(map[string]bool, len(sources))
	meals := make(map[string]string)
	components := make(map[string]struct {
		mealID   string
		position int
	})
	componentPositions := make(map[string][planningComponentLimit]string)
	ingredientPositions := make(map[string]map[int]string)
	integers := make(map[string]bool)
	quantities := make([]planningDemandQuantity, 0, len(sources))
	for _, source := range sources {
		key := source.ComponentID + "/" + source.IngredientID
		_, validSlot := slots[source.Slot]
		if !value.ValidUUID(source.MealID) || !value.ValidUUID(source.ComponentID) || !value.ValidUUID(source.IngredientID) ||
			!value.Date(source.Date) || !validSlot || source.ComponentPosition < 0 || source.ComponentPosition >= planningComponentLimit ||
			source.IngredientPosition < 0 || source.IngredientPosition >= planningIngredientLimit || seen[key] {
			return nil, Invalid{"Eindeutige vollständige Plan-Zutaten mit gültiger Reihenfolge erforderlich"}
		}
		seen[key] = true
		if prior, exists := components[source.ComponentID]; exists && (prior.mealID != source.MealID || prior.position != source.ComponentPosition) {
			return nil, Invalid{"Eine Komponente benötigt eine eindeutige Mahlzeit und Position"}
		}
		components[source.ComponentID] = struct {
			mealID   string
			position int
		}{source.MealID, source.ComponentPosition}
		positions := componentPositions[source.MealID]
		if prior := positions[source.ComponentPosition]; prior != "" && prior != source.ComponentID {
			return nil, Invalid{"Verschiedene Komponenten dürfen nicht dieselbe Mahlzeitenposition belegen"}
		}
		positions[source.ComponentPosition] = source.ComponentID
		componentPositions[source.MealID] = positions
		if ingredientPositions[source.ComponentID] == nil {
			ingredientPositions[source.ComponentID] = make(map[int]string)
		}
		if ingredientPositions[source.ComponentID][source.IngredientPosition] != "" {
			return nil, Invalid{"Eindeutige Zutatenposition innerhalb jeder Komponente erforderlich"}
		}
		ingredientPositions[source.ComponentID][source.IngredientPosition] = source.IngredientID
		when := source.Date + "/" + source.Slot
		if prior, exists := meals[source.MealID]; exists && prior != when {
			return nil, Invalid{"Eine Mahlzeit darf nicht mehrere widersprüchliche Termine enthalten"}
		}
		meals[source.MealID] = when
		if prior, exists := integers[source.ProductID]; exists && prior != source.IntegerQuantities {
			return nil, Invalid{"Widersprüchliche Mengeneigenschaften desselben Vorratsprodukts"}
		}
		integers[source.ProductID] = source.IntegerQuantities
		quantities = append(quantities, planningDemandQuantity{source.ProductID, source.Unit, source.Quantity})
	}
	if len(meals) > planningMealLimit {
		return nil, Invalid{"Aktiven Planungsumfang auf höchstens 42 ganze Mahlzeiten begrenzen"}
	}
	groups, complete, err := groupPlanningDemand(quantities)
	if err != nil {
		return nil, err
	}
	if !complete || len(groups) != len(available) {
		return nil, Invalid{"Vollständige Produktzuordnungen, Mengen und ausdrückliche Bestandsdeckung erforderlich"}
	}
	remaining := make(map[string]*big.Rat, len(groups))
	for pid := range groups {
		raw, exists := available[pid]
		stock, parseErr := stockDecimal(raw)
		if !exists || parseErr != nil || stock.Sign() < 0 {
			return nil, Invalid{"Aktuelle nichtnegative Deckung für jedes geprüfte Produkt erforderlich"}
		}
		remaining[pid] = stock
	}
	ordered := append([]planningPurchaseSource(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.Slot != b.Slot {
			return slots[a.Slot] < slots[b.Slot]
		}
		if a.MealID != b.MealID {
			return a.MealID < b.MealID
		}
		if a.ComponentPosition != b.ComponentPosition {
			return a.ComponentPosition < b.ComponentPosition
		}
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.IngredientPosition != b.IngredientPosition {
			return a.IngredientPosition < b.IngredientPosition
		}
		return a.IngredientID < b.IngredientID
	})
	out := make([]planningPurchaseAllocation, 0, len(ordered))
	for _, source := range ordered {
		stock := remaining[source.ProductID]
		used := new(big.Rat).Set(source.Quantity)
		if stock.Cmp(used) < 0 {
			used.Set(stock)
		}
		net := new(big.Rat).Sub(source.Quantity, used)
		stock.Sub(stock, used)
		bookable := net.Sign() == 0
		if net.Sign() > 0 {
			_, amountErr := pantryExactAmount(net, source.IntegerQuantities)
			bookable = amountErr == nil
		}
		out = append(out, planningPurchaseAllocation{source.ComponentID, source.IngredientID, source.ProductID,
			new(big.Rat).Set(source.Quantity), used, net, bookable})
	}
	return out, nil
}

func planningPurchaseDifference(desired, applied *big.Rat) (*big.Rat, *big.Rat, error) {
	if desired == nil || applied == nil || desired.Sign() < 0 || applied.Sign() < 0 {
		return nil, nil, Invalid{"Nichtnegative gewünschte und bisher bestätigte Einkaufsmenge erforderlich"}
	}
	delta := new(big.Rat).Sub(desired, applied)
	additional := new(big.Rat).Set(delta)
	if additional.Sign() < 0 {
		additional.SetInt64(0)
	}
	return delta, additional, nil
}
