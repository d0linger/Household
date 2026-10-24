package value

import (
	"errors"
	"math/big"
)

func Convert(quantity *big.Rat, from, to string) (*big.Rat, error) {
	type unit struct {
		dimension string
		factor    int64
	}
	units := map[string]unit{"piece": {"count", 1}, "g": {"mass", 1}, "kg": {"mass", 1000}, "ml": {"volume", 1}, "l": {"volume", 1000}}
	source, ok := units[from]
	target, valid := units[to]
	if !ok || !valid || source.dimension != target.dimension {
		return nil, errors.New("incompatible units")
	}
	return new(big.Rat).Mul(quantity, big.NewRat(source.factor, target.factor)), nil
}
