package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"household/internal/module"
)

func TestLocaleBoundariesAndSafeReturn(t *testing.T) {
	for _, value := range []string{"//evil.test", "https://evil.test", "/\\evil.test", "/%2fexample.test", "settings", "/x%0d%0aLocation:bad", strings.Repeat("a", 2049)} {
		if got := languageReturn(value); got != "/settings" {
			t.Errorf("unsafe return %q accepted: %q", value, got)
		}
	}
	if got := languageReturn("/settings/audit?q=tea&before=123"); got != "/settings/audit?q=tea&before=123" {
		t.Fatal("lost safe filter state", got)
	}
	for _, value := range []string{"en", "de", "fr", "../../en", "EN"} {
		r := httptest.NewRequest(http.MethodGet, "/settings", nil)
		r.AddCookie(&http.Cookie{Name: "household_locale", Value: value})
		want := "de"
		if value == "en" {
			want = "en"
		}
		if got := requestLocale(r); got != want {
			t.Errorf("cookie %q selected %q", value, got)
		}
		r = r.WithContext(context.WithValue(r.Context(), localeKey, "de"))
		if got := requestLocale(r); got != "de" {
			t.Fatal("device cookie overrode account locale")
		}
	}
}

func TestLocalizedMetadataPreservesCanonicalValuesAndConcurrentGerman(t *testing.T) {
	descriptor := module.Descriptor{ID: "chores", Title: "Aufgaben", Short: "Aufgaben", Entities: []module.Entity{{ID: "tasks", Title: "Aufgaben", Fields: []module.Field{{Name: "title", Label: "Name", Kind: "text", Default: "Speichern", Options: []string{"Speichern", "piece"}}}}}}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			for range 20 {
				en := localizedModule("en", descriptor)
				de := localizedModule("de", descriptor)
				if en.Title == descriptor.Title || de.Title != descriptor.Title {
					t.Error("request locale leaked")
				}
				field := en.Entities[0].Fields[0]
				if en.ID != descriptor.ID || en.Entities[0].ID != "tasks" || field.Default != "Speichern" || field.Options[0] != "Speichern" || field.Name != "title" {
					t.Error("canonical or authored value translated")
				}
			}
		})
	}
	group.Wait()
	if descriptor.Title != "Aufgaben" || descriptor.Entities[0].Title != "Aufgaben" {
		t.Fatal("shared descriptors mutated")
	}
}

func TestLocalizedExactPresentation(t *testing.T) {
	for _, pair := range []struct{ in, want string }{{"1.234,56 €", "1,234.56 €"}, {"9007199254740993,01", "9007199254740993.01"}, {"−0,01 €", "−0.01 €"}} {
		if got := localizedNumeric("en", pair.in); got != pair.want {
			t.Errorf("%q → %q", pair.in, got)
		}
		if got := localizedNumeric("de", pair.in); got != pair.in {
			t.Error("German amount changed")
		}
	}
}
