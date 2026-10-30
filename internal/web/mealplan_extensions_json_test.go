package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/store"
)

func TestPlanningClosedJSONPreservesNullAndRejectsAmbiguity(t *testing.T) {
	type manual struct {
		Version int64  `json:"meal_version"`
		Minor   string `json:"minor"`
	}
	type input struct {
		store.PlanningExtensionCommand
		Budget *string           `json:"budget_minor"`
		Manual map[string]manual `json:"manual_estimates"`
	}
	valid := `{"generation":1,"plan_version":2,"week":"2026-10-30","budget_minor":null,"manual_estimates":{"meal":{"meal_version":3,"minor":"123"}}}`
	for _, tc := range []struct {
		name, body, media string
		valid             bool
	}{
		{name: "embedded scope and nullable target", body: valid, valid: true},
		{name: "duplicate", body: strings.Replace(valid, `"generation":1`, `"generation":1,"generation":2`, 1)},
		{name: "escaped duplicate", body: strings.Replace(valid, `"generation":1`, `"generation":1,"\u0067eneration":2`, 1)},
		{name: "wrong case", body: strings.Replace(valid, `"generation"`, `"Generation"`, 1)},
		{name: "unknown nested", body: strings.Replace(valid, `"minor":"123"`, `"minor":"123","hidden":true`, 1)},
		{name: "duplicate nested", body: strings.Replace(valid, `"minor":"123"`, `"minor":"123","minor":"999"`, 1)},
		{name: "missing nested", body: strings.Replace(valid, `"meal_version":3,`, ``, 1)},
		{name: "null exact amount", body: strings.Replace(valid, `"minor":"123"`, `"minor":null`, 1)},
		{name: "null map", body: strings.Replace(valid, `{"meal":{"meal_version":3,"minor":"123"}}`, `null`, 1)},
		{name: "wrong number type", body: strings.Replace(valid, `"generation":1`, `"generation":"1"`, 1)},
		{name: "missing scope", body: strings.Replace(valid, `"generation":1,`, ``, 1)},
		{name: "trailing", body: valid + `{}`},
		{name: "null root", body: `null`},
		{name: "wrong media", body: valid, media: "text/plain"},
		{name: "too large", body: strings.Replace(valid, `"123"`, `"`+strings.Repeat("1", 1<<20)+`"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			media := tc.media
			if media == "" {
				media = "application/json"
			}
			r.Header.Set("Content-Type", media)
			var result input
			err := readPlanningExtensionJSON(httptest.NewRecorder(), r, &result, "budget_minor", "manual_estimates")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid && (result.Budget != nil || result.Manual["meal"].Minor != "123" || result.Generation != 1) {
				t.Fatal("nullable budget, exact amount or embedded scope changed")
			}
		})
	}
}

func TestPlanningExtensionWeekRejectsAmbiguousQueries(t *testing.T) {
	for _, raw := range []string{"", "week=2026-02-30", "week=2026-10-30&week=2026-10-30", "week=2026-10-30&extra=1", "week=%zz"} {
		if _, err := planningExtensionWeek(httptest.NewRequest("GET", "/?"+raw, nil)); err == nil {
			t.Fatal("ambiguous week accepted", raw)
		}
	}
	if week, err := planningExtensionWeek(httptest.NewRequest("GET", "/?week=2026-10-30", nil)); err != nil || week != "2026-10-30" {
		t.Fatal("valid week rejected", week, err)
	}
}
