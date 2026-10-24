package shopping

import m "household/internal/module"

var Module = m.Descriptor{ID: "shopping", Title: "Einkaufsliste", Short: "Einkauf", Icon: "shopping-cart", Order: 10, DefaultEnabled: true, Entities: []m.Entity{
	{ID: "lists", Title: "Listen", Primary: "name", Fields: []m.Field{m.Text("name", "Listenname", true), m.Long("description", "Beschreibung"), m.Ref("store_id", "Geschäft", "shopping/stores", false), m.Int("sort_order", "Reihenfolge")}},
	{ID: "items", Title: "Artikel", Primary: "name", Fields: []m.Field{m.Ref("list_id", "Liste", "shopping/lists", true), m.Text("name", "Artikel", true), m.Ref("product_id", "Produktvariante", "shopping/products", false), m.Qty("quantity", "Menge · Stück zählt Produktpackungen", false, true), m.Select("unit_code", "Einheit", "piece", "g", "kg", "ml", "l"), m.Text("group_name", "Gruppe", false), m.Select("availability", "Verfügbarkeit", "available", "unavailable", "substituted"), m.Ref("replacement_product_id", "Ersatzprodukt · nur bei Ersatz", "shopping/products", false), m.Ref("assignee_id", "Zuständig", "core/memberships", false), m.Long("note", "Notiz"), m.Int("sort_order", "Reihenfolge")}},
	{ID: "stores", Title: "Geschäfte", Primary: "name", Fields: []m.Field{m.Text("name", "Geschäft", true), m.Long("note", "Notiz")}},
	{ID: "aisles", Title: "Gangreihenfolge", Primary: "name", Fields: []m.Field{m.Ref("store_id", "Geschäft", "shopping/stores", true), m.Text("name", "Artikelgruppe", true), m.Int("sort_order", "Gangreihenfolge")}},
	{ID: "products", Title: "Produkte", Primary: "name", Fields: []m.Field{m.Text("name", "Produkt", true), m.Text("brand", "Marke", false), {Name: "pack_quantity", Label: "Menge pro Packung", Kind: "quantity", Positive: true, Required: true, Default: "1"}, m.Select("pack_unit", "Packungsinhalt · Einheit", "piece", "g", "kg", "ml", "l"), m.Long("note", "Notiz")}},
	{ID: "price_observations", Title: "Preise", Primary: "observed_on", Fields: []m.Field{m.Ref("product_id", "Produktvariante", "shopping/products", true), m.Ref("store_id", "Geschäft", "shopping/stores", true), m.Date("observed_on", "Preis beobachtet am", true), m.Qty("quantity", "Bezugsmenge · Stück zählt Produktpackungen", true, true), m.Select("unit_code", "Einheit", "piece", "g", "kg", "ml", "l"), m.Money("price_minor", "Preis für die Bezugsmenge · EUR", true), m.Long("note", "Notiz")}},
}}
