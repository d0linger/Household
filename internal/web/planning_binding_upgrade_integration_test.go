//go:build integration

package web

import (
	"os"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/value"
)

func TestPlanningIngredientBindingPopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		if schema == "fuel" {
			version = 2 // Keep the actual predecessor compatible with Core19.
		}
		switch schema {
		case "recipes":
			version = 4 // Recipes5 belongs to the later Core27 expansion.
		case "core":
			version = 19
		case "expenses":
			version = 3 // Expenses4 requires the later Core25 contract.
		case "shopping":
			version = 4 // MP17 Shopping receipts require Core21.
		case "pantry":
			version = 7
		case "mealplan":
			version = 3
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, product, location, lot, plan, meal, component, ingredient := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	tx, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior demand installation')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-demand','Prior member','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hh, user}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Prior product','kg',$3)", []any{product, hh, user}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Prior shelf',$3)", []any{location, hh, user}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,created_by) VALUES($1,$2,$3,$4,'2026-10-25',$5)", []any{lot, hh, product, location, user}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,1.000001,'receive',$3)", []any{hh, lot, user}},
		{"INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,'Prior plan','2026-10-25','2026-10-31',$3)", []any{plan, hh, user}},
		{"INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,kind,status,created_by) VALUES($1,$2,$3,'2026-10-26','Prior frozen meal','recipe','planned',$4)", []any{meal, hh, plan, user}},
		{`INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 VALUES($1,$2,$3,0,3,$4::uuid,$5,1,1,jsonb_build_object('recipe',jsonb_build_object('id',($4::uuid)::text,'title','Prior frozen source','base_servings','2'),
 'ingredients',jsonb_build_array(jsonb_build_object('id',$6::text,'name','Prior precise ingredient','quantity','0.000001','unit_code','kg')),
 'steps',jsonb_build_array(jsonb_build_object('instruction','Prior instruction'))),$7)`, []any{component, hh, meal, value.UUID(), value.UUID(), ingredient, user}},
	} {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = f.owner.QueryRow(f.ctx, "SELECT md5(snapshot::text) FROM mealplan.components WHERE id=$1", component).Scan(&before); err != nil {
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
	var stock, after, servings string
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1),md5(snapshot::text),servings::text,
 (SELECT count(*) FROM core.planning_ingredient_bindings) FROM mealplan.components WHERE id=$2`, lot, component).Scan(&stock, &after, &servings, &count); err != nil || stock != "1.000001" || after != before || servings != "3.000000" || count != 0 {
		t.Fatal("prior stock/frozen identity changed or inferred mappings appeared", stock, before, after, servings, count, err)
	}
}
