package web

import (
	"encoding/json"
	"net/url"
	"testing"

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
