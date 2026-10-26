package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSuggestionPassedUseByDoesNotCoverOrBoostAnAlternative(t *testing.T) {
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1/2")}}
	lots := []suggestionDateLot{{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "1", UseBy: "2026-10-25", BestBefore: "2026-10-27"}}
	got, err := evaluateSuggestionLots(lines, lots, "2026-10-26")
	if err != nil || got.Coverage.Outcome != "shortage" || got.Coverage.Products[0].Free.Rational != "0" || !got.DateReview || got.UrgentOn != "" || len(got.Reasons) != 2 || !got.Reasons[0].ExcludedFromCoverage || got.Reasons[0].Free.Rational != "1/2" {
		t.Fatal("passed use-by stock was recommended or erased", got, err)
	}
	// A future meal must not count a lot whose use-by is before that meal.
	got, err = evaluateSuggestionLots(lines, lots, "2026-11-02")
	if err != nil || got.Coverage.Products[0].Shortage.Rational != "1/2" {
		t.Fatal(got, err)
	}
}

func TestSuggestionUnusedOldAndUnrelatedLotsDoNotAffectDateRanking(t *testing.T) {
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1/4")}}
	lots := []suggestionDateLot{
		{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "100", UseBy: "2026-01-01"},
		{ID: "bbbbbbbb-0000-4000-8000-000000000003", ProductID: suggestionProductFixture, Free: "1", UseBy: "2026-10-28"},
		{ID: "bbbbbbbb-0000-4000-8000-000000000004", ProductID: "cccccccc-0000-4000-8000-000000000001", Free: "10", UseBy: "2026-01-01"},
	}
	original := append([]suggestionDateLot{}, lots...)
	got, err := evaluateSuggestionLots(lines, lots, "2026-10-26")
	if err != nil || got.Coverage.Outcome != "sufficient" || got.DateReview || got.UrgentOn != "2026-10-28" || len(got.Reasons) != 1 || got.Reasons[0].LotID != lots[1].ID || got.Reasons[0].Free.Rational != "1/4" || !reflect.DeepEqual(lots, original) {
		t.Fatal("unused lots changed ranking or input was mutated", got, err)
	}
}

func TestSuggestionDatesUseCompleteFreeLotsBeyondDisplaySamples(t *testing.T) {
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1/2")}}
	lots := make([]suggestionDateLot, 60)
	for i := range lots {
		lots[i] = suggestionDateLot{ID: fmt.Sprintf("bbbbbbbb-0000-4000-8000-%012d", i+1), ProductID: suggestionProductFixture, Free: "0.010000"}
	}
	lots[59].UseBy, lots[59].Free = "2026-10-27", "0.250000"
	got, err := evaluateSuggestionLots(lines, lots, "2026-10-26")
	if err != nil || got.Coverage.Outcome != "sufficient" || got.UrgentOn != "2026-10-27" || len(got.Reasons) != 1 || got.Reasons[0].LotID != lots[59].ID || got.Reasons[0].Free.Rational != "1/4" || got.Coverage.Products[0].Free.Rational != "21/25" {
		t.Fatal("a display sample was used as complete inventory", got, err)
	}
}

func TestSuggestionUnknownNeedAndZeroFreeLotsCannotInventUrgency(t *testing.T) {
	lot := suggestionDateLot{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "50", UseBy: "2026-10-27"}
	got, err := evaluateSuggestionLots([]planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg"}}, []suggestionDateLot{lot}, "2026-10-26")
	if err != nil || got.Coverage.Outcome != "incomplete" || got.UrgentOn != "" || len(got.Reasons) != 0 {
		t.Fatal("unknown demand received an invented relevant quantity", got, err)
	}
	lot.Free = "0"
	got, err = evaluateSuggestionLots([]planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1")}}, []suggestionDateLot{lot}, "2026-10-26")
	if err != nil || got.Coverage.Outcome != "shortage" || got.UrgentOn != "" || len(got.Reasons) != 0 {
		t.Fatal("claimed/empty quantity received a date priority", got, err)
	}
}

func TestSuggestionLotBoundsDuplicateIdentityAndDateWindow(t *testing.T) {
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1")}}
	lot := suggestionDateLot{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "1"}
	uppercase := lot
	uppercase.ID = strings.ToUpper(lot.ID)
	if _, err := evaluateSuggestionLots(lines, []suggestionDateLot{lot, uppercase}, "2026-10-26"); err == nil {
		t.Fatal("duplicate lot identity doubled availability")
	}
	if _, err := evaluateSuggestionLots(lines, make([]suggestionDateLot, 5001), "2026-10-26"); err == nil {
		t.Fatal("oversized lot dependency scope was truncated")
	}
	for _, tc := range []struct {
		date, planned string
		soon          bool
	}{
		{"2026-11-01", "2026-10-25", true}, // calendar dates across Austrian DST
		{"2026-11-02", "2026-10-25", false},
		{"2026-10-24", "2026-10-25", false},
		{"invalid", "2026-10-25", false},
	} {
		if suggestionDateIsSoon(tc.date, tc.planned) != tc.soon {
			t.Fatal("invalid seven-calendar-day priority window", tc)
		}
	}
}
