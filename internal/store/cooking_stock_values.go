package store

import (
	"math/big"
	"slices"
	"strings"
	"unicode/utf8"

	"household/internal/value"
)

type CookingStockAllocation struct {
	LotID    string `json:"lot_id"`
	Quantity string `json:"quantity"`
}

type CookingStockDecision struct {
	IngredientID   string                   `json:"ingredient_id"`
	Action         string                   `json:"action"`
	Reason         string                   `json:"reason"`
	Selection      string                   `json:"selection"`
	ProductID      string                   `json:"product_id"`
	ProductVersion int64                    `json:"product_version"`
	CatalogVersion int64                    `json:"catalog_version"`
	BindingID      string                   `json:"binding_id"`
	BindingVersion int64                    `json:"binding_version"`
	Allocations    []CookingStockAllocation `json:"allocations"`
}

type CookingStockLotReview struct {
	ID              string `json:"id"`
	Version         int64  `json:"version"`
	LocationVersion int64  `json:"location_version"`
	MovementCount   int64  `json:"movement_count"`
	Balance         string `json:"balance"`
}

type CookingStockInput struct {
	Generation       int64                   `json:"generation"`
	PantryGeneration int64                   `json:"pantry_generation"`
	SourceVersionID  string                  `json:"source_version_id"`
	ConfirmNoStock   bool                    `json:"confirm_no_stock"`
	Decisions        []CookingStockDecision  `json:"decisions"`
	Lots             []CookingStockLotReview `json:"lots"`
}

// Normalization makes reordered, equivalent retries identical without mutating
// the caller's slices. It never rounds actual consumption.
func normalizeCookingStock(in CookingStockInput) (CookingStockInput, error) {
	if in.Generation < 1 || in.PantryGeneration < 1 || !value.ValidUUID(in.SourceVersionID) || len(in.Decisions) > 500 || len(in.Lots) > 500 {
		return in, Invalid{"Gültige Quell- und Modulstände; höchstens 500 Zutaten und Chargen erforderlich"}
	}
	in.Decisions = slices.Clone(in.Decisions)
	in.Lots = slices.Clone(in.Lots)
	used, ingredients := map[string]bool{}, map[string]bool{}
	allocations := 0
	for i := range in.Decisions {
		d := &in.Decisions[i]
		d.Reason = strings.TrimSpace(d.Reason)
		if !value.ValidUUID(d.IngredientID) || ingredients[d.IngredientID] || !utf8.ValidString(d.Reason) || len(d.Reason) > 500 || strings.ContainsRune(d.Reason, 0) {
			return in, Invalid{"Jede Quellzutat genau einmal prüfen; Begründung auf 500 UTF-8-Bytes begrenzen"}
		}
		ingredients[d.IngredientID] = true
		d.Allocations = slices.Clone(d.Allocations)
		if d.Action == "skip" {
			if d.Reason == "" || d.Selection != "" || d.ProductID != "" || d.ProductVersion != 0 || d.CatalogVersion != 0 || d.BindingID != "" || d.BindingVersion != 0 || len(d.Allocations) != 0 {
				return in, Invalid{"Überspringen ausdrücklich begründen und ohne Produkt- oder Chargenangaben übermitteln"}
			}
			continue
		}
		if d.Action != "consume" || !value.ValidUUID(d.ProductID) || d.ProductVersion < 1 || len(d.Allocations) == 0 {
			return in, Invalid{"Verbrauch mit geprüftem Produkt und mindestens einer tatsächlichen Chargenmenge erforderlich"}
		}
		switch d.Selection {
		case "catalog":
			if d.CatalogVersion < 1 || d.BindingVersion < 1 || !value.ValidUUID(d.BindingID) {
				return in, Invalid{"Geprüften Katalog- und Zuordnungsstand übermitteln"}
			}
		case "manual":
			if d.Reason == "" || d.CatalogVersion != 0 || d.BindingID != "" || d.BindingVersion != 0 {
				return in, Invalid{"Einmalige Produktauswahl begründen; sie ändert keine Katalogzuordnung"}
			}
		default:
			return in, Invalid{"Katalogzuordnung oder begründete einmalige Produktauswahl erforderlich"}
		}
		seen := map[string]bool{}
		for j := range d.Allocations {
			a := &d.Allocations[j]
			q, err := value.Decimal(a.Quantity)
			if !value.ValidUUID(a.LotID) || seen[a.LotID] || err != nil || q.Sign() <= 0 {
				return in, Invalid{"Eindeutige Chargen und positive exakte Mengen erforderlich"}
			}
			a.Quantity = q.FloatString(6)
			seen[a.LotID], used[a.LotID] = true, true
			allocations++
		}
		if allocations > 500 {
			return in, Invalid{"Höchstens 500 Chargenzuweisungen pro Buchung zulässig"}
		}
		slices.SortFunc(d.Allocations, func(a, b CookingStockAllocation) int { return strings.Compare(a.LotID, b.LotID) })
	}
	seen := map[string]bool{}
	for i := range in.Lots {
		l := &in.Lots[i]
		q, err := stockDecimal(l.Balance)
		if !value.ValidUUID(l.ID) || seen[l.ID] || !used[l.ID] || l.Version < 1 || l.LocationVersion < 1 || l.MovementCount < 0 || err != nil || q.Sign() < 0 {
			return in, Invalid{"Jede verwendete Charge mit eindeutigem geprüftem Stand übermitteln"}
		}
		l.Balance = q.FloatString(6)
		seen[l.ID] = true
	}
	if len(seen) != len(used) {
		return in, Invalid{"Geprüfte Bestände aller verwendeten Chargen erforderlich"}
	}
	if (len(used) == 0) != in.ConfirmNoStock {
		return in, Invalid{"Abschluss ohne Entnahme ausdrücklich bestätigen; bei Verbrauch nicht setzen"}
	}
	slices.SortFunc(in.Decisions, func(a, b CookingStockDecision) int { return strings.Compare(a.IngredientID, b.IngredientID) })
	slices.SortFunc(in.Lots, func(a, b CookingStockLotReview) int { return strings.Compare(a.ID, b.ID) })
	return in, nil
}

func cookingStockActual(row Record, d CookingStockDecision, unit string, integers bool, base, servings *big.Rat) (string, *IngredientAmount, error) {
	actual := new(big.Rat)
	for _, a := range d.Allocations {
		q, err := value.Decimal(a.Quantity)
		if err != nil {
			return "", nil, err
		}
		actual.Add(actual, q)
	}
	amount, err := pantryExactAmount(actual, integers)
	if err != nil {
		return "", nil, err
	}
	demand, demandErr := ingredientDemand(String(row["quantity"]), String(row["unit_code"]), unit, base, servings)
	if (demandErr != nil || demand.Cmp(actual) != 0) && d.Reason == "" {
		return "", nil, Invalid{"Abweichende oder unbekannte Rezeptmenge mit einer tatsächlichen Menge und Begründung bestätigen"}
	}
	if demandErr != nil {
		return amount, nil, nil
	}
	suggested, err := ingredientAmount(demand)
	return amount, &suggested, err
}
