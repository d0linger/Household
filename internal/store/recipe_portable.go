package store

import "household/internal/recipeimport"

// portableRecipeFromSnapshot is an explicit export allowlist. Neither database
// identity nor audit, actor, attachment, version or private provenance crosses
// the Household recipe interchange boundary.
func portableRecipeFromSnapshot(raw []byte) (recipeimport.Recipe, error) {
	bundle, err := recipeBundle(raw)
	if err != nil {
		return recipeimport.Recipe{}, err
	}
	r := bundle.Recipe
	out := recipeimport.Recipe{
		Title: String(r["title"]), Summary: String(r["summary"]),
		Servings: String(r["base_servings"]), PrepMinutes: int(Int(r["prep_minutes"])),
		CookMinutes: int(Int(r["cook_minutes"])), SourceURL: String(r["source_url"]),
		SourceNote: String(r["source_note"]), Notes: String(r["notes"]),
		Ingredients: make([]recipeimport.Ingredient, 0, len(bundle.Ingredients)),
		Steps:       make([]recipeimport.Step, 0, len(bundle.Steps)),
		Tags:        make([]string, 0, len(bundle.Tags)),
	}
	for _, row := range bundle.Ingredients {
		out.Ingredients = append(out.Ingredients, recipeimport.Ingredient{
			Name: String(row["name"]), Section: String(row["section_name"]),
			Quantity: String(row["quantity"]), Unit: String(row["unit_code"]),
			QuantityNote:    String(row["quantity_note"]),
			PreparationNote: String(row["preparation_note"]),
		})
	}
	for _, row := range bundle.Steps {
		out.Steps = append(out.Steps, recipeimport.Step{
			Instruction: String(row["instruction"]), TimerSeconds: int(Int(row["timer_seconds"])),
		})
	}
	for _, row := range bundle.Tags {
		out.Tags = append(out.Tags, String(row["name"]))
	}
	// The database predates the interchange profile. Keep valid older recipes
	// editable even when a value exceeds the deliberately narrower file limit;
	// the explicit export boundary below still validates the portable format.
	return out, nil
}

func portableRecipeJSON(raw []byte) ([]byte, error) {
	recipe, err := portableRecipeFromSnapshot(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := recipeimport.ExportHousehold([]recipeimport.Recipe{recipe})
	if err != nil {
		return nil, Invalid{Message: "Rezept überschreitet das portable Format. Mengen, Zeiten oder Textumfang prüfen."}
	}
	return encoded, nil
}
