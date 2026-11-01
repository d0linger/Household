package finance

import (
	"strconv"
	"testing"

	"household/internal/value"
)

func TestMoneyKnownUnknownAndGenuineZeroRemainDistinct(t *testing.T) {
	tests := []struct {
		name    string
		input   MoneyInput
		unknown bool
		want    *int64
		fail    bool
	}{
		{name: "EUR", input: MoneyInput{Currency: "EUR", Exponent: 2, Minor: "123"}, want: amountPointer(123)},
		{name: "unknown draft", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "1"}, unknown: true},
		{name: "unknown cannot post", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "1"}, fail: true},
		{name: "three decimal original below EUR cent", input: MoneyInput{Currency: "KWD", Exponent: 3, Minor: "1", FX: testRate("1", "1")}, want: amountPointer(0)},
		{name: "positive half away", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "1", FX: testRate("1", "2")}, want: amountPointer(1)},
		{name: "negative half away", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "-1", FX: testRate("1", "2")}, want: amountPointer(-1)},
		{name: "exact below half beyond decimal scale", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "1", FX: testRate("499999999999999999", "999999999999999999")}, want: amountPointer(0)},
		{name: "zero exponent conversion", input: MoneyInput{Currency: "JPY", Exponent: 0, Minor: "1", FX: testRate("1", "100")}, want: amountPointer(1)},
		{name: "wrong exponent", input: MoneyInput{Currency: "JPY", Exponent: 2, Minor: "1", FX: testRate("1", "1")}, fail: true},
		{name: "unknown currency", input: MoneyInput{Currency: "XXX", Exponent: 2, Minor: "1"}, unknown: true, fail: true},
		{name: "EUR zero rejected", input: MoneyInput{Currency: "EUR", Exponent: 2, Minor: "0"}, fail: true},
		{name: "EUR quote forbidden", input: MoneyInput{Currency: "EUR", Exponent: 2, Minor: "1", FX: testRate("1", "1")}, fail: true},
		{name: "zero denominator", input: MoneyInput{Currency: "USD", Exponent: 2, Minor: "1", FX: testRate("1", "0")}, fail: true},
		{name: "overflow after exact multiplication", input: MoneyInput{Currency: "JPY", Exponent: 0, Minor: "1000000000000", FX: testRate("999999999999999999", "1")}, fail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeMoney(test.input, false, test.unknown)
			if (err != nil) != test.fail {
				t.Fatalf("error=%v, want failure=%v", err, test.fail)
			}
			if err != nil {
				return
			}
			if test.want == nil {
				if got.BaseMinor != nil {
					t.Fatal("unknown valuation became a known amount", *got.BaseMinor)
				}
			} else if got.BaseMinor == nil || *got.BaseMinor != *test.want {
				t.Fatalf("base=%v want=%d", got.BaseMinor, *test.want)
			}
			if got.FX != nil {
				got.FX.Source = "changed copy"
				if test.input.FX.Source == got.FX.Source {
					t.Fatal("normalization aliases caller quote")
				}
			}
		})
	}
}

func TestMinorStringsAreCanonicalAndBounded(t *testing.T) {
	for _, bad := range []string{"", "-0", "+1", "01", "-01", "1.0", "1e2", " 1", "1 ", "1000000000001", "-1000000000001", "１", "--1"} {
		if _, err := ParseMinor(bad); err == nil {
			t.Errorf("accepted noncanonical/out-of-range %q", bad)
		}
	}
	for _, good := range []string{"0", "1", "-1", "1000000000000", "-1000000000000"} {
		n, err := ParseMinor(good)
		if err != nil || strconv.FormatInt(n, 10) != good {
			t.Errorf("canonical value %q failed: %v", good, err)
		}
	}
	zero, err := NormalizeMoney(MoneyInput{Currency: "EUR", Exponent: 2, Minor: "0"}, true, false)
	if err != nil || zero.BaseMinor == nil || *zero.BaseMinor != 0 {
		t.Fatal("explicit opening zero was lost", zero, err)
	}
	foreignZero, err := NormalizeMoney(MoneyInput{Currency: "JPY", Exponent: 0, Minor: "0"}, true, false)
	if err != nil || foreignZero.BaseMinor == nil || *foreignZero.BaseMinor != 0 || foreignZero.FX != nil {
		t.Fatal("zero foreign opening invented a rate or became unknown", foreignZero, err)
	}
}

func TestSignedLineConversionPreservesExactTotalAndUUIDTies(t *testing.T) {
	low := "00000000-0000-4000-8000-000000000001"
	high := "00000000-0000-4000-8000-000000000002"
	tests := []struct {
		name  string
		lines []LineAmount
		want  []int64
	}{
		{name: "tie independent of presentation order", lines: []LineAmount{{ID: high, Minor: 1}, {ID: low, Minor: 1}}, want: []int64{0, 1}},
		{name: "discount can convert to zero", lines: []LineAmount{{ID: low, Minor: 100}, {ID: high, Minor: -1}}, want: []int64{50, 0}},
		{name: "negative rational floors", lines: []LineAmount{{ID: high, Minor: -1}, {ID: low, Minor: -1}}, want: []int64{-1, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ConvertLines(test.lines, "USD", 2, testRate("1", "2"))
			if err != nil || len(got) != len(test.want) {
				t.Fatal(got, err)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("line %d got%d want%d", i, got[i], test.want[i])
				}
			}
		})
	}
	if _, err := ConvertLines([]LineAmount{{ID: low, Minor: 1}, {ID: low, Minor: 2}}, "EUR", 2, nil); err == nil {
		t.Fatal("duplicate line identity accepted")
	}
}

func TestPersonSplitMethodsAndRefundSymmetry(t *testing.T) {
	low := "00000000-0000-4000-8000-000000000001"
	high := "00000000-0000-4000-8000-000000000002"
	for _, test := range []struct {
		method string
		basis  []string
	}{
		{method: "equal", basis: []string{"1", "1"}},
		{method: "percent", basis: []string{"500000", "500000"}},
		{method: "weights", basis: []string{"10", "10"}},
	} {
		t.Run(test.method, func(t *testing.T) {
			input := SplitInput{Method: test.method, Participants: []Participant{{MembershipID: high, Basis: test.basis[0]}, {MembershipID: low, Basis: test.basis[1]}}}
			for _, sign := range []int64{1, -1} {
				got, err := Split(input, sign)
				if err != nil || got[0].Minor != "0" || got[1].Minor != strconv.FormatInt(sign, 10) {
					t.Fatal("deterministic signed remainder lost", got, err)
				}
			}
		})
	}
	if _, err := Split(SplitInput{Method: "fixed", Participants: []Participant{{MembershipID: low, Basis: "-2"}, {MembershipID: high, Basis: "-3"}}}, -5); err != nil {
		t.Fatal(err)
	}
	if _, err := Split(SplitInput{Method: "percent", Participants: []Participant{{MembershipID: low, Basis: "999999"}}}, 100); err == nil {
		t.Fatal("inexact percentage basis accepted")
	}
	if _, err := Split(SplitInput{Method: "fixed", Participants: []Participant{{MembershipID: low, Basis: "2"}, {MembershipID: high, Basis: "-1"}}}, 1); err == nil {
		t.Fatal("inverted person obligation accepted")
	}
}

func TestStableChildIdentitiesAndCurrencyCatalogueCopies(t *testing.T) {
	parent := "00000000-0000-4000-8000-000000000001"
	first := StableID(parent, "movement/0")
	if !value.ValidUUID(first) || first != StableID(parent, "movement/0") || first == StableID(parent, "movement/1") {
		t.Fatal("preview/apply child identity is unstable")
	}
	currencies := Currencies()
	currencies[0].Code = "changed"
	if Currencies()[0].Code != "EUR" {
		t.Fatal("currency response aliases mutable global state")
	}
}

func testRate(n, d string) *FXInput {
	return &FXInput{Numerator: n, Denominator: d, EffectiveOn: "2026-10-30", Source: "Manuell geprüft"}
}

func amountPointer(n int64) *int64 { return &n }
