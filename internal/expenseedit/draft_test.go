package expenseedit

import (
	"reflect"
	"strings"
	"testing"

	"household/internal/value"
)

const memberFixture = "aaaaaaaa-0000-4000-8000-000000000001"
const categoryFixture = "bbbbbbbb-0000-4000-8000-000000000002"

func draftFixture() Input {
	return Input{Generation: 1, Action: "save", Header: HeaderInput{OccurredOn: "2026-10-26", Description: "Household invoice", AmountEUR: "9.99", Kind: "expense"}}
}

func TestDraftCanRetainUnallocatedDifferenceButPostingCannot(t *testing.T) {
	in := draftFixture()
	got, err := Normalize(in)
	if err != nil || got.SumMinor != "0" || got.RemainingMinor != "999" || got.ReadyToPost {
		t.Fatal("incomplete draft must keep its exact difference", got, err)
	}
	in.Action = "post"
	if got, err = Normalize(in); err == nil || !reflect.DeepEqual(got, Draft{}) {
		t.Fatal("unallocated post returned a usable partially normalized edit", got, err)
	}
}

func TestDraftSignedAllocationsAndExplicitEURUnits(t *testing.T) {
	in := draftFixture()
	in.Action = "post"
	in.Lines = []LineInput{{Key: value.UUID(), CategoryID: categoryFixture, AmountEUR: "10,01"},
		{Key: value.UUID(), CategoryID: categoryFixture, AmountEUR: "-0.02", Note: "Explicit discount"}}
	got, err := Normalize(in)
	if err != nil || !got.ReadyToPost || got.SumMinor != "999" || got.RemainingMinor != "0" || got.Lines[0].AmountMinor != 1001 || got.Lines[1].AmountMinor != -2 {
		t.Fatal("signed same-category allocations were rounded or collapsed", got, err)
	}
	// A string input is explicitly euros, never an already-normalized cent value.
	in.Header.AmountEUR, in.Action = "999", "save"
	got, err = Normalize(in)
	if err != nil || got.Header.AmountMinor != 99900 || got.RemainingMinor != "98901" {
		t.Fatal("implicit cent/euro reinterpretation", got, err)
	}
}

func TestDraftCanonicalValuesAndCallerOwnership(t *testing.T) {
	in := draftFixture()
	in.Header.Description, in.Header.Payee, in.Header.AmountEUR = "  Described invoice  ", " Shop ", "01,200000"
	in.Header.PaidByMembershipID = strings.ToUpper(memberFixture)
	in.Lines = []LineInput{{Key: strings.ToUpper(memberFixture), CategoryID: strings.ToUpper(categoryFixture), AmountEUR: "1.20", Note: " note "}}
	before := in
	before.Lines = append([]LineInput(nil), in.Lines...)
	got, err := Normalize(in)
	if err != nil || !got.ReadyToPost || got.Header.AmountMinor != 120 || got.Header.Description != "Described invoice" ||
		got.Header.Payee != "Shop" || got.Header.PaidByMembershipID != memberFixture || got.Lines[0].Key != memberFixture || got.Lines[0].CategoryID != categoryFixture || got.Lines[0].Note != "note" {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("normalization mutated caller-owned rows", in, before)
	}
	in.Header.AmountEUR = "1.2"
	other, err := Normalize(in)
	if err != nil || !reflect.DeepEqual(got, other) {
		t.Fatal("equivalent EUR forms changed canonical input", got, other, err)
	}
}

func TestDraftRefundDirectionAndCentPrecision(t *testing.T) {
	in := draftFixture()
	in.Header.Kind, in.Header.AmountEUR, in.Action = "refund", "-9.99", "post"
	in.Lines = []LineInput{{Key: value.UUID(), CategoryID: categoryFixture, AmountEUR: "-9.99"}}
	if got, err := Normalize(in); err != nil || !got.ReadyToPost || got.Header.AmountMinor != -999 {
		t.Fatal("exact negative refund rejected", got, err)
	}
	for _, tc := range []struct{ kind, amount string }{
		{"refund", "9.99"}, {"expense", "-9.99"}, {"reversal", "-9.99"}, {"", "-9.99"},
		{"expense", "0"}, {"expense", "0.001"}, {"expense", "NaN"}, {"expense", "1e2"}, {"expense", "10000000000.01"},
	} {
		in.Header.Kind, in.Header.AmountEUR, in.Action = tc.kind, tc.amount, "save"
		if got, err := Normalize(in); err == nil || !reflect.DeepEqual(got, Draft{}) {
			t.Fatal("invalid economic direction/precision returned a usable edit", tc, got, err)
		}
	}
}

func TestDraftAggregateRetainsWiderExactDifferenceAndLineBound(t *testing.T) {
	in := draftFixture()
	in.Header.AmountEUR = "10000000000"
	for range LineLimit {
		in.Lines = append(in.Lines, LineInput{Key: value.UUID(), CategoryID: categoryFixture, AmountEUR: "10000000000"})
	}
	got, err := Normalize(in)
	if err != nil || got.SumMinor != "100000000000000" || got.RemainingMinor != "-99000000000000" || got.ReadyToPost {
		t.Fatal("bounded aggregate was clamped to an individual amount or lost precision", got, err)
	}
	in.Lines = append(in.Lines, LineInput{Key: value.UUID(), CategoryID: categoryFixture, AmountEUR: "1"})
	if _, err := Normalize(in); err == nil {
		t.Fatal("101-row edit accepted")
	}
}

func TestDraftRejectsDuplicateIdentitiesAndStaleShape(t *testing.T) {
	valid := draftFixture()
	valid.EntryID, valid.Version = value.UUID(), 2
	valid.Lines = []LineInput{{Key: memberFixture, ID: categoryFixture, Version: 1, CategoryID: categoryFixture, AmountEUR: "9.99"}}
	if _, err := Normalize(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Input){
		func(in *Input) { in.Generation = 0 },
		func(in *Input) { in.Version = 0 },
		func(in *Input) { in.EntryID = "" },
		func(in *Input) { in.Lines[0].Version = 0 },
		func(in *Input) { in.Lines[0].ID = "" },
		func(in *Input) {
			in.Lines = append(in.Lines, LineInput{Key: strings.ToUpper(memberFixture), CategoryID: categoryFixture, AmountEUR: "1"})
		},
		func(in *Input) {
			in.Lines = append(in.Lines, LineInput{Key: value.UUID(), ID: strings.ToUpper(categoryFixture), Version: 1, CategoryID: categoryFixture, AmountEUR: "1"})
		},
		func(in *Input) { in.Lines[0].CategoryID = "invalid" },
		func(in *Input) { in.Lines[0].AmountEUR = "0" },
		func(in *Input) { in.Lines[0].Note = "nul\x00note" },
		func(in *Input) { in.Header.Description = strings.Repeat("界", 301) },
		func(in *Input) { in.Header.Payee = string([]byte{0xff}) },
		func(in *Input) { in.Header.OccurredOn = "2026-02-30" },
	} {
		in := valid
		in.Lines = append([]LineInput(nil), valid.Lines...)
		change(&in)
		if got, err := Normalize(in); err == nil || !reflect.DeepEqual(got, Draft{}) {
			t.Fatal("invalid identity/version/field shape returned a usable edit", in, got, err)
		}
	}
}
