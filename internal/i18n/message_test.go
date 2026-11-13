package i18n

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
)

func TestAuthoredMessagePreservesFormattedArguments(t *testing.T) {
	const source = "<script>Speichern</script> Änderungen warten auf Prüfung nach der Anmeldung."
	const want = "<script>Speichern</script> changes await review after signing in."
	if got := Message("en", source); got != want {
		t.Fatalf("translated message=%q", got)
	}
	if got := Message("de", source); got != source {
		t.Fatal("German argument changed")
	}
	if got := Message("en", "user-defined text outside an authored message"); got != "user-defined text outside an authored message" {
		t.Fatal("unknown text changed")
	}
}

func TestWrappedValidationKeepsQuotedUserNameUnchanged(t *testing.T) {
	name := `Speichern: "Artikel" · <script>unknown</script>`
	inner := "positive Menge erforderlich"
	source := fmt.Errorf("artikel %q: %w", name, errors.New(inner)).Error()
	want := "Item " + strconv.Quote(name) + ": " + Text("en", inner)
	if got := Message("en", source); got != want {
		t.Fatalf("wrapped error changed user content: %q, want %q", got, want)
	}
}
