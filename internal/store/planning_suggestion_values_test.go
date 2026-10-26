package store

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

const suggestionProductFixture = "aaaaaaaa-0000-4000-8000-000000000001"

func TestSuggestionDateEvidenceBoundsResponseAndKeepsReviewFirst(t *testing.T) {
	reasons := make([]suggestionDateReason, 80)
	for index := range reasons {
		reasons[index] = suggestionDateReason{LotID: fmt.Sprintf("lot-%03d", index), ProductID: suggestionProductFixture, Kind: "best_before", On: "2026-10-30"}
	}
	reasons[79].Kind, reasons[79].RequiresReview, reasons[79].ExcludedFromCoverage = "use_by", true, true
	before := append([]suggestionDateReason{}, reasons...)
	got := suggestionDateEvidence(reasons)
	if len(got) != 50 || got[0].LotID != "lot-079" || !got[0].ExcludedFromCoverage || !reflect.DeepEqual(reasons, before) {
		t.Fatal("bounded presentation lost review evidence or mutated evaluation", got)
	}
	if got := suggestionDateEvidence(nil); got == nil || len(got) != 0 {
		t.Fatal("empty date evidence must serialize as an empty list", got)
	}
}

func TestSuggestionPlanningDateUsesCurrentHouseholdDayAndRejectsPastCoverage(t *testing.T) {
	at := time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC)
	for _, requested := range []string{"", "2026-10-26", "2026-10-27"} {
		got, err := suggestionPlanningDate(at, "Europe/Vienna", requested)
		want := requested
		if want == "" {
			want = "2026-10-26"
		}
		if err != nil || got != want {
			t.Fatal("household day must not follow the earlier UTC date", got, want, err)
		}
	}
	for _, requested := range []string{"2026-10-25", "2026-10-24", "2026-02-30"} {
		if _, err := suggestionPlanningDate(at, "Europe/Vienna", requested); err == nil {
			t.Fatal("past planning date could make currently expired stock sufficient", requested)
		}
	}
	if _, err := suggestionPlanningDate(time.Time{}, "Europe/Vienna", ""); err == nil {
		t.Fatal("zero observation instant accepted")
	}
	if _, err := suggestionPlanningDate(at, "not-a-timezone", ""); err == nil {
		t.Fatal("unknown timezone accepted")
	}
}

func suggestionTestQuantity(t *testing.T, raw string) *big.Rat {
	t.Helper()
	result, ok := new(big.Rat).SetString(raw)
	if !ok {
		t.Fatal(raw)
	}
	return result
}

func TestSuggestionDemandGroupsBeforeSubtractingFreeStock(t *testing.T) {
	quantity := suggestionTestQuantity(t, "3/5")
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: quantity}, {ProductID: suggestionProductFixture, Unit: "kg", Quantity: quantity}}
	stock := map[string]suggestionAvailability{suggestionProductFixture: {Unit: "kg", Free: "1"}}
	got, err := evaluateSuggestionDemand(lines, stock)
	if err != nil || got.Outcome != "shortage" || got.KnownShortages != 1 || len(got.Products) != 1 || got.Products[0].Needed.Rational != "6/5" || got.Products[0].Shortage == nil || got.Products[0].Shortage.Rational != "1/5" {
		t.Fatal("duplicate product ingredients must share free stock once", got, err)
	}
	if quantity.RatString() != "3/5" || stock[suggestionProductFixture].Free != "1" {
		t.Fatal("evaluation mutated caller-owned demand or free pool")
	}
}

func TestSuggestionIndependentAlternativesReuseAnImmutablePool(t *testing.T) {
	stock := map[string]suggestionAvailability{suggestionProductFixture: {Unit: "kg", Free: "1"}}
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "3/4")}}
	for range 2 {
		got, err := evaluateSuggestionDemand(lines, stock)
		if err != nil || got.Outcome != "sufficient" || got.Products[0].Free.Rational != "1" || got.Products[0].Shortage == nil || got.Products[0].Shortage.Rational != "0" {
			t.Fatal("alternatives must not consume a shared pool", got, err)
		}
	}
}

func TestSuggestionUnknownDemandIsNeverZeroOrCovered(t *testing.T) {
	stock := map[string]suggestionAvailability{suggestionProductFixture: {Unit: "kg", Free: "100"}}
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1/2")}, {ProductID: suggestionProductFixture, Unit: "kg"}, {}}
	got, err := evaluateSuggestionDemand(lines, stock)
	if err != nil || got.Outcome != "incomplete" || got.Products[0].Complete || got.Products[0].Shortage != nil || got.Products[0].Needed.Rational != "1/2" {
		t.Fatal("partial known demand cannot claim complete coverage", got, err)
	}
	empty, err := evaluateSuggestionDemand(nil, nil)
	if err != nil || empty.Outcome != "empty" {
		t.Fatal("no ingredient evidence is not a covered recipe", empty, err)
	}
}

func TestSuggestionCoverageRejectsMissingNegativeAndIncompatibleStock(t *testing.T) {
	lines := []planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, "1")}}
	for _, available := range []map[string]suggestionAvailability{
		nil,
		{suggestionProductFixture: {Unit: "l", Free: "100"}},
		{suggestionProductFixture: {Unit: "kg", Free: "-1"}},
		{suggestionProductFixture: {Unit: "kg", Free: "unknown"}},
	} {
		if _, err := evaluateSuggestionDemand(lines, available); err == nil {
			t.Fatal("missing or invalid stock cannot be silently zero", available)
		}
	}
	if _, err := evaluateSuggestionDemand(make([]planningDemandQuantity, 501), nil); err == nil {
		t.Fatal("ingredient bound was silently truncated")
	}
}

func TestSuggestionCoverageRetainsSubprecisionAndLargeExactDemand(t *testing.T) {
	for _, raw := range []string{"1/3000000", "9007199254.740993"} {
		got, err := evaluateSuggestionDemand([]planningDemandQuantity{{ProductID: suggestionProductFixture, Unit: "kg", Quantity: suggestionTestQuantity(t, raw)}}, map[string]suggestionAvailability{suggestionProductFixture: {Unit: "kg", Free: "0"}})
		if err != nil || got.Outcome != "shortage" || got.Products[0].Shortage == nil || got.Products[0].Shortage.Rational != suggestionTestQuantity(t, raw).RatString() {
			t.Fatal("exact positive shortage was lost", got, err)
		}
		if raw == "1/3000000" && !got.Products[0].Shortage.BelowPrecision {
			t.Fatal("subprecision demand appeared as a real zero")
		}
	}
}

func TestSuggestionDatesKeepZeroDayOpeningAndDistinctReviewReasons(t *testing.T) {
	zero := int64(0)
	lot := suggestionDateLot{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "0.125001", OpenedOn: "2026-10-25", AfterOpenDays: &zero, UseBy: "2026-10-26", BestBefore: "2026-10-27"}
	got, err := suggestionLotDateReasons(lot, "2026-10-26")
	if err != nil || len(got) != 3 || got[0].Kind != "use_by" || got[0].Past || got[1].Kind != "opening_hint" || got[1].On != "2026-10-25" || !got[1].RequiresReview || got[2].Kind != "best_before" || got[2].Past || got[0].Free.Rational != "125001/1000000" {
		t.Fatal("calendar reasons were merged or zero-day opening became unknown", got, err)
	}
	lot.AfterOpenDays = nil
	got, err = suggestionLotDateReasons(lot, "2026-10-27")
	if err != nil || len(got) != 2 || got[0].Kind != "use_by" || !got[0].RequiresReview {
		t.Fatal("unknown opening duration must not invent a date, passed use-by needs review", got, err)
	}
	lot.Free = "0"
	got, err = suggestionLotDateReasons(lot, "2026-10-27")
	if err != nil || len(got) != 0 {
		t.Fatal("empty or fully claimed lot cannot boost urgency", got, err)
	}
}

func TestSuggestionDatesRejectInvalidInputs(t *testing.T) {
	valid := suggestionDateLot{ID: "bbbbbbbb-0000-4000-8000-000000000002", ProductID: suggestionProductFixture, Free: "1"}
	for _, change := range []func(*suggestionDateLot){
		func(l *suggestionDateLot) { l.Free = "-1" },
		func(l *suggestionDateLot) { l.BestBefore = "2026-02-30" },
		func(l *suggestionDateLot) { l.ID = "not-a-lot" },
		func(l *suggestionDateLot) { n := int64(-1); l.AfterOpenDays = &n },
	} {
		lot := valid
		change(&lot)
		if _, err := suggestionLotDateReasons(lot, "2026-10-26"); err == nil {
			t.Fatal("invalid lot reason accepted", lot)
		}
	}
	if _, err := suggestionLotDateReasons(valid, "2026-02-30"); err == nil {
		t.Fatal("invalid planning calendar date accepted")
	}
}

func TestSuggestionRankingIsPageBoundedStableAndDoesNotBoostReviewOnlyDates(t *testing.T) {
	var input []suggestionRank
	for i := range 20 {
		input = append(input, suggestionRank{RecipeID: fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", i+1), SourceVersionID: "bbbbbbbb-0000-4000-8000-000000000002", Title: "Same title", Outcome: "sufficient"})
	}
	input[0].UrgentOn = "2026-10-28"
	input[1].UrgentOn = "2026-10-27"
	input[2].DateReview, input[2].UrgentOn = true, "2020-01-01"
	input[3].Outcome = "shortage"
	input[4].Outcome = "incomplete"
	original := append([]suggestionRank{}, input...)
	want, err := rankSuggestionPage(input)
	if err != nil || want[0].RecipeID != input[1].RecipeID || want[1].RecipeID != input[0].RecipeID || want[17].RecipeID != input[2].RecipeID || want[18].Outcome != "shortage" || want[19].Outcome != "incomplete" || !reflect.DeepEqual(input, original) {
		t.Fatal("unexplained ranking or caller mutation", want, err)
	}
	for seed := range uint64(20) {
		random := rand.New(rand.NewPCG(seed+1, seed+2))
		shuffled := append([]suggestionRank{}, input...)
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got, err := rankSuggestionPage(shuffled)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("ranking changed with input order", seed, got, err)
		}
	}
	for _, bad := range [][]suggestionRank{
		append(append([]suggestionRank{}, input...), input[0]),
		{input[0], {RecipeID: strings.ToUpper(input[0].RecipeID), SourceVersionID: input[0].SourceVersionID, Outcome: "sufficient"}},
		{{RecipeID: input[0].RecipeID, SourceVersionID: input[0].SourceVersionID, Outcome: "invented"}},
	} {
		if _, err := rankSuggestionPage(bad); err == nil {
			t.Fatal("invalid/duplicate/overflow candidate page accepted")
		}
	}
}
