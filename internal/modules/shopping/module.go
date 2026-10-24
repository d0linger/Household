package shopping

import m "household/internal/module"

var Module = m.Descriptor{ID: "shopping", Title: "Einkaufsliste", Short: "Einkauf", Icon: "shopping-cart", Order: 10, DefaultEnabled: true, Entities: []m.Entity{
	{ID: "lists", Title: "Listen", Primary: "name", Fields: []m.Field{m.Text("name", "Listenname", true), m.Long("description", "Beschreibung"), m.Int("sort_order", "Reihenfolge")}},
	{ID: "items", Title: "Artikel", Primary: "name", Fields: []m.Field{m.Ref("list_id", "Liste", "shopping/lists", true), m.Text("name", "Artikel", true), m.Qty("quantity", "Menge", false, true), m.Select("unit_code", "Einheit", "piece", "g", "kg", "ml", "l"), m.Text("group_name", "Gruppe", false), m.Long("note", "Notiz"), m.Int("sort_order", "Reihenfolge")}},
}}
