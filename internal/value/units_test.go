package value

import (
	"math/big"
	"testing"
)

func TestConvert(t *testing.T) {
	t.Parallel()
	got, err := Convert(big.NewRat(250, 1), "g", "kg")
	if err != nil || got.Cmp(big.NewRat(1, 4)) != 0 {
		t.Fatalf("conversion %v %v", got, err)
	}
	if _, err = Convert(big.NewRat(1, 1), "l", "kg"); err == nil {
		t.Fatal("volume converted to mass without density")
	}
}
