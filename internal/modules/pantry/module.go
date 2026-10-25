package pantry

import m "household/internal/module"

var Module = m.Descriptor{ID: "pantry", Title: "Vorräte", Short: "Vorrat", Icon: "package", Order: 40, DefaultEnabled: true, Entities: []m.Entity{
	{ID: "products", Title: "Produkte", Primary: "name", Fields: []m.Field{m.Text("name", "Produkt", true), m.Select("unit_code", "Basiseinheit", "piece", "g", "kg", "ml", "l"), m.Qty("low_stock_threshold", "Mindestbestand", false, false), m.Bool("integer_quantities", "Nur ganze Stückzahlen"), m.Long("notes", "Notizen")}},
	{ID: "packs", Title: "Packungen", Primary: "label", Fields: []m.Field{m.Ref("product_id", "Produkt", "pantry/products", true), m.Text("label", "Packungsbezeichnung", true), m.Qty("quantity", "Inhalt pro Packung", true, true), m.Select("unit_code", "Inhaltseinheit", "piece", "g", "kg", "ml", "l"), m.Bool("disabled", "Packung nicht mehr verwenden")}},
	{ID: "barcodes", Title: "Barcodes", Primary: "code", Fields: []m.Field{m.Ref("product_id", "Produkt", "pantry/products", true), m.Ref("pack_id", "Packung · optional, vom selben Produkt", "pantry/packs", false), m.Text("code", "Barcode · führende Nullen beibehalten", true)}},
	{ID: "locations", Title: "Lagerorte", Primary: "name", Fields: []m.Field{m.Text("name", "Lagerort", true), m.Int("sort_order", "Reihenfolge")}},
	{ID: "lots", Title: "Chargen", Primary: "notes", Fields: []m.Field{m.Ref("product_id", "Produkt", "pantry/products", true), m.Ref("location_id", "Lagerort", "pantry/locations", true), m.Date("received_on", "Erhalten am", true), m.Date("best_before_on", "Mindestens haltbar bis", false), m.Date("use_by_on", "Zu verbrauchen bis", false), m.Date("opened_on", "Geöffnet am", false), {Name: "after_open_days", Label: "Hinweis nach Öffnung · Tage, leer = unbekannt", Kind: "integer"}, {Name: "after_open_source", Label: "Herkunft des Öffnungshinweises", Kind: "select", Options: []string{"", "user", "manufacturer"}}, m.Text("after_open_note", "Quelle des Öffnungshinweises", false), m.Text("notes", "Chargenbezeichnung", true)}},
	{ID: "stock_movements", Title: "Bestandsbewegungen", Primary: "reason", Immutable: true, Fields: []m.Field{m.Ref("lot_id", "Charge", "pantry/lots", true), m.Qty("quantity_delta", "Bestandsänderung", true, false), m.Select("reason", "Grund", "receive", "consume", "waste", "adjust"), m.Long("note", "Notiz")}},
}}
