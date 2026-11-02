// Package database owns pooling, migrations, and transaction-level infrastructure.
package database

import (
	"context"
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
	"github.com/jackc/pgx/v5/stdlib"
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
	migrationConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse migration configuration: %w", err)
	}
	migrationConfig.ConnectTimeout = 5 * time.Second
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
	}
	migrateSchema := func(schema string, through *uint) error {
		source, e := iofs.New(migrations, "migrations/"+schema)
		if e != nil {
			return fmt.Errorf("open migrations %s: %w", schema, e)
		}
		// Metadata deparsing must match the SECURITY DEFINER readiness functions,
		// regardless of an operator's connection search_path. Each physical
		// migration connection needs this; the advisory-lock connection is separate.
		db := stdlib.OpenDB(*migrationConfig, stdlib.OptionAfterConnect(migrationSearchPath))
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
		if through == nil {
			e = runner.Up()
		} else {
			version, dirty, versionErr := runner.Version()
			switch {
			case errors.Is(versionErr, migrate.ErrNilVersion):
				e = runner.Migrate(*through)
			case versionErr != nil:
				e = versionErr
			case dirty:
				e = fmt.Errorf("%s migration is dirty at the predecessor barrier", schema)
			case version < *through:
				e = runner.Migrate(*through)
			default:
				e = migrate.ErrNoChange
			}
		}
		sourceErr, dbErr := runner.Close()
		if e != nil && !errors.Is(e, migrate.ErrNoChange) {
			return fmt.Errorf("migrate %s: %w", schema, e)
		}
		if e = errors.Join(sourceErr, dbErr); e != nil {
			return fmt.Errorf("close migration %s: %w", schema, e)
		}
		return nil
	}
	// Released histories retain their exact Core26 and Core27 predecessors.
	// Explicit barriers also keep new optional-module migrations from running
	// early on a fresh installation. No stage ever downgrades an existing schema.
	coreLegacyBarrier := uint(26)
	if err = migrateSchema("core", &coreLegacyBarrier); err != nil {
		return err
	}
	recipePredecessor := uint(4)
	fuelPredecessor := uint(5)
	pantryPredecessor := uint(12)
	mealplanPredecessor := uint(7)
	expensePredecessor := uint(4)
	chorePredecessor := uint(1)
	meterPredecessor := uint(1)
	documentPredecessor := uint(1)
	for _, schema := range Schemas[1:] {
		var through *uint
		switch schema {
		case "recipes":
			through = &recipePredecessor
		case "fuel":
			through = &fuelPredecessor
		case "pantry":
			through = &pantryPredecessor
		case "mealplan":
			through = &mealplanPredecessor
		case "expenses":
			through = &expensePredecessor
		case "chores":
			through = &chorePredecessor
		case "meters":
			through = &meterPredecessor
		case "documents":
			through = &documentPredecessor
		}
		if err = migrateSchema(schema, through); err != nil {
			return err
		}
	}
	coreRecipeBarrier := uint(27)
	if err = migrateSchema("core", &coreRecipeBarrier); err != nil {
		return err
	}
	if err = migrateSchema("recipes", nil); err != nil {
		return err
	}
	fuelRecipeBarrier := uint(6)
	if err = migrateSchema("fuel", &fuelRecipeBarrier); err != nil {
		return err
	}
	// Preserve the exact Core27→Mealplan8→Core28→Fuel7/Pantry13 release
	// predecessors before installing Finance; newer schemas must not run early.
	for _, stage := range []struct {
		schema  string
		version uint
	}{{"mealplan", 8}, {"core", 28}, {"fuel", 7}, {"pantry", 13},
		{"core", 29}, {"expenses", 5}, {"core", 30}, {"core", 31},
		{"fuel", 8}, {"pantry", 14}, {"mealplan", 9},
		{"chores", 2}, {"core", 32}, {"fuel", 9}, {"pantry", 15}, {"mealplan", 10},
		{"meters", 2}, {"core", 33}, {"fuel", 10}, {"pantry", 16}, {"mealplan", 11},
		{"documents", 2}, {"core", 34}, {"fuel", 11}, {"pantry", 17}, {"mealplan", 12}} {
		if err = migrateSchema(stage.schema, &stage.version); err != nil {
			return err
		}
		if stage.schema == "core" && stage.version == 31 {
			if _, err = lock.Exec(ctx, "SELECT core.install_audit_triggers()"); err != nil {
				return fmt.Errorf("install finance audit predecessor: %w", err)
			}
		}
	}
	// Attach the final audit contract only after all independent module tables
	// exist. The separate migration role alone may install triggers.
	if _, err = lock.Exec(ctx, "SELECT core.install_audit_triggers()"); err != nil {
		return fmt.Errorf("install audit triggers: %w", err)
	}
	return nil
}

func migrationSearchPath(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := conn.Exec(ctx, "SET search_path=pg_catalog"); err != nil {
		// pgx stdlib does not close a connection rejected by AfterConnect.
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
		return fmt.Errorf("initialize migration schema resolution: %w", err)
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
	return inspectReadiness(ctx, pool, []readinessCheck{
		{"SELECT fuel.overview_ready()", "inspect vehicle overview contract: %w", "incomplete vehicle overview contract", 1},
		{"SELECT fuel.module_ready()", "inspect vehicle module contract: %w", "incomplete vehicle module contract", 1},
		{"SELECT shopping.repurchase_ready()", "inspect shopping history contract: %w", "incomplete shopping history contract", 1},
		{"SELECT shopping.barcode_ready()", "inspect shopping barcode contract: %w", "incomplete shopping barcode contract", 1},
		{"SELECT core.audit_ready()", "inspect audit contract: %w", "incomplete audit trigger contract", 1},
		{"SELECT pantry.projection_ready()", "inspect stock projection contract: %w", "incomplete stock projection contract", 1},
		{"SELECT recipes.catalog_ready()", "inspect ingredient catalogue contract: %w", "incomplete ingredient catalogue contract", 1},
		{"SELECT core.recipe_expansion_ready()", "inspect recipe core expansion: %w", "incomplete recipe core expansion", 1},
		{"SELECT recipes.expansion_ready()", "inspect recipe expansion: %w", "incomplete recipe expansion", 1},
		{"SELECT pantry.cooking_stock_ready()", "inspect cooking stock contract: %w", "incomplete cooking stock contract", 1},
		{"SELECT pantry.stock_batch_ready()", "inspect stock batch contract: %w", "incomplete stock batch contract", 1},
		{"SELECT pantry.replenishment_ready()", "inspect replenishment contract: %w", "incomplete replenishment contract", 1},
		{"SELECT pantry.waste_ready()", "inspect waste analysis contract: %w", "incomplete waste analysis contract", 1},
		{"SELECT pantry.prepared_capture_ready()", "inspect prepared food and scan contract: %w", "incomplete prepared food and scan contract", 1},
		{"SELECT core.meal_reservations_ready()", "inspect meal reservation contract: %w", "incomplete meal reservation contract", 1},
		{"SELECT core.planning_bindings_ready()", "inspect planning ingredient bindings: %w", "incomplete planning ingredient binding contract", 1},
		{"SELECT core.planning_purchases_ready()", "inspect planning purchase contract: %w", "incomplete planning purchase contract", 1},
		{"SELECT core.planning_participants_ready()", "inspect planning participant contract: %w", "incomplete planning participant contract", 1},
		{"SELECT mealplan.expansion_ready(),core.planning_extensions_ready()", "inspect planning extension contract: %w", "incomplete planning extension contract", 2},
		{"SELECT expenses.editing_ready()", "inspect expense editor integrity: %w", "incomplete expense editor integrity contract", 1},
		{"SELECT core.expense_editing_ready()", "inspect durable expense operations: %w", "incomplete durable expense operation contract", 1},
		{"SELECT expenses.financial_bindings_ready()", "inspect financial identity foundation: %w", "incomplete financial identity foundation", 1},
		{"SELECT core.financial_requirements_ready()", "inspect financial requirement foundation: %w", "incomplete financial requirement foundation", 1},
		{"SELECT expenses.finance_ready(),core.finance_ready()", "inspect complete finance contract: %w", "incomplete finance authority or domain contract", 2},
		{"SELECT chores.expansion_ready()", "inspect chore workspace contract: %w", "incomplete chore workspace contract", 1},
		{"SELECT meters.expansion_ready()", "inspect energy workspace contract: %w", "incomplete energy workspace contract", 1},
		{"SELECT documents.expansion_ready()", "inspect document workspace contract: %w", "incomplete document workspace contract", 1},
	})
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
