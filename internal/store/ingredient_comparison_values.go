package store

import (
	"math/big"

	"household/internal/value"
)

type IngredientAmount struct {
	Value          string `json:"value"`
	Rational       string `json:"rational"`
	Approximate    bool   `json:"approximate"`
	BelowPrecision bool   `json:"below_precision"`
}

func ingredientAmount(amount *big.Rat) (IngredientAmount, error) {
	if amount == nil || amount.Sign() < 0 {
		return IngredientAmount{}, Invalid{"Nichtnegative Vergleichsmenge erforderlich"}
	}
	raw := amount.FloatString(6)
	rounded, ok := new(big.Rat).SetString(raw)
	if !ok {
		return IngredientAmount{}, Invalid{"Vergleichsmenge konnte nicht dargestellt werden"}
	}
	return IngredientAmount{Value: raw, Rational: amount.RatString(), Approximate: amount.Cmp(rounded) != 0,
		BelowPrecision: amount.Sign() > 0 && rounded.Sign() == 0}, nil
}

func ingredientDemand(quantity, from, to string, base, servings *big.Rat) (*big.Rat, error) {
	q, err := value.Decimal(quantity)
	if err != nil || q.Sign() <= 0 || base == nil || servings == nil || base.Sign() <= 0 || servings.Sign() <= 0 {
		return nil, Invalid{"Positive Quellmenge und Portionen erforderlich"}
	}
	factor := new(big.Rat).Quo(servings, base)
	return value.Convert(new(big.Rat).Mul(q, factor), from, to)
}
