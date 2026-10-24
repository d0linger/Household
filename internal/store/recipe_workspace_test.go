package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"household/internal/module"
	"household/internal/modules/recipes"
	"household/internal/value"
)

func recipeRaw(data Record) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for key, val := range data {
		raw, err := json.Marshal(val)
		if err != nil {
			panic(err)
		}
		result[key] = raw
	}
	return result
}
func TestRecipeNormalizeExactOptionalAndFieldErrors(t *testing.T) {
	s := Store{Modules: []module.Descriptor{recipes.Module}}
	good := func() RecipeInput {
		return RecipeInput{Data: recipeRaw(Record{"title": "Soup", "base_servings": "2,500001"}), Ingredients: []RecipeRow{{Key: value.UUID(), Data: recipeRaw(Record{"name": "Salt"})}, {Key: value.UUID(), Data: recipeRaw(Record{"name": "Exact", "quantity": "9007199254.740993", "unit_code": "g"})}}, Steps: []RecipeRow{{Key: value.UUID(), Data: recipeRaw(Record{"instruction": "Mix", "timer_seconds": "0"})}}}
	}
	data, ingredients, steps, err := s.normalizeRecipe(good())
	if err != nil || data["base_servings"] != "2.500001" || data["notes"] != "" || data["prep_minutes"] != int64(0) || ingredients[0]["quantity"] != nil || ingredients[1]["quantity"] != "9007199254.740993" || steps[0]["timer_seconds"] != nil {
		t.Fatal(data, ingredients, steps, err)
	}
	nullable := good()
	nullable.Data["notes"] = json.RawMessage("null")
	nullable.Ingredients[0].Data["section_name"] = json.RawMessage("null")
	nullable.Ingredients[0].Data["quantity"] = json.RawMessage("null")
	data, ingredients, _, err = s.normalizeRecipe(nullable)
	if err != nil || data["notes"] != "" || ingredients[0]["section_name"] != "" || ingredients[0]["quantity"] != nil {
		t.Fatal("explicit null defaults lost field semantics", data, ingredients, err)
	}
	for _, tc := range []struct{ name, raw string }{{"missing unit", ""}, {"empty unit", `""`}, {"null unit", "null"}} {
		t.Run(tc.name, func(t *testing.T) {
			in := good()
			if tc.raw != "" {
				in.Ingredients[0].Data["unit_code"] = json.RawMessage(tc.raw)
			}
			_, rows, _, err := s.normalizeRecipe(in)
			if err != nil || rows[0]["unit_code"] != nil {
				t.Fatal("unknown unit must stay unknown", rows, err)
			}
		})
	}
	for _, tc := range []struct {
		name, path string
		change     func(*RecipeInput)
	}{
		{"required", "recipe.title", func(in *RecipeInput) { delete(in.Data, "title") }},
		{"long title", "recipe.title", func(in *RecipeInput) { in.Data["title"] = json.RawMessage(`"` + strings.Repeat("ü", 201) + `"`) }},
		{"integer", "recipe.prep_minutes", func(in *RecipeInput) { in.Data["prep_minutes"] = json.RawMessage(`"-1"`) }},
		{"unknown", "recipe.owner", func(in *RecipeInput) { in.Data["owner"] = json.RawMessage(`"x"`) }},
		{"precision", "ingredients.ROW.quantity", func(in *RecipeInput) { in.Ingredients[0].Data["quantity"] = json.RawMessage(`"0.0000001"`) }},
		{"unit", "ingredients.ROW.unit_code", func(in *RecipeInput) { in.Ingredients[0].Data["unit_code"] = json.RawMessage(`"mystery"`) }},
		{"name", "ingredients.ROW.name", func(in *RecipeInput) {
			in.Ingredients[0].Data["name"] = json.RawMessage(`"` + strings.Repeat("x", 201) + `"`)
		}},
		{"parent", "ingredients.ROW", func(in *RecipeInput) { in.Ingredients[0].Data["recipe_id"] = json.RawMessage(`"` + value.UUID() + `"`) }},
		{"position", "ingredients.ROW", func(in *RecipeInput) { in.Ingredients[0].Data["position"] = json.RawMessage(`1`) }},
		{"identity", "ingredients.ROW", func(in *RecipeInput) { in.Ingredients[0].ID = "foreign" }},
		{"empty step", "steps.STEP.instruction", func(in *RecipeInput) { in.Steps[0].Data["instruction"] = json.RawMessage(`" "`) }},
		{"duplicate key", "ingredients.ROW", func(in *RecipeInput) { in.Ingredients[1].Key = in.Ingredients[0].Key }},
		{"tags", "tags", func(in *RecipeInput) { in.TagIDs = []string{"bad"} }},
		{"duplicate tags", "tags", func(in *RecipeInput) { id := value.UUID(); in.TagIDs = []string{id, id} }},
		{"bound", "recipe.title", func(in *RecipeInput) { in.Ingredients = make([]RecipeRow, 501) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := good()
			path := strings.ReplaceAll(strings.ReplaceAll(tc.path, "ROW", in.Ingredients[0].Key), "STEP", in.Steps[0].Key)
			tc.change(&in)
			_, _, _, err := s.normalizeRecipe(in)
			var fields RecipeErrors
			if !errors.As(err, &fields) || fields[path] == "" {
				t.Fatalf("missing named error %s: %v", path, err)
			}
		})
	}
}

func TestRecipeBundlePrecisionFingerprintAndLimits(t *testing.T) {
	raw := []byte(`{"recipe":{"id":"source","base_servings":"9007199254.740993","version":9007199254740993},"ingredients":[],"steps":[],"tags":[]}`)
	bundle, err := recipeBundle(raw)
	if err != nil || String(bundle.Recipe["version"]) != "9007199254740993" || len(bundle.Fingerprint) != 64 {
		t.Fatal(bundle, err)
	}
	again, err := recipeBundle(raw)
	if err != nil || again.Fingerprint != bundle.Fingerprint {
		t.Fatal("unstable fingerprint")
	}
	changed, err := recipeBundle([]byte(strings.Replace(string(raw), "740993", "740994", 1)))
	if err != nil || changed.Fingerprint == bundle.Fingerprint {
		t.Fatal("changed exact amount not detected")
	}
	for _, raw := range [][]byte{nil, []byte("null"), []byte("{broken")} {
		if _, err := recipeBundle(raw); err == nil {
			t.Fatal("invalid bundle accepted")
		}
	}
	bundle.Ingredients = make([]Record, 501)
	raw, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recipeBundle(raw); err == nil {
		t.Fatal("oversized bundle accepted")
	}
}
