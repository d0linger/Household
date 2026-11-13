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

// The leased, disposable fixture reconstructs the reviewed Pantry17/Core38
// predecessor from the frozen Documents catalog, never arbitrary live SQL.
func notificationPantryRestore17(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationFuelRestore11(t, f)
	var version int
	var dirty bool
	if err := f.owner.QueryRow(f.ctx, "SELECT version,dirty FROM pantry.schema_migrations").Scan(&version, &dirty); err != nil || version != 18 || dirty {
		t.Fatal("Pantry predecessor requires clean isolated Pantry18", version, dirty, err)
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
	_, err = tx.Exec(f.ctx, `DROP FUNCTION pantry.notification_observations_ready();
DROP FUNCTION pantry.notification_observations_catalog();
DROP TABLE pantry.notification_stock_observations;
DO $restore$
DECLARE baseline jsonb;original_audit text;reviewed_audit text;reviewed_pantry text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 original_audit=baseline->'core.audit_ready()'->>'definition';
 reviewed_audit=replace(original_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=165');
 reviewed_audit=replace(reviewed_audit,'''auth_challenges'',''rate_limit_buckets''','''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 reviewed_audit=replace(reviewed_audit,'''jobs'',''outbox_events''','''jobs'',''notification_intents'',''notification_deliveries'',''outbox_events''');
 reviewed_pantry=replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(original_audit),md5(reviewed_audit));
 IF original_audit IS NULL OR reviewed_audit IS NULL OR reviewed_pantry IS NULL THEN
  RAISE EXCEPTION 'missing frozen Pantry17 predecessor';END IF;
 EXECUTE reviewed_audit;EXECUTE reviewed_pantry;
END $restore$;
UPDATE pantry.schema_migrations SET version=17 WHERE version=18 AND NOT dirty;
DO $check$
BEGIN
 IF NOT core.audit_ready() OR NOT pantry.prepared_capture_ready() OR NOT core.notification_foundation_ready()
  OR NOT core.finance_ready() OR NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'isolated Pantry17 reconstruction failed';END IF;
END $check$;`)
	if err != nil {
		t.Fatal("restore reviewed Pantry17", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationPantryPopulated17UpgradePreservesFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("fresh Pantry18 readiness", err)
	}
	product, lot := pantryFixture(t, f, "notification predecessor")
	pack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": product["id"], "label": "Exact predecessor pack", "quantity": "0.125001", "unit_code": "kg"})
	auditCreate(t, f, "pantry", "barcodes", store.Record{"product_id": product["id"], "pack_id": pack["id"], "code": "NOTIFICATION-PANTRY17"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "9007199254.740993"})
	in := pantryStockInput(t, f, store.String(lot["id"]))
	in.Quantity = "0.125001"
	if _, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, store.String(lot["id"]), value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("create exact predecessor stock operation", err)
	}
	facts, core := pantryExtensionsPriorFacts(t, f), notificationLegacySnapshot(t, f)
	notificationPantryRestore17(t, f)
	if pantryExtensionsPriorFacts(t, f) != facts || notificationLegacySnapshot(t, f) != core {
		t.Fatal("predecessor reconstruction rewrote domain facts")
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("populated Pantry17 upgrade or idempotent repeat", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("upgraded Pantry18 readiness", err)
		}
		if pantryExtensionsPriorFacts(t, f) != facts || notificationLegacySnapshot(t, f) != core {
			t.Fatal("Pantry18 changed exact stock facts, identities or existing jobs")
		}
		var observations, intents, policies int
		if err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM pantry.notification_stock_observations),
 (SELECT count(*) FROM core.notification_intents WHERE module_id='pantry'),
 (SELECT count(*) FROM core.audit_policies)`).Scan(&observations, &intents, &policies); err != nil || observations != 0 || intents != 0 || policies != 165 {
			t.Fatal("migration invented observation history or changed audit census", observations, intents, policies, err)
		}
	}
}

func TestNotificationPantryReadinessRejectsObservationDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, test := range []struct{ name, sql string }{
		{"required source activation", "ALTER TABLE pantry.notification_stock_observations ALTER COLUMN module_activated_at DROP NOT NULL"},
		{"public stock disclosure", "GRANT SELECT ON pantry.notification_stock_observations TO PUBLIC"},
		{"unbounded episode", "ALTER TABLE pantry.notification_stock_observations DROP CONSTRAINT notification_stock_observations_episode_check"},
		{"missing due index", "DROP INDEX pantry.notification_stock_observations_low"},
		{"table policy drift", "ALTER TABLE pantry.notification_stock_observations ENABLE ROW LEVEL SECURITY"},
		{"private catalog exposure", "GRANT EXECUTE ON FUNCTION pantry.notification_observations_catalog() TO household_app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tx.Rollback(ctx)
			}()
			if _, err = tx.Exec(f.ctx, test.sql); err != nil {
				t.Fatal("install rollback-only observation drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT pantry.notification_observations_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("observation readiness accepted catalog drift", ready, err)
			}
		})
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("readiness did not recover after probe rollback", err)
		}
	}
}
