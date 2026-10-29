package store

import (
	"bytes"
	"strings"
	"testing"

	"household/internal/value"
)

func leftoverValueFixture() PantryLeftoversInput {
	return PantryLeftoversInput{Generation: 1, SourceKind: "manual", Name: "Suppe", PreparedOn: "2026-01-01", PortionsUnknown: true,
		Containers: []PantryLeftoverContainer{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Label: "Behälter", Quantity: "1.25",
			LocationID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", LocationVersion: 1, ReceivedOn: "2026-01-02"}}}
}

func TestPantryLeftoversExactNormalizationAndIndependentInput(t *testing.T) {
	in := leftoverValueFixture()
	in.Containers[0].ID = strings.ToUpper(in.Containers[0].ID)
	in.Containers[0].Quantity = "1,250001"
	normalized, total, err := normalizePantryLeftovers(in)
	if err != nil || total != "1.250001" || normalized.Containers[0].ID != strings.ToLower(in.Containers[0].ID) {
		t.Fatal("exact six-place canonical normalization", normalized, total, err)
	}
	if in.Containers[0].Quantity != "1,250001" {
		t.Fatal("normalization mutated the caller's container slice")
	}
	again, _, err := normalizePantryLeftovers(normalized)
	first, e1 := pantryLeftoversDigest(normalized)
	second, e2 := pantryLeftoversDigest(again)
	if err != nil || e1 != nil || e2 != nil || !bytes.Equal(first, second) {
		t.Fatal("canonical review input is not idempotent", err, e1, e2)
	}
}

func TestPantryLeftoversClosedValuesAndPortionCaps(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*PantryLeftoversInput)
	}{
		{"unknown source", func(in *PantryLeftoversInput) { in.SourceKind = "other" }},
		{"manual foreign reference", func(in *PantryLeftoversInput) { in.SourceID = value.UUID() }},
		{"known or explicitly unknown", func(in *PantryLeftoversInput) { in.PortionsUnknown = false }},
		{"conflicting unknown cap", func(in *PantryLeftoversInput) { in.PreparedPortions = "2" }},
		{"sum exceeds known cap", func(in *PantryLeftoversInput) { in.PortionsUnknown, in.PreparedPortions = false, "1" }},
		{"empty leftover amount", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "" }},
		{"zero", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "0" }},
		{"negative", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "-1" }},
		{"seven decimal places", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "0.0000001" }},
		{"binary exponent", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "1e2" }},
		{"movement overflow", func(in *PantryLeftoversInput) { in.Containers[0].Quantity = "1000000000000" }},
		{"invalid date", func(in *PantryLeftoversInput) { in.PreparedOn = "2026-02-30" }},
		{"received before prepared", func(in *PantryLeftoversInput) { in.Containers[0].ReceivedOn = "2025-12-31" }},
		{"expiry before preparation", func(in *PantryLeftoversInput) { in.Containers[0].UseByOn = "2025-12-31" }},
		{"blank label", func(in *PantryLeftoversInput) { in.Containers[0].Label = " " }},
		{"NUL name", func(in *PantryLeftoversInput) { in.Name = "bad\x00name" }},
		{"missing location version", func(in *PantryLeftoversInput) { in.Containers[0].LocationVersion = 0 }},
		{"nonuuid location", func(in *PantryLeftoversInput) { in.Containers[0].LocationID = "shelf" }},
		{"product version without product", func(in *PantryLeftoversInput) { in.ProductVersion = 1 }},
		{"bad fingerprint", func(in *PantryLeftoversInput) { in.Fingerprint = "bad" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := leftoverValueFixture()
			test.edit(&in)
			if _, _, err := normalizePantryLeftovers(in); err == nil {
				t.Fatal("invalid unreviewed value accepted")
			}
		})
	}
}

func TestPantryLeftoversTwentyContainerAtomicBoundAndExactSum(t *testing.T) {
	in := leftoverValueFixture()
	in.Containers = nil
	for range 20 {
		line := leftoverValueFixture().Containers[0]
		line.ID, line.Quantity = value.UUID(), "0.000001"
		in.Containers = append(in.Containers, line)
	}
	in.PortionsUnknown, in.PreparedPortions = false, "0.000020"
	if _, total, err := normalizePantryLeftovers(in); err != nil || total != "0.000020" {
		t.Fatal("twenty exact micro-portions", total, err)
	}
	in.Containers = append(in.Containers, leftoverValueFixture().Containers[0])
	if _, _, err := normalizePantryLeftovers(in); err == nil {
		t.Fatal("twenty-first container accepted")
	}
	in = leftoverValueFixture()
	in.Containers = append(in.Containers, in.Containers[0])
	if _, _, err := normalizePantryLeftovers(in); err == nil {
		t.Fatal("duplicate line identity accepted")
	}
}
