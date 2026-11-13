//go:build integration

package web

import (
	"errors"
	"os"
	"sync"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func plannedLeftoverFixture(t *testing.T) (*integrationFixture, string, string, string) {
	t.Helper()
	f, pid := planningFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	in := planningInput(t, f, pid)
	in.Action = "save"
	in.Meal = store.PlanningMealInput{Title: "Six produced, four expected surplus", PlannedOn: "2026-10-23", Slot: "dinner", Kind: "recipe", Status: "planned", Servings: "6"}
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", GuestCount: 2}
	in.Components = []store.PlanningComponentInput{{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "6", ServingsMode: "meal"}}
	meal := store.String(planningMutation(t, f, pid, in)["meal_id"])
	target := plannedLeftoverTarget(t, f, pid, "2026-10-24", "lunch", "3")
	return f, pid, meal, target
}

func plannedLeftoverTarget(t *testing.T, f *integrationFixture, pid, date, slot, servings string) string {
	t.Helper()
	in := planningInput(t, f, pid)
	in.Action = "save"
	in.Meal = store.PlanningMealInput{Title: "Expected leftovers, no actual stock", PlannedOn: date, Slot: slot, Kind: "leftovers", Status: "planned", Servings: servings}
	return store.String(planningMutation(t, f, pid, in)["meal_id"])
}

func plannedLeftoverBase(t *testing.T, f *integrationFixture, pid string) store.PlanningExtensionCommand {
	t.Helper()
	in := planningInput(t, f, pid)
	return store.PlanningExtensionCommand{Generation: in.Generation, PlanVersion: in.PlanVersion, Week: "2026-10-22"}
}

func plannedLeftoverView(t *testing.T, f *integrationFixture, pid string) store.Record {
	t.Helper()
	view, err := f.app.Store.PlanningLeftovers(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func plannedLeftoverReviewSource(t *testing.T, f *integrationFixture, pid, mid string) store.Record {
	t.Helper()
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil || len(meal.Components) != 1 {
		t.Fatal(meal, err)
	}
	in := store.PlanningLeftoverInput{PlanningExtensionCommand: plannedLeftoverBase(t, f, pid), Action: "source_save", MealID: mid, MealVersion: meal.Version,
		ComponentID: meal.Components[0].ID, ComponentVersion: meal.Components[0].Version, ExpectedServings: "4"}
	prior := plannedLeftoverView(t, f, pid)["sources"].([]store.Record)
	if len(prior) != 0 {
		in.SourceID, in.SourceVersion = store.String(prior[0]["id"]), store.Int(prior[0]["version"])
	}
	if _, err = f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	return plannedLeftoverView(t, f, pid)["sources"].([]store.Record)[0]
}

func plannedLeftoverAllocation(t *testing.T, f *integrationFixture, pid, target string, source store.Record, servings string) store.PlanningLeftoverInput {
	t.Helper()
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, target)
	if err != nil {
		t.Fatal(err)
	}
	return store.PlanningLeftoverInput{PlanningExtensionCommand: plannedLeftoverBase(t, f, pid), Action: "allocation_save", SourceID: store.String(source["id"]),
		SourceVersion: store.Int(source["version"]), TargetMealID: target, TargetVersion: meal.Version, Servings: servings}
}

func TestPlanningLeftoversCapacityStaleReviewReplayAndNoPhysicalEffects(t *testing.T) {
	f, pid, mid, target := plannedLeftoverFixture(t)
	var before, after string
	counts := `SELECT jsonb_build_array((SELECT count(*) FROM pantry.stock_movements),(SELECT count(*) FROM recipes.cooking_records),
 (SELECT count(*) FROM core.meal_reservations),(SELECT count(*) FROM core.planning_purchase_requirements))::text`
	if err := f.pool.QueryRow(f.ctx, counts).Scan(&before); err != nil {
		t.Fatal(err)
	}
	source := plannedLeftoverReviewSource(t, f, pid, mid)
	if source["production_servings"] != "6.000000" || source["source_use_servings"] != "2.000000" {
		t.Fatal("surplus was added to total production or replaced by diner count", source)
	}
	in := plannedLeftoverAllocation(t, f, pid, target, source, "3")
	key := value.UUID()
	result, err := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	if replay, replayErr := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, key, value.UUID(), in); replayErr != nil || store.Int(replay["plan_version"]) != store.Int(result["plan_version"]) {
		t.Fatal("exact committed replay", replay, replayErr)
	}
	second := plannedLeftoverTarget(t, f, pid, "2026-10-25", "lunch", "2")
	over := plannedLeftoverAllocation(t, f, pid, second, source, "2")
	if _, err = f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), over); !errors.Is(err, store.ErrConflict) {
		t.Fatal("simultaneous destination capacity must not invent a fifth leftover portion", err)
	}
	view := plannedLeftoverView(t, f, pid)
	if view["sources"].([]store.Record)[0]["free_servings"] != "1.000000" || len(view["allocations"].([]store.Record)) != 1 {
		t.Fatal("failed allocation changed exact capacity", view)
	}
	edit := participantSaveInput(t, f, pid, mid)
	edit.Meal.Title = "Revised production review"
	planningMutation(t, f, pid, edit)
	view = plannedLeftoverView(t, f, pid)
	if view["allocations"].([]store.Record)[0]["current"] != false {
		t.Fatal("source edit did not invalidate destination review", view)
	}
	source = plannedLeftoverReviewSource(t, f, pid, mid)
	allocation := plannedLeftoverView(t, f, pid)["allocations"].([]store.Record)[0]
	if allocation["current"] != false || source["allocated_servings"] != "3.000000" {
		t.Fatal("source review silently confirmed destinations or freed stale capacity", source, allocation)
	}
	review := plannedLeftoverAllocation(t, f, pid, target, source, "3")
	review.Action, review.AllocationID, review.AllocationVersion = "allocation_review", store.String(allocation["id"]), store.Int(allocation["version"])
	if _, err = f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), review); err != nil {
		t.Fatal("explicit destination review", err)
	}
	if plannedLeftoverView(t, f, pid)["allocations"].([]store.Record)[0]["current"] != true {
		t.Fatal("reviewed forecast still unresolved")
	}
	if err = f.pool.QueryRow(f.ctx, counts).Scan(&after); err != nil || before != after {
		t.Fatal("forecast posted physical stock/cooking/reservations/procurement", before, after, err)
	}
}

func TestPlanningLeftoversChronologyGenericMutationDeletionAndCopy(t *testing.T) {
	f, pid, mid, target := plannedLeftoverFixture(t)
	source := plannedLeftoverReviewSource(t, f, pid, mid)
	in := plannedLeftoverAllocation(t, f, pid, target, source, "2")
	if _, err := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("mealplan")
	e, _ := m.Entity("meals")
	current, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, target, value.UUID(), value.UUID(),
		auditMutation(t, f, "mealplan", store.Record{"planned_on": "2026-10-23", "meal_slot": "lunch"}, current.Version)); err == nil {
		t.Fatal("generic CRUD bypassed same-day canonical chronology")
	}
	copyInput := planningInput(t, f, pid)
	copyInput.Action, copyInput.Name, copyInput.Week, copyInput.TargetDate = "copy", "Independent forecast week", "2026-10-22", "2026-11-05"
	copied := planningMutation(t, f, pid, copyInput)
	newPlan := store.String(copied["plan_id"])
	view, err := f.app.Store.PlanningLeftovers(f.ctx, f.identity, newPlan, "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	sources, allocations := view["sources"].([]store.Record), view["allocations"].([]store.Record)
	if len(sources) != 1 || len(allocations) != 1 || sources[0]["id"] == source["id"] || allocations[0]["current"] != true || sources[0]["source_meal_id"] == mid || allocations[0]["target_meal_id"] == target {
		t.Fatal("copy retained old identities or lost current independent forecast", view)
	}
	deleteInput := planningInput(t, f, pid)
	meal, _ := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	deleteInput.Action, deleteInput.MealID, deleteInput.Version = "delete", mid, meal.Version
	planningMutation(t, f, pid, deleteInput)
	view = plannedLeftoverView(t, f, pid)
	if len(view["sources"].([]store.Record)) != 0 || len(view["allocations"].([]store.Record)) != 0 {
		t.Fatal("source cascade retained forecast authority", view)
	}
	if _, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, target); err != nil {
		t.Fatal("source deletion removed the independent unresolved destination", err)
	}
}

func TestPlanningLeftoversCopyCrossingWindowRefusesAtomically(t *testing.T) {
	f, pid, mid, _ := plannedLeftoverFixture(t)
	target := plannedLeftoverTarget(t, f, pid, "2026-10-29", "lunch", "2")
	source := plannedLeftoverReviewSource(t, f, pid, mid)
	in := plannedLeftoverAllocation(t, f, pid, target, source, "2")
	if _, err := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	copyInput := planningInput(t, f, pid)
	copyInput.Action, copyInput.Name, copyInput.Week, copyInput.TargetDate = "copy", "Cannot conceal outside leftovers", "2026-10-22", "2026-11-05"
	if _, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), copyInput); !errors.Is(err, store.ErrConflict) {
		t.Fatal("crossing copy must return a named reconciliation conflict", err)
	}
	var plans int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM mealplan.plans WHERE household_id=$1", f.identity.HouseholdID).Scan(&plans); err != nil || plans != 1 {
		t.Fatal("refused crossing copy left a partial plan", plans, err)
	}
}

func TestPlanningExtensionsSchemaRepeatReadinessAndFutureFence(t *testing.T) {
	f := newIntegrationFixture(t)
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("complete staged migration repeat", err)
		}
	}
	var policies int
	var planning, pantry, fuel bool
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.audit_policies),mealplan.expansion_ready(),pantry.prepared_capture_ready(),fuel.module_ready()`).Scan(&policies, &planning, &pantry, &fuel); err != nil || policies != 165 || !planning || !pantry || !fuel {
		t.Fatal("joint planning/optional-module compatibility", policies, planning, pantry, fuel, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1", database.ExpectedVersion("core")+1); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT fuel.overview_ready()").Scan(&fuel); err != nil || fuel {
		t.Fatal("unknown future Core must remain denied", fuel, err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1", database.ExpectedVersion("core")); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("restored current contract", err)
	}
}

func TestPlanningLeftoversConcurrentDestinationsCannotDoublePromiseCapacity(t *testing.T) {
	f, pid, mid, first := plannedLeftoverFixture(t)
	second := plannedLeftoverTarget(t, f, pid, "2026-10-25", "lunch", "3")
	source := plannedLeftoverReviewSource(t, f, pid, mid)
	inputs := []store.PlanningLeftoverInput{
		plannedLeftoverAllocation(t, f, pid, first, source, "3"),
		plannedLeftoverAllocation(t, f, pid, second, source, "3"),
	}
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	var workers sync.WaitGroup
	for _, in := range inputs {
		workers.Add(1)
		go func(in store.PlanningLeftoverInput) {
			defer workers.Done()
			<-start
			_, err := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in)
			results <- err
		}(in)
	}
	close(start)
	workers.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, store.ErrConflict):
			conflicts++
		default:
			t.Fatal("unexpected concurrent result", err)
		}
	}
	view := plannedLeftoverView(t, f, pid)
	if passed != 1 || conflicts != 1 || len(view["allocations"].([]store.Record)) != 1 || view["sources"].([]store.Record)[0]["allocated_servings"] != "3.000000" {
		t.Fatal("two destinations double-promised four forecast portions", passed, conflicts, view)
	}
}
