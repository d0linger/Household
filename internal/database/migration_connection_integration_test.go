//go:build integration

package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigrationConnectionCanonicalSearchPathAndCanceledInitialization(t *testing.T) {
	dsn := os.Getenv("HOUSEHOLD_TEST_OWNER_URL")
	if dsn == "" {
		if os.Getenv("HOUSEHOLD_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("isolated PostgreSQL owner URL is required")
		}
		t.Skip("isolated PostgreSQL owner URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated PostgreSQL owner configuration")
	}
	if config.Database != "household_test" || config.User != "household_owner" {
		t.Fatal("refusing connection initialization outside household_test/household_owner")
	}
	config.ConnectTimeout = 5 * time.Second
	open := func(t *testing.T) *pgx.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		conn, err := pgx.ConnectConfig(ctx, config.Copy())
		if err != nil {
			t.Fatal("connect isolated PostgreSQL owner")
		}
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			if err := conn.Close(cleanup); err != nil {
				t.Error("close isolated connection")
			}
		})
		var actualDatabase, actualRole string
		if err := conn.QueryRow(ctx, "SELECT current_database(),current_user").Scan(&actualDatabase, &actualRole); err != nil ||
			actualDatabase != "household_test" || actualRole != "household_owner" {
			t.Fatal("actual connected database/role failed the isolated guard")
		}
		// Only session settings follow the actual role/database guard. This
		// reproduces the operator path that changed captured FK deparsing.
		if _, err := conn.Exec(ctx, "SET search_path=core,pg_catalog"); err != nil {
			t.Fatal("set isolated operator search path")
		}
		var initial string
		if err := conn.QueryRow(ctx, "SHOW search_path").Scan(&initial); err != nil || initial != "core, pg_catalog" {
			t.Fatal("actual initial operator search path was not established")
		}
		return conn
	}
	t.Run("success keeps usable connection with canonical resolution", func(t *testing.T) {
		conn := open(t)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := migrationSearchPath(ctx, conn); err != nil {
			t.Fatal("initialize canonical migration search path", err)
		}
		var actual string
		if err := conn.QueryRow(ctx, "SHOW search_path").Scan(&actual); err != nil || actual != "pg_catalog" || conn.IsClosed() {
			t.Fatal("successful initializer did not retain an open canonical connection", actual, err)
		}
	})
	t.Run("canceled initializer closes already open physical connection", func(t *testing.T) {
		conn := open(t)
		if conn.IsClosed() || conn.PgConn().PID() == 0 {
			t.Fatal("cancellation test requires an actual open PostgreSQL connection")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := migrationSearchPath(ctx, conn); !errors.Is(err, context.Canceled) {
			t.Fatalf("initializer lost cancellation identity: %v", err)
		}
		if !conn.IsClosed() {
			t.Fatal("failed AfterConnect initializer leaked its physical connection")
		}
	})
}
