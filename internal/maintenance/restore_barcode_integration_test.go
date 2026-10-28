//go:build integration

package maintenance

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type restoreBarcodeFixture struct {
	ctx   context.Context
	owner *pgxpool.Pool
	app   *pgxpool.Pool
}

func newRestoreBarcodeFixture(t *testing.T) restoreBarcodeFixture {
	t.Helper()
	ownerURL, appURL := os.Getenv("HOUSEHOLD_TEST_OWNER_URL"), os.Getenv("HOUSEHOLD_TEST_APP_URL")
	if ownerURL == "" || appURL == "" {
		if os.Getenv("HOUSEHOLD_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("both isolated PostgreSQL test URLs are required")
		}
		t.Skip("isolated household_test owner and App URLs are required")
	}
	ownerConfig, ownerErr := pgx.ParseConfig(ownerURL)
	appConfig, appErr := pgx.ParseConfig(appURL)
	if ownerErr != nil || appErr != nil {
		t.Fatal("invalid isolated PostgreSQL configuration")
	}
	loopback := ownerConfig.Host == "127.0.0.1" || ownerConfig.Host == "localhost" || ownerConfig.Host == "::1"
	endpoint := loopback && (ownerConfig.Port == 55434 || ownerConfig.Port == 5432 && os.Getenv("CI") == "true") ||
		ownerConfig.Host == "db" && ownerConfig.Port == 5432 && os.Getenv("HOUSEHOLD_REQUIRE_INTEGRATION") == "1"
	if ownerConfig.Database != "household_test" || appConfig.Database != "household_test" ||
		ownerConfig.User != "household_owner" || appConfig.User != "household_app" ||
		ownerConfig.Host != appConfig.Host || ownerConfig.Port != appConfig.Port || !endpoint {
		t.Fatal("refusing restore fixture outside the closed household_test endpoints and expected roles")
	}
	for _, config := range []*pgx.ConnConfig{ownerConfig, appConfig} {
		for _, fallback := range config.Fallbacks {
			if fallback == nil || fallback.Host != config.Host || fallback.Port != config.Port {
				t.Fatal("isolated restore fixture refuses an alternative fallback server")
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	owner, err := database.Open(ctx, ownerURL, 3)
	if err != nil {
		t.Fatal("connect isolated maintenance owner")
	}
	t.Cleanup(owner.Close)
	app, err := database.Open(ctx, appURL, 2)
	if err != nil {
		t.Fatal("connect isolated runtime role")
	}
	t.Cleanup(app.Close)
	for _, check := range []struct {
		pool *pgxpool.Pool
		role string
	}{{owner, "household_owner"}, {app, "household_app"}} {
		var name, sessionRole, currentRole string
		var restricted bool
		if err = check.pool.QueryRow(ctx, `SELECT current_database(),session_user,current_user,
 NOT(rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) FROM pg_roles WHERE rolname=current_user`).Scan(&name, &sessionRole, &currentRole, &restricted); err != nil ||
			name != "household_test" || sessionRole != check.role || currentRole != check.role || !restricted {
			t.Fatal("actual database/session/current role failed the isolated restore fixture guard")
		}
	}
	lock, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire isolated fixture lock connection")
	}
	var locked bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(726241999)").Scan(&locked); err != nil || !locked {
		lock.Release()
		t.Fatal("isolated database is already owned by another integration fixture")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if _, err := lock.Exec(cleanup, "SELECT pg_advisory_unlock(726241999)"); err != nil {
			t.Error("release isolated restore fixture lock")
		}
		lock.Release()
	})
	reset := func(ctx context.Context) error {
		names := make([]string, 0, len(database.Schemas))
		for _, schema := range database.Schemas {
			names = append(names, pgx.Identifier{schema}.Sanitize())
		}
		_, err := owner.Exec(ctx, "DROP SCHEMA IF EXISTS "+strings.Join(names, ",")+" CASCADE")
		return err
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := reset(cleanup); err != nil {
			t.Error("clean isolated compiled restore schemas")
		}
	})
	if err = reset(ctx); err != nil {
		t.Fatal("reset isolated compiled restore schemas")
	}
	if err = database.Migrate(ctx, ownerURL); err != nil {
		t.Fatal("migrate every current schema for restore regression", err)
	}
	if err = database.Ready(ctx, app); err != nil {
		t.Fatal("fresh complete App contract is not ready", err)
	}
	return restoreBarcodeFixture{ctx: ctx, owner: owner, app: app}
}

func restoreBarcodeDefinition(t *testing.T, f restoreBarcodeFixture) string {
	t.Helper()
	var definition string
	if err := f.owner.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid='shopping.barcodes'::regclass AND conname='barcodes_code_valid'`).Scan(&definition); err != nil {
		t.Fatal("read actual barcode constraint", err)
	}
	return definition
}

func restoreBarcodeReplace(t *testing.T, f restoreBarcodeFixture, definition string) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal("begin isolated constraint setup", err)
	}
	defer restoreBarcodeRollback(tx)
	if _, err = tx.Exec(f.ctx, "SET LOCAL search_path=pg_catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "ALTER TABLE shopping.barcodes DROP CONSTRAINT IF EXISTS barcodes_code_valid"); err != nil {
		t.Fatal(err)
	}
	if definition != "" {
		// All inputs are closed test constants or PostgreSQL's known original
		// definition. This deliberately reparses the real dump representation.
		if _, err = tx.Exec(f.ctx, "ALTER TABLE shopping.barcodes ADD CONSTRAINT barcodes_code_valid "+definition); err != nil {
			t.Fatal("reparse actual barcode constraint definition", err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("commit isolated constraint setup", err)
	}
}

func restoreBarcodeRollback(tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func restoreBarcodePermissions(t *testing.T, f restoreBarcodeFixture) string {
	t.Helper()
	var snapshot string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'owner',pg_get_userbyid(c.relowner),'table_acl',coalesce(c.relacl,'{}'::aclitem[])::text,
 'table_privileges',(SELECT jsonb_object_agg(p,has_table_privilege('household_app',c.oid,p))
  FROM unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) privileges(p)),
 'columns',(SELECT jsonb_agg(jsonb_build_array(a.attname,coalesce(a.attacl,'{}'::aclitem[])::text,
  has_column_privilege('household_app',c.oid,a.attnum,'SELECT'),has_column_privilege('household_app',c.oid,a.attnum,'INSERT'),
  has_column_privilege('household_app',c.oid,a.attnum,'UPDATE'),has_column_privilege('household_app',c.oid,a.attnum,'REFERENCES')) ORDER BY a.attnum)
  FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped))::text
 FROM pg_class c WHERE c.oid='shopping.barcodes'::regclass`).Scan(&snapshot); err != nil {
		t.Fatal("snapshot complete barcode column/table privileges", err)
	}
	return snapshot
}

func restoreBarcodeConstraintSnapshot(t *testing.T, f restoreBarcodeFixture) string {
	t.Helper()
	var snapshot string
	if err := f.owner.QueryRow(f.ctx, `SELECT coalesce(jsonb_agg(to_jsonb(k) ORDER BY k.conname),'[]')::text
 FROM pg_constraint k WHERE k.conrelid='shopping.barcodes'::regclass`).Scan(&snapshot); err != nil {
		t.Fatal("snapshot actual restored constraints", err)
	}
	return snapshot
}

func TestRestoreBarcodeConstraintDumpRoundTripAndFailClosed(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	permissions := restoreBarcodePermissions(t, f)
	original := restoreBarcodeDefinition(t, f)
	if original != restoreBarcodeOriginal {
		t.Fatal("fixture must start from the actual migration-pinned original", original)
	}
	t.Run("actual deparser round trip normalizes without rebasing readiness or privileges", func(t *testing.T) {
		restoreBarcodeReplace(t, f, original)
		if got := restoreBarcodeDefinition(t, f); got != restoreBarcodeFlat {
			t.Fatal("real original definition did not reproduce dump/reparse flattening", got)
		}
		if err := database.Ready(f.ctx, f.app); err == nil {
			t.Fatal("flattened dump shape unexpectedly satisfied the strict pinned contract")
		}
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err != nil {
			t.Fatal("normalize witnessed restore representation", err)
		}
		if got := restoreBarcodeDefinition(t, f); got != restoreBarcodeOriginal {
			t.Fatal("normalization did not restore the exact original deparse", got)
		}
		if err := database.Ready(f.ctx, f.app); err != nil {
			t.Fatal("normalization did not restore full unchanged App readiness", err)
		}
		before := restoreBarcodeConstraintSnapshot(t, f)
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err != nil || restoreBarcodeConstraintSnapshot(t, f) != before {
			t.Fatal("already original constraint was not an exact no-op", err)
		}
		if got := restoreBarcodePermissions(t, f); got != permissions {
			t.Fatal("normalization changed column/table ACLs or effective App privileges")
		}
	})
	for _, test := range []struct {
		name       string
		definition string
	}{
		{"unknown predicate", "CHECK(length(code) BETWEEN 1 AND 65 AND code ~ '^[!-~]+$')"},
		{"unvalidated known predicate", "CHECK(length(code) BETWEEN 1 AND 64 AND code ~ '^[!-~]+$') NOT VALID"},
		{"noninheritable known predicate", "CHECK(length(code) BETWEEN 1 AND 64 AND code ~ '^[!-~]+$') NO INHERIT"},
		{"missing predicate", ""},
	} {
		t.Run(test.name+" refuses without changing evidence", func(t *testing.T) {
			restoreBarcodeReplace(t, f, test.definition)
			before := restoreBarcodeConstraintSnapshot(t, f)
			if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err == nil {
				t.Fatal("unrecognized restored shape was normalized")
			}
			if restoreBarcodeConstraintSnapshot(t, f) != before || restoreBarcodePermissions(t, f) != permissions {
				t.Fatal("rejected normalization changed constraints or privileges")
			}
			restoreBarcodeReplace(t, f, "CHECK(length(code) BETWEEN 1 AND 64 AND code ~ '^[!-~]+$')")
		})
	}
	t.Run("runtime role cannot normalize a genuinely flattened constraint", func(t *testing.T) {
		restoreBarcodeReplace(t, f, original)
		before := restoreBarcodeConstraintSnapshot(t, f)
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.app); err == nil {
			t.Fatal("runtime role performed maintenance normalization")
		}
		if restoreBarcodeConstraintSnapshot(t, f) != before || restoreBarcodePermissions(t, f) != permissions {
			t.Fatal("runtime denial changed the original table contract")
		}
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err != nil {
			t.Fatal("owner could not normalize after runtime refusal", err)
		}
	})
	t.Run("unrelated drift rolls the known rewrite back atomically", func(t *testing.T) {
		restoreBarcodeReplace(t, f, original)
		if _, err := f.owner.Exec(f.ctx, "REVOKE UPDATE(id) ON shopping.barcodes FROM household_app"); err != nil {
			t.Fatal(err)
		}
		before := restoreBarcodeConstraintSnapshot(t, f)
		driftedACL := restoreBarcodePermissions(t, f)
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err == nil {
			t.Fatal("normalizer accepted unrelated column ACL drift")
		}
		if restoreBarcodeConstraintSnapshot(t, f) != before || restoreBarcodePermissions(t, f) != driftedACL {
			t.Fatal("failed final contract proof committed a partial rewrite")
		}
		if _, err := f.owner.Exec(f.ctx, "GRANT UPDATE(id) ON shopping.barcodes TO household_app"); err != nil {
			t.Fatal(err)
		}
		if err := normalizeRestoreBarcodeConstraint(f.ctx, f.owner); err != nil {
			t.Fatal("owner could not normalize the restored exact privilege contract", err)
		}
	})
	t.Run("exclusive lock contention fails immediately without altering the constraint", func(t *testing.T) {
		tx, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer restoreBarcodeRollback(tx)
		if _, err = tx.Exec(f.ctx, "LOCK TABLE shopping.barcodes IN ACCESS SHARE MODE"); err != nil {
			t.Fatal(err)
		}
		err = normalizeRestoreBarcodeConstraint(f.ctx, f.owner)
		var sqlErr *pgconn.PgError
		if !errors.As(err, &sqlErr) || sqlErr.Code != "55P03" {
			t.Fatal("normalizer did not retain NOWAIT lock refusal identity", err)
		}
	})
	t.Run("ASCII length controls and Unicode retain the exact accepted domain", func(t *testing.T) {
		tx, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer restoreBarcodeRollback(tx)
		if _, err = tx.Exec(f.ctx, `CREATE TEMP TABLE restore_barcode_original(code text COLLATE "C" NOT NULL
 CHECK(length(code) BETWEEN 1 AND 64 AND code ~ '^[!-~]+$')) ON COMMIT DROP;
 CREATE TEMP TABLE restore_barcode_flat(code text COLLATE "C" NOT NULL
 CHECK((length(code)>=1) AND (length(code)<=64) AND (code ~ '^[!-~]+$'))) ON COMMIT DROP`); err != nil {
			t.Fatal("create bounded temporary domain probes", err)
		}
		for _, test := range []struct {
			code  string
			valid bool
		}{
			{"!", true}, {"~", true}, {"0123456789-A_/.:+", true}, {"'\"\\[]", true}, {strings.Repeat("A", 64), true},
			{"", false}, {strings.Repeat("A", 65), false}, {"space here", false}, {"\t", false},
			{"\x01", false}, {"\n", false}, {"\r", false}, {string(rune(127)), false}, {"é", false}, {"Ａ", false}, {"😀", false},
		} {
			for _, table := range []string{"restore_barcode_original", "restore_barcode_flat"} {
				if _, err = tx.Exec(f.ctx, "SAVEPOINT barcode_boundary"); err != nil {
					t.Fatal(err)
				}
				_, insertErr := tx.Exec(f.ctx, "INSERT INTO "+pgx.Identifier{"pg_temp", table}.Sanitize()+"(code) VALUES($1)", test.code)
				if test.valid && insertErr != nil {
					t.Fatal("valid ASCII boundary was rejected", table, insertErr)
				}
				if !test.valid {
					var sqlErr *pgconn.PgError
					if !errors.As(insertErr, &sqlErr) || sqlErr.Code != "23514" {
						t.Fatal("invalid barcode escaped the original domain", table, insertErr)
					}
				}
				if _, err = tx.Exec(f.ctx, "ROLLBACK TO SAVEPOINT barcode_boundary"); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(f.ctx, "RELEASE SAVEPOINT barcode_boundary"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	if err := database.Ready(f.ctx, f.app); err != nil || restoreBarcodePermissions(t, f) != permissions {
		t.Fatal("complete restore regression left contract or privilege drift", err)
	}
}
