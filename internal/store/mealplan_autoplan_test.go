package store

import (
	"math/big"
	"testing"
	"time"
)

func TestPlanningAutoplanReviewHashBindsChoicesAndExcludesServerProof(t *testing.T) {
	in, _ := planningAutoplanFixture()
	first := PlanningAutoplanReviewHash(in)
	in.ReviewFingerprint, in.ReviewProof, in.ReviewExpiresAt = "fingerprint", "proof", 123
	if PlanningAutoplanReviewHash(in) != first {
		t.Fatal("server review material changed the user input identity")
	}
	in.Rules.Stock++
	if PlanningAutoplanReviewHash(in) == first {
		t.Fatal("weights were not review-bound")
	}
	in.Rules.Stock--
	in.Slots[0].LeaveEmpty = true
	if PlanningAutoplanReviewHash(in) == first {
		t.Fatal("empty-slot choice was not review-bound")
	}
}

func TestPlanningAutoplanValidationRejectsDuplicateSlotsAndForeignAlternatives(t *testing.T) {
	in, _ := planningAutoplanFixture()
	if err := validatePlanningAutoplan(in); err != nil {
		t.Fatal(err)
	}
	in.Slots = append(in.Slots, in.Slots[0])
	if err := validatePlanningAutoplan(in); err == nil {
		t.Fatal("duplicate dated slot accepted")
	}
	in.Slots = in.Slots[:1]
	in.Slots[0].SourceVersionID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	if err := validatePlanningAutoplan(in); err == nil {
		t.Fatal("unreviewed source alternative accepted")
	}
}

func TestPlanningAutoplanJointSimulationCannotSpendOneLotTwice(t *testing.T) {
	in, data := planningAutoplanFixture()
	in.Slots = append(in.Slots, PlanningAutoplanSlotInput{PlannedOn: "2026-11-01", Slot: "dinner", Servings: "1"})
	result, choices, err := simulatePlanningAutoplan(in, data)
	if err != nil || len(choices) != 2 {
		t.Fatal(result, choices, err)
	}
	slots := result["slots"].([]Record)
	if String(slots[0]["coverage"]) != "sufficient" || String(slots[1]["coverage"]) != "shortage" {
		t.Fatal("independent alternatives incorrectly claimed joint coverage", slots)
	}
	if data.Lots[0].Free != "1" {
		t.Fatal("simulation changed caller stock metadata")
	}
}

func TestPlanningAutoplanExistingClaimsAreNotSubtractedTwice(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreserved existing demand", true: "own reservation already removed from free"}[own], func(t *testing.T) {
			in, data := planningAutoplanFixture()
			mid := "b2222222-2222-4222-8222-222222222222"
			amount := IngredientAmount{Rational: "1"}
			data.Existing = PlanningDemand{Meals: []PlanningDemandMeal{{ID: mid, Date: "2026-10-30", Slot: "lunch"}},
				Lines:    []PlanningDemandLine{{MealID: mid, ProductID: planningAutoplanProductFixture, Demand: &amount}},
				Products: []*PlanningDemandProduct{{ID: planningAutoplanProductFixture, Unit: "kg"}}}
			if own {
				data.OwnClaims[mid+"/"+planningAutoplanProductFixture] = "1"
			}
			result, _, err := simulatePlanningAutoplan(in, data)
			if err != nil {
				t.Fatal(err)
			}
			coverage := String(result["slots"].([]Record)[0]["coverage"])
			want := "shortage"
			if own {
				want = "sufficient"
			}
			if coverage != want {
				t.Fatal("existing-demand/claim accounting", coverage, want)
			}
		})
	}
}

func TestPlanningAutoplanUnknownExistingDemandAndPassedUseByReceiveNoStockBonus(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		in, data := planningAutoplanFixture()
		if unknown {
			data.UnknownLines = 1
		} else {
			data.Lots[0].UseBy = "2026-10-29"
		}
		result, _, err := simulatePlanningAutoplan(in, data)
		if err != nil {
			t.Fatal(err)
		}
		slot := result["slots"].([]Record)[0]
		for _, reason := range slot["reasons"].([]Record) {
			if (reason["criterion"] == "stock" || reason["criterion"] == "urgency") && Int(reason["contribution"]) != 0 {
				t.Fatal("unknown or unsafe stock received bonus", reason)
			}
		}
	}
}

func TestPlanningAutoplanFractionalPoolNeverRoundsAvailabilityUp(t *testing.T) {
	if got := planningAutoplanFloor(big.NewRat(1, 3)); got != "0.333333" {
		t.Fatal(got)
	}
	if got := planningAutoplanFloor(big.NewRat(1, 2000000)); got != "0.000000" {
		t.Fatal(got)
	}
}

func TestPlanningAutoplanTieBreakAndVisibleSlotOrderAreDeterministic(t *testing.T) {
	in, data := planningAutoplanFixture()
	in.Rules = PlanningAutoplanRules{}
	other := data.Candidates[0]
	other.SourceVersionID = "a0000000-0000-4000-8000-000000000000"
	data.Candidates = append(data.Candidates, other)
	in.Slots = []PlanningAutoplanSlotInput{{PlannedOn: "2026-10-31", Slot: "snack", Servings: "1"},
		{PlannedOn: "2026-10-31", Slot: "breakfast", Servings: "1"}}
	_, choices, err := simulatePlanningAutoplan(in, data)
	if err != nil || choices[0].SourceVersionID != other.SourceVersionID || choices[0].Slot.Slot != "breakfast" || choices[1].Slot.Slot != "snack" {
		t.Fatal(choices, err)
	}
}

func TestPlanningAutoplanFingerprintIgnoresWallclockButBindsDependencies(t *testing.T) {
	in, data := planningAutoplanFixture()
	data.Scope.ReadAt = time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	first, err := planningAutoplanFingerprint(in, data)
	if err != nil {
		t.Fatal(err)
	}
	data.Scope.ReadAt = data.Scope.ReadAt.Add(time.Minute)
	second, err := planningAutoplanFingerprint(in, data)
	if err != nil || second != first {
		t.Fatal("wallclock alone invalidated semantic snapshot", first, second, err)
	}
	data.Lots[0].Free = "0.5"
	changed, err := planningAutoplanFingerprint(in, data)
	if err != nil || changed == first {
		t.Fatal("changed stock was not review-bound", changed, err)
	}
	data.Lots[0].Free = "1"
	data.Candidates[0].SnapshotHash = "changed-source"
	changed, err = planningAutoplanFingerprint(in, data)
	if err != nil || changed == first {
		t.Fatal("changed immutable source was not review-bound", changed, err)
	}
}

func TestPlanningAutoplanSourceZeroMinutesRemainUnknown(t *testing.T) {
	for _, raw := range []any{nil, 0, "0", "-1", "525601", "invalid"} {
		if planningAutoplanSourceMinutes(raw) != nil {
			t.Fatal("source default/invalid minutes were treated as known effort", raw)
		}
	}
	if minutes := planningAutoplanSourceMinutes("15"); minutes == nil || *minutes != 15 {
		t.Fatal(minutes)
	}
}

const planningAutoplanProductFixture = "b1111111-1111-4111-8111-111111111111"

func planningAutoplanFixture() (PlanningAutoplanInput, planningAutoplanData) {
	version := "c1111111-1111-4111-8111-111111111111"
	in := PlanningAutoplanInput{RecipesGeneration: 1,
		Candidates: []PlanningAutoplanCandidateInput{{SourceVersionID: version}},
		Slots:      []PlanningAutoplanSlotInput{{PlannedOn: "2026-10-31", Slot: "dinner", Servings: "1"}},
		Rules:      PlanningAutoplanRules{Stock: 100}}
	bundle := RecipeBundle{Recipe: Record{"title": "One kilogram", "base_servings": "1"},
		Ingredients: []Record{{"id": "d1111111-1111-4111-8111-111111111111", "catalog_id": planningBudgetCatalogFixture,
			"name": "Food", "quantity": "1", "unit_code": "kg"}}}
	data := planningAutoplanData{Scope: planningBudgetScope{RecipesEnabled: true, RecipesGeneration: 1, PantryEnabled: true},
		Candidates: []planningAutoplanCandidate{{SourceVersionID: version, RecipeID: "e1111111-1111-4111-8111-111111111111", Title: "One kilogram", Bundle: bundle}},
		Mappings: map[string]Record{planningBudgetCatalogFixture: {"binding_id": "f1111111-1111-4111-8111-111111111111", "assigned": true,
			"target_current": true, "product_id": planningAutoplanProductFixture, "unit": "kg"}},
		Lots:      []suggestionDateLot{{ID: "a1111111-1111-4111-8111-111111111111", ProductID: planningAutoplanProductFixture, Free: "1"}},
		OwnClaims: map[string]string{}}
	return in, data
}
