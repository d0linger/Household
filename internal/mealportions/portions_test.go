package mealportions

import (
	"reflect"
	"strings"
	"testing"
)

const (
	memberA = "aaaaaaaa-0000-4000-8000-000000000001"
	memberB = "bbbbbbbb-0000-4000-8000-000000000002"
)

func TestNormalizePortionReview(t *testing.T) {
	for _, tc := range []struct {
		name               string
		in                 Input
		derived, effective string
	}{
		{"legacy manual", Input{Servings: "1,500001"}, "0", "1.500001"},
		{"members and guests", Input{Mode: "participants", Members: []string{memberB, strings.ToUpper(memberA)}, Guests: 2, Servings: "4"}, "4", "4.000000"},
		{"guests alone", Input{Mode: "participants", Guests: 3}, "3", "3.000000"},
		{"manual override retains roster", Input{Mode: "manual", Members: []string{memberA}, Guests: 1, Servings: "0.750001"}, "2", "0.750001"},
		{"maximum guest count", Input{Mode: "participants", Guests: MaxGuests}, "100", "100.000000"},
		{"exact manual beyond JS integer precision", Input{Servings: "9007199254.740993"}, "0", "9007199254.740993"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.in)
			if err != nil || got.DerivedServings != tc.derived || got.Servings != tc.effective || got.Guests != tc.in.Guests {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNormalizeRejectsUnreviewedOrAmbiguousCounts(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
	}{
		{"unknown mode", Input{Mode: "automatic", Servings: "1"}},
		{"no participants", Input{Mode: "participants"}},
		{"stale reviewed servings", Input{Mode: "participants", Members: []string{memberA}, Servings: "2"}},
		{"invalid reviewed servings", Input{Mode: "participants", Guests: 1, Servings: "invalid"}},
		{"negative guests", Input{Guests: -1, Servings: "1"}},
		{"guest overflow", Input{Guests: MaxGuests + 1, Servings: "1"}},
		{"membership overflow", Input{Members: make([]string, MaxMembers+1), Servings: "1"}},
		{"duplicate member", Input{Members: []string{memberA, memberA}, Servings: "1"}},
		{"mixed-case duplicate", Input{Members: []string{memberA, strings.ToUpper(memberA)}, Servings: "1"}},
		{"invalid member", Input{Members: []string{"household-administrator"}, Servings: "1"}},
		{"zero manual", Input{Servings: "0"}},
		{"negative manual", Input{Servings: "-1"}},
		{"too precise", Input{Servings: "0.0000001"}},
		{"manual overflow", Input{Servings: "1000000000000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Normalize(tc.in); err == nil {
				t.Fatal("unreviewed input accepted")
			}
		})
	}
}

func TestNormalizeCanonicalIdentityAndIndependentOwnership(t *testing.T) {
	in := Input{Mode: "participants", Members: []string{memberB, strings.ToUpper(memberA)}, Guests: 1}
	got, err := Normalize(in)
	if err != nil || !reflect.DeepEqual(got.Members, []string{memberA, memberB}) {
		t.Fatal(got, err)
	}
	in.Members[0] = memberA
	if got.Members[1] != memberB {
		t.Fatal("review aliases caller's roster")
	}
	got.Members[0] = memberB
	if in.Members[1] != strings.ToUpper(memberA) {
		t.Fatal("caller aliases normalized review")
	}
}

func TestComponentServingsPreservesIndependentAmounts(t *testing.T) {
	for _, tc := range []struct{ name, mode, manual, meal, want string }{
		{"legacy component", "", "0.500001", "4", "0.500001"},
		{"manual component", "independent", "2.5", "4", "2.500000"},
		{"linked component", "meal", "100", "4", "4.000000"},
		{"exact linked override", "meal", "", "1.000001", "1.000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComponentServings(tc.mode, tc.manual, tc.meal)
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, tc := range []struct{ name, mode, manual, meal string }{
		{"bad mode", "auto", "1", "1"}, {"missing linked meal", "meal", "1", ""}, {"zero linked meal", "meal", "1", "0"}, {"negative manual", "independent", "-1", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ComponentServings(tc.mode, tc.manual, tc.meal); err == nil {
				t.Fatal("invalid component portions accepted")
			}
		})
	}
}
