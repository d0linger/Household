package value

import "testing"

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
	}{{"comma", "1,25", "1.250000", true}, {"negative", "-0.5", "-0.500000", true}, {"zero", "0", "0.000000", true}, {"exponent", "1e3", "", false}, {"fraction", "1/2", "", false}, {"precision", "0.0000001", "", false}, {"nan", "NaN", "", false}, {"large", "1000000000000", "", false}}
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
	}{{"comma", "12,34", 1234, true}, {"negative cents", "-0.50", -50, true}, {"whole", "42", 4200, true}, {"too precise", "12.345", 0, false}, {"exponent", "1e2", 0, false}, {"injection", "1;drop table", 0, false}}
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
func FuzzMinor(f *testing.F) {
	for _, input := range []string{"0", "12,50", "-0.50", "999999999999.00", "1e9"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		n, err := Minor(input)
		if err == nil && (n > 1000000000000 || n < -1000000000000) {
			t.Fatalf("accepted out-of-range amount %d", n)
		}
	})
}
