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

// Reconstruct the exact reviewed Meters2 catalog only inside the exclusively
// leased integration database. Empty notification sidecars are required: this
// helper is predecessor setup, not an application downgrade operation.
func notificationMetersRestore2(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationDocumentsRestore2(t, f)
	var valid bool
	var version int
	err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner',
 (SELECT version FROM meters.schema_migrations WHERE NOT dirty)`).Scan(&valid, &version)
	if err != nil || !valid {
		t.Fatal("Meters predecessor requires the isolated owner fixture", valid, err)
	}
	if version == 2 {
		return
	}
	if version != 3 {
		t.Fatal("unexpected Meters predecessor source version", version)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	_, err = tx.Exec(f.ctx, `DO $restore$
 DECLARE snapshot jsonb;item record;
 BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR EXISTS(SELECT 1 FROM meters.notification_settings) OR EXISTS(SELECT 1 FROM meters.notification_observations) THEN
 RAISE EXCEPTION 'Meter predecessor requires clean Core39/Expenses6 and unused sidecars';END IF;
 snapshot=meters.notification_predecessor_catalog();
 IF snapshot->'functions' IS NULL OR snapshot->'meters_catalog' IS NULL OR snapshot->'pantry_catalog' IS NULL THEN
 RAISE EXCEPTION 'missing reviewed Meters2 predecessor evidence';END IF;
 DROP TABLE meters.notification_observations,meters.notification_settings;
 DROP FUNCTION meters.notification_settings_guard(),meters.notification_settings_audit(),meters.notification_ready(),meters.notification_catalog();
 FOR item IN SELECT key,value FROM jsonb_each(snapshot->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 DROP FUNCTION meters.notification_predecessor_catalog();
 UPDATE meters.schema_migrations SET version=2 WHERE version=3 AND NOT dirty;
 IF meters.expansion_catalog() IS DISTINCT FROM snapshot->'meters_catalog'
 OR pantry.notification_observations_catalog() IS DISTINCT FROM snapshot->'pantry_catalog'
 OR NOT meters.expansion_ready() OR NOT core.audit_ready() OR NOT pantry.prepared_capture_ready()
 OR NOT pantry.notification_observations_ready() OR NOT core.finance_ready() OR NOT expenses.notification_ready()
 OR NOT core.notification_financial_ready() OR NOT fuel.notification_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
 RAISE EXCEPTION 'Meters2 reconstruction changed reviewed contracts';END IF;
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Meters2 predecessor", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationMetersLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'meters',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.meters x),
 'registers',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.registers x),
 'devices',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.devices x),
 'mappings',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.device_registers x),
 'readings',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.readings x),
 'revisions',(SELECT jsonb_agg(to_jsonb(x) ORDER BY reading_id,revision) FROM meters.reading_revisions x),
 'plans',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.reading_plans x),
 'completions',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM meters.plan_completions x),
 'receipts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY household_id,user_id,command_key) FROM meters.command_receipts x),
 'audit',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.audit_log x))::text`).Scan(&facts)
	if err != nil {
		t.Fatal("snapshot existing Meters facts", err)
	}
	return facts
}

func TestNotificationMetersPopulated2UpgradePreservesFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationMetersRestore2(t, f)
	state := f.app.Store.States()["meters"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "meters", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["meters"]
	}
	write := func(action string, data store.Record) store.Record {
		t.Helper()
		result, err := f.app.Store.EnergyWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.EnergyCommand{
			Generation: state.Generation, Action: action, Data: energyHTTPData(t, data),
		})
		if err != nil {
			t.Fatal("populate actual Meters2 operation", action, err)
		}
		return result
	}
	meter := write("meter.save", store.Record{"name": "Synthetic notification predecessor", "unit_code": "kWh"})
	var register string
	if err := f.owner.QueryRow(f.ctx, `SELECT id::text FROM meters.registers WHERE meter_id=$1 AND is_default`, meter["id"]).Scan(&register); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	device := write("device.save", store.Record{"register_id": register, "register_version": "1", "meter_id": meter["id"],
		"serial_number": "Synthetic predecessor physical meter", "installed_at": at.Add(-2 * time.Hour).Format(time.RFC3339), "initial_value": "0"})
	write("plan.save", store.Record{"register_id": register, "register_version": "1", "anchor_on": at.AddDate(0, 0, -1).Format(time.DateOnly),
		"interval_days": "1", "actual_only": true, "active": true})
	write("reading.save", store.Record{"register_id": register, "register_version": "1", "device_id": device["id"],
		"measured_at": at.Format(time.RFC3339), "value": "123.000001", "kind": "actual"})
	var populated bool
	if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM meters.plan_completions)
 AND EXISTS(SELECT 1 FROM meters.reading_revisions) AND EXISTS(SELECT 1 FROM meters.command_receipts)`).Scan(&populated); err != nil || !populated {
		t.Fatal("predecessor lacks real reading/completion/receipt history", populated, err)
	}
	facts := notificationMetersLegacyFacts(t, f)
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("upgrade populated Meters2 predecessor", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("complete readiness after Meters upgrade", err)
		}
		notificationConstraintTriggerCensus(t, f, "meters.notification_catalog()", []string{"meters.notification_settings", "meters.notification_observations"})
		if notificationMetersLegacyFacts(t, f) != facts {
			t.Fatal("upgrade or repeat migration changed existing measurements or audit history")
		}
		var untouched bool
		err := f.owner.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM meters.notification_settings)
 AND NOT EXISTS(SELECT 1 FROM meters.notification_observations)
 AND NOT EXISTS(SELECT 1 FROM core.notification_intents WHERE category='meters')
 AND NOT EXISTS(SELECT 1 FROM core.notification_preferences WHERE category='meters' AND enabled)
 AND (SELECT count(*) FROM core.audit_policies)=165
 AND (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='meters' AND c.relkind IN('r','p') AND c.relname<>'schema_migrations')=22`).Scan(&untouched)
		if err != nil || !untouched {
			t.Fatal("upgrade invented responsibility, consent or historical events, or changed catalog census", untouched, err)
		}
	}
}

func TestNotificationMetersReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationConstraintTriggerRecreation(t, f, "meters.notification_catalog()", "meters.notification_ready()", "meters.notification_settings")
	notificationConstraintTriggerRecreation(t, f, "meters.notification_catalog()", "meters.notification_ready()", "meters.notification_observations")
	for _, tc := range []struct{ name, sql string }{
		{"public settings read", `GRANT SELECT ON meters.notification_settings TO PUBLIC`},
		{"missing worker privilege", `REVOKE UPDATE ON meters.notification_observations FROM household_app`},
		{"clock guard disabled", `ALTER TABLE meters.notification_settings DISABLE TRIGGER notification_settings_guard`},
		{"audit disabled", `ALTER TABLE meters.notification_settings DISABLE TRIGGER notification_settings_audit`},
		{"public trigger helper", `GRANT EXECUTE ON FUNCTION meters.notification_settings_guard() TO PUBLIC`},
		{"public predecessor snapshot", `GRANT EXECUTE ON FUNCTION meters.notification_predecessor_catalog() TO PUBLIC`},
		{"row security changed", `ALTER TABLE meters.notification_settings ENABLE ROW LEVEL SECURITY`},
		{"extra content column", `ALTER TABLE meters.notification_settings ADD COLUMN unreviewed_measurement text`},
		{"nullable source stamp", `ALTER TABLE meters.notification_observations ALTER COLUMN source_stamp DROP NOT NULL`},
		{"unbounded observation state", `ALTER TABLE meters.notification_observations DROP CONSTRAINT notification_observations_state_check`},
		{"missing responsibility index", `DROP INDEX meters.notification_settings_responsible`},
		{"missing active episode index", `DROP INDEX meters.notification_observations_active`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = tx.Rollback(ctx)
			}()
			if _, err = tx.Exec(f.ctx, tc.sql); err != nil {
				t.Fatal("install rollback-only meter drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT meters.notification_ready() AND meters.expansion_ready() AND core.audit_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("Meter readiness accepted metadata drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rollback-only probes", err)
	}
}

func TestNotificationMetersUpgradeRejectsReplacedPredecessor(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationMetersRestore2(t, f)
	facts := notificationMetersLegacyFacts(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION meters.expansion_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT true'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("upgrade trusted a substituted Meters2 ready function")
	}
	var absent bool
	if err := f.owner.QueryRow(f.ctx, `SELECT to_regclass('meters.notification_settings') IS NULL
 AND to_regclass('meters.notification_observations') IS NULL AND to_regprocedure('meters.notification_ready()') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatal("rejected predecessor left partial notification objects", absent, err)
	}
	if notificationMetersLegacyFacts(t, f) != facts {
		t.Fatal("rejected predecessor changed existing facts")
	}
}
