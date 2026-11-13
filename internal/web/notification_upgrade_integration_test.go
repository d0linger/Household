//go:build integration

package web

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

// restoreNotificationPredecessor is restricted to the disposable, leased web
// integration fixture. It reconstructs the reviewed Core37 contract, not an
// inferred predecessor based on arbitrary current security-definer functions.
func restoreNotificationPredecessor(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationPantryRestore17(t, f)
	var version int
	var dirty bool
	if err := f.owner.QueryRow(f.ctx, "SELECT version,dirty FROM core.schema_migrations").Scan(&version, &dirty); err != nil || version != 38 || dirty {
		t.Fatalf("notification predecessor requires clean isolated Core38: version=%d dirty=%t err=%v", version, dirty, err)
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
	_, err = tx.Exec(f.ctx, `DROP FUNCTION core.notification_foundation_ready();
DROP FUNCTION core.notification_foundation_catalog();
DROP TABLE core.notification_deliveries,core.notification_device_preferences;
DROP TABLE core.notification_intents,core.notification_preferences;
ALTER TABLE core.push_subscriptions DROP CONSTRAINT push_subscriptions_notification_owner;
ALTER TABLE core.jobs DROP CONSTRAINT jobs_notification_household;
DELETE FROM core.audit_policies WHERE schema_name='core' AND table_name IN('notification_preferences','notification_device_preferences');
DO $restore$
DECLARE snapshot jsonb;baseline jsonb;catalog_before jsonb;catalog_after jsonb;producer text;
 authority text;overview text;fuel_ready text;old_audit text;audit_ready text;
BEGIN
 snapshot=documents.predecessor_catalog();baseline=snapshot->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';fuel_ready=baseline->'fuel.module_ready()'->>'definition';
 EXECUTE replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=37 AND NOT dirty)');
 EXECUTE replace(overview,'bool_and(version=33 AND NOT dirty)','bool_and(version=37 AND NOT dirty)');
 EXECUTE replace(fuel_ready,md5(authority),md5(replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=37 AND NOT dirty)')));
 catalog_before=snapshot->'finance_catalog';
 producer=replace(baseline->'core.finance_producers_ready()'->>'definition',
  '(SELECT count(*) FROM core.audit_policies)=152','(SELECT count(*) FROM core.audit_policies)=163');
 SELECT jsonb_set(catalog_before,'{functions}',jsonb_agg(CASE WHEN value->>0='core.finance_producers_ready()'
  THEN jsonb_set(value,'{1}',to_jsonb(producer)) ELSE value END ORDER BY ordinal)) INTO catalog_after
 FROM jsonb_array_elements(catalog_before->'functions') WITH ORDINALITY entries(value,ordinal);
 EXECUTE producer;
 EXECUTE replace(baseline->'core.finance_ready()'->>'definition',quote_literal(catalog_before::text)||'::jsonb',quote_literal(catalog_after::text)||'::jsonb');
 old_audit=baseline->'core.audit_ready()'->>'definition';
 audit_ready=replace(old_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 audit_ready=replace(audit_ready,'''auth_challenges'',''rate_limit_buckets''','''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 EXECUTE audit_ready;
 EXECUTE replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(audit_ready));
 EXECUTE replace(baseline->'core.planning_extensions_ready()'->>'definition',
  '(SELECT count(*) FROM core.audit_policies)=152','(SELECT count(*) FROM core.audit_policies)=163');
END $restore$;
UPDATE core.schema_migrations SET version=37 WHERE version=38 AND NOT dirty;
DO $check$
BEGIN
 IF NOT core.audit_ready() OR NOT core.finance_ready() OR NOT pantry.prepared_capture_ready()
  OR NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready()
  OR NOT fuel.module_ready() OR NOT fuel.overview_ready() OR (SELECT count(*) FROM core.audit_policies)<>163 THEN
  RAISE EXCEPTION 'isolated notification predecessor reconstruction failed';END IF;
END $check$;`)
	if err != nil {
		t.Fatal("restore isolated notification predecessor:", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("commit isolated notification predecessor:", err)
	}
}

func notificationLegacySnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var snapshot string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'users',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM core.users r),
 'sessions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM core.sessions r),
 'preferences',(SELECT jsonb_agg(to_jsonb(r) ORDER BY household_id,user_id) FROM core.user_preferences r),
 'push',(SELECT jsonb_agg(to_jsonb(r)-'encryption_format' ORDER BY id) FROM core.push_subscriptions r),
 'jobs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM core.jobs r),
 'notifications',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM core.notifications r))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal("snapshot synthetic predecessor data:", err)
	}
	return snapshot
}

func TestNotificationFoundationPopulatedCore37Upgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("fresh notification installation is not fully ready:", err)
	}
	// Synthetic ciphertext deliberately has no usable subscription endpoint and
	// is never decrypted or sent. Upgrade must preserve these exact stored bytes.
	_, err := f.owner.Exec(f.ctx, `INSERT INTO core.push_subscriptions(household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,encryption_format)
 VALUES($1,$2,$3,$4,'fixture-legacy-key',1)`, f.identity.HouseholdID, f.identity.UserID, make([]byte, 32), []byte("synthetic legacy encrypted subscription"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload_version,payload,dedupe_key)
 VALUES($1,'shopping',1,'shopping_push_v1',1,'{"schema":1,"fixture":"existing-job"}','fixture-existing-shopping-job')`, f.identity.HouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 VALUES($1,$2,'shopping','shopping_notice','Synthetic existing notice','/notifications','fixture-existing-notice')`, f.identity.HouseholdID, f.identity.UserID)
	if err != nil {
		t.Fatal(err)
	}
	before := notificationLegacySnapshot(t, f)
	restoreNotificationPredecessor(t, f)
	if after := notificationLegacySnapshot(t, f); before != after {
		t.Fatal("predecessor reconstruction changed existing synthetic records")
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("populated upgrade or idempotent rerun:", err)
		}
		if err = database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("upgraded contracts not ready:", err)
		}
		if after := notificationLegacySnapshot(t, f); before != after {
			t.Fatal("notification upgrade changed existing identities, ciphertext, preferences, jobs or notices")
		}
		var legacyFormat int
		if err = f.owner.QueryRow(f.ctx, "SELECT encryption_format FROM core.push_subscriptions WHERE encryption_key_id='fixture-legacy-key'").Scan(&legacyFormat); err != nil || legacyFormat != 1 {
			t.Fatal("upgrade must explicitly identify unchanged legacy subscription ciphertext", legacyFormat, err)
		}
		var added, policies int
		err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM core.notification_preferences)+(SELECT count(*) FROM core.notification_device_preferences)+
 (SELECT count(*) FROM core.notification_intents)+(SELECT count(*) FROM core.notification_deliveries),
 (SELECT count(*) FROM core.audit_policies)`).Scan(&added, &policies)
		if err != nil || added != 0 || policies != 165 {
			t.Fatal("upgrade opted in accounts, replayed history or lost audited tables", added, policies, err)
		}
	}
}

func TestNotificationFoundationRejectsUnreviewedPredecessors(t *testing.T) {
	f := newIntegrationFixture(t)
	restoreNotificationPredecessor(t, f)
	for _, fn := range []string{
		"fuel.module_ready()", "core.finance_producers_ready()", "core.finance_ready()",
		"core.audit_ready()", "pantry.prepared_capture_ready()", "core.planning_extensions_ready()",
	} {
		t.Run(fn, func(t *testing.T) {
			var reviewed string
			if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef($1::regprocedure)", fn).Scan(&reviewed); err != nil {
				t.Fatal(err)
			}
			// Change the outer function header. Catalog-pinned bodies can contain
			// nested dollar delimiters inside JSON; editing those is invalid SQL.
			tampered := strings.Replace(reviewed, "SET search_path TO 'pg_catalog'", "SET search_path TO 'pg_catalog', 'public'", 1)
			if tampered == reviewed {
				t.Fatal("cannot construct independently changed predecessor")
			}
			if _, err := f.owner.Exec(f.ctx, tampered); err != nil {
				t.Fatal(err)
			}
			err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL"))
			if err == nil || !strings.Contains(err.Error(), "Notifications") ||
				(!strings.Contains(err.Error(), "predecessor") && !strings.Contains(err.Error(), "catalog")) {
				t.Fatal("migration did not reject unreviewed security metadata:", err)
			}
			var partial bool
			if err := f.owner.QueryRow(f.ctx, "SELECT to_regclass('core.notification_preferences') IS NOT NULL").Scan(&partial); err != nil || partial {
				t.Fatal("failed migration left a partially installed notification schema", partial, err)
			}
			if _, err := f.owner.Exec(f.ctx, reviewed); err != nil {
				t.Fatal(err)
			}
			// Only the isolated fixture resets dirty migration metadata after the
			// expected rollback. Production recovery must never bypass this guard.
			if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=37,dirty=false"); err != nil {
				t.Fatal(err)
			}
		})
		if t.Failed() {
			return
		}
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal("reviewed predecessor did not upgrade after isolated repairs:", err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationFoundationReadinessRejectsSchemaTampering(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, change := range []string{
		"ALTER TABLE core.notification_preferences ALTER COLUMN preview_policy DROP NOT NULL",
		"DROP INDEX core.notification_intents_due",
		"ALTER TABLE core.notification_device_preferences DISABLE TRIGGER household_audit_change",
		"GRANT SELECT ON core.notification_intents TO PUBLIC",
		"UPDATE core.audit_policies SET safe_columns=array_append(safe_columns,'name') WHERE schema_name='core' AND table_name='notification_device_preferences'",
		"ALTER TABLE core.push_subscriptions DROP CONSTRAINT push_subscriptions_notification_owner CASCADE",
		"ALTER FUNCTION core.notification_foundation_catalog() VOLATILE",
		"GRANT EXECUTE ON FUNCTION core.notification_foundation_catalog() TO household_app",
	} {
		t.Run(change, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := tx.Rollback(ctx); err != nil {
					t.Error("restore transactional tamper:", err)
				}
			}()
			if _, err = tx.Exec(f.ctx, change); err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT core.notification_foundation_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("notification readiness accepted changed schema/ACL/policy", ready, err)
			}
		})
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("rollback did not restore exact notification readiness:", err)
		}
	}
}

func TestNotificationFoundationDatabaseOwnershipAndBounds(t *testing.T) {
	f := newIntegrationFixture(t)
	other := shoppingTestMember(t, f, "notification-other", "reader")
	subscription, intent := value.UUID(), value.UUID()
	_, err := f.owner.Exec(f.ctx, `INSERT INTO core.push_subscriptions(id,household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,encryption_format)
 VALUES($1,$2,$3,$4,$5,'fixture-key',1)`, subscription, f.identity.HouseholdID, f.identity.UserID, make([]byte, 32), []byte("synthetic ciphertext"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `INSERT INTO core.notification_intents(id,household_id,user_id,module_generation,resource_type,resource_id,
 source_revision,occurrence,category,event_at,not_before,expires_at,dedupe_key)
 VALUES($1,$2,$3,0,'test',$4,1,'owner-fixture','test',now(),now(),now()+interval '1 minute',$5)`,
		intent, f.identity.HouseholdID, f.identity.UserID, value.UUID(), "notification-v1/"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, query, code string
		args              []any
	}{
		{"foreign device owner", `INSERT INTO core.notification_device_preferences(subscription_id,household_id,user_id,category) VALUES($1,$2,$3,'test')`, "23503", []any{subscription, f.identity.HouseholdID, other.UserID}},
		{"foreign intent recipient", `INSERT INTO core.notification_deliveries(household_id,user_id,intent_id,subscription_id,preference_version,device_version) VALUES($1,$2,$3,$4,1,1)`, "23503", []any{f.identity.HouseholdID, other.UserID, intent, subscription}},
		{"missing membership", `INSERT INTO core.notification_preferences(household_id,user_id,category) VALUES($1,$2,'test')`, "23503", []any{f.identity.HouseholdID, value.UUID()}},
		{"missing consent time", `INSERT INTO core.notification_preferences(household_id,user_id,category,enabled) VALUES($1,$2,'test',true)`, "23514", []any{f.identity.HouseholdID, f.identity.UserID}},
		{"partial quiet hours", `INSERT INTO core.notification_preferences(household_id,user_id,category,quiet_start) VALUES($1,$2,'test',1200)`, "23514", []any{f.identity.HouseholdID, f.identity.UserID}},
		{"expired freshness bound", `UPDATE core.notification_intents SET expires_at=not_before+interval '61 seconds' WHERE id=$1`, "23514", []any{intent}},
		{"mismatched module", `UPDATE core.notification_intents SET module_id='expenses',module_generation=1 WHERE id=$1`, "23514", []any{intent}},
		{"unsupported schema", `UPDATE core.notification_intents SET schema_version=2 WHERE id=$1`, "23514", []any{intent}},
		{"zero revision", `UPDATE core.notification_intents SET source_revision=0 WHERE id=$1`, "23514", []any{intent}},
		{"infinite expiry", `UPDATE core.notification_intents SET expires_at='infinity' WHERE id=$1`, "23514", []any{intent}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := f.owner.Exec(f.ctx, test.query, test.args...)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != test.code {
				t.Fatalf("database accepted invalid notification identity/bounds: want %s, got %v", test.code, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("rejected writes damaged notification foundation:", err)
	}
}
