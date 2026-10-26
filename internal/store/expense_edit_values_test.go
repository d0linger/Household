package store

import (
	"reflect"
	"strings"
	"testing"

	"household/internal/expenseedit"
)

func TestExpenseBasisCanonicalIdentityAndCallerOwnership(t *testing.T) {
	const first = "aaaabbbb-1111-4111-8111-111111111111"
	const second = "ffffbbbb-2222-4222-8222-222222222222"
	in := []expenseedit.LineVersion{{ID: strings.ToUpper(second), Version: 3}, {ID: first, Version: 2}}
	before := append([]expenseedit.LineVersion(nil), in...)
	out, err := canonicalExpenseBasis(in)
	if err != nil || len(out) != 2 || out[0].ID != first || out[1].ID != second || !reflect.DeepEqual(in, before) {
		t.Fatal("canonical read set changed caller order or identity", out, err)
	}
	out[0].Version = 100
	if !reflect.DeepEqual(in, before) {
		t.Fatal("canonical result aliases caller")
	}
	for _, invalid := range [][]expenseedit.LineVersion{
		{{ID: first, Version: 1}, {ID: strings.ToUpper(first), Version: 2}},
		{{ID: first, Version: 0}}, {{ID: "other", Version: 1}}, make([]expenseedit.LineVersion, 101),
	} {
		if out, err := canonicalExpenseBasis(invalid); err == nil || out != nil {
			t.Fatal("invalid basis produced usable partial result", out, err)
		}
	}
	for _, empty := range [][]expenseedit.LineVersion{nil, {}} {
		out, err := canonicalExpenseBasis(empty)
		if err != nil || out == nil || len(out) != 0 {
			t.Fatal("nil/empty request sets do not share canonical representation", out, err)
		}
	}
}
