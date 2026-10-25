package store

import (
	"strings"
	"testing"
)

func TestCookingFinishValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CookingInput)
		valid  bool
	}{
		{"optional-rating", func(*CookingInput) {}, true},
		{"comma-and-trim", func(in *CookingInput) { in.ActualServings = "2,500001"; in.Notes = "  Note  " }, true},
		{"zero-servings", func(in *CookingInput) { in.ActualServings = "0" }, false},
		{"excess-precision", func(in *CookingInput) { in.ActualServings = "0.0000001" }, false},
		{"future-date", func(in *CookingInput) { in.CookedOn = "2026-10-26" }, false},
		{"invalid-date", func(in *CookingInput) { in.CookedOn = "2026-02-30" }, false},
		{"empty-date", func(in *CookingInput) { in.CookedOn = "" }, false},
		{"zero-rating", func(in *CookingInput) { v := 0; in.Rating = &v }, false},
		{"over-rating", func(in *CookingInput) { v := 6; in.Rating = &v }, false},
		{"five-rating", func(in *CookingInput) { v := 5; in.Rating = &v }, true},
		{"notes-byte-limit", func(in *CookingInput) { in.Notes = strings.Repeat("ü", 10001) }, false},
		{"notes-bound", func(in *CookingInput) { in.Notes = strings.Repeat("ü", 10000) }, true},
		{"null-note", func(in *CookingInput) { in.Notes = "a\x00b" }, false},
		{"invalid-utf8", func(in *CookingInput) { in.Notes = string([]byte{0xff}) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := CookingInput{ActualServings: "2.500001", CookedOn: "2026-10-25", Notes: "Note"}
			tc.change(&in)
			err := normalizeCookingFinish(&in, "2026-10-25")
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if tc.valid && (in.ActualServings != "2.500001" || in.Notes != strings.TrimSpace(in.Notes)) {
				t.Fatal("canonical finish", in)
			}
		})
	}
}

func TestCookingProgressSnapshotBoundary(t *testing.T) {
	s := CookingSession{Source: RecipeBundle{Ingredients: []Record{{"id": "a"}, {"id": "b"}}, Steps: []Record{{"instruction": "One"}, {"instruction": "Two"}}}}
	for _, tc := range []struct {
		name    string
		step    int
		checked []string
		valid   bool
	}{
		{"initial", 0, nil, true}, {"last-step", 1, []string{"b", "a"}, true}, {"negative-step", -1, nil, false}, {"beyond-step", 2, nil, false}, {"foreign", 0, []string{"c"}, false}, {"duplicate", 0, []string{"a", "a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCookingProgress(s, CookingInput{CurrentStep: tc.step, CheckedIDs: tc.checked})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
