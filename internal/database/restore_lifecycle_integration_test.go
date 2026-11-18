//go:build integration

package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// HOUSEHOLD_TEST_SUPERUSER_URL is optional: a postgres superuser connection to
// the maintenance database of the disposable test server. The test creates and
// drops household_restore_auto_* databases there.
func TestAutomaticRestoreDatabasesAreIndependent(t *testing.T) {
	maintenanceURL := os.Getenv("HOUSEHOLD_TEST_SUPERUSER_URL")
	if maintenanceURL == "" {
		t.Skip("HOUSEHOLD_TEST_SUPERUSER_URL is not set")
	}
	cfg, err := pgx.ParseConfig(maintenanceURL)
	if err != nil {
		t.Fatal("parse disposable test server URL")
	}
	// Only the disposable test server (compose.test.yaml on loopback 55434, or
	// the CI service) qualifies, including every fallback pgx might try.
	approved := func(host string, port uint16) bool {
		loopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
		return loopback && (port == 55434 || port == 5432 && os.Getenv("CI") == "true")
	}
	if cfg.Database != "postgres" || cfg.User != "postgres" || !approved(cfg.Host, cfg.Port) {
		t.Fatal("refusing restore lifecycle outside the disposable test server's postgres database")
	}
	for _, fallback := range cfg.Fallbacks {
		if fallback == nil || fallback.Host != cfg.Host || fallback.Port != cfg.Port {
			t.Fatal("refusing a fallback endpoint other than the approved test server")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	admin := func(t *testing.T) *pgx.Conn {
		t.Helper()
		conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
		if err != nil {
			t.Fatal("connect disposable maintenance database")
		}
		return conn
	}
	exists := func(name string) bool {
		t.Helper()
		conn := admin(t)
		defer func() { _ = conn.Close(context.Background()) }()
		var found bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	// A leftover of an interrupted run older than six hours is dropped; a
	// younger one may still be running and is kept. Random suffixes keep
	// concurrent invocations apart; each fixture is cleaned up once created.
	suffix := func() string {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(raw)
	}
	stale := fmt.Sprintf("%s%d_%s", AutomaticRestorePrefix, time.Now().Add(-7*time.Hour).Unix(), suffix())
	young := fmt.Sprintf("%s%d_%s", AutomaticRestorePrefix, time.Now().Add(-time.Minute).Unix(), suffix())
	setup := admin(t)
	defer func() { _ = setup.Close(context.Background()) }()
	for _, name := range []string{stale, young} {
		if _, err = setup.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal("create leftover fixture", err)
		}
		t.Cleanup(func() {
			conn, err := pgx.ConnectConfig(context.Background(), cfg.Copy())
			if err == nil {
				_, _ = conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
				_ = conn.Close(context.Background())
			}
		})
	}
	var first, second string
	err = WithAutomaticRestoreDatabase(ctx, maintenanceURL, func(name string) error {
		first = name
		if !IsAutomaticRestoreDatabase(name) || !exists(name) {
			return errors.New("automatic restore database was not created under its run name")
		}
		// A concurrent check gets its own database and never drops this one.
		if err := WithAutomaticRestoreDatabase(ctx, maintenanceURL, func(name string) error {
			second = name
			return nil
		}); err != nil {
			return err
		}
		if second == "" || second == first || exists(second) || !exists(first) {
			return errors.New("concurrent automatic checks were not independent")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists(first) || exists(stale) || !exists(young) {
		t.Fatal("cleanup dropped the wrong databases", exists(first), exists(stale), exists(young))
	}
	failed := errors.New("verification failed")
	var failedName string
	if err = WithAutomaticRestoreDatabase(ctx, maintenanceURL, func(name string) error { failedName = name; return failed }); !errors.Is(err, failed) || exists(failedName) {
		t.Fatal("a failed check must report its error and still drop its database", err)
	}
	// Maintenance through a restore database is refused before the callback.
	selfURL, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	selfURL.Path = "/household_restore"
	ran := false
	if err = WithAutomaticRestoreDatabase(ctx, selfURL.String(), func(string) error { ran = true; return nil }); !errors.Is(err, ErrRestoreSelfTarget) || ran {
		t.Fatal("maintenance through household_restore itself was not refused by its guard", err, ran)
	}
}
