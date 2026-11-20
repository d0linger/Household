package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"household/internal/store"
)

func TestRecipeFactsShowOnlyRecordedTimes(t *testing.T) {
	tmpl := serviceWorkerTestServer(t).templates.Funcs(template.FuncMap{"integer": store.Int, "qty": formatQuantity})
	for _, tc := range []struct {
		name, prep, cook   string
		showPrep, showCook bool
	}{
		{"default times", "0", "0", false, false},
		{"preparation only", "10", "0", true, false},
		{"cooking only", "0", "30", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := Page{Record: store.Record{"id": "test", "title": "Curry", "base_servings": "2", "prep_minutes": tc.prep, "cook_minutes": tc.cook}}
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "recipe-workspace", page); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			if strings.Contains(html, "<li>Vorbereitung · ") != tc.showPrep || strings.Contains(html, "<li>Kochzeit · ") != tc.showCook || !strings.Contains(html, "<li>2 Portionen</li>") {
				t.Error("recipe facts differ from the recorded preparation, cooking time or servings")
			}
		})
	}
}
