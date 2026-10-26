// Package database owns pooling, migrations, and transaction-level infrastructure.
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

var Schemas = []string{"core", "shopping", "fuel", "recipes", "pantry", "expenses", "chores", "meters", "documents", "mealplan"}

func Open(ctx context.Context, dsn string, max int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	cfg.MaxConns = max
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "household"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return p, nil
}

func Migrate(ctx context.Context, dsn string) error {
	lock, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect migration lock: %w", err)
	}
	defer func() { _ = lock.Close(context.Background()) }()
	if _, err = lock.Exec(ctx, "SELECT pg_advisory_lock(726241905)"); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	for _, schema := range Schemas {
		// Schema identifiers originate only in this compiled allowlist.
		if _, err = lock.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
			return fmt.Errorf("create schema %s: %w", schema, err)
		}
		source, e := iofs.New(migrations, "migrations/"+schema)
		if e != nil {
			return fmt.Errorf("open migrations %s: %w", schema, e)
		}
		db, e := sql.Open("pgx/v5", dsn)
		if e != nil {
			return fmt.Errorf("open migration connection: %w", e)
		}
		driver, e := migratepgx.WithInstance(db, &migratepgx.Config{SchemaName: schema, StatementTimeout: time.Minute})
		if e != nil {
			_ = db.Close()
			return fmt.Errorf("initialize migration %s: %w", schema, e)
		}
		runner, e := migrate.NewWithInstance("iofs", source, "pgx5", driver)
		if e != nil {
			_ = db.Close()
			return fmt.Errorf("initialize migration runner: %w", e)
		}
		runner.LockTimeout = 30 * time.Second
		e = runner.Up()
		sourceErr, dbErr := runner.Close()
		if e != nil && !errors.Is(e, migrate.ErrNoChange) {
			return fmt.Errorf("migrate %s: %w", schema, e)
		}
		if e = errors.Join(sourceErr, dbErr); e != nil {
			return fmt.Errorf("close migration %s: %w", schema, e)
		}
	}
	// Core migrates first; attach its audit contract only after all independent
	// module tables exist. The separate migration role alone may install triggers.
	if _, err = lock.Exec(ctx, "SELECT core.install_audit_triggers()"); err != nil {
		return fmt.Errorf("install audit triggers: %w", err)
	}
	return nil
}

func Ready(ctx context.Context, pool *pgxpool.Pool) error {
	for _, schema := range Schemas {
		var version int
		var dirty bool
		err := pool.QueryRow(ctx, "SELECT version,dirty FROM "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()).Scan(&version, &dirty)
		if err != nil {
			return fmt.Errorf("read schema %s: %w", schema, err)
		}
		if dirty || version != ExpectedVersion(schema) {
			return fmt.Errorf("incompatible schema %s", schema)
		}
	}
	var audited bool
	if err := pool.QueryRow(ctx, "SELECT core.audit_ready()").Scan(&audited); err != nil {
		return fmt.Errorf("inspect audit contract: %w", err)
	}
	if !audited {
		return errors.New("incomplete audit trigger contract")
	}
	var projected bool
	if err := pool.QueryRow(ctx, "SELECT pantry.projection_ready()").Scan(&projected); err != nil {
		return fmt.Errorf("inspect stock projection contract: %w", err)
	}
	if !projected {
		return errors.New("incomplete stock projection contract")
	}
	var catalogued bool
	if err := pool.QueryRow(ctx, "SELECT recipes.catalog_ready()").Scan(&catalogued); err != nil {
		return fmt.Errorf("inspect ingredient catalogue contract: %w", err)
	}
	if !catalogued {
		return errors.New("incomplete ingredient catalogue contract")
	}
	var cookingStock bool
	if err := pool.QueryRow(ctx, "SELECT pantry.cooking_stock_ready()").Scan(&cookingStock); err != nil {
		return fmt.Errorf("inspect cooking stock contract: %w", err)
	}
	if !cookingStock {
		return errors.New("incomplete cooking stock contract")
	}
	var stockBatch bool
	if err := pool.QueryRow(ctx, "SELECT pantry.stock_batch_ready()").Scan(&stockBatch); err != nil {
		return fmt.Errorf("inspect stock batch contract: %w", err)
	}
	if !stockBatch {
		return errors.New("incomplete stock batch contract")
	}
	var replenishment bool
	if err := pool.QueryRow(ctx, "SELECT pantry.replenishment_ready()").Scan(&replenishment); err != nil {
		return fmt.Errorf("inspect replenishment contract: %w", err)
	}
	if !replenishment {
		return errors.New("incomplete replenishment contract")
	}
	var reservations bool
	if err := pool.QueryRow(ctx, "SELECT core.meal_reservations_ready()").Scan(&reservations); err != nil {
		return fmt.Errorf("inspect meal reservation contract: %w", err)
	}
	if !reservations {
		return errors.New("incomplete meal reservation contract")
	}
	var planningBindings bool
	if err := pool.QueryRow(ctx, "SELECT core.planning_bindings_ready()").Scan(&planningBindings); err != nil {
		return fmt.Errorf("inspect planning ingredient bindings: %w", err)
	}
	if !planningBindings {
		return errors.New("incomplete planning ingredient binding contract")
	}
	var planningPurchases bool
	if err := pool.QueryRow(ctx, "SELECT core.planning_purchases_ready()").Scan(&planningPurchases); err != nil {
		return fmt.Errorf("inspect planning purchase contract: %w", err)
	}
	if !planningPurchases {
		return errors.New("incomplete planning purchase contract")
	}
	var participants bool
	if err := pool.QueryRow(ctx, "SELECT core.planning_participants_ready()").Scan(&participants); err != nil {
		return fmt.Errorf("inspect planning participant contract: %w", err)
	}
	if !participants {
		return errors.New("incomplete planning participant contract")
	}
	var expenseEditing bool
	if err := pool.QueryRow(ctx, "SELECT expenses.editing_ready()").Scan(&expenseEditing); err != nil {
		return fmt.Errorf("inspect expense editor integrity: %w", err)
	}
	if !expenseEditing {
		return errors.New("incomplete expense editor integrity contract")
	}
	var expenseOperations bool
	if err := pool.QueryRow(ctx, "SELECT core.expense_editing_ready()").Scan(&expenseOperations); err != nil {
		return fmt.Errorf("inspect durable expense operations: %w", err)
	}
	if !expenseOperations {
		return errors.New("incomplete durable expense operation contract")
	}
	var financialBindings bool
	if err := pool.QueryRow(ctx, "SELECT expenses.financial_bindings_ready()").Scan(&financialBindings); err != nil {
		return fmt.Errorf("inspect financial identity foundation: %w", err)
	}
	if !financialBindings {
		return errors.New("incomplete financial identity foundation")
	}
	var financialRequirements bool
	if err := pool.QueryRow(ctx, "SELECT core.financial_requirements_ready()").Scan(&financialRequirements); err != nil {
		return fmt.Errorf("inspect financial requirement foundation: %w", err)
	}
	if !financialRequirements {
		return errors.New("incomplete financial requirement foundation")
	}
	return nil
}

func ExpectedVersion(schema string) int {
	entries, err := fs.Glob(migrations, "migrations/"+schema+"/*.up.sql")
	if err != nil {
		return 0
	}
	latest := 0
	for _, entry := range entries {
		n, _ := strconv.Atoi(strings.SplitN(path.Base(entry), "_", 2)[0])
		if n > latest {
			latest = n
		}
	}
	return latest
}
