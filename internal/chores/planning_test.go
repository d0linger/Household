package chores

import (
	"testing"
	"time"
)

func TestDayProposalExplainsUnknownDurationBudgetAndSuitability(t *testing.T) {
	ten, twenty, difficulty := 10, 20, 2
	now := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	items := []PlanItem{{ID: "unknown", Title: "Unknown", Priority: 3, Eligible: true, DueAt: now}, {ID: "selected", Priority: 2, Eligible: true, DueAt: now, EstimatedMinutes: &ten, Difficulty: &difficulty},
		{ID: "budget", Priority: 1, Eligible: true, DueAt: now, EstimatedMinutes: &twenty}, {ID: "blocked", Priority: 3, Eligible: true, DueAt: now, EstimatedMinutes: &ten, BlockedReason: "Voraussetzung fehlt"}}
	plan, err := PlanDay(items, 15, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected) != 1 || plan.Selected[0].Item.ID != "selected" || plan.SelectedMinutes != 10 || plan.RemainingMinutes != 5 || len(plan.Unestimated) != 1 || plan.Unestimated[0].Item.ID != "unknown" || len(plan.Excluded) != 2 {
		t.Fatal(plan)
	}
	if items[0].ID != "unknown" {
		t.Fatal("proposal reordered caller data")
	}
	if _, err = PlanDay(append(items, items[0]), 15, nil); err == nil {
		t.Fatal("duplicate occurrence selected")
	}
	zero := 0
	if _, err = PlanDay([]PlanItem{{ID: "invalid", Eligible: true, EstimatedMinutes: &zero}}, 15, nil); err == nil {
		t.Fatal("unknown estimate was replaced with zero")
	}
}

func TestRotationRespectsEligiblePresentSubstitutesAndDoesNotInventOne(t *testing.T) {
	members := []Candidate{{MembershipID: "a", Active: true, Eligible: true, Absent: true, SubstituteID: "c"}, {MembershipID: "b", Active: true, Eligible: false}, {MembershipID: "c", Active: true, Eligible: true}}
	assignment, err := Rotate(members, 0)
	if err != nil || assignment.MembershipID != "c" || assignment.ReasonCode != "substitute" {
		t.Fatal(assignment, err)
	}
	members[2].Absent = true
	assignment, err = Rotate(members, 0)
	if err != nil || assignment.MembershipID != "" || assignment.ReasonCode != "no_available_member" {
		t.Fatal(assignment, err)
	}
	if _, err = Rotate(append(members, members[0]), 0); err == nil {
		t.Fatal("duplicate candidate accepted")
	}
}

func TestDependenciesRejectCyclesDuplicatesAndMissingVertices(t *testing.T) {
	if err := ValidateGraph(map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}, "d": {}}); err != nil {
		t.Fatal("shared prerequisite is not a cycle", err)
	}
	for _, graph := range []map[string][]string{{"a": {"a"}}, {"a": {"b"}, "b": {"a"}}, {"a": {"b"}}, {"a": {"b", "b"}, "b": {}}} {
		if err := ValidateGraph(graph); err == nil {
			t.Fatal("invalid dependency graph accepted", graph)
		}
	}
}

func TestDigestQuietTimeDefersAcrossMidnightAndDST(t *testing.T) {
	start, end := 22*60, 7*60
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	instant, err := DigestInstant("2026-03-28", 23*60, &start, &end, location)
	if err != nil || instant.UTC().Format(time.RFC3339) != "2026-03-29T05:00:00Z" {
		t.Fatal(instant, err)
	}
	if !QuietAt(23*60, &start, &end) || !QuietAt(6*60, &start, &end) || QuietAt(7*60, &start, &end) {
		t.Fatal("quiet boundary")
	}
	if _, err = DigestInstant("2026-03-28", 540, &start, nil, location); err == nil {
		t.Fatal("partial quiet interval accepted")
	}
}
