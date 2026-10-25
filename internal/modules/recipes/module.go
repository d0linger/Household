package recipes

import m "household/internal/module"

var Module = m.Descriptor{ID: "recipes", Title: "Kochbuch", Short: "Kochbuch", Icon: "book-open", Order: 30, DefaultEnabled: true, Entities: []m.Entity{
	{ID: "recipes", Title: "Rezepte", Primary: "title", Fields: []m.Field{m.Text("title", "Rezeptname", true), m.Long("summary", "Kurzbeschreibung"), m.Qty("base_servings", "Portionen", true, true), m.Int("prep_minutes", "Vorbereitung in Minuten"), m.Int("cook_minutes", "Kochzeit in Minuten"), m.Text("source_note", "Quelle", false), m.Long("notes", "Notizen")}},
	{ID: "ingredient_catalog", Title: "Zutatenkatalog", Primary: "name", Fields: []m.Field{m.Text("name", "Katalogzutat", true), m.Bool("archived", "Katalogzutat nicht mehr verwenden"), m.Long("notes", "Notizen")}},
	{ID: "ingredient_aliases", Title: "Zutatenaliase", Primary: "name", Fields: []m.Field{m.Ref("catalog_id", "Katalogzutat", "recipes/ingredient_catalog", true), m.Text("name", "Weitere Schreibweise", true)}},
	{ID: "ingredients", Title: "Zutaten", Primary: "name", Fields: []m.Field{m.Ref("recipe_id", "Rezept", "recipes/recipes", true), m.Int("position", "Reihenfolge"), m.Text("section_name", "Abschnitt", false), m.Text("name", "Zutat", true), m.Ref("catalog_id", "Ausdrückliche Katalogzuordnung · optional", "recipes/ingredient_catalog", false), m.Qty("quantity", "Menge", false, true), {Name: "unit_code", Label: "Einheit", Kind: "select", Options: []string{"piece", "g", "kg", "ml", "l"}}, m.Text("quantity_note", "Mengenhinweis", false), m.Text("preparation_note", "Vorbereitung", false)}},
	{ID: "steps", Title: "Zubereitung", Primary: "instruction", Fields: []m.Field{m.Ref("recipe_id", "Rezept", "recipes/recipes", true), m.Int("position", "Schritt"), m.Long("instruction", "Anleitung"), m.Int("timer_seconds", "Timer in Sekunden")}},
	{ID: "tags", Title: "Tags", Primary: "name", Fields: []m.Field{m.Text("name", "Tag", true)}},
}}
