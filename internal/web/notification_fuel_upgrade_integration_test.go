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
)

// This helper runs only inside the leased disposable integration fixture. The
// migration froze the exact reviewed predecessor before changing its catalog.
func notificationFuelRestore11(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationFinanceRestore38And5(t, f)
	var valid bool
	if err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner'
 AND (SELECT count(*)=1 AND bool_and(version=12 AND NOT dirty) FROM fuel.schema_migrations)`).Scan(&valid); err != nil || !valid {
		t.Fatal("Fuel11 reconstruction requires isolated clean Fuel12", valid, err)
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
 snapshot=fuel.notification_predecessor_catalog();
 IF snapshot->'functions' IS NULL OR jsonb_array_length(snapshot->'audit_policies')<>2 THEN
 RAISE EXCEPTION 'missing Fuel11 frozen predecessor';END IF;
 DROP TRIGGER fuel_notification_assignment ON fuel.vehicles;
 DROP TRIGGER fuel_notification_assignment ON fuel.maintenance_plans;
 DROP FUNCTION fuel.notification_assignment_clock();
 DROP FUNCTION fuel.notification_ready();
 DROP TABLE fuel.notification_maintenance_observations;
 ALTER TABLE fuel.vehicles DROP COLUMN responsible_member_id,DROP COLUMN responsible_since;
 ALTER TABLE fuel.maintenance_plans DROP COLUMN assignee_since;
 UPDATE core.audit_policies p SET safe_columns=old.safe_columns
 FROM jsonb_populate_recordset(NULL::core.audit_policies,snapshot->'audit_policies') old
 WHERE p.schema_name=old.schema_name AND p.table_name=old.table_name;
 FOR item IN SELECT key,value FROM jsonb_each(snapshot->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';
 END LOOP;
 DROP FUNCTION fuel.notification_predecessor_catalog();
 UPDATE fuel.schema_migrations SET version=11 WHERE version=12 AND NOT dirty;
 IF fuel.module_catalog() IS DISTINCT FROM snapshot->'fuel_catalog'
 OR pantry.notification_observations_catalog() IS DISTINCT FROM snapshot->'pantry_observations_catalog'
 OR NOT fuel.module_ready() OR NOT core.audit_ready() OR NOT pantry.notification_observations_ready() THEN
 RAISE EXCEPTION 'Fuel11 reconstruction failed';END IF;
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Fuel11 predecessor", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationFuelLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'vehicles',(SELECT jsonb_agg(to_jsonb(v)-'responsible_member_id'-'responsible_since' ORDER BY id) FROM fuel.vehicles v),
 'plans',(SELECT jsonb_agg(to_jsonb(p)-'assignee_since' ORDER BY id) FROM fuel.maintenance_plans p),
 'series',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM fuel.odometer_series s),
 'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM fuel.module_operations o))::text`).Scan(&facts)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestNotificationFuelPopulated11UpgradePreservesFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	vehicle, series := fuelModuleNativeVehicle(t, f)
	fuelModuleNativeApply(t, f, "maintenance.save", store.String(vehicle["id"]), "", 0,
		store.Record{"title": "Synthetic predecessor maintenance", "due_on": "2099-01-01", "due_odometer": "1000.000001",
			"odometer_series_id": series["id"], "assignee_id": f.identity.MembershipID}, nil)
	facts, core := notificationFuelLegacyFacts(t, f), notificationLegacySnapshot(t, f)
	notificationFuelRestore11(t, f)
	if notificationFuelLegacyFacts(t, f) != facts || notificationLegacySnapshot(t, f) != core {
		t.Fatal("reconstruction changed facts")
	}
	var before time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("populated Fuel11 upgrade", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("upgraded readiness", err)
		}
		if notificationFuelLegacyFacts(t, f) != facts || notificationLegacySnapshot(t, f) != core {
			t.Fatal("migration changed domain facts or account state")
		}
		var valid bool
		err := f.owner.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM fuel.vehicles WHERE responsible_member_id IS NOT NULL OR responsible_since IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM fuel.maintenance_plans WHERE assignee_id IS NOT NULL AND (assignee_since IS NULL OR assignee_since<$1))
 AND NOT EXISTS(SELECT 1 FROM fuel.notification_maintenance_observations)
 AND NOT EXISTS(SELECT 1 FROM core.notification_intents WHERE category='fuel')
 AND (SELECT count(*)=165 FROM core.audit_policies)`, before).Scan(&valid)
		if err != nil || !valid {
			t.Fatal("upgrade invented history or lost assignment cutoff", valid, err)
		}
	}
}

func TestNotificationFuelReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct{ name, sql string }{
		{"public observation", `GRANT SELECT ON fuel.notification_maintenance_observations TO PUBLIC`},
		{"row security", `ALTER TABLE fuel.notification_maintenance_observations ENABLE ROW LEVEL SECURITY`},
		{"missing generation", `ALTER TABLE fuel.notification_maintenance_observations ALTER COLUMN module_generation DROP NOT NULL`},
		{"missing index", `DROP INDEX fuel.notification_maintenance_observations_due`},
		{"disabled clock", `ALTER TABLE fuel.vehicles DISABLE TRIGGER fuel_notification_assignment`},
		{"public clock", `GRANT EXECUTE ON FUNCTION fuel.notification_assignment_clock() TO PUBLIC`},
		{"assignment integrity", `ALTER TABLE fuel.vehicles DROP CONSTRAINT vehicles_responsible_member`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tx.Rollback(ctx)
			}()
			if _, err = tx.Exec(f.ctx, tc.sql); err != nil {
				t.Fatal("install rollback-only drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT fuel.module_ready() AND fuel.notification_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("accepted catalog drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rollback", err)
	}
}
