//go:build integration

package web

import (
	"os"
	"testing"

	"household/internal/database"
	"household/internal/value"
)

func TestPlanningExtensionsPopulatedCore27Mealplan7UpgradePreservesFacts(t *testing.T) {
	f, _, originalMeal, _ := plannedLeftoverFixture(t)
	var snapshot []byte
	var recipe, sourceVersion string
	var sourceGeneration, sourceRevision int64
	if err := f.pool.QueryRow(f.ctx, `SELECT snapshot,source_recipe_id,source_version_id,source_generation,source_revision
 FROM mealplan.components WHERE meal_id=$1`, originalMeal).Scan(&snapshot, &recipe, &sourceVersion, &sourceGeneration, &sourceRevision); err != nil {
		t.Fatal(err)
	}
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 26, "recipes": 4, "fuel": 5, "pantry": 12, "mealplan": 7}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if pinned, ok := prior[schema]; ok {
			version = pinned
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	migratePriorAuditSchema(t, f, "core", 27)
	migratePriorAuditSchema(t, f, "recipes", 5)
	migratePriorAuditSchema(t, f, "fuel", 6)
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal(err)
	}
	var oldPolicies int
	var oldPantry, oldFuel bool
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.audit_policies),pantry.prepared_capture_ready(),fuel.module_ready()`).Scan(&oldPolicies, &oldPantry, &oldFuel); err != nil || oldPolicies != 95 || !oldPantry || !oldFuel {
		t.Fatal("genuine complete previous release required", oldPolicies, oldPantry, oldFuel, err)
	}
	hh, user, plan, meal, component, target := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{query: "INSERT INTO core.households(id,name) VALUES($1,'Previous Mealplan7 household')", args: []any{hh}},
		{query: "INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'previous-mealplan7','Previous member','fixture-no-login')", args: []any{user}},
		{query: "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", args: []any{hh, user}},
		{query: "INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,'Previous untouched plan','2026-10-22','2026-10-28',$3)", args: []any{plan, hh, user}},
		{query: `INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,meal_slot,title,servings,kind,status,created_by,servings_mode,guest_count)
 VALUES($1,$2,$3,'2026-10-23','dinner','Six produced, two diners',6,'recipe','planned',$4,'manual',2),
 ($5,$2,$3,'2026-10-24','lunch','Unreviewed legacy leftovers',2,'leftovers','planned',$4,'manual',0)`, args: []any{meal, hh, plan, user, target}},
		{query: `INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by,servings_mode)
 VALUES($1,$2,$3,0,6,$4,$5,$6,$7,$8,$9,'meal')`, args: []any{component, hh, meal, recipe, sourceVersion, sourceGeneration, sourceRevision, snapshot, user}},
	} {
		if _, err := f.owner.Exec(f.ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	projection := `SELECT jsonb_build_object('plan',(SELECT to_jsonb(p) FROM mealplan.plans p WHERE id=$1),
 'meals',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM mealplan.meals m WHERE plan_id=$1),
 'components',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM mealplan.components c JOIN mealplan.meals m ON m.id=c.meal_id WHERE m.plan_id=$1))::text`
	var before, after string
	if err := f.owner.QueryRow(f.ctx, projection, plan).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("previous-release forward migration/repeat", err)
		}
	}
	if err := f.owner.QueryRow(f.ctx, projection, plan).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade rewrote old quantities, roster, snapshots, versions or dates", before, after, err)
	}
	var invented int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM mealplan.leftover_sources)+(SELECT count(*) FROM mealplan.leftover_allocations)
 +(SELECT count(*) FROM mealplan.weekly_settings)+(SELECT count(*) FROM mealplan.calendar_feeds)+(SELECT count(*) FROM mealplan.calendar_tombstones)`).Scan(&invented); err != nil || invented != 0 {
		t.Fatal("upgrade inferred unreviewed forecasts, estimates or sharing authority", invented, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("joint upgraded compatibility", err)
	}
}
