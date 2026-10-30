package recipeimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var isoMinutes = regexp.MustCompile(`^PT(?:(\d{1,3})H)?(?:(\d{1,4})M)?$`)

func decodeProfile(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes || jsonDepth(raw) > 32 {
		return nil, errors.New("profil ist leer, zu groß oder zu tief verschachtelt")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("Profil-JSON ist ungültig")
	}
	return parsed, nil
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func field(m map[string]any, names ...string) string {
	for _, name := range names {
		if value := text(m[name]); value != "" {
			return value
		}
	}
	return ""
}

func array(v any) []any {
	rows, _ := v.([]any)
	return rows
}

func durationMinutes(raw any) (int, bool) {
	value := text(raw)
	if value == "" {
		return 0, true
	}
	if strings.HasPrefix(value, "PT") {
		parts := isoMinutes.FindStringSubmatch(value)
		if parts == nil || parts[1] == "" && parts[2] == "" {
			return 0, false
		}
		hours, _ := strconv.Atoi(parts[1])
		minutes, _ := strconv.Atoi(parts[2])
		total := hours*60 + minutes
		return total, total <= 10080
	}
	minutes, err := strconv.Atoi(value)
	return minutes, err == nil && minutes >= 0 && minutes <= 10080
}

func servings(v any) string {
	if options := array(v); len(options) > 0 {
		return servings(options[0])
	}
	s := strings.TrimSpace(text(v))
	if s == "" {
		return "1"
	}
	for _, suffix := range []string{" servings", " serving", " Portionen", " Portion"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.TrimSpace(s)
}

func splitLines(raw string) []string {
	parts := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if clean := strings.TrimSpace(part); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

func ingredientText(v any) Ingredient {
	if str := text(v); str != "" {
		return Ingredient{Name: str, QuantityNote: "Menge aus Freitext prüfen"}
	}
	m := object(v)
	if len(m) == 0 {
		return Ingredient{}
	}
	name := field(m, "name", "display")
	if name == "" {
		name = field(object(m["food"]), "name", "pluralName")
	}
	unit := field(m, "unitCode", "unit")
	if nested := object(m["unit"]); len(nested) > 0 {
		unit = field(nested, "name", "abbreviation")
	}
	item := Ingredient{Name: name, Quantity: field(m, "value", "quantity", "amount"), Unit: normalizeUnit(unit)}
	if item.Unit == "" && unit != "" {
		item.QuantityNote = "Einheit aus Quelle prüfen: " + unit
		item.Quantity = ""
	}
	if note := field(m, "note", "originalText"); note != "" {
		item.QuantityNote = note
	}
	return item
}

func normalizeUnit(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "piece", "pieces", "stück", "stueck":
		return "piece"
	case "g", "gram", "grams", "gramm":
		return "g"
	case "kg", "kilogram", "kilogramm":
		return "kg"
	case "ml", "milliliter", "millilitre":
		return "ml"
	case "l", "liter", "litre":
		return "l"
	case "tsp", "teaspoon", "tl":
		return "tsp"
	case "tbsp", "tablespoon", "el":
		return "tbsp"
	default:
		return ""
	}
}

func recipeSteps(raw any) []Step {
	if str := text(raw); str != "" {
		parts := splitLines(str)
		out := make([]Step, 0, len(parts))
		for _, part := range parts {
			out = append(out, Step{Instruction: part})
		}
		return out
	}
	out := []Step{}
	for _, row := range array(raw) {
		if str := text(row); str != "" {
			out = append(out, Step{Instruction: str})
			continue
		}
		m := object(row)
		if list := array(m["itemListElement"]); len(list) > 0 {
			out = append(out, recipeSteps(list)...)
			continue
		}
		if instruction := field(m, "text", "instruction", "name"); instruction != "" {
			out = append(out, Step{Instruction: instruction})
		}
	}
	return out
}

func recipeIngredients(raw any) []Ingredient {
	if str := text(raw); str != "" {
		parts := splitLines(str)
		out := make([]Ingredient, 0, len(parts))
		for _, part := range parts {
			out = append(out, ingredientText(part))
		}
		return out
	}
	rows := array(raw)
	out := make([]Ingredient, 0, len(rows))
	for _, row := range rows {
		out = append(out, ingredientText(row))
	}
	return out
}

func tags(raw any) []string {
	if str := text(raw); str != "" {
		return strings.Split(str, ",")
	}
	out := []string{}
	for _, entry := range array(raw) {
		if name := text(entry); name != "" {
			out = append(out, name)
		} else if name := field(object(entry), "name"); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func schemaTypeRecipe(v any) bool {
	if str := text(v); str != "" {
		return str == "Recipe" || strings.HasSuffix(str, "/Recipe")
	}
	for _, entry := range array(v) {
		if schemaTypeRecipe(entry) {
			return true
		}
	}
	return false
}

func collectSchemaRecipes(v any, depth int, out *[]map[string]any) {
	if depth > 32 || len(*out) > MaxRecipes {
		return
	}
	if rows := array(v); rows != nil {
		for _, row := range rows {
			collectSchemaRecipes(row, depth+1, out)
		}
		return
	}
	m := object(v)
	if len(m) == 0 {
		return
	}
	if schemaTypeRecipe(m["@type"]) {
		*out = append(*out, m)
	}
	if graph, ok := m["@graph"]; ok {
		collectSchemaRecipes(graph, depth+1, out)
	}
}

func schemaRecipe(m map[string]any) (Recipe, error) {
	item := Recipe{Title: field(m, "name", "headline"), Summary: field(m, "description"),
		Servings: servings(m["recipeYield"]), SourceURL: field(m, "url"),
		Ingredients: recipeIngredients(m["recipeIngredient"]), Steps: recipeSteps(m["recipeInstructions"]),
		Tags: tags(m["keywords"]), SourceFormat: "schemaorg-recipe"}
	var ok bool
	item.PrepMinutes, ok = durationMinutes(m["prepTime"])
	if !ok {
		item.Losses = append(item.Losses, "Vorbereitungsdauer nicht exakt als Minuten übernommen")
	}
	item.CookMinutes, ok = durationMinutes(m["cookTime"])
	if !ok {
		item.Losses = append(item.Losses, "Kochdauer nicht exakt als Minuten übernommen")
	}
	if len(item.Ingredients) > 0 {
		item.Losses = append(item.Losses, "Freitext-Zutatenmengen vor dem Speichern prüfen")
	}
	return normalizeExternalRecipe(item)
}

func normalizeExternalRecipe(item Recipe) (Recipe, error) {
	if item.SourceURL != "" {
		if _, err := CanonicalURL(item.SourceURL); err != nil {
			item.Losses = append(item.Losses, "Nicht-HTTPS- oder ungültige Quelladresse wurde nicht übernommen")
			item.SourceURL = ""
		}
	}
	return Normalize(item)
}

func ParseSchemaJSONLD(raw []byte) ([]Recipe, error) {
	parsed, err := decodeProfile(raw)
	if err != nil {
		return nil, err
	}
	var candidates []map[string]any
	collectSchemaRecipes(parsed, 0, &candidates)
	if len(candidates) == 0 || len(candidates) > MaxRecipes {
		return nil, errors.New("keine unterstützte oder zu viele Schema.org-Rezepte")
	}
	out := make([]Recipe, 0, len(candidates))
	for i, candidate := range candidates {
		item, parseErr := schemaRecipe(candidate)
		if parseErr != nil {
			return nil, fmt.Errorf("Schema.org-Rezept %d: %w", i+1, parseErr)
		}
		out = append(out, item)
	}
	return out, nil
}

func ParseMealie(raw []byte) ([]Recipe, error) {
	parsed, err := decodeProfile(raw)
	if err != nil {
		return nil, err
	}
	m := object(parsed)
	if m == nil || field(m, "name") == "" {
		return nil, errors.New("Mealie-Einzelrezept wird nicht erkannt")
	}
	item := Recipe{Title: field(m, "name"), Summary: field(m, "description"),
		Servings: servings(m["recipeServings"]), SourceURL: field(m, "orgURL", "url"),
		Ingredients: recipeIngredients(m["recipeIngredient"]), Steps: recipeSteps(m["recipeInstructions"]),
		Tags: tags(m["tags"]), SourceFormat: "mealie-single-json"}
	item.PrepMinutes, _ = durationMinutes(m["prepTime"])
	item.CookMinutes, _ = durationMinutes(m["cookTime"])
	item.Losses = []string{"Mealie-IDs, Bilder, Werkzeuge und nicht unterstützte Profilfelder werden nicht übernommen"}
	clean, err := normalizeExternalRecipe(item)
	if err != nil {
		return nil, err
	}
	return []Recipe{clean}, nil
}

func ParseTandoorJSON(raw []byte) ([]Recipe, error) {
	parsed, err := decodeProfile(raw)
	if err != nil {
		return nil, err
	}
	m := object(parsed)
	if m == nil || field(m, "name") == "" {
		return nil, errors.New("Tandoor-Rezeptdatei wird nicht erkannt")
	}
	item := Recipe{Title: field(m, "name"), Summary: field(m, "description"),
		Servings: servings(m["servings"]), SourceURL: field(m, "source_url", "sourceUrl"),
		Ingredients: recipeIngredients(m["ingredients"]), Steps: recipeSteps(m["steps"]),
		Tags: tags(m["keywords"]), SourceFormat: "tandoor-selected-json"}
	item.PrepMinutes, _ = durationMinutes(m["working_time"])
	item.CookMinutes, _ = durationMinutes(m["waiting_time"])
	item.Losses = []string{"Tandoor-IDs, Bilder und nicht unterstützte Organisationsfelder werden nicht übernommen"}
	clean, err := normalizeExternalRecipe(item)
	if err != nil {
		return nil, err
	}
	return []Recipe{clean}, nil
}

func ParsePaprikaJSON(raw []byte) ([]Recipe, error) {
	parsed, err := decodeProfile(raw)
	if err != nil {
		return nil, err
	}
	m := object(parsed)
	if m == nil || field(m, "name") == "" {
		return nil, errors.New("Paprika-Rezeptdatei wird nicht erkannt")
	}
	item := Recipe{Title: field(m, "name"), Summary: field(m, "description"),
		Servings: servings(m["servings"]), SourceURL: field(m, "source_url"), SourceNote: field(m, "source"),
		Notes: field(m, "notes"), Ingredients: recipeIngredients(m["ingredients"]),
		Steps: recipeSteps(m["directions"]), Tags: tags(m["categories"]), SourceFormat: "paprika-selected-json"}
	item.PrepMinutes, _ = durationMinutes(m["prep_time"])
	item.CookMinutes, _ = durationMinutes(m["cook_time"])
	item.Losses = []string{"Paprika-Bilder, interne IDs und nicht unterstützte Profilfelder werden nicht übernommen"}
	clean, err := normalizeExternalRecipe(item)
	if err != nil {
		return nil, err
	}
	return []Recipe{clean}, nil
}
