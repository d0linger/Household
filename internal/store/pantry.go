package store

import (
	"fmt"
	"math/big"
	"regexp"
)

var stockPattern = regexp.MustCompile(`^-?[0-9]{1,30}(\.[0-9]{1,6})?$`)

// stockDecimal accepts database aggregates, whose range exceeds one movement.
// Request quantities still pass value.Decimal's narrower validation.
func stockDecimal(s string) (*big.Rat, error) {
	if !stockPattern.MatchString(s) {
		return nil, fmt.Errorf("invalid stock aggregate")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid stock aggregate")
	}
	return r, nil
}
