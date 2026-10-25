package store

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"household/internal/value"
)

func cookingStockTestInput() CookingStockInput {
	lid := value.UUID()
	return CookingStockInput{Generation: 1, PantryGeneration: 1, SourceVersionID: value.UUID(), Decisions: []CookingStockDecision{{IngredientID: value.UUID(), Action: "consume", Reason: " Einmalig gewählt ", Selection: "manual", ProductID: value.UUID(), ProductVersion: 1, Allocations: []CookingStockAllocation{{LotID: lid, Quantity: "1.0"}}}}, Lots: []CookingStockLotReview{{ID: lid, Version: 1, LocationVersion: 1, MovementCount: 1, Balance: "2"}}}
}

func TestNormalizeCookingStock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CookingStockInput)
	}{
		{"generation", func(in *CookingStockInput) { in.Generation = 0 }},
		{"source", func(in *CookingStockInput) { in.SourceVersionID = "invalid" }},
		{"too many ingredients", func(in *CookingStockInput) { in.Decisions = make([]CookingStockDecision, 501) }},
		{"duplicate ingredient", func(in *CookingStockInput) { in.Decisions = append(in.Decisions, in.Decisions[0]) }},
		{"long reason", func(in *CookingStockInput) { in.Decisions[0].Reason = strings.Repeat("ä", 251) }},
		{"invalid utf8", func(in *CookingStockInput) { in.Decisions[0].Reason = string([]byte{255}) }},
		{"manual explanation", func(in *CookingStockInput) { in.Decisions[0].Reason = " " }},
		{"catalog identity", func(in *CookingStockInput) { in.Decisions[0].Selection = "catalog" }},
		{"negative", func(in *CookingStockInput) { in.Decisions[0].Allocations[0].Quantity = "-1" }},
		{"precision", func(in *CookingStockInput) { in.Decisions[0].Allocations[0].Quantity = "0.0000001" }},
		{"duplicate allocation", func(in *CookingStockInput) {
			in.Decisions[0].Allocations = append(in.Decisions[0].Allocations, in.Decisions[0].Allocations[0])
		}},
		{"missing lot review", func(in *CookingStockInput) { in.Lots = nil }},
		{"extra lot review", func(in *CookingStockInput) { in.Lots[0].ID = value.UUID() }},
		{"duplicate lot review", func(in *CookingStockInput) { in.Lots = append(in.Lots, in.Lots[0]) }},
		{"negative balance", func(in *CookingStockInput) { in.Lots[0].Balance = "-1" }},
		{"missing version", func(in *CookingStockInput) { in.Lots[0].Version = 0 }},
		{"unconfirmed no stock", func(in *CookingStockInput) { in.Decisions = nil; in.Lots = nil }},
		{"false no stock", func(in *CookingStockInput) { in.ConfirmNoStock = true }},
		{"skip with hidden allocation", func(in *CookingStockInput) { in.Decisions[0].Action = "skip" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := cookingStockTestInput()
			tc.change(&in)
			if _, err := normalizeCookingStock(in); err == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
	in := cookingStockTestInput()
	before, _ := json.Marshal(in)
	out, err := normalizeCookingStock(in)
	if err != nil || out.Decisions[0].Reason != "Einmalig gewählt" || out.Decisions[0].Allocations[0].Quantity != "1.000000" || out.Lots[0].Balance != "2.000000" {
		t.Fatal(out, err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("normalization mutated caller")
	}
	in.Decisions = []CookingStockDecision{{IngredientID: value.UUID(), Action: "skip", Reason: "Nicht verwendet"}}
	in.Lots = nil
	in.ConfirmNoStock = true
	if _, err = normalizeCookingStock(in); err != nil {
		t.Fatal("explicit all-skip rejected", err)
	}
}

func TestCookingStockActual(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, from, to, actual, reason string
		integers, wantErr                        bool
	}{
		{"conversion", "100", "g", "kg", "0.05", "", false, false},
		{"unexplained deviation", "100", "g", "kg", "0.06", "", false, true},
		{"explained deviation", "100", "g", "kg", "0.06", "Tatsächlich gewogen", false, false},
		{"unknown", "", "g", "kg", "0.06", "Tatsächlich gewogen", false, false},
		{"unknown unconfirmed", "", "g", "kg", "0.06", "", false, true},
		{"incompatible", "1", "l", "kg", "1", "Gewogen", false, false},
		{"integer", "1", "pcs", "pcs", "0.5", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := CookingStockDecision{Reason: tc.reason, Allocations: []CookingStockAllocation{{Quantity: tc.actual}}}
			_, _, err := cookingStockActual(Record{"quantity": tc.quantity, "unit_code": tc.from}, d, tc.to, tc.integers, big.NewRat(2, 1), big.NewRat(1, 1))
			if (err != nil) != tc.wantErr {
				t.Fatal(err)
			}
		})
	}
}
