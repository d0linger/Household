package store

import (
	"math/big"
	"testing"
)

func TestIngredientDemandRetainsExactRationals(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, from, to string
		base, servings           int64
		want                     string
		approx, below            bool
	}{
		{"grams to kilograms", "100", "g", "kg", 2, 3, "3/20", false, false},
		{"milliliters to liters", "100", "ml", "l", 3, 1, "1/30", true, false},
		{"beyond binary precision", "9007199254.740993", "g", "g", 1, 1, "9007199254740993/1000000", false, false},
		{"sub micro demand", "0.000001", "g", "kg", 1000000, 1, "1/1000000000000000", true, true},
		{"third of a piece remains read-only rational", "1", "piece", "piece", 3, 1, "1/3", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			demand, err := ingredientDemand(tc.quantity, tc.from, tc.to, big.NewRat(tc.base, 1), big.NewRat(tc.servings, 1))
			if err != nil || demand.RatString() != tc.want {
				t.Fatal(demand, err)
			}
			amount, err := ingredientAmount(demand)
			if err != nil || amount.Rational != tc.want || amount.Approximate != tc.approx || amount.BelowPrecision != tc.below {
				t.Fatal(amount, err)
			}
		})
	}
	if _, err := ingredientDemand("1", "g", "ml", big.NewRat(1, 1), big.NewRat(1, 1)); err == nil {
		t.Fatal("incompatible dimensions treated as available")
	}
	for _, raw := range []string{"", "-1", "NaN", "0"} {
		if _, err := ingredientDemand(raw, "g", "g", big.NewRat(1, 1), big.NewRat(1, 1)); err == nil {
			t.Fatal("invalid quantity accepted", raw)
		}
	}
	if _, err := ingredientAmount(nil); err == nil {
		t.Fatal("unknown amount became zero")
	}
}
