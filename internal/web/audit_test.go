package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/module"
	"household/internal/store"
)

func TestAuditValue(t *testing.T) {
	for _, tc := range []struct {
		name              string
		value             any
		present, redacted bool
		kind, want        string
	}{
		{"missing", nil, false, false, "", "Nicht vorhanden"},
		{"null", nil, true, false, "", "Leer"},
		{"redacted", "secret sentinel", true, true, "", "Geschützt"},
		{"exact large integer", json.Number("9007199254740993"), true, false, "", "9007199254740993"},
		{"exact minor amount", json.Number("1000000"), true, false, "money", "10000,00 €"},
		{"false", false, true, false, "boolean", "Nein"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := auditValue(tc.value, tc.present, tc.redacted, tc.kind); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuditFilters(t *testing.T) {
	s := &Server{Store: store.New(nil, []module.Descriptor{{ID: "shopping"}})}
	for _, tc := range []struct {
		name, query string
		valid       bool
	}{
		{"default", "", true}, {"cursor", "module=shopping&action=UPDATE&before=42", true},
		{"unknown module", "module=unknown", false}, {"negative cursor", "before=-1", false},
		{"overflow", "before=999999999999999999999", false}, {"invalid resource", "resource=other", false},
		{"invalid request", "request=other", false}, {"invalid action", "action=two+words", false},
		{"literal wildcard search", "q=milk%25_", true}, {"unicode search", "q=" + url.QueryEscape(strings.Repeat("ä", 100)), true},
		{"overlong search", "q=" + strings.Repeat("x", 101), false}, {"control in search", "q=milk%00bread", false},
		{"invalid UTF8 search", "q=%FF", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.parseAuditFilter(q)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestDescribeAudit(t *testing.T) {
	s := &Server{Store: store.New(nil, []module.Descriptor{{ID: "shopping", Title: "Einkaufsliste", Entities: []module.Entity{{ID: "lists", Title: "Listen", Fields: []module.Field{{Name: "name", Label: "Listenname", Kind: "text"}}}}}})}
	const user = "00000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name                   string
		entry                  auditEntry
		actor, username, title string
		titleLabel             bool
	}{
		{name: "current user", entry: auditEntry{ActorKind: "user", ActorID: user, ActorUsername: "alex", Module: "shopping", Entity: "lists", Resource: user, Label: "shopping.lists · 00000000"}, actor: "Ehemaliger oder unbekannter Benutzer", username: "alex", title: "Listen", titleLabel: true},
		{name: "missing former user", entry: auditEntry{ActorKind: "user", ActorID: user, Module: "core", Entity: "sessions", Label: "core.sessions"}, actor: "Ehemaliger oder unbekannter Benutzer", title: "Sitzung", titleLabel: true},
		{name: "worker never claims user", entry: auditEntry{ActorKind: "worker", ActorUsername: "alex", Module: "core", Entity: "jobs"}, actor: "Hintergrunddienst", title: "Hintergrundaufgabe", titleLabel: true},
		{name: "legacy operator target is not actor", entry: auditEntry{ActorKind: "legacy", Action: "operator_password_recovery", ActorID: user, ActorUsername: "alex", Module: "core", Entity: "users"}, actor: "Früheres Betreiberereignis · ausführende Identität nicht erfasst", title: "Benutzerkonto", titleLabel: true},
		{name: "user label stays content", entry: auditEntry{ActorKind: "user", ActorUsername: "alex", Module: "shopping", Entity: "lists", Label: "Sicherheit"}, actor: "Ehemaliger oder unbekannter Benutzer", username: "alex", title: "Sicherheit"},
		{name: "unknown table is technical detail", entry: auditEntry{ActorKind: "database", Module: "shopping", Entity: "future_records", Label: "shopping.future_records"}, actor: "Datenbank", title: "Datensatz", titleLabel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.describeAudit(&tc.entry)
			if tc.entry.ActorLabel != tc.actor || tc.entry.ActorUsername != tc.username || tc.entry.Title != tc.title || tc.entry.TitleLabel != tc.titleLabel {
				t.Fatalf("description: actor=%q username=%q title=%q label=%v", tc.entry.ActorLabel, tc.entry.ActorUsername, tc.entry.Title, tc.entry.TitleLabel)
			}
		})
	}
}

func TestAuditFieldPresentation(t *testing.T) {
	s := &Server{Store: store.New(nil, []module.Descriptor{{ID: "expenses", Title: "Finanzen", Entities: []module.Entity{{ID: "entries", Title: "Ausgaben", Fields: []module.Field{{Name: "amount_minor", Label: "Betrag", Kind: "money"}}}}}})}
	entry := auditEntry{Module: "expenses", Entity: "entries", Changes: map[string]auditChange{
		"amount_minor": {Before: json.Number("9007199254740993"), After: json.Number("9007199254740994"), BeforePresent: true, AfterPresent: true},
		"description":  {Before: "private before", After: "private after", BeforePresent: true, AfterPresent: true, Redacted: true},
		"name":         {Before: nil, After: "Nein", BeforePresent: false, AfterPresent: true},
		"active":       {Before: nil, After: false, BeforePresent: true, AfterPresent: true},
		"user_id":      {After: "00000000-0000-4000-8000-000000000001", AfterPresent: true},
		"version":      {Before: json.Number("1"), After: json.Number("2"), BeforePresent: true, AfterPresent: true},
	}}
	s.describeAudit(&entry)
	if entry.FieldCount != 6 || len(entry.Fields) != 4 || len(entry.TechnicalFields) != 2 {
		t.Fatalf("field separation: %+v", entry)
	}
	fields := map[string]auditFieldView{}
	for _, field := range entry.Fields {
		fields[field.Name] = field
	}
	if fields["amount_minor"].Before != "90071992547409,93 €" || fields["amount_minor"].After != "90071992547409,94 €" {
		t.Fatalf("lost exact amount: %+v", fields["amount_minor"])
	}
	if fields["description"].Before != "Geschützt" || fields["description"].After != "Geschützt" || !fields["description"].BeforeLabel {
		t.Fatal("protected value disclosed")
	}
	if fields["name"].Before != "Nicht vorhanden" || !fields["name"].BeforeLabel || fields["name"].AfterLabel {
		t.Fatal("absence and user text translation boundary lost")
	}
	if fields["active"].Before != "Leer" || fields["active"].After != "Nein" || !fields["active"].AfterLabel {
		t.Fatal("null and boolean distinction lost")
	}
	s.describeAudit(&entry)
	if len(entry.Fields) != 4 || len(entry.TechnicalFields) != 2 {
		t.Fatal("description repeated field rows")
	}
}

func TestAuditTemplateKeepsUserContent(t *testing.T) {
	s := &Server{Store: store.New(nil, nil)}
	entry := auditEntry{ID: 1, CreatedAt: time.Now(), ActorKind: "user", ActorUsername: "Sicherheit", Module: "core", Entity: "users", Action: "UPDATE", Outcome: "success", Label: "Sicherheit", Changes: map[string]auditChange{
		"name":          {Before: "Nein", After: "<img src=x onerror=alert(1)>", BeforePresent: true, AfterPresent: true},
		"password_hash": {After: "private sentinel", AfterPresent: true, Redacted: true},
	}}
	s.describeAudit(&entry)
	tmpl, err := template.New("audit").Funcs(template.FuncMap{"t": func(source string, args ...any) string {
		if translated, ok := map[string]string{"Sicherheit": "Security", "Nein": "No", "Geschützt": "Protected"}[source]; ok {
			source = translated
		}
		if len(args) > 0 {
			return fmt.Sprintf(source, args...)
		}
		return source
	}}).ParseFS(files, "templates/audit.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tmpl.New("settings-nav").Parse(""); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = tmpl.ExecuteTemplate(&output, "audit", Page{Extra: auditResult{Entries: []auditEntry{entry}}}); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	if strings.Contains(body, "Security") || strings.Contains(body, ">No<") || strings.Contains(body, "private sentinel") || strings.Contains(body, "<img src=x") {
		t.Fatal("content translated or unescaped/private value rendered")
	}
	for _, want := range []string{">Sicherheit<", ">Nein<", "Protected", "&lt;img"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
