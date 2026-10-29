//go:build integration

package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditTrailContract(t *testing.T) {
	f := newIntegrationFixture(t)
	t.Run("all_domain_tables_and_policies_are_installed", func(t *testing.T) {
		privateFuel := []string{"household_state", "segment_state", "consumption_segments", "segment_jobs", "module_operations", "module_effects", "capture_transitions", "legacy_operation_fences"}
		authoritativeFuel := []string{"vehicles", "odometer_series", "fillups", "odometer_observations", "charging_sessions", "trips", "vehicle_costs", "maintenance_plans", "maintenance_events", "user_settings", "import_jobs", "import_rows", "capture_jobs", "integration_bindings"}
		var missing, domainTables int
		err := f.owner.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE p.table_name IS NULL),count(*)
			FROM pg_tables t LEFT JOIN core.audit_policies p ON p.schema_name=t.schemaname AND p.table_name=t.tablename
			WHERE t.schemaname=ANY($1::text[]) AND t.tablename NOT IN('schema_migrations','fuel_types')
			AND NOT(t.schemaname='pantry' AND t.tablename='lot_balances')
			AND NOT(t.schemaname='fuel' AND t.tablename=ANY($2::text[]))`,
			[]string{"shopping", "fuel", "recipes", "pantry", "expenses", "chores", "meters", "documents", "mealplan"}, privateFuel).Scan(&missing, &domainTables)
		if err != nil || missing != 0 || domainTables != 66 {
			t.Fatalf("domain coverage: missing=%d tables=%d error=%v", missing, domainTables, err)
		}
		// Dedicated API tables remain audited business facts. Only the exact
		// approved derived/protocol tables are exempt from row-value auditing.
		var fuelTables, fuelPolicies, privateTables, privatePolicies int
		if err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM pg_tables WHERE schemaname='fuel' AND tablename=ANY($1::text[])),
 (SELECT count(*) FROM core.audit_policies WHERE schema_name='fuel' AND table_name=ANY($1::text[])),
 (SELECT count(*) FROM pg_tables WHERE schemaname='fuel' AND tablename=ANY($2::text[])),
 (SELECT count(*) FROM core.audit_policies WHERE schema_name='fuel' AND table_name=ANY($2::text[]))`, authoritativeFuel, privateFuel).
			Scan(&fuelTables, &fuelPolicies, &privateTables, &privatePolicies); err != nil || fuelTables != 14 || fuelPolicies != 14 || privateTables != 8 || privatePolicies != 0 {
			t.Fatalf("Fuel classification: facts=%d policies=%d private=%d privatePolicies=%d error=%v", fuelTables, fuelPolicies, privateTables, privatePolicies, err)
		}
		pantryExtensions := []string{"leftover_operations", "prepared_lots", "prepared_transfers", "foodfacts_cache", "foodfacts_operations", "foodfacts_provenance"}
		var extensionTables, extensionPolicies int
		var redacted bool
		if err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM pg_tables WHERE schemaname='pantry' AND tablename=ANY($1::text[])),
 count(*),coalesce(bool_and(label_column IS NULL AND NOT(safe_columns && ARRAY['name','dish_name','label','location_name','result','source_snapshot','request_hash','payload_sha256','selected_fields']::text[])),false)
 FROM core.audit_policies WHERE schema_name='pantry' AND table_name=ANY($1::text[])`, pantryExtensions).
			Scan(&extensionTables, &extensionPolicies, &redacted); err != nil || extensionTables != 6 || extensionPolicies != 6 || !redacted {
			t.Fatalf("Pantry extension classification: tables=%d policies=%d redacted=%t error=%v", extensionTables, extensionPolicies, redacted, err)
		}
		var ready bool
		if err = f.pool.QueryRow(f.ctx, "SELECT core.audit_ready()").Scan(&ready); err != nil || !ready {
			t.Fatalf("registered table triggers not ready: %t/%v", ready, err)
		}
		if _, err = f.owner.Exec(f.ctx, "CREATE TABLE core.audit_unclassified_fixture(id uuid PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		if err = f.pool.QueryRow(f.ctx, "SELECT core.audit_ready()").Scan(&ready); err != nil || ready {
			t.Fatalf("unclassified future table was accepted: %t/%v", ready, err)
		}
		if _, err = f.owner.Exec(f.ctx, "DROP TABLE core.audit_unclassified_fixture"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("create_update_noop_and_delete", func(t *testing.T) {
		created, request := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Audit before"})
		rid := store.String(created["id"])
		var actor, kind, name, role string
		if err := f.owner.QueryRow(f.ctx, `SELECT actor_user_id,actor_kind,changes->'name'->>'after',db_role FROM core.audit_log
			WHERE resource_id=$1 AND action='CREATE' AND request_id=$2`, rid, request).Scan(&actor, &kind, &name, &role); err != nil {
			t.Fatal(err)
		}
		if actor != f.identity.UserID || kind != "user" || name != "Audit before" || role != "household_app" {
			t.Fatalf("create provenance: actor=%s kind=%s name=%s role=%s", actor, kind, name, role)
		}
		m, _ := f.app.Store.Module("shopping")
		e, _ := m.Entity("lists")
		request = value.UUID()
		updated, err := f.app.Store.Write(f.ctx, f.identity, m, e, rid, value.UUID(), request, auditMutation(t, f, "shopping", store.Record{"name": "Audit after"}, 1))
		if err != nil {
			t.Fatal(err)
		}
		var before, after string
		if err = f.owner.QueryRow(f.ctx, `SELECT changes->'name'->>'before',changes->'name'->>'after' FROM core.audit_log WHERE resource_id=$1 AND action='UPDATE' AND request_id=$2`, rid, request).Scan(&before, &after); err != nil || before != "Audit before" || after != "Audit after" {
			t.Fatalf("update diff: %q -> %q: %v", before, after, err)
		}
		noopRequest := value.UUID()
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), noopRequest)
		if _, err = audit.Exec(ctx, f.pool, "UPDATE shopping.lists SET name=name WHERE id=$1", rid); err != nil {
			t.Fatal(err)
		}
		auditCount(t, f, noopRequest, 0)
		request = value.UUID()
		if err = f.app.Store.Delete(f.ctx, f.identity, m, e, rid, store.Int(updated["version"]), f.app.Store.States()["shopping"].Generation, request); err != nil {
			t.Fatal(err)
		}
		var hasAfter bool
		if err = f.owner.QueryRow(f.ctx, `SELECT changes->'name'->>'before',(changes->'name'->>'after_present')::boolean FROM core.audit_log WHERE resource_id=$1 AND action='DELETE' AND request_id=$2`, rid, request).Scan(&before, &hasAfter); err != nil || before != "Audit after" || hasAfter {
			t.Fatalf("delete snapshot: before=%q afterPresent=%t error=%v", before, hasAfter, err)
		}
	})

	t.Run("rollback_and_idempotency", func(t *testing.T) {
		request := value.UUID()
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), request)
		tx, err := audit.Begin(ctx, f.pool)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(f.ctx) }()
		if _, err = tx.Exec(ctx, "INSERT INTO shopping.lists(household_id,name,created_by) VALUES($1,'Rollback audit',$2)", f.identity.HouseholdID, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		auditCount(t, f, request, 0)
		m, _ := f.app.Store.Module("shopping")
		e, _ := m.Entity("lists")
		key, request := value.UUID(), value.UUID()
		in := auditMutation(t, f, "shopping", store.Record{"name": "Idempotent audit"}, 0)
		created, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", key, request, in)
		if err != nil {
			t.Fatal(err)
		}
		replayRequest := value.UUID()
		replayed, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", key, replayRequest, in)
		if err != nil || store.String(replayed["id"]) != store.String(created["id"]) {
			t.Fatalf("replay identity differs: %v", err)
		}
		auditCount(t, f, replayRequest, 0)
	})

	t.Run("null_presence_and_ignored_session_touch", func(t *testing.T) {
		list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Nullable audit"})
		item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Unquantified item"})
		var beforePresent, afterPresent, nullAfter bool
		if err := f.owner.QueryRow(f.ctx, `SELECT (changes->'quantity'->>'before_present')::boolean,(changes->'quantity'->>'after_present')::boolean,changes->'quantity'->'after'='null'::jsonb FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'`, item["id"]).Scan(&beforePresent, &afterPresent, &nullAfter); err != nil || beforePresent || !afterPresent || !nullAfter {
			t.Fatalf("absent versus SQL null: before=%t after=%t null=%t error=%v", beforePresent, afterPresent, nullAfter, err)
		}
		request := value.UUID()
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), request)
		if _, err := audit.Exec(ctx, f.pool, "UPDATE core.sessions SET last_seen_at=now(),idle_expires_at=least(now()+interval '1 hour',absolute_expires_at) WHERE id=$1", f.identity.SessionID); err != nil {
			t.Fatal(err)
		}
		auditCount(t, f, request, 0)
	})

	t.Run("exact_values_and_redacted_fields", func(t *testing.T) {
		const quantity = "9007199254.740993"
		const secret = "PRIVATE AUDIT NOTE MUST NOT APPEAR"
		recipe, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Exact audit", "base_servings": quantity, "notes": secret})
		var after, note string
		var redacted bool
		if err := f.owner.QueryRow(f.ctx, `SELECT changes->'base_servings'->>'after',changes->'notes'->>'after',(changes->'notes'->>'redacted')::boolean FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'`, recipe["id"]).Scan(&after, &note, &redacted); err != nil {
			t.Fatal(err)
		}
		if after != quantity || note != "***REDACTED***" || !redacted {
			t.Fatalf("exact/redacted values: %q %q %t", after, note, redacted)
		}
		entry, _ := auditCreate(t, f, "expenses", "entries", store.Record{"occurred_on": "2026-10-24", "description": "Audited amount", "amount_minor": "10000.01", "kind": "expense"})
		if err := f.owner.QueryRow(f.ctx, `SELECT changes->'amount_minor'->>'after' FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'`, entry["id"]).Scan(&after); err != nil || after != "1000001" {
			t.Fatalf("exact minor units: %q/%v", after, err)
		}
		var leaked bool
		if err := f.owner.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE to_jsonb(audit_log)::text LIKE $1)", "%"+secret+"%").Scan(&leaked); err != nil || leaked {
			t.Fatalf("private note persisted in audit: %t/%v", leaked, err)
		}
	})

	t.Run("cascade_and_composite_identity", func(t *testing.T) {
		recipe, _ := auditCreate(t, f, "recipes", "recipes", store.Record{"title": "Cascade audit", "base_servings": "2"})
		ingredient, _ := auditCreate(t, f, "recipes", "ingredients", store.Record{"recipe_id": recipe["id"], "name": "Rice", "quantity": "1.25", "unit_code": "kg"})
		tag, _ := auditCreate(t, f, "recipes", "tags", store.Record{"name": "Audit tag"})
		request := value.UUID()
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), request)
		if _, err := audit.Exec(ctx, f.pool, "INSERT INTO recipes.recipe_tags(household_id,recipe_id,tag_id) VALUES($1,$2,$3)", f.identity.HouseholdID, recipe["id"], tag["id"]); err != nil {
			t.Fatal(err)
		}
		var household, recipeID, tagID string
		if err := f.owner.QueryRow(f.ctx, "SELECT entity_key->>'household_id',entity_key->>'recipe_id',entity_key->>'tag_id' FROM core.audit_log WHERE request_id=$1 AND action='CREATE' AND resource_type='recipe_tags'", request).Scan(&household, &recipeID, &tagID); err != nil || household != f.identity.HouseholdID || recipeID != recipe["id"] || tagID != tag["id"] {
			t.Fatalf("composite entity key: %s/%s/%s error=%v", household, recipeID, tagID, err)
		}
		m, _ := f.app.Store.Module("recipes")
		e, _ := m.Entity("recipes")
		request = value.UUID()
		if err := f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(recipe["id"]), 1, f.app.Store.States()["recipes"].Generation, request); err != nil {
			t.Fatal(err)
		}
		var children int
		if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE request_id=$1 AND action='DELETE' AND (resource_id=$2 OR (resource_type='recipe_tags' AND entity_key->>'recipe_id'=$3))`, request, ingredient["id"], recipe["id"]).Scan(&children); err != nil || children != 2 {
			t.Fatalf("cascade audit: children=%d error=%v", children, err)
		}
	})

	t.Run("runtime_audit_privileges", func(t *testing.T) {
		for _, query := range []string{
			"INSERT INTO core.audit_log(action,outcome,request_id) VALUES('forged','success',gen_random_uuid())",
			"UPDATE core.audit_log SET action='tampered'",
			"DELETE FROM core.audit_log",
			"TRUNCATE core.audit_log",
			"UPDATE core.audit_policies SET safe_columns=ARRAY['password_hash']",
			"SELECT core.install_audit_triggers()",
			"ALTER TABLE shopping.lists DISABLE TRIGGER household_audit_change",
		} {
			if _, err := f.pool.Exec(f.ctx, query); err == nil {
				t.Fatalf("runtime was permitted forbidden audit operation: %s", query)
			}
		}
	})

	t.Run("pooled_actor_isolation", func(t *testing.T) { testAuditActorIsolation(t, f) })
	t.Run("operator_purge_includes_truncated_rows", func(t *testing.T) { testAuditPurge(t, f) })
}

func TestAuditPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	// Recreate the previous release from its actual checked-in migrations while
	// retaining the fixture's exclusive lock and guarded household_test database.
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		migratePriorAuditSchema(t, f, schema)
	}
	household, user, list, event := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Populated previous release')", []any{household}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-release','Prior release','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{household, user}},
		{"INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Preserved prior list',$3)", []any{list, household, user}},
		{"INSERT INTO shopping.items(household_id,list_id,name,quantity,created_by) VALUES($1,$2,'Preserved precise quantity',9007199254.740993,$3)", []any{household, list, user}},
	} {
		if _, err := f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	// The previous runtime could append semantic history. That grant must be
	// replaced by the constrained writer without rewriting historical content.
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO core.audit_log(event_id,household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details) VALUES($1,$2,$3,'shopping','lists',$4,'record_write','success',$5,'{\"prior_release\":true}')", event, household, user, list, value.UUID()); err != nil {
		t.Fatal(err)
	}
	ownerURL := os.Getenv("HOUSEHOLD_TEST_OWNER_URL")
	if err := database.Migrate(f.ctx, ownerURL); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, ownerURL); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var kind, changeText, name, quantity string
	var legacyDetails bool
	if err := f.owner.QueryRow(f.ctx, "SELECT actor_kind,changes::text,(details->>'prior_release')::boolean FROM core.audit_log WHERE event_id=$1", event).Scan(&kind, &changeText, &legacyDetails); err != nil || kind != "legacy" || changeText != "{}" || !legacyDetails {
		t.Fatalf("legacy history rewritten: kind=%s changes=%s details=%t error=%v", kind, changeText, legacyDetails, err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT l.name,i.quantity::text FROM shopping.lists l JOIN shopping.items i ON i.list_id=l.id WHERE l.id=$1", list).Scan(&name, &quantity); err != nil || name != "Preserved prior list" || quantity != "9007199254.740993" {
		t.Fatalf("domain data changed during upgrade: %s/%s/%v", name, quantity, err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO core.audit_log(action,outcome,request_id) VALUES('forged','success',gen_random_uuid())"); err == nil {
		t.Fatal("upgrade retained unrestricted runtime audit insertion")
	}
	request := value.UUID()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, user, household), request)
	if _, err := audit.Exec(ctx, f.pool, "UPDATE shopping.lists SET name='Updated after upgrade' WHERE id=$1", list); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.owner.QueryRow(f.ctx, "SELECT changes->'name'->>'before',changes->'name'->>'after' FROM core.audit_log WHERE request_id=$1 AND action='UPDATE'", request).Scan(&before, &after); err != nil || before != "Preserved prior list" || after != "Updated after upgrade" {
		t.Fatalf("upgraded row audit: %q -> %q/%v", before, after, err)
	}
}

func TestAuditWorkerFailures(t *testing.T) {
	f := newIntegrationFixture(t)
	unsupported, exhausted := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,kind,dedupe_key,state,attempts,lease_until)
		VALUES($1,$3,'unsupported-audit-fixture',$1::uuid::text,'pending',0,NULL),
		($2,$3,'thumbnail',$2::uuid::text,'running',5,now()-interval '1 minute')`, unsupported, exhausted, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	t.Cleanup(func() { _ = w.Close() })
	// Worker execution must clear even an accidentally inherited human actor.
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct{ id, action string }{{unsupported, "worker_job_unsupported"}, {exhausted, "worker_lease_exhausted"}} {
		var actor *string
		var kind, outcome, household string
		if err := f.owner.QueryRow(f.ctx, "SELECT actor_user_id,actor_kind,outcome,household_id FROM core.audit_log WHERE resource_id=$1 AND action=$2", expected.id, expected.action).Scan(&actor, &kind, &outcome, &household); err != nil || actor != nil || kind != "worker" || outcome != "failure" || household != f.identity.HouseholdID {
			t.Fatalf("worker terminal audit: action=%s actor=%v kind=%s outcome=%s household=%s error=%v", expected.action, actor, kind, outcome, household, err)
		}
	}
}

func migratePriorAuditSchema(t *testing.T, f *integrationFixture, schema string, override ...uint) {
	t.Helper()
	if _, err := f.owner.Exec(f.ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	// go test executes in the package directory on native and Docker test hosts.
	source, err := iofs.New(os.DirFS("../database"), "migrations/"+schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx/v5", os.Getenv("HOUSEHOLD_TEST_OWNER_URL"))
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{SchemaName: schema})
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		t.Fatal(err)
	}
	version := uint(1)
	switch schema {
	case "core":
		version = 4
	case "expenses":
		version = 2
	}
	if len(override) == 1 {
		version = override[0]
	}
	migrationErr := runner.Migrate(version)
	sourceErr, databaseErr := runner.Close()
	if migrationErr != nil && !errors.Is(migrationErr, migrate.ErrNoChange) {
		t.Fatal(migrationErr)
	}
	if err = errors.Join(sourceErr, databaseErr); err != nil {
		t.Fatal(err)
	}
}

func auditMutation(t *testing.T, f *integrationFixture, mid string, values store.Record, version int64) store.Mutation {
	t.Helper()
	data := map[string]json.RawMessage{}
	for key, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		data[key] = raw
	}
	return store.Mutation{Data: data, Version: version, Generation: f.app.Store.States()[mid].Generation}
}

func auditCreate(t *testing.T, f *integrationFixture, mid, entity string, data store.Record) (store.Record, string) {
	t.Helper()
	m, ok := f.app.Store.Module(mid)
	e, found := m.Entity(entity)
	if !ok || !found {
		t.Fatalf("unknown fixture entity %s/%s", mid, entity)
	}
	request := value.UUID()
	record, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", value.UUID(), request, auditMutation(t, f, mid, data, 0))
	if err != nil {
		t.Fatal(err)
	}
	return record, request
}

func auditCount(t *testing.T, f *integrationFixture, request string, want int) {
	t.Helper()
	var count int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE request_id=$1", request).Scan(&count); err != nil || count != want {
		t.Fatalf("request audit count=%d want=%d error=%v", count, want, err)
	}
}

func testAuditActorIsolation(t *testing.T, f *integrationFixture) {
	t.Helper()
	second := value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'audit-second','Audit second','not-a-login-hash')", second); err != nil {
		t.Fatal(err)
	}
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	type expected struct{ user, request, row string }
	expectations := []expected{{f.identity.UserID, value.UUID(), value.UUID()}, {second, value.UUID(), value.UUID()}}
	errors := make(chan error, len(expectations))
	var group sync.WaitGroup
	for _, expected := range expectations {
		group.Go(func() {
			ctx := audit.WithRequest(audit.WithActor(f.ctx, expected.user, f.identity.HouseholdID), expected.request)
			_, err := audit.Exec(ctx, pool, "INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,$3,$4)", expected.row, f.identity.HouseholdID, "Actor "+expected.user, expected.user)
			errors <- err
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range expectations {
		var actor, request string
		if err = f.owner.QueryRow(f.ctx, "SELECT actor_user_id,request_id FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'", expected.row).Scan(&actor, &request); err != nil || actor != expected.user || request != expected.request {
			t.Fatalf("pooled attribution actor=%s request=%s error=%v", actor, request, err)
		}
	}
	rid := value.UUID()
	if _, err = pool.Exec(f.ctx, "INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Unattributed database mutation',$3)", rid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	var actor *string
	var kind string
	if err = f.owner.QueryRow(f.ctx, "SELECT actor_user_id,actor_kind FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'", rid).Scan(&actor, &kind); err != nil || actor != nil || kind != "database" {
		t.Fatalf("pool leaked prior actor: actor=%v kind=%s error=%v", actor, kind, err)
	}
}

func testAuditPurge(t *testing.T, f *integrationFixture) {
	t.Helper()
	mid := "documents"
	state := f.app.Store.States()[mid]
	if err := f.app.Store.SetModule(f.ctx, f.identity, mid, true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	record, _ := auditCreate(t, f, mid, mid, store.Record{"title": "Private purge document"})
	state = f.app.Store.States()[mid]
	if err := f.app.Store.SetModule(f.ctx, f.identity, mid, false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, request := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,$3,$4,$5,$6)", request, f.identity.HouseholdID, mid, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal(err)
	}
	var actor *string
	var kind, requestedBy string
	if err := f.owner.QueryRow(f.ctx, "SELECT actor_user_id,actor_kind,details->>'requested_by' FROM core.audit_log WHERE request_id=$1 AND action='module_purged'", request).Scan(&actor, &kind, &requestedBy); err != nil || actor != nil || kind != "operator" || requestedBy != f.identity.UserID {
		t.Fatalf("purge provenance: actor=%v kind=%s requester=%s error=%v", actor, kind, requestedBy, err)
	}
	var deleted int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE request_id=$1 AND resource_id=$2 AND action='DELETE' AND actor_kind='operator'", request, record["id"]).Scan(&deleted); err != nil || deleted != 1 {
		t.Fatalf("TRUNCATE omitted deleted row: %d/%v", deleted, err)
	}
}
