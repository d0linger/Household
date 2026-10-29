package value

import (
	"math/big"
	"testing"
)

func TestPortionsRemainAnIndependentExactDimension(t *testing.T) {
	q := big.NewRat(1250001, 1000000)
	got, err := Convert(q, "portion", "portion")
	if err != nil || got.Cmp(q) != 0 {
		t.Fatal("exact same-unit portion", got, err)
	}
	got.SetInt64(7)
	if q.Cmp(big.NewRat(1250001, 1000000)) != 0 {
		t.Fatal("conversion aliased the source rational")
	}
	for _, unit := range []string{"piece", "g", "kg", "ml", "l", "kWh", "unknown"} {
		if _, err = Convert(q, "portion", unit); err == nil {
			t.Fatal("portion silently converted to another dimension", unit)
		}
		if _, err = Convert(q, unit, "portion"); err == nil {
			t.Fatal("other dimension silently became portions", unit)
		}
	}
}
