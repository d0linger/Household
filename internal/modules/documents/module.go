package documents

import m "household/internal/module"

var Module = m.Descriptor{ID: "documents", Title: "Dokumente & Garantien", Short: "Dokumente", Icon: "files", Order: 80, Entities: []m.Entity{
	{ID: "documents", Title: "Dokumente", Primary: "title", Fields: []m.Field{m.Text("title", "Dokument", true), m.Text("category", "Kategorie", false), m.Text("issuer", "Aussteller", false), m.Date("valid_from", "Gültig ab", false), m.Date("valid_until", "Gültig bis", false), m.Date("warranty_until", "Garantie bis", false), m.Int("remind_days", "Erinnerungstage vor Ablauf"), m.Long("notes", "Notizen")}},
}}
