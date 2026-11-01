//go:build integration

package web

import (
	"os"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestPlanningPurchasePopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 20, "shopping": 4, "fuel": 2, "recipes": 4, "pantry": 8, "mealplan": 4, "expenses": 3}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
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
	// Seed the actual Recipes4 write shape in one transaction. The current
	// editor writes Recipes5 metadata and cannot create a historical fixture.
	rid := value.UUID()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	seed, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal("begin prior recipe write", err)
	}
	defer func() { _ = seed.Rollback(f.ctx) }()
	if _, err = seed.Exec(ctx, `INSERT INTO recipes.recipes
 (id,household_id,title,base_servings,created_by)
 VALUES($1,$2,'Existing immutable source',2,$3)`, rid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal("prior recipe metadata", err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO recipes.ingredients
 (household_id,recipe_id,position,name,quantity,unit_code,created_by)
 VALUES($1,$2,1,'Existing mapped ingredient',0.25,'kg',$3)`, f.identity.HouseholdID, rid, f.identity.UserID); err != nil {
		t.Fatal("prior recipe exact ingredient", err)
	}
	if _, err = seed.Exec(ctx, `INSERT INTO recipes.steps
 (household_id,recipe_id,position,instruction,timer_seconds,created_by)
 VALUES($1,$2,1,'Mix everything',NULL,$3),($1,$2,2,'Wait',90,$3)`,
		f.identity.HouseholdID, rid, f.identity.UserID); err != nil {
		t.Fatal("prior recipe ordered steps", err)
	}
	if err = seed.Commit(ctx); err != nil {
		t.Fatal("commit prior immutable recipe version", err)
	}
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the real prior-schema client contract. The current weekly Store reads
	// Mealplan7 participant fields and must not be run against historical Mealplan4.
	cid := value.UUID()
	if _, err = audit.Exec(ctx, f.pool, `INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT $1,$2,$3,0,3,v.recipe_id,v.id,$5,v.revision,v.snapshot,$6 FROM recipes.versions v WHERE v.household_id=$2 AND v.id=$4`,
		cid, f.identity.HouseholdID, mid, source["source_version_id"], store.Int(source["source_generation"]), f.identity.UserID); err != nil {
		t.Fatal("prior client frozen component", err)
	}
	if _, err = audit.Exec(ctx, f.pool, `INSERT INTO core.planning_ingredient_bindings(household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,product_id,pantry_generation,unit_code,created_by,changed_by)
 SELECT c.household_id,$2,$3,c.meal_id,c.id,(c.snapshot->'ingredients'->0->>'id')::uuid,sha256(convert_to(c.snapshot::text,'UTF8')),$4,$5,'kg',$6,$6
 FROM mealplan.components c WHERE c.id=$1 AND c.household_id=$7`, cid, f.app.Store.States()["mealplan"].Generation, pid, product["id"], f.app.Store.States()["pantry"].Generation, f.identity.UserID, f.identity.HouseholdID); err != nil {
		t.Fatal("prior client confirmed binding", err)
	}
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
