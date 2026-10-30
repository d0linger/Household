package store

import (
	"math/big"
	"strings"

	"household/internal/value"
)

type RecipeConvertedQuantity struct {
	Display     string `json:"display"`
	Exact       string `json:"exact"`
	Approximate bool   `json:"approximate"`
	UnitCode    string `json:"unit_code"`
}

func recipeUnitAllowed(code string) bool {
	switch code {
	case "piece", "g", "kg", "ml", "l", "tsp", "tbsp":
		return true
	default:
		return false
	}
}

func recipeUnitFactor(code string) (*big.Rat, string, bool) {
	switch code {
	case "piece":
		return big.NewRat(1, 1), "count", true
	case "g":
		return big.NewRat(1, 1), "mass", true
	case "kg":
		return big.NewRat(1000, 1), "mass", true
	case "ml":
		return big.NewRat(1, 1), "volume", true
	case "l":
		return big.NewRat(1000, 1), "volume", true
	case "tsp":
		return big.NewRat(5, 1), "volume", true
	case "tbsp":
		return big.NewRat(15, 1), "volume", true
	default:
		return nil, "", false
	}
}

// recipeConvertQuantity keeps conversion and cost arithmetic exact. Density is
// required for mass-volume conversions and never guessed from another food.
func recipeConvertQuantity(quantity *big.Rat, from, to string, density *big.Rat) (*big.Rat, bool) {
	fromFactor, fromDim, fromOK := recipeUnitFactor(from)
	toFactor, toDim, toOK := recipeUnitFactor(to)
	if quantity == nil || !fromOK || !toOK || quantity.Sign() < 0 {
		return nil, false
	}
	base := new(big.Rat).Mul(quantity, fromFactor)
	if fromDim != toDim {
		if density == nil || density.Sign() <= 0 {
			return nil, false
		}
		switch {
		case fromDim == "volume" && toDim == "mass":
			base.Mul(base, density)
		case fromDim == "mass" && toDim == "volume":
			base.Quo(base, density)
		default:
			return nil, false
		}
	}
	return base.Quo(base, toFactor), true
}

func recipeDisplayQuantity(quantity *big.Rat, code string) RecipeConvertedQuantity {
	if quantity == nil {
		return RecipeConvertedQuantity{UnitCode: code}
	}
	display := quantity.FloatString(6)
	printed, _ := new(big.Rat).SetString(display)
	approximate := printed.Cmp(quantity) != 0
	if printed.Sign() == 0 && quantity.Sign() != 0 {
		display = "<0.000001"
	} else if strings.Contains(display, ".") {
		display = strings.TrimRight(strings.TrimRight(display, "0"), ".")
	}
	return RecipeConvertedQuantity{Display: display, Exact: quantity.RatString(),
		Approximate: approximate, UnitCode: code}
}

func recipePositiveDecimal(raw string) (*big.Rat, bool) {
	amount, err := value.Decimal(raw)
	return amount, err == nil && amount.Sign() > 0
}

// Recipe price totals are displayed in minor units only when the exact amount
// is integral. A fractional cent is retained as an approximation, never hidden.
func recipeMoneyMinor(exact *big.Rat) (string, bool) {
	if exact == nil || exact.Sign() < 0 {
		return "", false
	}
	printed := exact.FloatString(0)
	parsed, _ := new(big.Rat).SetString(printed)
	return printed, parsed.Cmp(exact) != 0
}
