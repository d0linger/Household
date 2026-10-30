package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPlanningLeftoversNormalizeReviewedExactCommands(t *testing.T) {
	id := "10000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name string
		in   PlanningLeftoverInput
		ok   bool
	}{
		{name: "explicit production subset", in: PlanningLeftoverInput{Action: "source_save", MealID: id, MealVersion: 2, ComponentID: id, ComponentVersion: 3, ExpectedServings: "4"}, ok: true},
		{name: "exact partial allocation", in: PlanningLeftoverInput{Action: "allocation_save", SourceID: id, SourceVersion: 1, TargetMealID: id, TargetVersion: 4, Servings: "0.000001"}, ok: true},
		{name: "zero forecast", in: PlanningLeftoverInput{Action: "source_save", MealID: id, MealVersion: 2, ComponentID: id, ComponentVersion: 3, ExpectedServings: "0"}},
		{name: "implicit source version", in: PlanningLeftoverInput{Action: "source_delete", SourceID: id}},
		{name: "excess precision", in: PlanningLeftoverInput{Action: "allocation_save", SourceID: id, SourceVersion: 1, TargetMealID: id, TargetVersion: 4, Servings: "0.0000001"}},
		{name: "review needs existing allocation", in: PlanningLeftoverInput{Action: "allocation_review", SourceID: id, SourceVersion: 1, TargetMealID: id, TargetVersion: 4, Servings: "1"}},
		{name: "unknown action", in: PlanningLeftoverInput{Action: "consume"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := normalizePlanningLeftover(&tc.in)
			if (err == nil) != tc.ok {
				t.Fatalf("accepted=%v, want=%v, err=%v", err == nil, tc.ok, err)
			}
			if err == nil && tc.in.Action == "source_save" && tc.in.ExpectedServings != "4.000000" {
				t.Fatal("normalization must retain exact forecast, not multiply production", tc.in)
			}
		})
	}
}

func TestPlanningLeftoversMapOnlyExpectedDataConflicts(t *testing.T) {
	constraint := &pgconn.PgError{Code: "23514", Message: "private SQL detail"}
	var conflict PlanningConflict
	if !errors.As(planningLeftoverError(constraint), &conflict) || conflict.Error() == constraint.Message {
		t.Fatal("expected reviewed conflict without raw database details")
	}
	unexpected := errors.New("connection unavailable")
	if planningLeftoverError(unexpected) != unexpected {
		t.Fatal("infrastructure failures must retain their error identity")
	}
}
