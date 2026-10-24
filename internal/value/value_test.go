package value

import (
	"math/big"
	"testing"
)

func TestUUID(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := UUID()
		if !ValidUUID(id) || seen[id] {
			t.Fatalf("invalid or repeated UUID %q", id)
		}
		seen[id] = true
	}
}
func TestDecimal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, input, want string
		valid             bool
	}{
		{name: "comma", input: "1,25", want: "1.250000", valid: true},
		{name: "negative", input: "-0.5", want: "-0.500000", valid: true},
		{name: "zero", input: "0", want: "0.000000", valid: true},
		{name: "negative zero", input: "-0.000000", want: "0.000000", valid: true},
		{name: "decimal leading zero", input: "010", want: "10.000000", valid: true},
		{name: "trim whitespace", input: "  1.25\n", want: "1.250000", valid: true},
		{name: "maximum", input: "999999999999.999999", want: "999999999999.999999", valid: true},
		{name: "minimum", input: "-999999999999.999999", want: "-999999999999.999999", valid: true},
		{name: "exponent", input: "1e3"},
		{name: "fraction", input: "1/2"},
		{name: "precision", input: "0.0000001"},
		{name: "NaN", input: "NaN"},
		{name: "large", input: "1000000000000"},
		{name: "decimal separator without digits", input: "1."},
		{name: "mixed separators", input: "1,234.56"},
		{name: "grouping separators", input: "1.234.567"},
		{name: "Unicode digits", input: "１２"},
		{name: "embedded whitespace", input: "1 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Decimal(tc.input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && got.FloatString(6) != tc.want {
				t.Fatalf("got %s want %s", got.FloatString(6), tc.want)
			}
		})
	}
}
func TestMinor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, input string
		want        int64
		valid       bool
	}{
		{name: "comma", input: "12,34", want: 1234, valid: true},
		{name: "negative cents", input: "-0.50", want: -50, valid: true},
		{name: "whole", input: "42", want: 4200, valid: true},
		{name: "zero", input: "0", valid: true},
		{name: "trailing zero precision", input: "12.340000", want: 1234, valid: true},
		{name: "maximum", input: "10000000000.00", want: 1000000000000, valid: true},
		{name: "minimum", input: "-10000000000.00", want: -1000000000000, valid: true},
		{name: "above maximum", input: "10000000000.01"},
		{name: "below minimum", input: "-10000000000.01"},
		{name: "too precise", input: "12.345"},
		{name: "exponent", input: "1e2"},
		{name: "injection", input: "1;drop table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Minor(tc.input)
			if (err == nil) != tc.valid || tc.valid && got != tc.want {
				t.Fatalf("got %d,%v", got, err)
			}
		})
	}
}

func TestDate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{name: "minimum", input: "1900-01-01", want: true},
		{name: "maximum", input: "2200-12-31", want: true},
		{name: "leap year", input: "2000-02-29", want: true},
		{name: "nonleap century", input: "2100-02-29"},
		{name: "before range", input: "1899-12-31"},
		{name: "after range", input: "2201-01-01"},
		{name: "invalid day", input: "2026-04-31"},
		{name: "timestamp not date", input: "2026-10-24T00:00:00Z"},
		{name: "locale format", input: "24.10.2026"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Date(tc.input); got != tc.want {
				t.Fatalf("Date(%q) = %v; want %v", tc.input, got, tc.want)
			}
		})
	}
}

func FuzzDecimal(f *testing.F) {
	for _, input := range []string{"0", "010", "-1.000001", "999999999999.999999", "1e9", "\xff", "1,5"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := Decimal(input)
		if err != nil {
			return
		}
		if scaled := new(big.Rat).Mul(got, big.NewRat(1000000, 1)); !scaled.IsInt() {
			t.Fatal("accepted a value that cannot be stored exactly in numeric(18,6)")
		}
		if absolute := new(big.Rat).Abs(got); absolute.Cmp(big.NewRat(1000000000000, 1)) >= 0 {
			t.Fatal("accepted an out-of-range quantity")
		}
		canonical := got.FloatString(6)
		roundTrip, err := Decimal(canonical)
		if err != nil || got.Cmp(roundTrip) != 0 {
			t.Fatalf("decimal round trip through %q failed: %v", canonical, err)
		}
	})
}

func FuzzMinor(f *testing.F) {
	for _, input := range []string{"0", "12,50", "-0.50", "999999999999.00", "1e9"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		n, err := Minor(input)
		if err == nil && (n > 1000000000000 || n < -1000000000000) {
			t.Fatalf("accepted out-of-range amount %d", n)
		}
		if err == nil {
			canonical := big.NewRat(n, 100).FloatString(2)
			roundTrip, err := Minor(canonical)
			if err != nil || roundTrip != n {
				t.Fatalf("money round trip through %q failed: %d, %v", canonical, roundTrip, err)
			}
		}
	})
}
