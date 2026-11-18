//go:build integration

package database

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// HOUSEHOLD_TEST_SUPERUSER_URL is optional: a postgres superuser connection to
// the maintenance database of the isolated test server. The test creates and
// drops household_restore there.
func TestRestoreDatabaseLifecycleIsSerialized(t *testing.T) {
	maintenanceURL := os.Getenv("HOUSEHOLD_TEST_SUPERUSER_URL")
	if maintenanceURL == "" {
		t.Skip("HOUSEHOLD_TEST_SUPERUSER_URL is not set")
	}
	cfg, err := pgx.ParseConfig(maintenanceURL)
	// Only the disposable test server (compose.test.yaml on loopback 55434, or
	// the CI service) qualifies: the test creates and drops household_restore.
	loopback := cfg != nil && (cfg.Host == "127.0.0.1" || cfg.Host == "localhost" || cfg.Host == "::1")
	disposablePort := cfg != nil && (cfg.Port == 55434 || cfg.Port == 5432 && os.Getenv("CI") == "true")
	if err != nil || cfg.Database != "postgres" || cfg.User != "postgres" || !loopback || !disposablePort {
		t.Fatal("refusing restore lifecycle outside the disposable test server's postgres database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	exists := func() bool {
		t.Helper()
		conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
		if err != nil {
			t.Fatal("connect isolated maintenance database")
		}
		defer func() { _ = conn.Close(context.Background()) }()
		var found bool
		if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", RestoreDatabase).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if exists() {
		t.Fatal("refusing to run: household_restore already exists on this server")
	}
	err = WithRestoreDatabase(ctx, maintenanceURL, func() error {
		if !exists() {
			return errors.New("household_restore was not created")
		}
		// A concurrent restore check must not drop this one's database.
		if err := WithRestoreDatabase(ctx, maintenanceURL, func() error { return errors.New("second check ran") }); !errors.Is(err, ErrRestoreBusy) {
			return errors.New("second restore check was not refused: " + errString(err))
		}
		if !exists() {
			return errors.New("refused check dropped the active database")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Fatal("household_restore survived a completed check")
	}
	failed := errors.New("verification failed")
	if err = WithRestoreDatabase(ctx, maintenanceURL, func() error { return failed }); !errors.Is(err, failed) || exists() {
		t.Fatal("a failed check must report its error and still drop household_restore", err)
	}
	restoreURL, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	restoreURL.Path = "/" + RestoreDatabase
	if err = WithRestoreDatabase(ctx, restoreURL.String(), func() error { return nil }); !errors.Is(err, ErrRestoreSelfTarget) {
		t.Fatal("maintenance through household_restore itself was not refused by its guard", err)
	}
	// Advisory locks are per database: a second maintenance database would not
	// exclude a concurrent check, so it is refused before any DDL.
	restoreURL.Path = "/template1"
	if err = WithRestoreDatabase(ctx, restoreURL.String(), func() error { return errors.New("ran through another maintenance database") }); err == nil || exists() {
		t.Fatal("a non-canonical maintenance database was accepted", err)
	}
	if err = WithRestoreLock(ctx, maintenanceURL, func() error {
		if err := WithRestoreDatabase(ctx, maintenanceURL, func() error { return errors.New("ran under an explicit check's lock") }); !errors.Is(err, ErrRestoreBusy) {
			return errors.New("automatic check was not refused during an explicit check: " + errString(err))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
