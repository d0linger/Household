package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestPantryBatchRecoveryPrecedesSupportingCopy(t *testing.T) {
	s := serviceWorkerTestServer(t)
	for _, locale := range []string{"de", "en"} {
		t.Run(locale, func(t *testing.T) {
			tmpl := s.templates
			if locale == "en" {
				tmpl = s.templatesEN
			}
			var output bytes.Buffer
			if err := tmpl.ExecuteTemplate(&output, "pantry-batches", Page{Locale: locale}); err != nil {
				t.Fatal(err)
			}
			previous := -1
			for _, id := range []string{"pb-error", "pb-retry", "pb-introduction", "pb-status", "pb-reload"} {
				marker := `id="` + id + `"`
				position := strings.Index(output.String(), marker)
				if position <= previous || strings.Count(output.String(), marker) != 1 {
					t.Fatalf("recovery alert and retry must precede guidance/status/reload: %s", id)
				}
				previous = position
			}
		})
	}
}
