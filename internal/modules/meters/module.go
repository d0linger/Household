package meters

import m "household/internal/module"

var Module = m.Descriptor{ID: "meters", Title: "Zählerstände", Short: "Zähler", Icon: "gauge", Order: 70, Entities: []m.Entity{
	{ID: "meters", Title: "Zähler", Primary: "name", Fields: []m.Field{m.Text("name", "Zähler", true), m.Select("unit_code", "Einheit", "kWh", "m3", "l"), m.Text("location_note", "Standort", false)}},
	{ID: "devices", Title: "Geräte", Primary: "serial_number", Fields: []m.Field{m.Ref("meter_id", "Zähler", "meters/meters", true), m.Text("serial_number", "Gerätenummer", true), m.Date("installed_on", "Eingebaut am", true), m.Date("removed_on", "Ausgebaut am", false), m.Qty("initial_reading", "Anfangsstand", true, false), m.Qty("rollover_value", "Zählerüberlauf bei", false, true)}},
	{ID: "readings", Title: "Ablesungen", Primary: "note", Fields: []m.Field{m.Ref("device_id", "Gerät", "meters/devices", true), m.Time("measured_at", "Abgelesen am", true), m.Qty("value", "Zählerstand", true, false), m.Select("kind", "Art", "actual", "estimated"), m.Text("note", "Notiz", false)}},
}}
