//go:build integration

package web

import (
	"context"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestPlanningPurchasePopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	// Keep the real fixture password hash, not an invented credential or a
	// hand-authored session. Current authentication resumes only after upgrade.
	var passwordHash string
	if err := f.owner.QueryRow(f.ctx, "SELECT password_hash FROM core.users WHERE id=$1", f.identity.UserID).Scan(&passwordHash); err != nil {
		t.Fatal("read existing fixture password hash", err)
	}
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 20, "shopping": 4, "fuel": 2, "recipes": 4, "pantry": 8, "mealplan": 4, "expenses": 3}
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
		if pinned, ok := prior[schema]; ok {
			version = pinned
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal("install the actual historical audit triggers", err)
	}
	pid, mid, cid, lot := seedPriorPlanningPurchase(t, f, passwordHash)
	var historical bool
	err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 AND bool_and(version=20 AND NOT dirty) FROM core.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=4 AND NOT dirty) FROM shopping.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=2 AND NOT dirty) FROM fuel.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=4 AND NOT dirty) FROM recipes.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=8 AND NOT dirty) FROM pantry.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=4 AND NOT dirty) FROM mealplan.schema_migrations)
 AND (SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM expenses.schema_migrations)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='core.sessions'::regclass AND attname='reauthenticated_at' AND NOT attisdropped)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='mealplan.meals'::regclass AND attname='guest_count' AND NOT attisdropped)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='mealplan.plans'::regclass AND attname='notification_active_since' AND NOT attisdropped)
 AND to_regclass('core.passkey_credentials') IS NULL AND NOT EXISTS(SELECT 1 FROM core.sessions)
 AND core.audit_ready() AND core.planning_bindings_ready() AND pantry.projection_ready()`).Scan(&historical)
	if err != nil || !historical {
		t.Fatal("fixture is not the actual historical schema without modern authentication columns or sessions", historical, err)
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
 (SELECT count(*) FROM core.planning_purchase_requirements),(SELECT count(*) FROM core.planning_purchase_operations)`, lot).Scan(&stock, &requirements, &operations); err != nil || stock != "0.375001" || requirements != 0 || operations != 0 {
		t.Fatal("upgrade inferred active scope/purchases or changed exact stock", stock, requirements, operations, err)
	}
	f.token, f.identity, err = f.app.Auth.Login(f.ctx, f.app.Cfg.AdminUsername, integrationPassword, "", "127.0.0.1", "Upgraded planning purchases fixture")
	if err != nil {
		t.Fatal("normal authentication after forward migration", err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}})
	if err != nil || !view.Ready || len(view.Lines) != 1 || purchaseDesiredTotal(t, view) != "0" {
		t.Fatal("prior mapped source unavailable to explicit read-only preview", view, err)
	}
}

// Seed only columns and write contracts actually present in the pinned schema.
// Existing database guards and deferred recipe capture remain enabled.
func seedPriorPlanningPurchase(t *testing.T, f *integrationFixture, passwordHash string) (pid, mid, cid, lot string) {
	t.Helper()
	hh, uid, member := f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID
	bootstrap, err := audit.Begin(audit.WithSystem(f.ctx, "bootstrap"), f.pool)
	if err != nil {
		t.Fatal("begin historical bootstrap", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = bootstrap.Rollback(ctx)
	}()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO core.households(id,name) VALUES($1,$2)`, []any{hh, f.app.Cfg.HouseholdName}},
		{`INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) VALUES($1,$2,$3,$4,false)`, []any{uid, f.app.Cfg.AdminUsername, "Administrator", passwordHash}},
		{`INSERT INTO core.memberships(id,household_id,user_id,role) VALUES($1,$2,$3,'admin')`, []any{member, hh, uid}},
	} {
		if _, err = bootstrap.Exec(f.ctx, query.sql, query.args...); err != nil {
			t.Fatal("seed historical household identity", err)
		}
	}
	// The registry contributes only static module IDs/defaults, never a current
	// Store write, authentication query or cached generation against Core20.
	for _, m := range f.app.Store.Modules {
		if _, err = bootstrap.Exec(f.ctx, `INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES($1,$1,'0.1.0')`, m.ID); err != nil {
			t.Fatal("seed historical module installation", err)
		}
		state := "disabled"
		if m.DefaultEnabled || m.ID == "mealplan" || m.ID == "recipes" || m.ID == "pantry" || m.ID == "shopping" {
			state = "enabled"
		}
		if _, err = bootstrap.Exec(f.ctx, `INSERT INTO core.module_states(household_id,module_id,state,changed_by) VALUES($1,$2,$3,$4)`, hh, m.ID, state, uid); err != nil {
			t.Fatal("seed historical module state", err)
		}
	}
	if err = bootstrap.Commit(f.ctx); err != nil {
		t.Fatal("commit historical household identity", err)
	}
	var day string
	if err = f.pool.QueryRow(f.ctx, `SELECT ((clock_timestamp() AT TIME ZONE timezone)::date+1)::text FROM core.households WHERE id=$1`, hh).Scan(&day); err != nil {
		t.Fatal(err)
	}
	product, location, rid := value.UUID(), value.UUID(), value.UUID()
	pid, mid, cid, lot = value.UUID(), value.UUID(), value.UUID(), value.UUID()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, uid, hh), value.UUID())
	seed, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal("begin historical domain seed", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = seed.Rollback(cleanup)
	}()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES($1,$2,$3,'kg',$4)`, []any{product, hh, "Rice prior-planning-purchases", uid}},
		{`INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,$3,$4)`, []any{location, hh, "Shelf prior-planning-purchases", uid}},
		{`INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,created_by) VALUES($1,$2,$3,$4,'2026-01-01',$5)`, []any{lot, hh, product, location, uid}},
		{`INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,$3::numeric,'receive',$4)`, []any{hh, lot, "0.375001", uid}},
		{`INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,$3,$4,$4,$5)`, []any{pid, hh, "Existing mapped plan", day, uid}},
		{`INSERT INTO mealplan.meals(id,household_id,plan_id,title,planned_on,kind,status,servings,created_by) VALUES($1,$2,$3,$4,$5,'recipe','planned',3,$6)`, []any{mid, hh, pid, "Existing mapped recipe", day, uid}},
		{`INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,$3,2,$4)`, []any{rid, hh, "Existing immutable source", uid}},
		{`INSERT INTO recipes.ingredients(household_id,recipe_id,position,name,quantity,unit_code,created_by) VALUES($1,$2,1,$3,0.25,'kg',$4)`, []any{hh, rid, "Existing mapped ingredient", uid}},
		{`INSERT INTO recipes.steps(household_id,recipe_id,position,instruction,timer_seconds,created_by) VALUES($1,$2,1,$3,NULL,$5),($1,$2,2,$4,90,$5)`, []any{hh, rid, "Mix everything", "Wait", uid}},
	} {
		if _, err = seed.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal("seed real historical domain row", err)
		}
	}
	// The real deferred Recipes2 trigger, still present in Recipes4, captures
	// the complete immutable version at this commit. Never fabricate its JSON.
	if err = seed.Commit(ctx); err != nil {
		t.Fatal("commit historical recipe snapshot and stock projection", err)
	}
	tag, err := audit.Exec(ctx, f.pool, `INSERT INTO mealplan.components
 (id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT $1,$2,$3,0,3,v.recipe_id,v.id,s.data_generation,v.revision,v.snapshot,$5
 FROM recipes.versions v JOIN core.module_states s ON s.household_id=v.household_id AND s.module_id='recipes' AND s.state='enabled'
 WHERE v.household_id=$2 AND v.recipe_id=$4 ORDER BY v.revision DESC LIMIT 1`, cid, hh, mid, rid, uid)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("historical client copied no genuine committed recipe snapshot", tag.RowsAffected(), err)
	}
	tag, err = audit.Exec(ctx, f.pool, `INSERT INTO core.planning_ingredient_bindings
 (household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,product_id,pantry_generation,unit_code,created_by,changed_by)
 SELECT c.household_id,m.data_generation,$3,c.meal_id,c.id,(c.snapshot->'ingredients'->0->>'id')::uuid,
 sha256(convert_to(c.snapshot::text,'UTF8')),$4,p.data_generation,'kg',$5,$5
 FROM mealplan.components c JOIN core.module_states m ON m.household_id=c.household_id AND m.module_id='mealplan' AND m.state='enabled'
 JOIN core.module_states p ON p.household_id=c.household_id AND p.module_id='pantry' AND p.state='enabled'
 WHERE c.id=$1 AND c.household_id=$2`, cid, hh, pid, product, uid)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("historical client failed to confirm exact ingredient binding", tag.RowsAffected(), err)
	}
	return pid, mid, cid, lot
}
