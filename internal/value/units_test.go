package value

import (
	"math/big"
	"testing"
)

func TestConvert(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, from, to string
		input, want    *big.Rat
	}{
		{name: "grams to kilograms", from: "g", to: "kg", input: big.NewRat(250, 1), want: big.NewRat(1, 4)},
		{name: "kilograms to grams", from: "kg", to: "g", input: big.NewRat(1, 4), want: big.NewRat(250, 1)},
		{name: "millilitres to litres", from: "ml", to: "l", input: big.NewRat(125, 1), want: big.NewRat(1, 8)},
		{name: "litres to millilitres", from: "l", to: "ml", input: big.NewRat(1, 8), want: big.NewRat(125, 1)},
		{name: "teaspoons to millilitres", from: "tsp", to: "ml", input: big.NewRat(3, 2), want: big.NewRat(15, 2)},
		{name: "tablespoons to teaspoons", from: "tbsp", to: "tsp", input: big.NewRat(2, 1), want: big.NewRat(6, 1)},
		{name: "litre to tablespoons exact", from: "l", to: "tbsp", input: big.NewRat(3, 100), want: big.NewRat(2, 1)},
		{name: "same unit", from: "piece", to: "piece", input: big.NewRat(3, 2), want: big.NewRat(3, 2)},
		{name: "teaspoon is not mass", from: "tsp", to: "g", input: big.NewRat(1, 1)},
		{name: "mass cannot become volume", from: "g", to: "ml", input: big.NewRat(1, 1)},
		{name: "volume cannot become mass", from: "l", to: "kg", input: big.NewRat(1, 1)},
		{name: "count cannot become mass", from: "piece", to: "g", input: big.NewRat(1, 1)},
		{name: "unknown source", from: "cup", to: "l", input: big.NewRat(1, 1)},
		{name: "unknown target", from: "l", to: "cup", input: big.NewRat(1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := new(big.Rat).Set(tc.input)
			got, err := Convert(tc.input, tc.from, tc.to)
			if tc.want == nil {
				if err == nil || got != nil {
					t.Fatalf("incompatible conversion succeeded: %v, %v", got, err)
				}
				return
			}
			if err != nil || got.Cmp(tc.want) != 0 {
				t.Fatalf("Convert() = %v, %v; want %v", got, err, tc.want)
			}
			if tc.input.Cmp(before) != 0 {
				t.Fatal("conversion mutated the source quantity")
			}
			got.SetInt64(99)
			if tc.input.Cmp(before) != 0 {
				t.Fatal("converted quantity aliases the input")
			}
		})
	}
}
