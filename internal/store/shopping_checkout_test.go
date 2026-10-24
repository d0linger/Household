package store

import "testing"

func TestCheckoutQuantityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, from, to, want string
		integer                        bool
	}{
		{"large exact", "9007199254.740993", "g", "g", "9007199254.740993", false},
		{"conversion", "1.234567", "kg", "g", "1234.567000", false},
		{"fraction lost", "0.000001", "g", "kg", "", false},
		{"overflow", "999999999999", "kg", "g", "", false},
		{"density guessed", "1", "g", "ml", "", false},
		{"fractional count", "1.5", "piece", "piece", "", true},
		{"integer base", "0.001", "kg", "g", "1.000000", true},
		{"negative", "-1", "g", "g", "", false},
		{"zero", "0", "g", "g", "", false},
		{"unknown unit", "1", "crate", "crate", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkoutQuantity(tc.quantity, tc.from, tc.to, tc.integer)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("quantity=%q error=%v", got, err)
			}
		})
	}
}

func TestCheckoutUnknownAndZeroPrice(t *testing.T) {
	unknown, err := checkoutPrice("")
	if err != nil || unknown != nil {
		t.Fatal("unknown price became a known amount")
	}
	zero, err := checkoutPrice("0")
	if err != nil || zero == nil || *zero != 0 {
		t.Fatal("explicit free position lost")
	}
	for _, invalid := range []string{"-1", "1.001", "10000000000.01", "1e2"} {
		if _, err = checkoutPrice(invalid); err == nil {
			t.Fatalf("invalid price accepted: %q", invalid)
		}
	}
}
