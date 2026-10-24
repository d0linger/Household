package store

import "testing"

func TestShoppingMergedQuantity(t *testing.T) {
	for _, tc := range []struct{ name, a, au, b, bu, pack, pu, want string }{
		{"packs", "2", "piece", "3", "piece", "500", "g", "5.000000"},
		{"content to packs", "2", "piece", "1", "kg", "500", "g", "4.000000"},
		{"packs to content", "1", "kg", "2", "piece", "500", "g", "2.000000"},
		{"fractional pack", "1", "piece", "250", "g", "500", "g", "1.500000"},
		{"exact tiny", "0.000001", "g", "0.000001", "g", "1", "g", "0.000002"},
		{"large exact", "9007199254.740993", "g", "0.000001", "g", "1", "g", "9007199254.740994"},
		{"decimal boundary", "999999999999.999998", "g", "0.000001", "g", "1", "g", "999999999999.999999"},
		{"rounding disallowed", "1", "piece", "1", "g", "3", "g", ""},
		{"micro rounding disallowed", "1", "kg", "0.000001", "g", "1", "kg", ""},
		{"overflow", "999999999999.999999", "g", "0.000001", "g", "1", "g", ""},
		{"dimension mismatch", "1", "g", "1", "ml", "1", "g", ""},
		{"missing unit", "1", "", "1", "piece", "1", "piece", ""},
		{"missing quantity", "", "g", "1", "g", "1", "g", ""},
		{"zero", "0", "g", "1", "g", "1", "g", ""},
		{"negative", "1", "g", "-1", "g", "1", "g", ""},
		{"zero pack", "1", "piece", "1", "piece", "0", "g", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shoppingMergedQuantity(tc.a, tc.au, tc.b, tc.bu, tc.pack, tc.pu)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("unsafe conversion accepted: %s", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %s/%v, want %s", got, err, tc.want)
			}
		})
	}
}

func TestShoppingMergeRequiresReviewedIdentity(t *testing.T) {
	a := Record{"list_id": "list", "product_id": "product", "replacement_product_id": "replacement", "availability": "substituted", "assignee_id": "member", "note": "note", "group_name": "group"}
	b := Record{}
	for k, v := range a {
		b[k] = v
	}
	if err := shoppingMergeCompatible(a, b); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"list_id", "product_id", "replacement_product_id", "availability", "assignee_id", "note", "group_name", "checked_at"} {
		t.Run(field, func(t *testing.T) {
			old := b[field]
			b[field] = "different"
			if shoppingMergeCompatible(a, b) == nil {
				t.Fatal("incompatible detail was silently discarded")
			}
			b[field] = old
		})
	}
	a["product_id"], b["product_id"] = "", ""
	if shoppingMergeCompatible(a, b) == nil {
		t.Fatal("name-only merge allowed")
	}
}
