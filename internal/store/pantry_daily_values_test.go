package store

import "testing"

func TestPantryConsumptionExactReview(t *testing.T) {
	for _, tc := range []struct {
		name, mode, quantity, balance, pack, from, to, want string
		integers, invalid                                   bool
	}{
		{name: "exact large", mode: "amount", quantity: "9007199254.740993", balance: "9007199254.740993", want: "9007199254.740993"},
		{name: "half", mode: "half", balance: "3.000002", want: "1.500001"},
		{name: "half precision rejected", mode: "half", balance: "0.000001", invalid: true},
		{name: "half integer rejected", mode: "half", balance: "3", integers: true, invalid: true},
		{name: "pack conversion", mode: "pack", quantity: "2", balance: "5", pack: "500", from: "g", to: "kg", want: "1.000000"},
		{name: "partial pack", mode: "pack", quantity: "0.5", balance: "1", pack: "500", from: "ml", to: "l", want: "0.250000"},
		{name: "dimension mismatch", mode: "pack", quantity: "1", balance: "3", pack: "500", from: "g", to: "ml", invalid: true},
		{name: "stock shortfall", mode: "pack", quantity: "3", balance: "1", pack: "500", from: "g", to: "kg", invalid: true},
		{name: "no stock", mode: "half", balance: "0", invalid: true},
		{name: "negative", mode: "amount", quantity: "-1", balance: "3", invalid: true},
		{name: "oversized half", mode: "half", balance: "999999999999999999999.000000", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pantryConsumption(tc.mode, tc.quantity, tc.balance, tc.pack, tc.from, tc.to, tc.integers)
			if (err != nil) != tc.invalid || !tc.invalid && got != tc.want {
				t.Fatalf("got %q/%v; want %q, invalid=%t", got, err, tc.want, tc.invalid)
			}
		})
	}
}

func TestPantryBarcodeIdentity(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"leading zero", "0012345678905", "0012345678905"},
		{"custom case preserved", "Shelf-A.42", "Shelf-A.42"},
		{"scanner padding", " 0012345678905\r\n", "0012345678905"},
		{"inner separator rejected", "AB CD", ""},
		{"unicode rejected", "Ä123", ""},
		{"control rejected", "AB\x00CD", ""},
		{"empty rejected", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pantryBarcode(tc.input)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatal(got, err)
			}
		})
	}
}

func TestPantryDateHintAndPriority(t *testing.T) {
	days := int64(1)
	if got, err := pantryOpeningHint("2026-10-25", &days); err != nil || got != "2026-10-26" {
		t.Fatal("DST calendar hint", got, err)
	}
	if got, err := pantryOpeningHint("2028-02-28", &days); err != nil || got != "2028-02-29" {
		t.Fatal("leap calendar hint", got, err)
	}
	if got, err := pantryOpeningHint("2026-10-25", nil); err != nil || got != "" {
		t.Fatal("unknown duration invented", got, err)
	}
	days = 0
	if got, err := pantryOpeningHint("2026-10-25", &days); err != nil || got != "2026-10-25" {
		t.Fatal("known zero lost", got, err)
	}
	date, kind := pantryPriority("2026-10-26", "2026-10-25", "2026-10-25")
	if date != "2026-10-25" || kind != "use_by" {
		t.Fatal("date type tie changed", date, kind)
	}
	date, kind = pantryPriority("", "", "")
	if date != "" || kind != "unknown" {
		t.Fatal("unknown date invented", date, kind)
	}
}
