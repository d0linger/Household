package store

import (
	"math/big"

	"household/internal/value"
)

const planningIngredientLimit = 500

// Quantities have already been scaled to component portions and converted to
// the explicitly mapped product's base unit. A nil quantity remains unknown.
type planningDemandQuantity struct {
	ProductID string
	Unit      string
	Quantity  *big.Rat
}

type planningDemandGroup struct {
	Unit     string
	Quantity *big.Rat
	Complete bool
}

// Group before subtracting stock: independent components can require the same
// product. Neither input quantities nor pointers owned by the caller are changed.
func groupPlanningDemand(lines []planningDemandQuantity) (map[string]*planningDemandGroup, bool, error) {
	if len(lines) > planningIngredientLimit {
		return nil, false, Invalid{"Bedarf auf höchstens 500 Zutaten begrenzen"}
	}
	groups := make(map[string]*planningDemandGroup)
	complete := len(lines) > 0
	for _, line := range lines {
		if line.ProductID == "" {
			complete = false
			continue
		}
		if !value.ValidUUID(line.ProductID) {
			return nil, false, Invalid{"Gültige bestätigte Produktzuordnung erforderlich"}
		}
		if _, err := value.Convert(new(big.Rat), line.Unit, line.Unit); err != nil {
			return nil, false, Invalid{"Gültige Produktbasiseinheit erforderlich"}
		}
		group := groups[line.ProductID]
		if group == nil {
			group = &planningDemandGroup{Unit: line.Unit, Quantity: new(big.Rat), Complete: true}
			groups[line.ProductID] = group
		} else if group.Unit != line.Unit {
			return nil, false, Invalid{"Bedarf eines Produkts zuerst in dieselbe Basiseinheit umrechnen"}
		}
		if line.Quantity == nil {
			group.Complete, complete = false, false
			continue
		}
		if line.Quantity.Sign() < 0 {
			return nil, false, Invalid{"Nichtnegative Bedarfsmenge erforderlich"}
		}
		group.Quantity.Add(group.Quantity, line.Quantity)
	}
	return groups, complete, nil
}

// Available is the sum of eligible per-lot availability after other meals'
// claims, not a physical total with aggregate reservations subtracted afterward.
func planningDemandShortage(group *planningDemandGroup, available string) (*IngredientAmount, error) {
	stock, err := stockDecimal(available)
	if err != nil {
		return nil, err
	}
	if stock.Sign() < 0 {
		return nil, Invalid{"Nichtnegativer verfügbarer Bestand erforderlich"}
	}
	if group == nil || group.Quantity == nil || group.Quantity.Sign() < 0 {
		return nil, Invalid{"Gültiger zusammengefasster Bedarf erforderlich"}
	}
	if !group.Complete {
		return nil, nil
	}
	shortage := new(big.Rat).Sub(group.Quantity, stock)
	if shortage.Sign() < 0 {
		shortage.SetInt64(0)
	}
	amount, err := ingredientAmount(shortage)
	return &amount, err
}
