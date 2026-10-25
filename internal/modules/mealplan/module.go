package mealplan

import m "household/internal/module"

var Module = m.Descriptor{ID: "mealplan", Title: "Mahlzeitenplanung", Short: "Mahlzeiten", Icon: "calendar-days", Order: 90, Entities: []m.Entity{
	{ID: "plans", Title: "Pläne", Primary: "name", Fields: []m.Field{m.Text("name", "Plan", true), m.Date("starts_on", "Beginn", true), m.Date("ends_on", "Ende", true), m.Bool("is_template", "Als Wochenvorlage führen")}},
	{ID: "meals", Title: "Mahlzeiten", Primary: "title", Fields: []m.Field{m.Ref("plan_id", "Plan", "mealplan/plans", true), m.Date("planned_on", "Tag", true), m.Select("meal_slot", "Mahlzeit", "dinner", "lunch", "breakfast", "snack"), m.Select("kind", "Planungsart", "free", "recipe", "leftovers", "out", "order"), m.Select("status", "Planungsstatus", "planned", "open", "purchased", "cooked", "cancelled"), m.Text("title", "Gericht", true), m.Qty("servings", "Portionen", true, true), m.Long("notes", "Notizen")}},
}}
