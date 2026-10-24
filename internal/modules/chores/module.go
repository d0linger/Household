package chores

import m "household/internal/module"

var Module = m.Descriptor{ID: "chores", Title: "Aufgaben", Short: "Aufgaben", Icon: "check-check", Order: 60, Entities: []m.Entity{
	{ID: "task_templates", Title: "Aufgabenvorlagen", Primary: "title", Fields: []m.Field{m.Text("title", "Aufgabe", true), m.Long("description", "Beschreibung"), m.Ref("assignee_id", "Verantwortlich", "core/memberships", false), m.Int("repeat_days", "Wiederholung in Tagen · 0 = einmalig"), m.Bool("active", "Aktiv")}},
	{ID: "task_occurrences", Title: "Aufgaben", Primary: "occurrence_key", Fields: []m.Field{m.Ref("template_id", "Vorlage", "chores/task_templates", true), m.Text("occurrence_key", "Bezeichnung / Termin-ID", true), m.Time("due_at", "Fällig am", true), m.Ref("assignee_id", "Verantwortlich", "core/memberships", false)}},
}}
