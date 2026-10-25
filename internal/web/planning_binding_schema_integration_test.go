//go:build integration

package web

import (
	"errors"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPlanningIngredientBindingMalformedSourceAndReadiness(t *testing.T) {
	f, pid, mid, cid, ingredient, product := planningBindingFixture(t)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	for _, tc := range []struct{ name, expression string }{
		{"null other name", "jsonb_set(snapshot,'{ingredients,1,name}','null'::jsonb)"},
		{"duplicate identity", "jsonb_set(snapshot,'{ingredients,1,id}',snapshot->'ingredients'->0->'id')"},
		{"uppercase other identity", `jsonb_set(snapshot,'{ingredients,1,id}','"ABCDEF01-0000-4000-8000-000000000001"'::jsonb)`},
		{"too many ingredients", "jsonb_set(snapshot,'{ingredients}',(SELECT jsonb_agg((snapshot->'ingredients'->0)||jsonb_build_object('id',gen_random_uuid(),'position',n)) FROM generate_series(1,501) g(n)))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := value.UUID()
			// Only static test expressions are concatenated; every record identifier remains a parameter.
			query := `INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT $1,household_id,meal_id,1,servings,source_recipe_id,source_version_id,source_generation,source_revision,` + tc.expression + `,created_by FROM mealplan.components WHERE id=$2`
			if _, err := audit.Exec(ctx, f.pool, query, bad, cid); err != nil {
				t.Fatal("legacy malformed source fixture", err)
			}
			_, err := audit.Exec(ctx, f.pool, `INSERT INTO core.planning_ingredient_bindings(household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,product_id,pantry_generation,unit_code,created_by,changed_by)
 SELECT household_id,$2,$3,meal_id,id,$4,sha256(convert_to(snapshot::text,'UTF8')),$5,$6,'kg',$7,$7 FROM mealplan.components WHERE id=$1`,
				bad, f.app.Store.States()["mealplan"].Generation, pid, ingredient, product["id"], f.app.Store.States()["pantry"].Generation, f.identity.UserID)
			var pgError *pgconn.PgError
			if !errors.As(err, &pgError) || pgError.Code != "23514" {
				t.Fatal("direct SQL accepted ambiguous source or failed for an unrelated reason", err)
			}
			if _, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid}); err == nil {
				t.Fatal("read preview accepted malformed or excessive whole-meal ingredients")
			}
			if _, err = audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, bad); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct{ breakQuery, repairQuery string }{
		{"REVOKE SELECT ON core.planning_ingredient_bindings FROM household_app", "GRANT SELECT ON core.planning_ingredient_bindings TO household_app"},
		{"REVOKE UPDATE ON core.planning_ingredient_bindings FROM household_app", "GRANT UPDATE ON core.planning_ingredient_bindings TO household_app"},
		{"GRANT DELETE ON core.planning_ingredient_bindings TO household_app", "REVOKE DELETE ON core.planning_ingredient_bindings FROM household_app"},
	} {
		if _, err := f.owner.Exec(f.ctx, tc.breakQuery); err != nil {
			t.Fatal(err)
		}
		readErr := database.Ready(f.ctx, f.pool)
		if _, err := f.owner.Exec(f.ctx, tc.repairQuery); err != nil {
			t.Fatal(err)
		}
		if readErr == nil {
			t.Fatal("readiness accepted changed individual runtime privilege", tc.breakQuery)
		}
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
