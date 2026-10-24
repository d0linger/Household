package store

import "testing"

func TestStockAggregateRange(t *testing.T) {
	for _, valid := range []string{"0", "-0.000001", "1999999999999.999998", "9007199254740993.123456"} {
		if _, err := stockDecimal(valid); err != nil {
			t.Fatalf("valid database sum %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "NaN", "1/2", "1e20", "0.0000001", "1234567890123456789012345678901", " 2"} {
		if _, err := stockDecimal(invalid); err == nil {
			t.Fatalf("invalid sum accepted %q", invalid)
		}
	}
}
