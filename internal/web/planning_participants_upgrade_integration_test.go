//go:build integration

package web

import (
	"os"
	"testing"

	"household/internal/database"
	"household/internal/value"
)

func TestPlanningParticipantsPopulatedPriorUpgradePreservesIndependentQuantities(t *testing.T) {
	f, _, _, cid, _, _ := planningBindingFixture(t)
	var snapshot []byte
	var recipeID, versionID string
	var sourceGeneration, sourceRevision int64
	if err := f.pool.QueryRow(f.ctx, "SELECT snapshot,source_recipe_id,source_version_id,source_generation,source_revision FROM mealplan.components WHERE id=$1", cid).Scan(&snapshot, &recipeID, &versionID, &sourceGeneration, &sourceRevision); err != nil {
		t.Fatal(err)
	}
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "documents" {
			version = 1 // Documents2 requires the later complete Core33 predecessor.
		}
		if schema == "meters" {
			version = 1 // Meters2 requires the later complete Core32 predecessor.
		}
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		if schema == "recipes" {
			version = 4 // Recipes5 belongs to the later Core27 expansion.
		}
		if schema == "fuel" {
			version = 2 // Preserve the genuine prior Core22 installation.
		}
		if schema == "core" {
			version = 22
		}
		if schema == "pantry" {
			version = 11 // Pantry12 is outside this genuine Core22/Fuel2 predecessor.
		}
		if schema == "mealplan" {
			version = 6
		}
		if schema == "expenses" {
			version = 3 // Expenses4 requires the later Core25 contract.
		}
		if schema == "shopping" {
			version = 7 // Shopping8 requires Core26 or its approved producer interface.
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, plan, meal, component := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior participant upgrade')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-participant','Prior member','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hh, user}},
		{"INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,'Prior week','2026-10-22','2026-10-28',$3)", []any{plan, hh, user}},
		{"INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,meal_slot,title,servings,kind,status,created_by) VALUES($1,$2,$3,'2026-10-26','dinner','Prior independent quantities',1.000001,'recipe','planned',$4)", []any{meal, hh, plan, user}},
		{`INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 VALUES($1,$2,$3,0,2.500001,$4,$5,$6,$7,$8,$9)`, []any{component, hh, meal, recipeID, versionID, sourceGeneration, sourceRevision, snapshot, user}},
	} {
		if _, err := f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("participant populated migration/repeat", err)
		}
	}
	var mealQuantity, componentQuantity, mealMode, componentMode string
	var guests, roster, policies int
	if err := f.pool.QueryRow(f.ctx, `SELECT m.servings::text,c.servings::text,m.servings_mode,c.servings_mode,m.guest_count,
 (SELECT count(*) FROM mealplan.participants)
 FROM mealplan.meals m JOIN mealplan.components c ON c.household_id=m.household_id AND c.meal_id=m.id WHERE m.id=$1 AND c.id=$2`, meal, component).
		Scan(&mealQuantity, &componentQuantity, &mealMode, &componentMode, &guests, &roster); err != nil || mealQuantity != "1.000001" || componentQuantity != "2.500001" || mealMode != "manual" || componentMode != "independent" || guests != 0 || roster != 0 {
		t.Fatal("upgrade rewrote old quantities or fabricated participant intent", mealQuantity, componentQuantity, mealMode, componentMode, guests, roster, policies, err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_policies").Scan(&policies); err != nil || policies != 163 {
		t.Fatal("owner-only policy census", policies, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("participant upgrade readiness", err)
	}
}
