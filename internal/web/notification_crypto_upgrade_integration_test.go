//go:build integration

package web

import (
	"context"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"

	"github.com/jackc/pgx/v5"
)

// This reconstructs the actual predecessor only in the disposable owner
// fixture. A version2 record cannot safely be downgraded and is refused.
func notificationCryptoRestore39(t *testing.T, f *integrationFixture) {
	t.Helper()
	var valid bool
	var version int
	err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner',
 (SELECT version FROM core.schema_migrations WHERE NOT dirty)`).Scan(&valid, &version)
	if err != nil || !valid {
		t.Fatal("crypto predecessor requires isolated owner fixture", valid, err)
	}
	if version == 39 {
		return
	}
	if version != 40 {
		t.Fatal("unexpected crypto predecessor source", version)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer notificationCryptoRollback(tx)
	_, err = tx.Exec(f.ctx, `DO $restore$
 DECLARE snapshot jsonb;item record;
 BEGIN
 IF NOT core.crypto_envelopes_ready() OR NOT core.security_events_ready()
 OR NOT(SELECT count(*)=1 AND bool_and(version=13 AND NOT dirty) FROM mealplan.schema_migrations)
 OR EXISTS(SELECT 1 FROM core.totp_credentials WHERE encryption_format<>1)
 OR EXISTS(SELECT 1 FROM core.passkey_credentials WHERE encryption_format<>1 OR encryption_key_id<>'v1')
 OR EXISTS(SELECT 1 FROM core.auth_challenges WHERE encryption_format IS NOT NULL AND(encryption_format<>1 OR encryption_key_id<>'v1'))
 OR EXISTS(SELECT 1 FROM core.push_subscriptions WHERE encryption_format<>1)
 OR EXISTS(SELECT 1 FROM core.notifications WHERE left(kind,9)='security_') THEN
 RAISE EXCEPTION 'Crypto predecessor requires reviewed unused version2 protocol state';END IF;
 snapshot=core.crypto_envelopes_predecessor_catalog();
 IF snapshot->'crypto_catalog' IS NULL OR snapshot->'fuel_catalog' IS NULL OR snapshot->'functions' IS NULL THEN
 RAISE EXCEPTION 'Missing frozen Core39 evidence';END IF;
 DROP TRIGGER security_events_guard ON core.notifications;
 DROP FUNCTION core.security_events_ready(),core.security_events_catalog(),core.security_events_guard();
 ALTER TABLE core.totp_credentials DROP COLUMN encryption_format;
 ALTER TABLE core.passkey_credentials DROP COLUMN encryption_format,DROP COLUMN encryption_key_id;
 ALTER TABLE core.push_subscriptions DROP COLUMN encryption_format;
 ALTER TABLE core.auth_challenges DROP COLUMN encryption_format,DROP COLUMN encryption_key_id;
 FOR item IN SELECT key,value FROM jsonb_each(snapshot->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 UPDATE core.schema_migrations SET version=39 WHERE version=40 AND NOT dirty;
 IF core.crypto_envelopes_catalog() IS DISTINCT FROM snapshot->'crypto_catalog'
 OR fuel.module_catalog() IS DISTINCT FROM snapshot->'fuel_catalog'
 OR NOT fuel.module_ready() OR NOT fuel.overview_ready() OR NOT fuel.notification_ready()
 OR NOT core.audit_ready() OR NOT core.notification_foundation_ready() OR NOT core.notification_financial_ready()
 OR NOT mealplan.notification_ready() OR NOT documents.notification_ready() OR NOT meters.notification_ready()
 OR NOT expenses.notification_ready() OR NOT pantry.notification_observations_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Core39 reconstruction changed reviewed contracts';END IF;
 DROP FUNCTION core.crypto_envelopes_ready(),core.crypto_envelopes_catalog(),core.crypto_envelopes_predecessor_catalog();
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Core39 predecessor", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationCryptoRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func notificationCryptoLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'totp',(SELECT jsonb_agg(to_jsonb(x)-'encryption_format' ORDER BY user_id) FROM core.totp_credentials x),
 'passkeys',(SELECT jsonb_agg(to_jsonb(x)-ARRAY['encryption_format','encryption_key_id'] ORDER BY id) FROM core.passkey_credentials x),
 'challenges',(SELECT jsonb_agg(to_jsonb(x)-ARRAY['encryption_format','encryption_key_id'] ORDER BY id) FROM core.auth_challenges x),
 'push',(SELECT jsonb_agg(to_jsonb(x)-'encryption_format' ORDER BY id) FROM core.push_subscriptions x),
 'notifications',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.notifications x),
 'policies',(SELECT jsonb_agg(to_jsonb(x) ORDER BY schema_name,table_name) FROM core.audit_policies x),
 'audit',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.audit_log x))::text`).Scan(&facts)
	if err != nil {
		t.Fatal("capture historical protected record facts", err)
	}
	return facts
}

func TestNotificationCryptoPopulated39UpgradePreservesFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationCryptoRestore39(t, f)
	tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer notificationCryptoRollback(tx)
	// These are deliberately malformed protocol fixtures. A metadata migration
	// preserves them byte-for-byte; runtime cryptography rejects them separately.
	for _, sql := range []string{
		`INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,last_accepted_counter,enabled_at)
 VALUES($1,'\x01','v1',42,'2026-01-01')`,
		`INSERT INTO core.passkey_credentials(user_id,credential_id,encrypted_credential,name,last_used_at)
 VALUES($1,'\x0102','\x01','Synthetic predecessor','2026-01-02')`,
		`INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at,consumed_at)
 SELECT CASE WHEN purpose='passkey-login' THEN NULL ELSE $1::uuid END,purpose,sha256(convert_to(purpose,'UTF8')),
 '{"sealed":"malformed fixture"}'::jsonb,'2026-01-03','2026-01-02'
 FROM unnest(ARRAY['passkey-login','passkey-register','passkey-reauth','password-reset']) purpose`,
	} {
		if _, err = tx.Exec(f.ctx, sql, f.identity.UserID); err != nil {
			t.Fatal("seed actual Core39 protected record", err)
		}
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.push_subscriptions(household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,disabled_at)
 VALUES($1,$2,sha256('synthetic predecessor'::bytea),'\x01','fixture-legacy-key','2026-01-02')`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	facts := notificationCryptoLegacyFacts(t, f)
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("upgrade real populated Core39", err)
		}
		if err = database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("complete upgraded readiness", err)
		}
		notificationConstraintTriggerCensus(t, f, "core.notification_financial_catalog()", []string{"core.notification_financial_epochs"})
		notificationConstraintTriggerCensus(t, f, "expenses.notification_catalog()", []string{"expenses.notification_payment_reminders", "expenses.notification_budget_observations"})
		notificationConstraintTriggerCensus(t, f, "meters.notification_catalog()", []string{"meters.notification_settings", "meters.notification_observations"})
		notificationConstraintTriggerCensus(t, f, "documents.notification_catalog()", []string{"documents.notification_settings", "documents.notification_processing_events", "documents.notification_import_events"})
		if notificationCryptoLegacyFacts(t, f) != facts {
			t.Fatal("metadata upgrade changed historical ciphertext, state, identifiers or audit")
		}
		var correct bool
		err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 AND bool_and(encryption_format=1 AND encryption_key_id='v1') FROM core.totp_credentials)
 AND(SELECT count(*)=1 AND bool_and(encryption_format=1 AND encryption_key_id='v1') FROM core.passkey_credentials)
 AND(SELECT count(*)=3 AND bool_and(encryption_format=1 AND encryption_key_id='v1') FROM core.auth_challenges WHERE purpose LIKE 'passkey-%')
 AND(SELECT count(*)=1 AND bool_and(encryption_format IS NULL AND encryption_key_id IS NULL) FROM core.auth_challenges WHERE purpose='password-reset')
 AND(SELECT count(*)=1 AND bool_and(encryption_format=1 AND encryption_key_id='fixture-legacy-key') FROM core.push_subscriptions)
 AND NOT EXISTS(SELECT 1 FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum
 WHERE a.attrelid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)
 AND a.attname IN('encryption_format','encryption_key_id'))`).Scan(&correct)
		if err != nil || !correct {
			t.Fatal("metadata was not explicitly and selectively backfilled", correct, err)
		}
	}
}

func TestNotificationCryptoReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationConstraintTriggerRecreation(t, f, "core.notification_financial_catalog()", "core.notification_financial_ready()", "core.notification_financial_epochs")
	notificationConstraintTriggerRecreation(t, f, "expenses.notification_catalog()", "expenses.notification_ready()", "expenses.notification_payment_reminders")
	notificationConstraintTriggerRecreation(t, f, "expenses.notification_catalog()", "expenses.notification_ready()", "expenses.notification_budget_observations")
	notificationConstraintTriggerRecreation(t, f, "meters.notification_catalog()", "meters.notification_ready()", "meters.notification_settings")
	notificationConstraintTriggerRecreation(t, f, "meters.notification_catalog()", "meters.notification_ready()", "meters.notification_observations")
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_settings")
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_processing_events")
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_import_events")
	for _, tc := range []struct{ name, sql string }{
		{"format nullable", `ALTER TABLE core.totp_credentials ALTER COLUMN encryption_format DROP NOT NULL`},
		{"implicit downgrade default", `ALTER TABLE core.passkey_credentials ALTER COLUMN encryption_format SET DEFAULT 1`},
		{"missing key", `ALTER TABLE core.passkey_credentials ALTER COLUMN encryption_key_id DROP NOT NULL`},
		{"missing challenge bounds", `ALTER TABLE core.auth_challenges DROP CONSTRAINT auth_challenges_encryption_envelope`},
		{"missing rotation index", `DROP INDEX core.push_subscriptions_encryption_rotation`},
		{"public catalog", `GRANT EXECUTE ON FUNCTION core.crypto_envelopes_catalog() TO PUBLIC`},
		{"app predecessor", `GRANT EXECUTE ON FUNCTION core.crypto_envelopes_predecessor_catalog() TO household_app`},
		{"unsafe catalog search path", `ALTER FUNCTION core.crypto_envelopes_catalog() SET search_path=public`},
		{"replaced predecessor", `CREATE OR REPLACE FUNCTION core.crypto_envelopes_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT NULL::jsonb'`},
		{"extra protocol field", `ALTER TABLE core.totp_credentials ADD COLUMN unreviewed_plaintext text`},
		{"public protocol read", `GRANT SELECT ON core.passkey_credentials TO PUBLIC`},
		{"protocol trigger grant", `GRANT TRIGGER ON core.push_subscriptions TO household_app`},
		{"row security drift", `ALTER TABLE core.auth_challenges ENABLE ROW LEVEL SECURITY`},
		{"secret audit exposure", `UPDATE core.audit_policies SET safe_columns=safe_columns||'encrypted_secret'::text WHERE schema_name='core' AND table_name='totp_credentials'`},
		{"security guard disabled", `ALTER TABLE core.notifications DISABLE TRIGGER security_events_guard`},
		{"security guard public", `GRANT EXECUTE ON FUNCTION core.security_events_guard() TO PUBLIC`},
		{"security catalog replaced", `CREATE OR REPLACE FUNCTION core.security_events_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT NULL::jsonb'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer notificationCryptoRollback(tx)
			if _, err = tx.Exec(f.ctx, tc.sql); err != nil {
				t.Fatal("install rollback-only crypto drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT coalesce(core.crypto_envelopes_ready() AND core.security_events_ready(),false)").Scan(&ready); err != nil || ready {
				t.Fatal("readiness accepted encryption/security metadata drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rolled back drift probes", err)
	}
}

func TestNotificationCryptoUpgradeRejectsReplacedPredecessor(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationCryptoRestore39(t, f)
	facts := notificationCryptoLegacyFacts(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION fuel.overview_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT true'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("crypto upgrade accepted substituted Fuel/Core39 authority")
	}
	var absent bool
	err := f.owner.QueryRow(f.ctx, `SELECT to_regprocedure('core.crypto_envelopes_catalog()') IS NULL
 AND to_regprocedure('core.crypto_envelopes_ready()') IS NULL AND to_regprocedure('core.security_events_guard()') IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='core.totp_credentials'::regclass AND attname='encryption_format' AND NOT attisdropped)`).Scan(&absent)
	if err != nil || !absent || notificationCryptoLegacyFacts(t, f) != facts {
		t.Fatal("rejected predecessor left partial metadata or changed facts", absent, err)
	}
}

func TestNotificationCryptoEnvelopeConstraints(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct {
		name, sql string
		valid     bool
	}{
		{"valid bound TOTP", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,encryption_format)
 SELECT user_id,repeat('x',29)::bytea,repeat('a',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, true},
		{"format must be explicit", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id)
 SELECT user_id,repeat('x',29)::bytea,repeat('a',64) FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"unknown format", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,encryption_format)
 SELECT user_id,repeat('x',29)::bytea,repeat('a',64),3 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"uppercase key", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,encryption_format)
 SELECT user_id,repeat('x',29)::bytea,repeat('A',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"short ciphertext", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,encryption_format)
 SELECT user_id,'\x01',repeat('a',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"oversized TOTP", `INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id,encryption_format)
 SELECT user_id,repeat('x',4097)::bytea,repeat('a',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"null passkey key", `INSERT INTO core.passkey_credentials(user_id,credential_id,encrypted_credential,name,encryption_key_id,encryption_format)
 SELECT user_id,'\x01',repeat('x',29)::bytea,'Synthetic',NULL,2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"oversized push", `INSERT INTO core.push_subscriptions(household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,encryption_format)
 SELECT household_id,user_id,sha256('fixture'::bytea),repeat('x',16385)::bytea,repeat('a',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"anonymous bound login", `INSERT INTO core.auth_challenges(purpose,token_hash,payload,expires_at,encryption_key_id,encryption_format)
 SELECT 'passkey-login',sha256('fixture'::bytea),jsonb_build_object('sealed',encode(repeat('x',29)::bytea,'base64')),now()+interval '1 minute',repeat('a',64),2
 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, true},
		{"anonymous registration", `INSERT INTO core.auth_challenges(purpose,token_hash,payload,expires_at,encryption_key_id,encryption_format)
 SELECT 'passkey-register',sha256('fixture'::bytea),jsonb_build_object('sealed',encode(repeat('x',29)::bytea,'base64')),now()+interval '1 minute',repeat('a',64),2
 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"encrypted challenge null metadata", `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at)
 SELECT user_id,'passkey-reauth',sha256('fixture'::bytea),'{}',now()+interval '1 minute' FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"ordinary challenge null metadata", `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at)
 SELECT user_id,'password-reset',sha256('fixture'::bytea),'{}',now()+interval '1 minute' FROM core.memberships WHERE user_id=$1 AND household_id=$2`, true},
		{"ordinary challenge cannot carry crypto metadata", `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at,encryption_key_id,encryption_format)
 SELECT user_id,'password-reset',sha256('fixture'::bytea),'{}',now()+interval '1 minute','v1',1 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"challenge envelope extra field", `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at,encryption_key_id,encryption_format)
 SELECT user_id,'passkey-register',sha256('fixture'::bytea),jsonb_build_object('sealed',encode(repeat('x',29)::bytea,'base64'),'plaintext','forbidden'),now()+interval '1 minute',repeat('a',64),2
 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
		{"challenge envelope empty", `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at,encryption_key_id,encryption_format)
 SELECT user_id,'passkey-register',sha256('fixture'::bytea),'{}',now()+interval '1 minute',repeat('a',64),2 FROM core.memberships WHERE user_id=$1 AND household_id=$2`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.pool)
			if err != nil {
				t.Fatal(err)
			}
			defer notificationCryptoRollback(tx)
			tag, err := tx.Exec(f.ctx, tc.sql, f.identity.UserID, f.identity.HouseholdID)
			if tc.valid && (err != nil || tag.RowsAffected() != 1) {
				t.Fatal("valid explicit protocol metadata rejected", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("invalid protocol metadata accepted")
			}
		})
	}
}

func TestNotificationSecurityEventIdentityConstraints(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct {
		name, extra string
	}{
		{"different dedupe identity", "'11111111-1111-4111-8111-111111111111'::uuid, 'security-event/22222222-2222-4222-8222-222222222222', NULL::timestamptz,NULL::timestamptz"},
		{"zero identity", "'00000000-0000-0000-0000-000000000000'::uuid, 'security-event/00000000-0000-0000-0000-000000000000', NULL::timestamptz,NULL::timestamptz"},
		{"pre-read event", "'11111111-1111-4111-8111-111111111111'::uuid, 'security-event/11111111-1111-4111-8111-111111111111', now(),NULL::timestamptz"},
		{"pre-dismissed event", "'11111111-1111-4111-8111-111111111111'::uuid, 'security-event/11111111-1111-4111-8111-111111111111', NULL::timestamptz,now()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), f.pool)
			if err != nil {
				t.Fatal(err)
			}
			defer notificationCryptoRollback(tx)
			// The SQL suffix is a closed test-case constant, not an input value.
			_, err = tx.Exec(f.ctx, `INSERT INTO core.notifications(household_id,user_id,kind,title,target_path,id,dedupe_key,read_at,dismissed_at)
 SELECT $1,$2,'security_password_changed','Sicherheitsereignis','/settings/security',`+tc.extra, f.identity.HouseholdID, f.identity.UserID)
			if err == nil {
				t.Fatal("security event accepted forged identity or unread state")
			}
		})
	}
}
