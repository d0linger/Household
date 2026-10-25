package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"household/internal/value"
)

func TestNormalizePlanningBinding(t *testing.T) {
	valid := PlanningIngredientBindingInput{Generation: 1, PantryGeneration: 1, PlanVersion: 1, MealVersion: 2, ComponentVersion: 3,
		SourceHash: strings.Repeat("AB", 32), ProductID: planningDemandProduct, ProductVersion: 1, Unit: "kg"}
	for _, tc := range []struct {
		name   string
		change func(*PlanningIngredientBindingInput)
	}{
		{"bad hash", func(in *PlanningIngredientBindingInput) { in.SourceHash = strings.Repeat("x", 64) }},
		{"short hash", func(in *PlanningIngredientBindingInput) { in.SourceHash = "ab" }},
		{"missing source generation", func(in *PlanningIngredientBindingInput) { in.Generation = 0 }},
		{"missing target generation", func(in *PlanningIngredientBindingInput) { in.PantryGeneration = 0 }},
		{"missing plan version", func(in *PlanningIngredientBindingInput) { in.PlanVersion = 0 }},
		{"missing meal version", func(in *PlanningIngredientBindingInput) { in.MealVersion = 0 }},
		{"missing component version", func(in *PlanningIngredientBindingInput) { in.ComponentVersion = 0 }},
		{"binding without version", func(in *PlanningIngredientBindingInput) { in.BindingID = value.UUID() }},
		{"version without binding", func(in *PlanningIngredientBindingInput) { in.Version = 1 }},
		{"bad target", func(in *PlanningIngredientBindingInput) { in.ProductID = "invalid" }},
		{"target without version", func(in *PlanningIngredientBindingInput) { in.ProductVersion = 0 }},
		{"target without unit", func(in *PlanningIngredientBindingInput) { in.Unit = "" }},
		{"cleared target retains metadata", func(in *PlanningIngredientBindingInput) { in.ProductID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.change(&in)
			if _, err := normalizePlanningBinding(&in); err == nil {
				t.Fatal("invalid reviewed mapping accepted")
			}
		})
	}
	hash, err := normalizePlanningBinding(&valid)
	if err != nil || len(hash) != 32 || valid.SourceHash != strings.Repeat("ab", 32) {
		t.Fatal("hash normalization", valid, err)
	}
	valid.BindingID, valid.Version, valid.ProductID, valid.ProductVersion, valid.Unit = value.UUID(), 4, "", 0, ""
	if _, err = normalizePlanningBinding(&valid); err != nil {
		t.Fatal("reviewed explicit unmap rejected", err)
	}
}

func TestPlanningIngredientSource(t *testing.T) {
	id := "abcdef01-0000-4000-8000-000000000001"
	valid := Record{"id": id, "name": "Unknown quantity is still an identifiable ingredient", "quantity": nil}
	for _, tc := range []struct {
		name string
		rows []Record
	}{
		{"duplicate identity", []Record{valid, valid}},
		{"uppercase identity", []Record{{"id": strings.ToUpper(id), "name": "Ingredient"}}},
		{"missing other name", []Record{valid, {"id": value.UUID()}}},
		{"null other name", []Record{valid, {"id": value.UUID(), "name": nil}}},
		{"nonstring other name", []Record{valid, {"id": value.UUID(), "name": 12}}},
		{"blank name", []Record{{"id": id, "name": " \t\n"}}},
		{"long name", []Record{{"id": id, "name": strings.Repeat("ä", 201)}}},
		{"ingredient bound", make([]Record, 501)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(RecipeBundle{Recipe: Record{}, Ingredients: tc.rows, Steps: []Record{}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = planningIngredientSource(raw, id); err == nil {
				t.Fatal("ambiguous or malformed frozen source accepted")
			}
		})
	}
	raw, err := json.Marshal(RecipeBundle{Recipe: Record{}, Ingredients: []Record{valid}, Steps: []Record{}})
	if err != nil {
		t.Fatal(err)
	}
	if found, readErr := planningIngredientSource(raw, id); readErr != nil || found["quantity"] != nil {
		t.Fatal("unknown quantity destroyed source identity", found, readErr)
	}
	if _, err = planningIngredientSource(raw, value.UUID()); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing ingredient", err)
	}
	for _, malformed := range [][]byte{nil, []byte("null"), []byte("{"), []byte(strings.Repeat(" ", planningComponentBytes+1))} {
		if _, err = planningIngredientSource(malformed, id); err == nil {
			t.Fatal("invalid or oversized source accepted")
		}
	}
}
