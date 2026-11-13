//go:build integration

package web

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func fuelModuleLifecyclePriorFence(t *testing.T, f *integrationFixture) string {
	t.Helper()
	fuelModuleUpgradePriorCatalog(t, f)
	key, vehicle := value.UUID(), value.UUID()
	input := auditMutation(t, f, "fuel", store.Record{"name": "SYNTHETIC prior Fuel lifecycle", "fuel_type": "diesel"}, 0)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "fuel", input.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer fuelModuleWriteRollback(tx)
	// Explicit synthetic prior-state fixture, not execution of an old binary:
	// the actual Fuel2 App transaction creates a matching fact/cache pair. Only
	// the actual Fuel3 migration is allowed to create its private minimal fence.
	var response []byte
	err = tx.QueryRow(ctx, `INSERT INTO fuel.vehicles AS v(id,household_id,name,fuel_type,created_by)
 VALUES($1,$2,'SYNTHETIC prior Fuel lifecycle','diesel',$3) RETURNING to_jsonb(v)`, vehicle, f.identity.HouseholdID, f.identity.UserID).Scan(&response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,'write/fuel/vehicles/',$3,$4,'fuel',$5,200,$6)`, f.identity.HouseholdID, f.identity.UserID, key, hash[:], input.Generation, response); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	return key
}

func fuelModuleLifecycleCatalog(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var fingerprint string
	// module_catalog() is a private FUNCTION; it fingerprints actual schema,
	// policies, triggers and fixed fuel types, rather than mutable fact rows.
	if err := f.owner.QueryRow(f.ctx, "SELECT encode(sha256(convert_to(fuel.module_catalog()::text,'UTF8')),'hex')").Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func fuelModuleLifecycleRetained(t *testing.T, f *integrationFixture, generation int64) (string, [3]int64) {
	t.Helper()
	var fingerprint string
	var counts [3]int64
	err := f.owner.QueryRow(f.ctx, `SELECT encode(sha256(convert_to(jsonb_build_object(
 'operations',(SELECT jsonb_agg(to_jsonb(o)-ARRAY['result','result_sha256'] ORDER BY o.id) FROM fuel.module_operations o WHERE o.household_id=$1 AND o.generation=$2),
 'effects',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.operation_id,e.ordinal) FROM fuel.module_effects e JOIN fuel.module_operations o ON o.id=e.operation_id WHERE o.household_id=$1 AND o.generation=$2),
 'legacy',(SELECT jsonb_agg(to_jsonb(l) ORDER BY l.user_id,l.operation_key) FROM fuel.legacy_operation_fences l WHERE l.household_id=$1 AND l.generation=$2))::text,'UTF8')),'hex'),
 (SELECT count(*) FROM fuel.module_operations WHERE household_id=$1 AND generation=$2),
 (SELECT count(*) FROM fuel.module_effects e JOIN fuel.module_operations o ON o.id=e.operation_id WHERE o.household_id=$1 AND o.generation=$2),
 (SELECT count(*) FROM fuel.legacy_operation_fences WHERE household_id=$1 AND generation=$2)`, f.identity.HouseholdID, generation).
		Scan(&fingerprint, &counts[0], &counts[1], &counts[2])
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint, counts
}

func TestFuelModuleNativeDisablePurgeEnableRetainsOnlyProtocolFences(t *testing.T) {
	f := newIntegrationFixture(t)
	legacyKey := fuelModuleLifecyclePriorFence(t, f)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	accepted, oldCommand, oldKey := fuelModuleNativeApply(t, f, "fillup.save", store.String(vehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(series["id"]), "2026-02-01", "1000.123456", "40", "6800"), nil)
	if accepted.RecordID == "" {
		t.Fatal("test requires an actually accepted typed fillup")
	}
	generation := oldCommand.Generation
	plan, _, _ := fuelModuleNativeApply(t, f, "maintenance.save", store.String(vehicle["id"]), "", 0,
		store.Record{"title": "SYNTHETIC lifecycle observation", "due_on": "2099-01-01", "active": true}, nil)
	for range 2 {
		if _, err := f.app.Store.StageFuelNotifications(f.ctx, 50); err != nil {
			t.Fatal("actual lifecycle source scan", err)
		}
	}
	var observed bool
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM fuel.notification_maintenance_observations
 WHERE household_id=$1 AND plan_id=$2 AND module_generation=$3 AND state='ineligible' AND episode=0
 AND detected_at IS NULL AND consent_cutoff IS NULL)`, f.identity.HouseholdID, plan.RecordID, generation).Scan(&observed); err != nil || !observed {
		t.Fatal("actual scanner did not record the unassigned date-only plan without inventing an alert", observed, err)
	}
	catalog := fuelModuleLifecycleCatalog(t, f)
	retained, retainedCounts := fuelModuleLifecycleRetained(t, f, generation)
	if retainedCounts[0] < 4 || retainedCounts[1] < 4 || retainedCounts[2] != 1 {
		t.Fatal("test requires nonempty genuine operations/effects and a migration-created legacy fence", retainedCounts)
	}
	state := f.app.Store.States()["fuel"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "fuel", false, state.Version, value.UUID()); err != nil {
		t.Fatal("normal public module disable", err)
	}
	if _, err := f.app.Store.StageFuelNotifications(f.ctx, 50); err != nil {
		t.Fatal("disabled module scan", err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM fuel.notification_maintenance_observations
 WHERE household_id=$1 AND plan_id=$2 AND module_generation=$3 AND state='ineligible' AND episode=0)`,
		f.identity.HouseholdID, plan.RecordID, generation).Scan(&observed); err != nil || !observed {
		t.Fatal("deactivation discarded or advanced the retained observation", observed, err)
	}
	backup, request := value.UUID(), value.UUID()
	// Synthetic VERIFIED-BACKUP-ELIGIBILITY metadata only. No backup or restore
	// is performed or claimed here; the deployment wrapper verifies those apart.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'SYNTHETIC lifecycle eligibility','not-a-real-backup',$2)`, backup, "synthetic-eligibility-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'fuel',$3,$4,$5)`, request, f.identity.HouseholdID, generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	beforeWrongRole := fuelModuleWriteSnapshot(t, f)
	if err := maintenance.Purge(f.ctx, f.pool, request); err == nil || !strings.Contains(err.Error(), "separate maintenance role") {
		t.Fatal("App database role performed or incorrectly attempted an operator purge", err)
	}
	fuelModuleWriteUnchanged(t, f, beforeWrongRole)
	if err := maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal("actual owner-role module purge", err)
	}
	if after, counts := fuelModuleLifecycleRetained(t, f, generation); after != retained || counts != retainedCounts {
		t.Fatal("purge removed or rewrote retained operation/effect/legacy identity", counts, retainedCounts)
	}
	var payloads, businessFacts, currentGeneration int64
	var moduleState, purgeState string
	err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM fuel.module_operations WHERE household_id=$1 AND generation=$2 AND(result IS NOT NULL OR result_sha256 IS NOT NULL)),
 (SELECT count(*) FROM fuel.vehicles)+(SELECT count(*) FROM fuel.odometer_series)+(SELECT count(*) FROM fuel.fillups)+
 (SELECT count(*) FROM fuel.odometer_observations)+(SELECT count(*) FROM fuel.charging_sessions)+(SELECT count(*) FROM fuel.trips)+
 (SELECT count(*) FROM fuel.vehicle_costs)+(SELECT count(*) FROM fuel.maintenance_plans)+(SELECT count(*) FROM fuel.maintenance_events)+
 (SELECT count(*) FROM fuel.integration_bindings)+(SELECT count(*) FROM fuel.import_jobs)+(SELECT count(*) FROM fuel.import_rows)+
 (SELECT count(*) FROM fuel.capture_jobs)+(SELECT count(*) FROM fuel.user_settings)+
 (SELECT count(*) FROM fuel.notification_maintenance_observations),
 s.data_generation,s.state,p.state FROM core.module_states s JOIN core.module_purge_requests p ON p.id=$3
 WHERE s.household_id=$1 AND s.module_id='fuel'`, f.identity.HouseholdID, generation, request).
		Scan(&payloads, &businessFacts, &currentGeneration, &moduleState, &purgeState)
	if err != nil || payloads != 0 || businessFacts != 0 || currentGeneration != generation+1 || moduleState != "disabled" || purgeState != "completed" {
		t.Fatal("purge retained business payload or failed its generation fence", payloads, businessFacts, currentGeneration, moduleState, purgeState, err)
	}
	if after := fuelModuleLifecycleCatalog(t, f); after != catalog {
		t.Fatal("purge changed the actual private schema/catalog fingerprint")
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("purge destroyed application readiness", err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	state = f.app.Store.States()["fuel"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "fuel", true, state.Version, value.UUID()); err != nil {
		t.Fatal("normal public module enable", err)
	}
	for range 2 {
		if _, err := f.app.Store.StageFuelNotifications(f.ctx, 50); err != nil {
			t.Fatal("new-generation source scan", err)
		}
	}
	var oldObservations int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM fuel.notification_maintenance_observations
 WHERE household_id=$1`, f.identity.HouseholdID).Scan(&oldObservations); err != nil || oldObservations != 0 {
		t.Fatal("new generation revived a purged maintenance observation", oldObservations, err)
	}
	beforeReplay := fuelModuleWriteSnapshot(t, f)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, oldKey, value.UUID(), oldCommand); err == nil || result.OperationID != "" {
		t.Fatal("old generation command revived a purged accepted result", result, err)
	}
	for _, key := range []string{oldKey, legacyKey} {
		if reply, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, generation); err == nil || reply != nil {
			t.Fatal("old generation status disclosed retained receipt", reply, err)
		}
		if reply, err := f.app.Store.FuelModuleOperationStatus(f.ctx, f.identity, key, generation+1); err != nil || reply["state"] != "unknown" || reply["result"] != nil {
			t.Fatal("new generation status fabricated acceptance of an old key", reply, err)
		}
	}
	// Changing client generation/stamp cannot make removed vehicle/series IDs
	// become current facts. The new operation must roll back without a receipt.
	current := fuelModuleNativeCommand(t, f, "fillup.save", store.String(vehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(series["id"]), "2026-02-01", "1000.123456", "40", "6800"), nil)
	if result, err := f.app.Store.FuelModuleExecute(f.ctx, f.identity, oldKey, value.UUID(), current); err == nil || result.OperationID != "" {
		t.Fatal("new client generation recreated old removed source IDs", result, err)
	}
	fuelModuleWriteUnchanged(t, f, beforeReplay)
	freshVehicle, freshSeries := fuelModuleNativeVehicle(t, f)
	fresh, _, _ := fuelModuleNativeApply(t, f, "fillup.save", store.String(freshVehicle["id"]), "", 0,
		fuelModuleWriteFillData(store.String(freshSeries["id"]), "2026-02-01", "10", "1", "100"), nil)
	if fresh.RecordID == accepted.RecordID || fresh.RecordID == "" || fresh.Action != "fillup.save" {
		t.Fatal("fresh explicit review after purge did not create a new fact", fresh)
	}
	if after, counts := fuelModuleLifecycleRetained(t, f, generation); after != retained || counts != retainedCounts {
		t.Fatal("fresh next-generation write changed old minimal fences", counts, retainedCounts)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("fresh post-purge state is not ready", err)
	}
}
