//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgxpool"
)

type energyNative struct {
	f                       *recipeExpansionFixture
	meter, register, device string
	start                   time.Time
}

func energyNativeWrite(t *testing.T, f *recipeExpansionFixture, action, id string, version int64, data Record) Record {
	t.Helper()
	result, err := f.store.EnergyWrite(f.ctx, f.identity, value.UUID(), value.UUID(), EnergyCommand{Generation: f.store.States()["meters"].Generation, Action: action, ID: id, Version: version, Data: energyRaw(data)})
	if err != nil {
		t.Fatal(action, err)
	}
	return result
}

func newEnergyNative(t *testing.T, mode string) *energyNative {
	t.Helper()
	f := newRecipeExpansionFixture(t)
	fixture := &energyNative{f: f, start: time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)}
	if mode == "retention" {
		fixture.start = fixture.start.AddDate(0, 0, -18)
	}
	meter := energyNativeWrite(t, f, "meter.save", "", 0, Record{"name": "Native electricity", "unit_code": "kWh"})
	fixture.meter = String(meter["id"])
	if err := f.owner.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE household_id=$1 AND meter_id=$2 AND is_default`, f.identity.HouseholdID, fixture.meter).Scan(&fixture.register); err != nil {
		t.Fatal(err)
	}
	if mode != "manual" {
		energyNativeWrite(t, f, "register.save", fixture.register, 1, Record{"meter_id": fixture.meter, "name": "Native telemetry", "measurement_kind": "cumulative", "unit_code": "kWh", "direction": "import", "timezone": "UTC", "input_mode": "telemetry", "max_gap_seconds": "86400", "min_history": "7"})
	}
	data := Record{"meter_id": fixture.meter, "register_id": fixture.register, "register_version": fmt.Sprint(fixture.registerVersion(t)), "serial_number": "Native physical meter", "installed_at": fixture.start.Format(time.RFC3339Nano), "initial_value": "0"}
	device := energyNativeWrite(t, f, "device.save", "", 0, data)
	fixture.device = String(device["id"])
	return fixture
}

func (f *energyNative) registerVersion(t *testing.T) int64 {
	t.Helper()
	var version int64
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT version FROM meters.registers WHERE id=$1`, f.register).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
func (f *energyNative) readingData(t *testing.T, at time.Time, quantity, kind string) Record {
	t.Helper()
	return Record{"register_id": f.register, "register_version": fmt.Sprint(f.registerVersion(t)), "device_id": f.device, "measured_at": at.Format(time.RFC3339Nano), "value": quantity, "kind": kind}
}
func (f *energyNative) query() EnergyQuery {
	return EnergyQuery{RegisterID: f.register, From: f.start.Format(time.DateOnly), Through: f.start.Format(time.DateOnly), Grain: "day"}
}

func energyNativeLease(t *testing.T, f *recipeExpansionFixture) (string, string) {
	t.Helper()
	job, lease := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,dedupe_key,state,lease_token,lease_until)
 VALUES($1,$2,'meters',$3,$4,$5,'running',$6,clock_timestamp()+interval '60 seconds')`, job, f.identity.HouseholdID, f.store.States()["meters"].Generation, EnergyWorkspaceJobKind, "native-energy/"+job, lease); err != nil {
		t.Fatal(err)
	}
	return job, lease
}

func energyNativeProject(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	for range 200 {
		var pending bool
		if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM meters.dirty_ranges WHERE household_id=$1)`, f.identity.HouseholdID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		job, lease := energyNativeLease(t, f)
		if err := f.store.CompleteEnergyWorkspaceJob(f.ctx, job, lease); err != nil {
			t.Fatal("actual leased energy projection", err)
		}
	}
	t.Fatal("bounded projection failed to finish")
}

func TestEnergyNativeExactReadingsCorrectionsConcurrencyAndPermanentReplay(t *testing.T) {
	f := newEnergyNative(t, "manual")
	first := energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(time.Hour), "10", "actual"))
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(2*time.Hour), "20", "actual"))
	pending, err := f.f.store.EnergySeries(f.f.ctx, f.f.identity, f.query())
	if err != nil || pending["recalculation_pending"] != true || pending["total_quantity"] != nil {
		t.Fatal("stale totals exposed", pending, err)
	}
	energyNativeProject(t, f.f)
	series, err := f.f.store.EnergySeries(f.f.ctx, f.f.identity, f.query())
	if err != nil || String(series["total_quantity"]) != "20.000000" || Int(series["coverage_seconds"]) != 7200 || series["cost_complete"] != false {
		t.Fatal(series, err)
	}
	data := f.readingData(t, f.start.Add(time.Hour), "12", "actual")
	data["reason"] = "Original photograph reviewed"
	key := value.UUID()
	command := EnergyCommand{Generation: f.f.store.States()["meters"].Generation, Action: "reading.save", ID: String(first["id"]), Version: 1, Data: energyRaw(data)}
	corrected, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command)
	if err != nil || Int(corrected["version"]) != 2 {
		t.Fatal(corrected, err)
	}
	var revisions int
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM meters.reading_revisions WHERE reading_id=$1`, first["id"]).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatal("correction rewrote original", revisions, err)
	}
	if _, err = f.f.app.Exec(f.f.ctx, `DELETE FROM meters.reading_revisions WHERE reading_id=$1`, first["id"]); err == nil {
		t.Fatal("immutable facts deleted")
	}
	command.Version = 2
	data["value"] = "13"
	command.Data = energyRaw(data)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 2)
	for n := range 2 {
		wg.Go(func() {
			<-start
			_, results[n] = f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command)
		})
	}
	close(start)
	wg.Wait()
	passed := 0
	for _, err := range results {
		if err == nil {
			passed++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if passed != 1 {
		t.Fatal("optimistic correction concurrency failed", results)
	}
	command.Version = 1
	data["value"] = "12"
	command.Data = energyRaw(data)
	replayed, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command)
	if err != nil || Int(replayed["version"]) != 2 {
		t.Fatal("unchanged-key durable proof lost after a later correction", replayed, err)
	}
	if _, err = f.f.owner.Exec(f.f.ctx, `UPDATE core.memberships SET role='reader' WHERE id=$1`, f.f.identity.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command); !errors.Is(err, ErrForbidden) {
		t.Fatal("receipt restored revoked permission", err)
	}
}

func TestEnergyNativePlanUsesCurrentActualRevisionAndCSVIsAtomic(t *testing.T) {
	f := newEnergyNative(t, "manual")
	day := f.start.Format(time.DateOnly)
	energyNativeWrite(t, f.f, "plan.save", "", 0, Record{"register_id": f.register, "register_version": "1", "anchor_on": day, "interval_days": "7", "actual_only": true, "active": true})
	reading := energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(time.Hour), "10", "estimated"))
	plans, err := f.f.store.EnergyManagement(f.f.ctx, f.f.identity, "plans", f.register, EnergyQuery{})
	if err != nil || String(plans["rows"].([]Record)[0]["next_due_on"]) != day {
		t.Fatal("estimate completed actual-only plan", plans, err)
	}
	data := f.readingData(t, f.start.Add(time.Hour), "10", "actual")
	data["reason"] = "Measured on original device"
	energyNativeWrite(t, f.f, "reading.save", String(reading["id"]), 1, data)
	plans, err = f.f.store.EnergyManagement(f.f.ctx, f.f.identity, "plans", f.register, EnergyQuery{})
	if err != nil || String(plans["rows"].([]Record)[0]["next_due_on"]) == day {
		t.Fatal("actual revision did not complete plan", plans, err)
	}
	data["kind"] = "estimated"
	energyNativeWrite(t, f.f, "reading.save", String(reading["id"]), 2, data)
	plans, err = f.f.store.EnergyManagement(f.f.ctx, f.f.identity, "plans", f.register, EnergyQuery{})
	if err != nil || String(plans["rows"].([]Record)[0]["next_due_on"]) != day {
		t.Fatal("superseded actual still completes plan", plans, err)
	}
	data["kind"] = "actual"
	energyNativeWrite(t, f.f, "reading.save", String(reading["id"]), 3, data)
	csv := "measured_at;value;kind;unit_code\n" + f.start.Add(2*time.Hour).Format(time.RFC3339) + ";20;actual;kWh\n" + f.start.Add(3*time.Hour).Format(time.RFC3339) + ";30;actual;kWh"
	preview := energyNativeWrite(t, f.f, "import.preview", "", 0, Record{"register_id": f.register, "register_version": "1", "device_id": f.device, "timezone": "Europe/Vienna", "unit_code": "kWh", "delimiter": ";", "csv": csv})
	if Int(preview["rows"]) != 2 {
		t.Fatal(preview)
	}
	apply := energyNativeWrite(t, f.f, "import.apply", String(preview["id"]), 1, Record{"confirmed": true})
	if Int(apply["inserted"]) != 2 || Int(apply["duplicates"]) != 0 {
		t.Fatal(apply)
	}
	preview = energyNativeWrite(t, f.f, "import.preview", "", 0, Record{"register_id": f.register, "register_version": "1", "device_id": f.device, "timezone": "Europe/Vienna", "unit_code": "kWh", "delimiter": ";", "csv": csv})
	apply = energyNativeWrite(t, f.f, "import.apply", String(preview["id"]), 1, Record{"confirmed": true})
	if Int(apply["inserted"]) != 0 || Int(apply["duplicates"]) != 2 {
		t.Fatal("exact import deduplication", apply)
	}
}

func TestEnergyNativeDeviceReplacementNeverSubtractsDifferentCounters(t *testing.T) {
	f := newEnergyNative(t, "manual")
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(time.Hour), "100", "actual"))
	var mapping string
	var version int64
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT id::text,version FROM meters.device_registers WHERE register_id=$1 AND device_id=$2`, f.register, f.device).Scan(&mapping, &version); err != nil {
		t.Fatal(err)
	}
	replaced := energyNativeWrite(t, f.f, "device.replace", "", 0, Record{"register_id": f.register, "register_version": "1", "old_mapping_id": mapping, "old_mapping_version": fmt.Sprint(version), "changed_at": f.start.Add(2 * time.Hour).Format(time.RFC3339), "old_final": "", "new_initial": "7", "serial_number": "New actual device", "gap_acknowledged": true, "reason": "Unknown retired final"})
	f.device = String(replaced["new_device_id"])
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(3*time.Hour), "17", "actual"))
	energyNativeProject(t, f.f)
	var unknown, known int
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FILTER(WHERE quantity IS NULL AND quality='gap'),count(*) FILTER(WHERE quantity=10) FROM meters.intervals WHERE register_id=$1`, f.register).Scan(&unknown, &known); err != nil || unknown != 1 || known != 1 {
		t.Fatal("device counters merged", unknown, known, err)
	}
	if _, err := f.f.app.Exec(f.f.ctx, `UPDATE meters.devices SET initial_reading=999 WHERE id=$1`, f.device); err == nil {
		t.Fatal("reviewed device boundary rewritten")
	}
}

func energyMigrateChorePredecessor(t *testing.T, ctx context.Context, url string, owner *pgxpool.Pool) {
	t.Helper()
	choreMigrateFinancePredecessor(t, ctx, url, owner)
	for _, stage := range []struct {
		schema  string
		version uint
	}{{"chores", 2}, {"core", 32}, {"fuel", 9}, {"pantry", 15}, {"mealplan", 10}} {
		recipeExpansionMigrateTo(t, url, stage.schema, stage.version)
	}
	if _, err := owner.Exec(ctx, `SELECT core.install_audit_triggers()`); err != nil {
		t.Fatal(err)
	}
}

func TestEnergyNativePopulatedCore32UpgradePreservesLegacyFactsAndReadiness(t *testing.T) {
	f := newRecipeExpansionFixtureForMigration(t, energyMigrateChorePredecessor)
	meter, device, reading := value.UUID(), value.UUID(), value.UUID()
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO meters.meters(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Prior electricity','kWh',$3)`, []any{meter, f.identity.HouseholdID, f.identity.UserID}},
		{`INSERT INTO meters.devices(id,household_id,meter_id,serial_number,installed_on,initial_reading,created_by) VALUES($1,$2,$3,'Prior physical device',$4,100,$5)`, []any{device, f.identity.HouseholdID, meter, at.AddDate(0, 0, -1).Format(time.DateOnly), f.identity.UserID}},
		{`INSERT INTO meters.readings(id,household_id,device_id,measured_at,value,created_by) VALUES($1,$2,$3,$4,125.123456,$5)`, []any{reading, f.identity.HouseholdID, device, at, f.identity.UserID}},
	} {
		if _, err := audit.Exec(ctx, f.owner, statement.sql, statement.args...); err != nil {
			t.Fatal("real predecessor data", err)
		}
	}
	if err := database.Migrate(f.ctx, f.ownerURL); err != nil {
		t.Fatal("populated forward upgrade", err)
	}
	var exact, ready bool
	var policies, domain int
	err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM meters.readings x JOIN meters.device_registers d ON d.device_id=x.device_id AND d.register_id=x.register_id WHERE x.id=$1 AND x.value=125.123456 AND x.measured_at=$2 AND d.legacy_boundary),meters.expansion_ready() AND core.audit_ready() AND fuel.module_ready() AND pantry.prepared_capture_ready() AND mealplan.expansion_ready(),(SELECT count(*) FROM core.audit_policies),(SELECT count(*) FROM pg_tables WHERE schemaname='meters' AND tablename<>'schema_migrations')`, reading, at).Scan(&exact, &ready, &policies, &domain)
	if err != nil || !exact || !ready || policies != 152 || domain != 20 {
		t.Fatal(exact, ready, policies, domain, err)
	}
	if _, err = f.owner.Exec(f.ctx, `GRANT EXECUTE ON FUNCTION meters.guard_measurement() TO household_app`); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT meters.expansion_ready()`).Scan(&ready); err != nil || ready {
		t.Fatal("function ACL drift accepted", ready, err)
	}
}

func TestEnergyNativeFreshVersionGenerationAndImportRollback(t *testing.T) {
	f := newEnergyNative(t, "manual")
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(time.Hour), "10", "actual"))
	data := f.readingData(t, f.start.Add(2*time.Hour), "20", "actual")
	data["register_version"] = "2"
	command := EnergyCommand{Generation: f.f.store.States()["meters"].Generation, Action: "reading.save", Data: energyRaw(data)}
	if _, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("stale register form accepted", err)
	}
	data["register_version"] = "1"
	command.Data = energyRaw(data)
	command.Generation++
	if _, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong generation accepted", err)
	}
	csv := "measured_at;value;kind;unit_code\n" + f.start.Add(2*time.Hour).Format(time.RFC3339) + ";20;actual;kWh\n" + f.start.Add(3*time.Hour).Format(time.RFC3339) + ";5;actual;kWh"
	preview := energyNativeWrite(t, f.f, "import.preview", "", 0, Record{"register_id": f.register, "register_version": "1", "device_id": f.device, "timezone": "Europe/Vienna", "unit_code": "kWh", "delimiter": ";", "csv": csv})
	_, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), EnergyCommand{Generation: f.f.store.States()["meters"].Generation, Action: "import.apply", ID: String(preview["id"]), Version: 1, Data: energyRaw(Record{"confirmed": true})})
	if err == nil {
		t.Fatal("contradictory staged import accepted")
	}
	var count int
	var state string
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT(SELECT count(*) FROM meters.readings WHERE register_id=$1),(SELECT state FROM meters.import_batches WHERE id=$2)`, f.register, preview["id"]).Scan(&count, &state); err != nil || count != 1 || state != "preview" {
		t.Fatal("partial import committed", count, state, err)
	}
}
