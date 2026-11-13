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

// This is isolated predecessor setup, never an application downgrade. Retained
// notification settings make reconstruction unsafe and are explicitly refused.
func notificationMealplanRestore12(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationCryptoRestore39(t, f)
	var valid bool
	var version int
	err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner',
 (SELECT version FROM mealplan.schema_migrations WHERE NOT dirty)`).Scan(&valid, &version)
	if err != nil || !valid {
		t.Fatal("Mealplan predecessor requires the isolated owner fixture", valid, err)
	}
	if version == 12 {
		return
	}
	if version != 13 {
		t.Fatal("unexpected Mealplan predecessor source version", version)
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
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM meters.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM documents.schema_migrations)
 OR EXISTS(SELECT 1 FROM mealplan.notification_settings) THEN
 RAISE EXCEPTION 'Mealplan predecessor requires accepted shared schemas and unused notification settings';END IF;
 snapshot=mealplan.notification_predecessor_catalog();
 IF snapshot->'functions' IS NULL OR snapshot->'mealplan_catalog' IS NULL OR snapshot->'extension_catalog' IS NULL
 OR snapshot->'pantry_catalog' IS NULL THEN RAISE EXCEPTION 'missing reviewed Mealplan12 predecessor evidence';END IF;
 DROP TRIGGER notification_plan_clock ON mealplan.plans;
 DROP TABLE mealplan.notification_settings;
 ALTER TABLE mealplan.plans DROP COLUMN notification_active_since;
 DROP FUNCTION mealplan.notification_settings_guard(),mealplan.notification_settings_audit(),mealplan.notification_plan_clock(),mealplan.notification_ready();
 FOR item IN SELECT key,value FROM jsonb_each(snapshot->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 IF mealplan.notification_catalog() IS DISTINCT FROM snapshot->'mealplan_catalog'
 OR mealplan.extension_catalog() IS DISTINCT FROM snapshot->'extension_catalog'
 OR pantry.notification_observations_catalog() IS DISTINCT FROM snapshot->'pantry_catalog'
 OR NOT mealplan.expansion_ready() OR NOT core.planning_extensions_ready() OR NOT documents.notification_ready()
 OR NOT meters.notification_ready() OR NOT core.audit_ready() OR NOT pantry.prepared_capture_ready()
 OR NOT pantry.notification_observations_ready() OR NOT core.finance_ready() OR NOT expenses.notification_ready()
 OR NOT core.notification_financial_ready() OR NOT fuel.notification_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Mealplan12 reconstruction changed reviewed contracts';END IF;
 DROP FUNCTION mealplan.notification_catalog(),mealplan.notification_predecessor_catalog();
 UPDATE mealplan.schema_migrations SET version=12 WHERE version=13 AND NOT dirty;
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Mealplan12 predecessor", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationMealplanLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'plans',(SELECT jsonb_agg(to_jsonb(x)-'notification_active_since' ORDER BY id) FROM mealplan.plans x),
 'meals',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM mealplan.meals x),
 'components',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM mealplan.components x),
 'participants',(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM mealplan.participants x),
 'leftovers',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM mealplan.leftover_sources x),
 'allocations',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM mealplan.leftover_allocations x),
 'weekly',(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM mealplan.weekly_settings x),
 'feeds',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM mealplan.calendar_feeds x),
 'tombstones',(SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM mealplan.calendar_tombstones x),
 'reminders',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.reminders x WHERE module_id='mealplan'),
 'jobs',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.jobs x WHERE module_id='mealplan'),
 'audit',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.audit_log x))::text`).Scan(&facts)
	if err != nil {
		t.Fatal("snapshot existing Mealplan facts", err)
	}
	return facts
}

func TestNotificationMealplanPopulated12UpgradePreservesFacts(t *testing.T) {
	f, pid := planningFixture(t)
	notificationMealplanRestore12(t, f)
	// Populate the actual predecessor through the existing application commands;
	// old meal dates intentionally represent history, not new notification consent.
	for _, slot := range []string{"lunch", "dinner"} {
		in := planningInput(t, f, pid)
		in.Action = "save"
		in.Meal = store.PlanningMealInput{Title: "Synthetic predecessor meal", PlannedOn: "2026-10-25", Slot: slot,
			Kind: "free", Status: "planned", Servings: "2"}
		planningMutation(t, f, pid, in)
	}
	auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Synthetic retained template", "starts_on": "2026-10-22",
		"ends_on": "2026-11-04", "is_template": true})
	facts := notificationMealplanLegacyFacts(t, f)
	var before time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("upgrade populated Mealplan12 predecessor", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("complete readiness after Mealplan upgrade", err)
		}
		if notificationMealplanLegacyFacts(t, f) != facts {
			t.Fatal("upgrade or repeated migration changed existing planning or audit facts")
		}
		var untouched bool
		err := f.owner.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM mealplan.notification_settings)
 AND NOT EXISTS(SELECT 1 FROM mealplan.plans WHERE notification_active_since<$1)
 AND (SELECT count(*) FROM core.audit_policies)=165
 AND (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='mealplan' AND c.relkind IN('r','p') AND c.relname<>'schema_migrations')=10`, before).Scan(&untouched)
		if err != nil || !untouched {
			t.Fatal("upgrade invented notifications, responsibility or invalid activation baselines", untouched, err)
		}
	}
}

func TestNotificationMealplanReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct{ name, sql string }{
		{"public settings read", `GRANT SELECT ON mealplan.notification_settings TO PUBLIC`},
		{"app can truncate settings", `GRANT TRUNCATE ON mealplan.notification_settings TO household_app`},
		{"column grant drift", `GRANT UPDATE(steps) ON mealplan.notification_settings TO PUBLIC`},
		{"plan activation disabled", `ALTER TABLE mealplan.plans DISABLE TRIGGER notification_plan_clock`},
		{"nullable plan clock", `ALTER TABLE mealplan.plans ALTER COLUMN notification_active_since DROP NOT NULL`},
		{"settings guard disabled", `ALTER TABLE mealplan.notification_settings DISABLE TRIGGER notification_settings_guard`},
		{"settings audit disabled", `ALTER TABLE mealplan.notification_settings DISABLE TRIGGER notification_settings_audit`},
		{"public trigger helper", `GRANT EXECUTE ON FUNCTION mealplan.notification_settings_guard() TO PUBLIC`},
		{"public predecessor", `GRANT EXECUTE ON FUNCTION mealplan.notification_predecessor_catalog() TO PUBLIC`},
		{"public catalog", `GRANT EXECUTE ON FUNCTION mealplan.notification_catalog() TO PUBLIC`},
		{"row security drift", `ALTER TABLE mealplan.notification_settings ENABLE ROW LEVEL SECURITY`},
		{"unreviewed column", `ALTER TABLE mealplan.notification_settings ADD COLUMN private_recipe_content text`},
		{"nullable reviewed version", `ALTER TABLE mealplan.notification_settings ALTER COLUMN meal_version DROP NOT NULL`},
		{"unbounded settings version", `ALTER TABLE mealplan.notification_settings DROP CONSTRAINT notification_settings_version_check`},
		{"missing responsible index", `DROP INDEX mealplan.notification_settings_responsible`},
		{"clock check removed", `ALTER TABLE mealplan.plans DROP CONSTRAINT mealplan_notification_active_clock`},
		{"extra table", `CREATE TABLE mealplan.unreviewed_notification_outbox(id uuid)`},
		{"unsafe helper search path", `ALTER FUNCTION mealplan.notification_plan_clock() SET search_path=public`},
		{"catalog implementation replaced", `CREATE OR REPLACE FUNCTION mealplan.notification_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT NULL::jsonb'`},
		{"predecessor implementation replaced", `CREATE OR REPLACE FUNCTION mealplan.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT NULL::jsonb'`},
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
				t.Fatal("install rollback-only Mealplan drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT coalesce(mealplan.notification_ready(),false) AND mealplan.expansion_ready() AND core.audit_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("Mealplan readiness accepted metadata drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rollback-only probes", err)
	}
}

func TestNotificationMealplanUpgradeRejectsReplacedPredecessor(t *testing.T) {
	f, _ := planningFixture(t)
	notificationMealplanRestore12(t, f)
	facts := notificationMealplanLegacyFacts(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION mealplan.expansion_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT true'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("upgrade trusted a substituted Mealplan12 readiness function")
	}
	var absent bool
	err := f.owner.QueryRow(f.ctx, `SELECT to_regclass('mealplan.notification_settings') IS NULL
 AND to_regprocedure('mealplan.notification_catalog()') IS NULL AND to_regprocedure('mealplan.notification_ready()') IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='mealplan.plans'::regclass AND attname='notification_active_since' AND NOT attisdropped)`).Scan(&absent)
	if err != nil || !absent {
		t.Fatal("rejected predecessor left partial Mealplan notification metadata", absent, err)
	}
	if notificationMealplanLegacyFacts(t, f) != facts {
		t.Fatal("rejected predecessor changed existing facts")
	}
}
