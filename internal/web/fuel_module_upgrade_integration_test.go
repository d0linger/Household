//go:build integration

package web

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func fuelModuleUpgradePriorCatalog(t *testing.T, f *integrationFixture, fuelVersion ...uint) {
	t.Helper()
	priorFuel := uint(2)
	if len(fuelVersion) > 1 || len(fuelVersion) == 1 && fuelVersion[0] != 1 && fuelVersion[0] != 2 {
		t.Fatal("only genuine Fuel1/Fuel2 predecessor fixtures are supported")
	}
	if len(fuelVersion) == 1 {
		priorFuel = fuelVersion[0]
	}
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "meters" {
			version = 1 // Meters2 requires the later complete Core32 predecessor.
		}
		if schema == "chores" {
			version = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		switch schema {
		case "core":
			version = 26
		case "shopping":
			version = 8
		case "fuel":
			version = priorFuel
		case "recipes":
			version = 4 // Recipes5 belongs to the later Core27 expansion.
		case "mealplan":
			version = 7 // Mealplan8 requires the later Core28 extension stage.
		case "pantry":
			version = 11 // Pantry12 requires the later accepted Fuel5/Core26 audit catalog.
		case "expenses":
			version = 4 // Finance5 requires Core29; this is the genuine Core26 release.
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	// Install the genuine complete predecessor's audit catalog only after every
	// independent prior schema exists. Dropping Fuel alone leaves Fuel5's census.
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal("install complete prior audit catalog", err)
	}
	priorShape := "fuel.overview_ready()"
	if priorFuel == 1 {
		priorShape = "to_regprocedure('fuel.overview_ready()') IS NULL"
	}
	var priorReady, fuelShape bool
	if err := f.pool.QueryRow(f.ctx, "SELECT core.audit_ready(),"+priorShape).Scan(&priorReady, &fuelShape); err != nil || !priorReady || !fuelShape {
		t.Fatal("prior catalog is not an actual compatible Core26/Shopping8/Fuel predecessor", priorFuel, priorReady, fuelShape, err)
	}
	modules, enabled := []string{}, map[string]bool{}
	for _, descriptor := range f.app.Store.Modules {
		modules = append(modules, descriptor.ID)
		enabled[descriptor.ID] = descriptor.DefaultEnabled
	}
	if err := f.app.Auth.Bootstrap(f.ctx, modules, enabled); err != nil {
		t.Fatal("bootstrap genuine prior schema", err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET must_change_password=false WHERE username=$1", f.app.Cfg.AdminUsername); err != nil {
		t.Fatal(err)
	}
	var err error
	f.token, f.identity, err = f.app.Auth.Login(f.ctx, f.app.Cfg.AdminUsername, integrationPassword, "", "127.0.0.1", "Fuel predecessor upgrade fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func fuelModuleUpgradeDomain(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'vehicles',(SELECT jsonb_agg(to_jsonb(v)-ARRAY['tank_capacity','battery_capacity','current_series_id'] ORDER BY v.id) FROM fuel.vehicles v),
 'series',(SELECT jsonb_agg(to_jsonb(s)-ARRAY['final_reading','previous_series_id','distance_offset'] ORDER BY s.id) FROM fuel.odometer_series s),
 'fillups',(SELECT jsonb_agg(to_jsonb(f)-ARRAY['quantity_unit','currency_code','source_kind','source_job_id','source_row','source_fingerprint'] ORDER BY f.id) FROM fuel.fillups f))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestFuelModuleNativePopulatedFuel2UpgradeRetainsFactsAndOldIntentFences(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleUpgradePriorCatalog(t, f)
	generation := f.app.Store.States()["fuel"].Generation
	vehicle, series, first, second, oldKey := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	vehicleInput := auditMutation(t, f, "fuel", store.Record{"name": "SYNTHETIC preserved Fuel2 vehicle", "registration": "PRIOR-ONLY", "fuel_type": "diesel", "notes": "Original vehicle note", "next_service_on": "2026-12-01"}, 0)
	fillInput := auditMutation(t, f, "fuel", store.Record{"vehicle_id": vehicle, "odometer_series_id": series, "occurred_on": "2026-02-01", "fuel_type": "diesel",
		"odometer": "1000.123456", "quantity": "40", "total_minor": "68.00", "unit_price": "1.7", "station": "Prior station", "full_tank": true, "previous_fill_missing": false, "notes": "Original exact fillup"}, 0)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "fuel", generation)
	if err != nil {
		t.Fatal(err)
	}
	defer fuelModuleWriteRollback(tx)
	// This is an explicitly synthetic prior-state fixture, not execution of an
	// old application binary. Real App authority/session and the actual Fuel2 SQL
	// schema insert the prior facts and matching legacy cache in ONE transaction.
	// No Fuel module operation, effect, or private fence is manually manufactured.
	var vehicleJSON, fillJSON []byte
	err = tx.QueryRow(ctx, `INSERT INTO fuel.vehicles AS v(id,household_id,name,registration,fuel_type,notes,next_service_on,created_by)
 VALUES($1,$2,'SYNTHETIC preserved Fuel2 vehicle','PRIOR-ONLY','diesel','Original vehicle note','2026-12-01',$3) RETURNING to_jsonb(v)`, vehicle, f.identity.HouseholdID, f.identity.UserID).Scan(&vehicleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO fuel.odometer_series(id,household_id,vehicle_id,started_on,initial_reading,reason,created_by)
 VALUES($1,$2,$3,'2026-01-01',0,'Original historical counter',$4)`, series, f.identity.HouseholdID, vehicle, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO fuel.fillups AS f(id,household_id,vehicle_id,odometer_series_id,occurred_on,fuel_type,odometer,quantity,total_minor,unit_price,station,full_tank,previous_fill_missing,notes,created_by)
 VALUES($1,$2,$3,$4,'2026-02-01','diesel',1000.123456,40,6800,1.7,'Prior station',true,false,'Original exact fillup',$5) RETURNING to_jsonb(f)`, first, f.identity.HouseholdID, vehicle, series, f.identity.UserID).Scan(&fillJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO fuel.fillups(id,household_id,vehicle_id,odometer_series_id,occurred_on,fuel_type,odometer,quantity,total_minor,unit_price,station,full_tank,previous_fill_missing,created_by)
 VALUES($1,$2,$3,$4,'2026-02-02','diesel',2000.654321,5,850,1.7,'Original partial',false,true,$5)`, second, f.identity.HouseholdID, vehicle, series, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	for _, cached := range []struct {
		entity   string
		input    store.Mutation
		response []byte
	}{{"vehicles", vehicleInput, vehicleJSON}, {"fillups", fillInput, fillJSON}} {
		encoded, err := json.Marshal(cached.input)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(encoded)
		if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'fuel',$6,200,$7)`, f.identity.HouseholdID, f.identity.UserID, "write/fuel/"+cached.entity+"/", oldKey, hash[:], generation, cached.response); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	beforeDomain := fuelModuleUpgradeDomain(t, f)
	var oldVersion, priorCacheCount int
	var householdCurrency string
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT version FROM fuel.schema_migrations),
 (SELECT count(*) FROM core.idempotency_records WHERE module_id='fuel' AND key=$1),
 (SELECT currency_code FROM core.households WHERE id=$2)`, oldKey, f.identity.HouseholdID).Scan(&oldVersion, &priorCacheCount, &householdCurrency); err != nil || oldVersion != 2 || priorCacheCount != 2 || householdCurrency != "EUR" {
		t.Fatal("prior fixture did not bind two genuine cache operations to the one legacy key", oldVersion, priorCacheCount, householdCurrency, err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("Fuel2 to Fuel7 migration/repetition", err)
		}
		if err = database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("upgraded application not ready", err)
		}
		if after := fuelModuleUpgradeDomain(t, f); after != beforeDomain {
			t.Fatal("upgrade changed prior exact facts, flags, versions, timestamps or author")
		}
	}
	var fences, operations, effects, fuelVersion, goodDimensions int
	err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM fuel.legacy_operation_fences WHERE household_id=$1 AND user_id=$2 AND generation=$3 AND operation_key=$4),
 (SELECT count(*) FROM fuel.module_operations),(SELECT count(*) FROM fuel.module_effects),
 (SELECT version FROM fuel.schema_migrations),
 (SELECT count(*) FROM fuel.fillups WHERE household_id=$1 AND quantity_unit='l' AND currency_code='EUR' AND source_kind='manual' AND source_job_id IS NULL)`,
		f.identity.HouseholdID, f.identity.UserID, generation, oldKey).Scan(&fences, &operations, &effects, &fuelVersion, &goodDimensions)
	if err != nil || fences != 1 || operations != 0 || effects != 0 || fuelVersion != database.ExpectedVersion("fuel") || goodDimensions != 2 {
		t.Fatal("upgrade fabricated producer proof or failed minimal legacy fences/dimensions", fences, operations, effects, fuelVersion, goodDimensions, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE module_id='fuel'"); err != nil {
		t.Fatal(err)
	}
	before := fuelModuleWriteSnapshot(t, f)
	module, ok := f.app.Store.Module("fuel")
	if !ok {
		t.Fatal("Fuel descriptor missing")
	}
	for _, old := range []struct {
		entity string
		input  store.Mutation
	}{{"vehicles", vehicleInput}, {"fillups", fillInput}} {
		entity, ok := module.Entity(old.entity)
		if !ok {
			t.Fatal("legacy descriptor missing", old.entity)
		}
		result, err := f.app.Store.Write(f.ctx, f.identity, module, entity, "", oldKey, value.UUID(), old.input)
		if result != nil {
			t.Fatal("cutover reconstructed an unproved old receipt", result)
		}
		fuelModuleWriteSQLCode(t, err, "23514")
		for range 2 {
			status, reply, raw := fuelModuleNativeHTTP(t, f, http.MethodPost, "/api/v1/m/fuel/"+old.entity, old.input, oldKey)
			if status != http.StatusConflict || reply["error"] != "Daten widersprechen bestehenden Einträgen oder einer Integritätsregel" || !value.ValidUUID(store.String(reply["request_id"])) || len(reply) != 2 {
				t.Fatal("repeated legacy old-key HTTP refusal was not bounded/actionable", status, reply)
			}
			for _, private := range []string{vehicle, series, first, second, oldKey, "Prior station", "Original exact fillup", "SQLSTATE", "legacy_operation_fences", "response_payload"} {
				if strings.Contains(string(raw), private) {
					t.Fatal("old-key HTTP error disclosed historical content or protocol internals", private)
				}
			}
		}
	}
	command := fuelModuleNativeCommand(t, f, "fillup.save", vehicle, "", 0, fuelModuleWriteFillData(series, "2026-02-01", "1000.123456", "40", "6800"), nil)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, oldKey, value.UUID(), command); err == nil || result.OperationID != "" {
		t.Fatal("typed protocol reused an old intent after legacy cache pruning", result, err)
	}
	fuelModuleWriteUnchanged(t, f, before)
	fillups, _ := module.Entity("fillups")
	created, err := f.app.Store.Write(f.ctx, f.identity, module, fillups, "", value.UUID(), value.UUID(), fillInput)
	if err != nil || !value.ValidUUID(store.String(created["id"])) || created["currency_code"] != "EUR" {
		t.Fatal("explicit new intent could not use the current compatibility adapter", created, err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM fuel.fillups WHERE household_id=$1", f.identity.HouseholdID).Scan(&count); err != nil || count != 3 {
		t.Fatal("new intent did not create exactly one fact", count, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("accepted post-upgrade write changed readiness", err)
	}
}
