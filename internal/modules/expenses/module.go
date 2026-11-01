package expenses

import m "household/internal/module"

var Module = m.Descriptor{ID: "expenses", Title: "Finanzen", Short: "Finanzen", Icon: "wallet", Order: 50, DefaultEnabled: true, Entities: []m.Entity{
	{ID: "entries", Title: "Ausgaben", Primary: "description", Fields: []m.Field{m.Date("occurred_on", "Datum", true), m.Text("description", "Beschreibung", true), m.Text("payee", "Empfänger / Geschäft", false), m.Money("amount_minor", "Betrag in € · Erstattung negativ", false), m.Select("kind", "Art", "expense", "refund"), m.Ref("paid_by_membership_id", "Bezahlt von", "core/memberships", false)}},
	{ID: "categories", Title: "Kategorien", Primary: "name", Fields: []m.Field{m.Text("name", "Kategorie", true), m.Int("sort_order", "Reihenfolge")}},
	{ID: "entry_lines", Title: "Aufteilungen", Primary: "note", Fields: []m.Field{m.Ref("entry_id", "Ausgabe", "expenses/entries", true), m.Ref("category_id", "Kategorie", "expenses/categories", true), m.Money("amount_minor", "Teilbetrag in €", false), m.Text("note", "Bezeichnung", false), m.Int("position", "Reihenfolge")}},
}}
