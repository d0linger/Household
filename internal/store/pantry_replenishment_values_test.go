package store

import "testing"

func TestPantryReplenishment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, balance, minimum, target, packQuantity, packUnit, baseUnit, quantity, count, surplus, reason string
	}{
		{name: "legacy minimum", balance: "0.25", minimum: "1", quantity: "0.750000", reason: "below_minimum"},
		{name: "target", balance: "0.25", minimum: "1", target: "3", quantity: "2.750000", reason: "below_minimum"},
		{name: "at minimum", balance: "1", minimum: "1", target: "3", quantity: "0", reason: "minimum_met"},
		{name: "unknown minimum", balance: "0", quantity: "0", reason: "minimum_unknown"},
		{name: "explicit zero minimum", balance: "0", minimum: "0", target: "3", quantity: "0", reason: "minimum_met"},
		{name: "whole packs", balance: "0.25", minimum: "1", target: "3", packQuantity: "500", packUnit: "g", quantity: "3.000000", count: "6", surplus: "0.250000", reason: "below_minimum"},
		{name: "exact packs", balance: "0.5", minimum: "1", target: "3", packQuantity: "500", packUnit: "g", quantity: "2.500000", count: "5", surplus: "0.000000", reason: "below_minimum"},
		{name: "micro remainder", balance: "0.499999", minimum: "1", target: "3", packQuantity: "500", packUnit: "g", quantity: "3.000000", count: "6", surplus: "0.499999", reason: "below_minimum"},
		{name: "wide aggregate", balance: "1999999999998.999998", minimum: "999999999999", quantity: "0", reason: "minimum_met"},
		{name: "volume packs", balance: "0.25", minimum: "1", target: "3", packQuantity: "500", packUnit: "ml", baseUnit: "l", quantity: "3.000000", count: "6", surplus: "0.250000", reason: "below_minimum"},
		{name: "count beyond binary float integers", balance: "0", minimum: "999999999999", packQuantity: "0.000001", packUnit: "kg", quantity: "999999999999.000000", count: "999999999999000000", surplus: "0.000000", reason: "below_minimum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			product := Record{"unit_code": "kg"}
			if tc.baseUnit != "" {
				product["unit_code"] = tc.baseUnit
			}
			if tc.minimum != "" {
				product["low_stock_threshold"] = tc.minimum
			}
			if tc.target != "" {
				product["target_stock"] = tc.target
			}
			pack := Record{}
			if tc.packQuantity != "" {
				pack = Record{"quantity": tc.packQuantity, "unit_code": tc.packUnit}
			}
			out, err := pantryReplenishment(product, tc.balance, pack)
			if err != nil || out.Reason != tc.reason || out.Quantity != tc.quantity || out.PackCount != tc.count {
				t.Fatal(out, err)
			}
			if tc.surplus != "" && out.Surplus != tc.surplus {
				t.Fatal("wrong exact surplus", out)
			}
		})
	}
}

func TestPantryReplenishmentRejectsInvalidBasis(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		product Record
		balance string
		pack    Record
	}{
		{name: "negative stock", product: Record{}, balance: "-1"},
		{name: "target without minimum", product: Record{"target_stock": "2"}, balance: "0"},
		{name: "target below minimum", product: Record{"low_stock_threshold": "2", "target_stock": "1"}, balance: "0"},
		{name: "negative target", product: Record{"low_stock_threshold": "0", "target_stock": "-1"}, balance: "0"},
		{name: "fractional target", product: Record{"low_stock_threshold": "1", "target_stock": "1.5", "integer_quantities": true}, balance: "0"},
		{name: "pack overflow", product: Record{"low_stock_threshold": "999999999999", "target_stock": "999999999999", "unit_code": "kg"}, balance: "0", pack: Record{"quantity": "2", "unit_code": "kg"}},
		{name: "dimension", product: Record{"low_stock_threshold": "1", "unit_code": "kg"}, balance: "0", pack: Record{"quantity": "1", "unit_code": "l"}},
		{name: "disabled pack", product: Record{"low_stock_threshold": "1", "unit_code": "kg"}, balance: "0", pack: Record{"quantity": "1", "unit_code": "kg", "disabled": true}},
		{name: "fractional integer pack", product: Record{"low_stock_threshold": "1", "target_stock": "1", "unit_code": "piece", "integer_quantities": true}, balance: "0", pack: Record{"quantity": "0.6", "unit_code": "piece"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := pantryReplenishment(tc.product, tc.balance, tc.pack); err == nil {
				t.Fatal("invalid basis accepted", out)
			}
		})
	}
}
