//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestThemePreferencesUpgradeAndValidation(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatalf("fresh database is not ready: %v", err)
	}
	save := func(t *testing.T, theme string, offline bool, status int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"theme": theme, "offline": offline})
		if err != nil {
			t.Fatal(err)
		}
		response, raw, err := f.request(http.MethodPost, "/api/v1/core/preferences", f.token,
			f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(body), nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("status %d, want %d: %s", response.StatusCode, status, raw)
		}
	}
	type preferences struct {
		theme   string
		offline bool
		updated time.Time
	}
	read := func(t *testing.T) preferences {
		t.Helper()
		var result preferences
		err := f.pool.QueryRow(f.ctx, `SELECT theme,offline_storage_enabled,updated_at
FROM core.user_preferences WHERE household_id=$1 AND user_id=$2`,
			f.identity.HouseholdID, f.identity.UserID).Scan(&result.theme, &result.offline, &result.updated)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// Recreate only the previous theme constraint/version inside this fixture,
	// then exercise the actual ordered migration runner with an existing row.
	save(t, "dark", true, http.StatusOK)
	before := read(t)
	restoreNavigationPredecessor(t, f)
	_, err := f.owner.Exec(f.ctx, `DROP TABLE core.passkey_credentials;
DROP INDEX core.auth_challenges_active_binding;
ALTER TABLE core.sessions DROP COLUMN reauthenticated_at;
ALTER TABLE core.user_preferences
DROP CONSTRAINT user_preferences_theme_check,
ADD CONSTRAINT user_preferences_theme_check CHECK(theme IN('system','light','dark'));
UPDATE core.schema_migrations SET version=34 WHERE version=36 AND NOT dirty`)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the exact previous Fuel authority/version contract as well as
	// the preference constraint, so this exercises a real Core34 predecessor.
	_, err = f.owner.Exec(f.ctx, `DO $restore$
DECLARE baseline jsonb;authority text;overview text;ready text;previous_authority text;old_audit text;previous_audit text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 previous_authority=replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=34 AND NOT dirty)');
 EXECUTE previous_authority;
 EXECUTE replace(overview,'bool_and(version=33 AND NOT dirty)','bool_and(version=34 AND NOT dirty)');
 EXECUTE replace(ready,md5(authority),md5(previous_authority));
 old_audit=baseline->'core.audit_ready()'->>'definition';
 previous_audit=replace(old_audit,
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 EXECUTE previous_audit;
 EXECUTE replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(previous_audit));
END $restore$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("reject_unreviewed_authority_predecessor", func(t *testing.T) {
		var reviewed string
		if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('fuel.module_ready()'::regprocedure)").Scan(&reviewed); err != nil {
			t.Fatal(err)
		}
		modified := strings.Replace(reviewed, "AS $function$", "AS $function$\n-- unreviewed predecessor\n", 1)
		if modified == reviewed {
			t.Fatal("cannot construct unreviewed predecessor fixture")
		}
		if _, err := f.owner.Exec(f.ctx, modified); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.owner.Exec(f.ctx, reviewed); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=34,dirty=false"); err != nil {
				t.Fatal(err)
			}
		}()
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil || !strings.Contains(err.Error(), "unreviewed Theme preferences Fuel predecessor") {
			t.Fatalf("migration did not reject an unreviewed authority predecessor: %v", err)
		}
	})
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
		if got := read(t); got != before {
			t.Fatalf("upgrade or repeated migration changed existing preferences: %#v", got)
		}
		if err = database.Ready(f.ctx, f.pool); err != nil {
			t.Fatalf("upgraded database is not ready: %v", err)
		}
	}
	var version int
	var dirty bool
	if err = f.owner.QueryRow(f.ctx, "SELECT version,dirty FROM core.schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != database.ExpectedVersion("core") || dirty {
		t.Fatalf("theme upgrade not installed cleanly: version=%d dirty=%v", version, dirty)
	}
	for _, state := range []struct {
		name    string
		version int
		dirty   bool
	}{{"old_core", database.ExpectedVersion("core") - 1, false}, {"future_core", database.ExpectedVersion("core") + 1, false}, {"dirty_core", database.ExpectedVersion("core"), true}} {
		t.Run(state.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1,dirty=$2", state.version, state.dirty); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=$1,dirty=false", database.ExpectedVersion("core")); err != nil {
					t.Fatal(err)
				}
			})
			if err := database.Ready(f.ctx, f.pool); err == nil {
				t.Fatal("database readiness accepted an incompatible Core version/state")
			}
			var overviewReady bool
			if err := f.pool.QueryRow(f.ctx, "SELECT fuel.overview_ready()").Scan(&overviewReady); err != nil {
				t.Fatal(err)
			}
			if overviewReady {
				t.Fatal("Fuel readiness accepted an incompatible Core version/state")
			}
		})
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatalf("database readiness did not recover after version guards: %v", err)
	}
	for _, theme := range []string{"system", "light", "dark", "pink-light", "pink-dark", "pink-system"} {
		t.Run(theme, func(t *testing.T) {
			save(t, theme, true, http.StatusOK)
			if got := read(t); got.theme != theme || !got.offline {
				t.Fatalf("theme or independent offline consent not persisted: %#v", got)
			}
		})
	}
	for _, theme := range []string{"", "pink", "Pink-Dark", "dark ", "<script>"} {
		t.Run("reject_"+theme, func(t *testing.T) {
			before := read(t)
			save(t, theme, false, http.StatusUnprocessableEntity)
			if got := read(t); got != before {
				t.Fatal("invalid theme changed stored theme or offline consent")
			}
		})
	}
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	_, err = audit.Exec(ctx, f.pool, "UPDATE core.user_preferences SET theme=$1 WHERE user_id=$2", "unrecognized", f.identity.UserID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23514" || constraint.ConstraintName != "user_preferences_theme_check" {
		t.Fatalf("database accepted or misclassified an invalid theme: %v", err)
	}
}

func TestSecurityPreferencesUpgradeFrom35(t *testing.T) {
	f := newIntegrationFixture(t)
	restoreNavigationPredecessor(t, f)
	// Preserve a pending legacy challenge and an old, valid session while
	// reconstructing the reviewed Core35 schema and frozen Fuel definitions.
	var challengeID string
	if err := f.owner.QueryRow(f.ctx, `INSERT INTO core.auth_challenges
(user_id,purpose,token_hash,payload,expires_at)
VALUES($1,'upgrade-fixture',decode(repeat('ab',32),'hex'),'{"legacy":true}',now()+interval '10 minutes')
RETURNING id::text`, f.identity.UserID).Scan(&challengeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.sessions SET created_at=now()-interval '1 hour' WHERE id=$1`, f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	_, err := f.owner.Exec(f.ctx, `DROP TABLE core.passkey_credentials;
DROP INDEX core.auth_challenges_active_binding;
ALTER TABLE core.sessions DROP COLUMN reauthenticated_at;
ALTER TABLE core.user_preferences DROP CONSTRAINT user_preferences_theme_check,
ADD CONSTRAINT user_preferences_theme_check CHECK(theme IN('system','light','dark','pink-light','pink-dark'));
UPDATE core.schema_migrations SET version=35 WHERE version=36 AND NOT dirty;
DO $restore$
DECLARE baseline jsonb;authority text;overview text;ready text;previous_authority text;old_audit text;previous_audit text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 previous_authority=replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=35 AND NOT dirty)');
 EXECUTE previous_authority;
 EXECUTE replace(overview,'bool_and(version=33 AND NOT dirty)','bool_and(version=35 AND NOT dirty)');
 EXECUTE replace(ready,md5(authority),md5(previous_authority));
 old_audit=baseline->'core.audit_ready()'->>'definition';
 previous_audit=replace(old_audit,
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 EXECUTE previous_audit;
 EXECUTE replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(previous_audit));
END $restore$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("reject_unreviewed_security_predecessor", func(t *testing.T) {
		var reviewed string
		if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_functiondef('fuel.module_ready()'::regprocedure)").Scan(&reviewed); err != nil {
			t.Fatal(err)
		}
		modified := strings.Replace(reviewed, "AS $function$", "AS $function$\n-- unreviewed security predecessor\n", 1)
		if modified == reviewed {
			t.Fatal("cannot construct unreviewed predecessor fixture")
		}
		if _, err := f.owner.Exec(f.ctx, modified); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.owner.Exec(f.ctx, reviewed); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Exec(f.ctx, "UPDATE core.schema_migrations SET version=35,dirty=false"); err != nil {
				t.Fatal(err)
			}
		}()
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil || !strings.Contains(err.Error(), "unreviewed Settings security Fuel predecessor") {
			t.Fatalf("migration did not reject an unreviewed security predecessor: %v", err)
		}
	})
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatalf("security upgrade is not ready: %v", err)
		}
		var originalFreshness bool
		if err := f.pool.QueryRow(f.ctx, `SELECT reauthenticated_at=created_at AND
reauthenticated_at<now()-interval '10 minutes' FROM core.sessions WHERE id=$1`, f.identity.SessionID).Scan(&originalFreshness); err != nil || !originalFreshness {
			t.Fatalf("upgrade granted fresh session authority: retained=%v err=%v", originalFreshness, err)
		}
		var legacyChallenge bool
		if err := f.pool.QueryRow(f.ctx, `SELECT payload='{"legacy":true}'::jsonb AND consumed_at IS NULL
FROM core.auth_challenges WHERE id=$1`, challengeID).Scan(&legacyChallenge); err != nil || !legacyChallenge {
			t.Fatalf("upgrade changed a pending legacy challenge: retained=%v err=%v", legacyChallenge, err)
		}
		var passkeys int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.passkey_credentials").Scan(&passkeys); err != nil || passkeys != 0 {
			t.Fatalf("upgrade created unexpected credentials: count=%d err=%v", passkeys, err)
		}
	}
}
