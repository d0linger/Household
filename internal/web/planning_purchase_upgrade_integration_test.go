//go:build integration

package web

import (
	"os"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestPlanningPurchasePopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 20, "shopping": 4, "pantry": 8, "mealplan": 4}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if pinned, ok := prior[schema]; ok {
			version = pinned
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	moduleIDs, enabled := make([]string, 0), make(map[string]bool)
	for _, m := range f.app.Store.Modules {
		moduleIDs = append(moduleIDs, m.ID)
		enabled[m.ID] = m.DefaultEnabled
	}
	if err := f.app.Auth.Bootstrap(f.ctx, moduleIDs, enabled); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET must_change_password=false WHERE username=$1", f.app.Cfg.AdminUsername); err != nil {
		t.Fatal(err)
	}
	var err error
	f.token, f.identity, err = f.app.Auth.Login(f.ctx, f.app.Cfg.AdminUsername, integrationPassword, "", "127.0.0.1", "Prior planning purchases fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var day string
	if err = f.pool.QueryRow(f.ctx, "SELECT ((clock_timestamp() AT TIME ZONE timezone)::date+1)::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	product, lot := pantryFixture(t, f, "prior-planning-purchases")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "0.375001", "reason": "receive"})
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Existing mapped plan", "starts_on": day, "ends_on": day})
	meal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": plan["id"], "title": "Existing mapped recipe", "planned_on": day, "kind": "recipe", "status": "planned", "servings": "3"})
	pid, mid := store.String(plan["id"]), store.String(meal["id"])
	recipe := recipeInput(t, f)
	recipe.Data = recipeData(t, store.Record{"title": "Existing immutable source", "base_servings": "2"})
	recipe.Ingredients = []store.RecipeRow{{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Existing mapped ingredient", "quantity": "0.25", "unit_code": "kg"})}}
	rid := recipeSave(t, f, "", recipe)
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	input := planningInput(t, f, pid)
	input.Action, input.MealID, input.Version = "save", mid, store.Int(meal["version"])
	input.Meal = store.PlanningMealInput{Title: "Existing mapped recipe", PlannedOn: day, Slot: "dinner", Kind: "recipe", Status: "planned", Servings: "3"}
	input.Components = []store.PlanningComponentInput{{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "3"}}
	planningMutation(t, f, pid, input)
	var cid string
	if err = f.pool.QueryRow(f.ctx, "SELECT id FROM mealplan.components WHERE meal_id=$1", mid).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	var before, after string
	const fingerprint = `SELECT md5(c.snapshot::text||b::text) FROM mealplan.components c
 JOIN core.planning_ingredient_bindings b ON b.household_id=c.household_id AND b.component_id=c.id WHERE c.id=$1`
	if err = f.pool.QueryRow(f.ctx, fingerprint, cid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, fingerprint, cid).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade rewrote existing frozen source or binding", before, after, err)
	}
	var stock string
	var requirements, operations int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1),
 (SELECT count(*) FROM core.planning_purchase_requirements),(SELECT count(*) FROM core.planning_purchase_operations)`, lot["id"]).Scan(&stock, &requirements, &operations); err != nil || stock != "0.375001" || requirements != 0 || operations != 0 {
		t.Fatal("upgrade inferred active scope/purchases or changed exact stock", stock, requirements, operations, err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}})
	if err != nil || !view.Ready || len(view.Lines) != 1 || purchaseDesiredTotal(t, view) != "0" {
		t.Fatal("prior mapped source unavailable to explicit read-only preview", view, err)
	}
}
