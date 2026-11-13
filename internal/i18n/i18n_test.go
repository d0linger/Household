package i18n

import (
	"bytes"
	"html/template"
	"strings"
	"sync"
	"testing"
)

func TestLanguageSelectionIsBounded(t *testing.T) {
	for _, item := range []struct{ input, want string }{{"de", "de"}, {"de-AT", "de"}, {"en", "en"}, {"en-GB", "en"}, {"en-US", "en"}, {"../../en", "de"}, {"fr", "de"}, {"", "de"}} {
		if got := Normalize(item.input); got != item.want {
			t.Errorf("Normalize(%q)=%q, want %q", item.input, got, item.want)
		}
	}
}

func TestCatalogDoesNotMutateUserContent(t *testing.T) {
	name := "<script>alert('Speichern')</script>"
	if got := Text("en", "%s", name); got != name {
		t.Fatalf("parameter changed: %q", got)
	}
	tmpl := template.Must(template.New("message").Funcs(template.FuncMap{"t": func(s string, args ...any) string { return Text("en", s, args...) }}).Parse(`{{t "%s" .}}`))
	var output bytes.Buffer
	if err := tmpl.Execute(&output, name); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "<script>") || !strings.Contains(output.String(), "&lt;script&gt;") {
		t.Fatalf("translation bypassed contextual escaping: %s", output.String())
	}
}

func TestCatalogLookupAndConcurrency(t *testing.T) {
	copy := English()
	copy["Speichern"] = "corrupted"
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			for range 100 {
				if Text("en", "Speichern") != "Save" || Text("de", "Speichern") != "Speichern" {
					t.Error("locale crossed concurrent requests")
				}
			}
		})
	}
	group.Wait()
	if _, ok := Lookup("en", "user-defined grocery name"); ok {
		t.Fatal("unknown record text was cataloged")
	}
}
