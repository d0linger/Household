//go:build integration

package web

import (
	"context"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
)

// Reconstruct the released, populated predecessor only in the leased test DB.
// Both snapshots were captured before their migration's first domain DDL.
func notificationFinanceRestore38And5(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationMetersRestore2(t, f)
	var valid bool
	var core, expenses int
	err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner',
 (SELECT version FROM core.schema_migrations WHERE NOT dirty),(SELECT version FROM expenses.schema_migrations WHERE NOT dirty)`).Scan(&valid, &core, &expenses)
	if err != nil || !valid {
		t.Fatal("Finance predecessor requires the isolated owner fixture", valid, err)
	}
	if core == 38 && expenses == 5 {
		return
	}
	if core != 39 || expenses != 6 {
		t.Fatal("unexpected Finance predecessor source versions", core, expenses)
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
 DECLARE expense_before jsonb;core_before jsonb;item record;
 BEGIN
 expense_before=expenses.notification_predecessor_catalog();core_before=core.notification_financial_predecessor_catalog();
 IF expense_before->'functions' IS NULL OR core_before->'finance_catalog' IS NULL
 OR expense_before->'expenses_catalog' IS DISTINCT FROM core_before->'expenses_catalog' THEN
 RAISE EXCEPTION 'missing reviewed Finance predecessor evidence';END IF;
 DROP TABLE expenses.notification_payment_reminders,expenses.notification_budget_observations;
 DROP FUNCTION expenses.notification_payment_guard(),expenses.notification_payment_audit(),expenses.notification_ready(),expenses.notification_catalog();
 FOR item IN SELECT key,value FROM jsonb_each(expense_before->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 DROP FUNCTION expenses.notification_predecessor_catalog();
 UPDATE expenses.schema_migrations SET version=5 WHERE version=6 AND NOT dirty;
 DROP TRIGGER notification_financial_epoch ON core.financial_policies;
 DROP FUNCTION core.notification_financial_epoch_change(),core.notification_financial_ready(),core.notification_financial_catalog();
 DROP FUNCTION core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid);
 DROP TABLE core.notification_financial_epochs;
 FOR item IN SELECT key,value FROM jsonb_each(core_before->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 DROP FUNCTION core.notification_financial_predecessor_catalog();
 UPDATE core.schema_migrations SET version=38 WHERE version=39 AND NOT dirty;
 IF core.finance_catalog() IS DISTINCT FROM core_before->'finance_catalog'
 OR expenses.finance_catalog() IS DISTINCT FROM core_before->'expenses_catalog'
 OR fuel.module_catalog() IS DISTINCT FROM core_before->'fuel_catalog'
 OR pantry.notification_observations_catalog() IS DISTINCT FROM core_before->'pantry_catalog'
 OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT fuel.module_ready()
 OR NOT pantry.notification_observations_ready() OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
 RAISE EXCEPTION 'Finance predecessor reconstruction changed reviewed contracts';END IF;
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Core38 and Expenses5", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationFinanceLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM expenses.entries e),
 'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM expenses.entry_lines l),
 'categories',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM expenses.categories c),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM core.financial_policies p),
 'subjects',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM core.financial_subjects s),
 'origins',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM core.finance_origin_resources o),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM core.audit_log a))::text`).Scan(&facts)
	if err != nil {
		t.Fatal("snapshot existing Finance facts", err)
	}
	return facts
}

func TestNotificationFinancePopulated38And5Upgrade(t *testing.T) {
	f, _, _, _ := expenseIntegrityFixture(t)
	facts := notificationFinanceLegacyFacts(t, f)
	notificationFinanceRestore38And5(t, f)
	if notificationFinanceLegacyFacts(t, f) != facts {
		t.Fatal("predecessor reconstruction changed populated financial history")
	}
	var before time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("upgrade populated financial predecessor", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("full readiness after Finance upgrade", err)
		}
		notificationConstraintTriggerCensus(t, f, "core.notification_financial_catalog()", []string{"core.notification_financial_epochs"})
		notificationConstraintTriggerCensus(t, f, "expenses.notification_catalog()", []string{"expenses.notification_payment_reminders", "expenses.notification_budget_observations"})
		if notificationFinanceLegacyFacts(t, f) != facts {
			t.Fatal("upgrade or repeat migration changed existing financial history")
		}
		var untouched bool
		err := f.owner.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM expenses.notification_payment_reminders)
 AND NOT EXISTS(SELECT 1 FROM expenses.notification_budget_observations)
 AND NOT EXISTS(SELECT 1 FROM core.notification_intents WHERE category='expenses')
 AND NOT EXISTS(SELECT 1 FROM core.notification_financial_epochs WHERE epoch<>0 OR updated_at<$1)
 AND (SELECT count(*) FROM core.notification_financial_epochs)=(SELECT count(*) FROM core.households)
 AND (SELECT count(*) FROM core.audit_policies)=165`, before).Scan(&untouched)
		if err != nil || !untouched {
			t.Fatal("upgrade invented financial reminders, consent, or historical epochs", untouched, err)
		}
	}
}

func TestNotificationFinanceReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationConstraintTriggerRecreation(t, f, "core.notification_financial_catalog()", "core.notification_financial_ready()", "core.notification_financial_epochs")
	notificationConstraintTriggerRecreation(t, f, "expenses.notification_catalog()", "expenses.notification_ready()", "expenses.notification_payment_reminders")
	notificationConstraintTriggerRecreation(t, f, "expenses.notification_catalog()", "expenses.notification_ready()", "expenses.notification_budget_observations")
	for _, tc := range []struct{ name, sql string }{
		{"app epoch write", `GRANT UPDATE ON core.notification_financial_epochs TO household_app`},
		{"public epoch", `GRANT SELECT ON core.notification_financial_epochs TO PUBLIC`},
		{"missing permission trigger", `ALTER TABLE core.financial_policies DISABLE TRIGGER notification_financial_epoch`},
		{"unbounded epoch", `ALTER TABLE core.notification_financial_epochs DROP CONSTRAINT notification_financial_epochs_epoch_check`},
		{"public recipient helper", `GRANT EXECUTE ON FUNCTION core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid) TO PUBLIC`},
		{"budget owner isolation", `ALTER TABLE expenses.notification_budget_observations DROP CONSTRAINT notification_budget_observations_household_id_user_id_fkey`},
		{"missing budget cursor index", `DROP INDEX expenses.notification_budget_observations_crossed`},
		{"reminder audit disabled", `ALTER TABLE expenses.notification_payment_reminders DISABLE TRIGGER notification_payment_audit`},
		{"reminder clock disabled", `ALTER TABLE expenses.notification_payment_reminders DISABLE TRIGGER notification_payment_guard`},
		{"reminder row security", `ALTER TABLE expenses.notification_payment_reminders ENABLE ROW LEVEL SECURITY`},
		{"extra reminder payload", `ALTER TABLE expenses.notification_payment_reminders ADD COLUMN unreviewed_amount bigint`},
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
				t.Fatal("install rollback-only Finance drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT core.notification_financial_ready() AND expenses.notification_ready() AND core.audit_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("Finance readiness accepted metadata drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rollback-only probes", err)
	}
}

func TestNotificationFinanceUpgradeRejectsReplacedPredecessor(t *testing.T) {
	f, _, _, _ := expenseIntegrityFixture(t)
	notificationFinanceRestore38And5(t, f)
	facts := notificationFinanceLegacyFacts(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION core.finance_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT true'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("upgrade trusted a substituted ready function")
	}
	var absent bool
	if err := f.owner.QueryRow(f.ctx, "SELECT to_regclass('core.notification_financial_epochs') IS NULL AND to_regclass('expenses.notification_payment_reminders') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal("rejected predecessor left partial notification objects", absent, err)
	}
	if notificationFinanceLegacyFacts(t, f) != facts {
		t.Fatal("rejected predecessor changed financial facts")
	}
}

// Inspect both ends of every sidecar foreign key; generated trigger names must
// never enter the stable catalog. These are owner-only isolated test probes.
func notificationConstraintTriggerCensus(t *testing.T, f *integrationFixture, catalog string, relations []string) {
	t.Helper()
	var complete bool
	err := f.owner.QueryRow(f.ctx,
		`SELECT jsonb_array_length(`+catalog+`->'constraint_triggers')=(
 SELECT count(*) FROM pg_trigger t LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint
 WHERE t.tgisinternal AND(t.tgrelid=ANY($1::text[]::regclass[]) OR k.conrelid=ANY($1::text[]::regclass[])))
 AND (SELECT count(*) FROM pg_trigger t JOIN pg_constraint k ON k.oid=t.tgconstraint
 WHERE t.tgisinternal AND k.contype='f' AND k.conrelid=ANY($1::text[]::regclass[]))=
 4*(SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid=ANY($1::text[]::regclass[]))
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE contype='f' AND conrelid=ANY($1::text[]::regclass[]))`, relations).Scan(&complete)
	if err != nil || !complete {
		t.Fatal("stable FK catalog must include checks and actions for every constraint", complete, err)
	}
}

func notificationConstraintTriggerRecreation(t *testing.T, f *integrationFixture, catalog, ready string, relation string) {
	t.Helper()
	for _, drift := range []bool{false, true} {
		name := "equivalent FK recreation"
		if drift {
			name = "FK deferrability drift"
		}
		t.Run(relation+"/"+name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer notificationCryptoRollback(tx)
			var constraint, definition, before string
			var oldTriggers []string
			err = tx.QueryRow(f.ctx, `SELECT conname,pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid=$1::regclass AND contype='f' ORDER BY conname LIMIT 1`, relation).Scan(&constraint, &definition)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.QueryRow(f.ctx, "SELECT ("+catalog+")->'constraint_triggers'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			const names = `SELECT array_agg(t.tgname::text ORDER BY t.tgname) FROM pg_trigger t
 JOIN pg_constraint k ON k.oid=t.tgconstraint
 WHERE k.conrelid=$1::regclass AND k.conname=$2 AND t.tgisinternal`
			if err = tx.QueryRow(f.ctx, names, relation, constraint).Scan(&oldTriggers); err != nil || len(oldTriggers) != 4 {
				t.Fatal("expected four real FK triggers", oldTriggers, err)
			}
			// Relation is a closed test constant; the constraint identifier and
			// definition come from PostgreSQL's own catalog/deparser.
			var quotedRelation, quotedConstraint string
			if err = tx.QueryRow(f.ctx, "SELECT $1::regclass::text,quote_ident($2)", relation, constraint).Scan(&quotedRelation, &quotedConstraint); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(f.ctx, "ALTER TABLE "+quotedRelation+" DROP CONSTRAINT "+quotedConstraint); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(f.ctx, "ALTER TABLE "+quotedRelation+" ADD CONSTRAINT "+quotedConstraint+" "+definition); err != nil {
				t.Fatal(err)
			}
			if drift {
				if _, err = tx.Exec(f.ctx, "ALTER TABLE "+quotedRelation+" ALTER CONSTRAINT "+quotedConstraint+" DEFERRABLE INITIALLY DEFERRED"); err != nil {
					t.Fatal(err)
				}
			}
			var current []string
			if err = tx.QueryRow(f.ctx, names, relation, constraint).Scan(&current); err != nil || len(current) != 4 {
				t.Fatal("recreated FK trigger census", current, err)
			}
			for _, old := range oldTriggers {
				for _, next := range current {
					if old == next {
						t.Fatal("probe did not recreate generated FK trigger identity")
					}
				}
			}
			var same, valid bool
			if err = tx.QueryRow(f.ctx, "SELECT ("+catalog+")->'constraint_triggers'=$1::jsonb,"+ready, before).Scan(&same, &valid); err != nil || same == drift || valid == drift {
				t.Fatal("FK catalog must tolerate generated identities and reject semantic drift", drift, same, valid, err)
			}
		})
	}
	t.Run(relation+"/incoming FK drift", func(t *testing.T) {
		tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
		if err != nil {
			t.Fatal(err)
		}
		defer notificationCryptoRollback(tx)
		var before, quotedRelation, columns string
		if err = tx.QueryRow(f.ctx, "SELECT ("+catalog+")->'constraint_triggers'").Scan(&before); err != nil {
			t.Fatal(err)
		}
		// Derive the actual primary-key columns, including composite keys.
		if err = tx.QueryRow(f.ctx, `SELECT k.conrelid::regclass::text,
 string_agg(quote_ident(a.attname),',' ORDER BY selected.ordinal)
 FROM pg_constraint k CROSS JOIN LATERAL unnest(k.conkey) WITH ORDINALITY selected(attnum,ordinal)
 JOIN pg_attribute a ON a.attrelid=k.conrelid AND a.attnum=selected.attnum
 WHERE k.conrelid=$1::regclass AND k.contype='p' GROUP BY k.conrelid`, relation).Scan(&quotedRelation, &columns); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, "CREATE TABLE core.notification_incoming_probe AS SELECT * FROM "+quotedRelation+" WITH NO DATA"); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, "ALTER TABLE core.notification_incoming_probe ADD FOREIGN KEY ("+columns+") REFERENCES "+quotedRelation+" ("+columns+") ON DELETE CASCADE"); err != nil {
			t.Fatal(err)
		}
		var same, valid bool
		if err = tx.QueryRow(f.ctx, "SELECT ("+catalog+")->'constraint_triggers'=$1::jsonb,"+ready, before).Scan(&same, &valid); err != nil || same || valid {
			t.Fatal("incoming FK action triggers must change catalog and fail readiness", same, valid, err)
		}
	})
}
