//go:build integration

package web

import (
	"errors"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func planningBindingFixture(t *testing.T) (*integrationFixture, string, string, string, string, store.Record) {
	t.Helper()
	return planningBindingRecipeFixture(t, nil)
}

func planningBindingRecipeFixture(t *testing.T, ingredients []store.RecipeRow) (*integrationFixture, string, string, string, string, store.Record) {
	t.Helper()
	f, pid, mid, product, _ := reservationFixture(t)
	recipe := recipeInput(t, f)
	recipe.Data = recipeData(t, store.Record{"title": "Two independently scaled ingredients", "base_servings": "1"})
	recipe.Ingredients = []store.RecipeRow{
		{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "First source", "quantity": "0.6", "unit_code": "kg"})},
		{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Second source", "quantity": "600", "unit_code": "g"})},
	}
	if ingredients != nil {
		recipe.Ingredients = ingredients
	}
	rid := recipeSave(t, f, "", recipe)
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	in := planningInput(t, f, pid)
	in.Action, in.MealID, in.Version = "save", mid, meal.Version
	in.Meal = store.PlanningMealInput{Title: "Frozen ingredient source", PlannedOn: meal.PlannedOn, Slot: meal.Slot, Kind: "recipe", Status: "planned", Servings: "1"}
	in.Components = []store.PlanningComponentInput{{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "1"}}
	planningMutation(t, f, pid, in)
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil || len(meal.Components) != 1 {
		t.Fatal("binding source fixture", meal, err)
	}
	cid := meal.Components[0].ID
	var ingredient string
	if err = f.pool.QueryRow(f.ctx, "SELECT snapshot->'ingredients'->0->>'id' FROM mealplan.components WHERE id=$1", cid).Scan(&ingredient); err != nil {
		t.Fatal(err)
	}
	return f, pid, mid, cid, ingredient, product
}

func planningBindingReview(t *testing.T, f *integrationFixture, pid, mid, cid, ingredient string, product store.Record) store.PlanningIngredientBindingInput {
	t.Helper()
	in := store.PlanningIngredientBindingInput{Generation: f.app.Store.States()["mealplan"].Generation,
		PantryGeneration: f.app.Store.States()["pantry"].Generation}
	if err := f.pool.QueryRow(f.ctx, `SELECT p.version,m.version,c.version,encode(sha256(convert_to(c.snapshot::text,'UTF8')),'hex'),
 coalesce(b.id::text,''),coalesce(b.version,0) FROM mealplan.plans p JOIN mealplan.meals m ON m.household_id=p.household_id AND m.plan_id=p.id
 JOIN mealplan.components c ON c.household_id=m.household_id AND c.meal_id=m.id
 LEFT JOIN core.planning_ingredient_bindings b ON b.household_id=c.household_id AND b.mealplan_generation=$5 AND b.component_id=c.id AND b.ingredient_id=$6
 WHERE p.household_id=$1 AND p.id=$2 AND m.id=$3 AND c.id=$4`, f.identity.HouseholdID, pid, mid, cid, in.Generation, ingredient).
		Scan(&in.PlanVersion, &in.MealVersion, &in.ComponentVersion, &in.SourceHash, &in.BindingID, &in.Version); err != nil {
		t.Fatal(err)
	}
	if product != nil {
		in.ProductID = store.String(product["id"])
		if err := f.pool.QueryRow(f.ctx, "SELECT version,unit_code FROM pantry.products WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, in.ProductID).Scan(&in.ProductVersion, &in.Unit); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

func TestPlanningIngredientBindingReplayAndClearedIdentity(t *testing.T) {
	f, pid, mid, cid, ingredient, _ := planningBindingFixture(t)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "First confirmed target", "unit_code": "kg"})
	in := planningBindingReview(t, f, pid, mid, cid, ingredient, product)
	in.SourceHash = strings.ToUpper(in.SourceHash)
	key := value.UUID()
	result, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, key, value.UUID(), in)
	if err != nil || store.Int(result["applied_version"]) != 1 {
		t.Fatal("confirmed plan mapping", result, err)
	}
	bindingID := store.String(result["applied_id"])
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.components SET servings=2 WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET name='Renamed after commit' WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	in.SourceHash = strings.ToLower(in.SourceHash)
	replay, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, key, value.UUID(), in)
	if err != nil || replay["replayed"] != true || store.String(replay["applied_id"]) != bindingID || store.Int(replay["applied_version"]) != 1 {
		t.Fatal("canonical original proof after portion/product metadata change", replay, err)
	}
	replacement, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Replacement confirmed target", "unit_code": "kg"})
	replacementInput := planningBindingReview(t, f, pid, mid, cid, ingredient, replacement)
	if _, err = f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, value.UUID(), value.UUID(), replacementInput); err != nil {
		t.Fatal("rebind current source", err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "DELETE FROM pantry.products WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, product["id"]); err != nil {
		t.Fatal("delete original target after rebind", err)
	}
	replay, err = f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, key, value.UUID(), in)
	if err != nil || replay["replayed"] != true || store.Int(replay["applied_version"]) != 1 || store.String(replay["binding"].(store.Record)["product_id"]) != store.String(replacement["id"]) {
		t.Fatal("original proof depends on deleted original target or revived it", replay, err)
	}
	freshCreate := planningBindingReview(t, f, pid, mid, cid, ingredient, replacement)
	freshCreate.BindingID, freshCreate.Version = "", 0
	clearInput := planningBindingReview(t, f, pid, mid, cid, ingredient, nil)
	cleared, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, value.UUID(), value.UUID(), clearInput)
	if err != nil || store.String(cleared["applied_id"]) != bindingID || store.Int(cleared["applied_version"]) != 3 || cleared["binding"].(store.Record)["assigned"] != false {
		t.Fatal("unmap lost source identity", cleared, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE household_id=$1 AND module_id='mealplan'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, key, value.UUID(), freshCreate); !errors.Is(err, store.ErrConflict) {
		t.Fatal("fresh source/product metadata allowed a create to revive the retained cleared binding", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.SetPlanningIngredientBinding(f.ctx, foreign, pid, mid, cid, ingredient, value.UUID(), value.UUID(), clearInput); err == nil {
		t.Fatal("foreign household changed a plan mapping")
	}
}

func TestPlanningIngredientBindingPrivilegesDisabledCleanupAndCascade(t *testing.T) {
	f, pid, mid, cid, ingredient, _ := planningBindingFixture(t)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Mapped product without lots", "unit_code": "kg"})
	in := planningBindingReview(t, f, pid, mid, cid, ingredient, product)
	result, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"DELETE FROM core.planning_ingredient_bindings", "TRUNCATE core.planning_ingredient_bindings",
		"SELECT mealplan.remove_deleted_ingredient_bindings()",
		"UPDATE core.planning_ingredient_bindings SET ingredient_id=gen_random_uuid()",
		"UPDATE core.planning_ingredient_bindings SET unit_code='g'",
	} {
		if _, err = f.pool.Exec(f.ctx, query); err == nil {
			t.Fatal("runtime bypassed binding guard", query)
		}
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.components DISABLE TRIGGER planning_binding_component_delete"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("missing privileged cleanup hook accepted")
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.components ENABLE TRIGGER planning_binding_component_delete"); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "DELETE FROM pantry.products WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, product["id"]); err != nil {
		t.Fatal("product cleanup depends on enabled planning", err)
	}
	var assigned bool
	var version int64
	if err = f.pool.QueryRow(f.ctx, "SELECT product_id IS NOT NULL,version FROM core.planning_ingredient_bindings WHERE id=$1", result["applied_id"]).Scan(&assigned, &version); err != nil || assigned || version != 2 {
		t.Fatal("target cleanup removed identity or retained deleted product", assigned, version, err)
	}
	if _, err = audit.Exec(ctx, f.pool, "DELETE FROM mealplan.meals WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, mid); err != nil {
		t.Fatal("component cascade cleanup requires app DELETE privilege", err)
	}
	var remaining int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.planning_ingredient_bindings WHERE id=$1", result["applied_id"]).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("actual source deletion retained plan mapping payload", remaining, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningIngredientBindingConcurrentActors(t *testing.T) {
	f, pid, mid, cid, ingredient, first := planningBindingFixture(t)
	second, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Competing target", "unit_code": "kg"})
	other := shoppingTestMember(t, f, "concurrent-planning-editor", "editor")
	inputs := []store.PlanningIngredientBindingInput{planningBindingReview(t, f, pid, mid, cid, ingredient, first), planningBindingReview(t, f, pid, mid, cid, ingredient, second)}
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, actor := range []auth.Identity{f.identity, other} {
		go func(actor auth.Identity, in store.PlanningIngredientBindingInput) {
			<-start
			_, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, actor, pid, mid, cid, ingredient, value.UUID(), value.UUID(), in)
			results <- err
		}(actor, inputs[i])
	}
	close(start)
	wins := 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal("unexpected contender result", err)
		}
	}
	var count int
	var version int64
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*),max(version) FROM core.planning_ingredient_bindings WHERE component_id=$1 AND ingredient_id=$2", cid, ingredient).Scan(&count, &version); err != nil || wins != 1 || count != 1 || version != 1 {
		t.Fatal("two actors overwrote an unreviewed mapping", wins, count, version, err)
	}
}
