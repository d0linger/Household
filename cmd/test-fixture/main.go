//go:build testtools

// test-fixture prepares the synthetic browser account in the disposable test database.
// It is excluded from the production build and never accepts a production database.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"time"

	"household/internal/config"
	"household/internal/database"
	"household/internal/modules"
	"household/internal/web"
)

func main() {
	if err := prepare(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Synthetic browser account prepared in household_test")
}

func prepare() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil || cfg.Production || parsed.Path != "/household_test" || parsed.User == nil || parsed.User.Username() != "household_owner" || cfg.AdminUsername != "bootstrap" {
		return errors.New("fixture requires development, household_owner, household_test and the bootstrap account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL, 3)
	if err != nil {
		return err
	}
	defer pool.Close()
	var db, role string
	if err = pool.QueryRow(ctx, "SELECT current_database(),current_user").Scan(&db, &role); err != nil {
		return err
	}
	if db != "household_test" || role != "household_owner" {
		return errors.New("connected fixture database or role is unsafe")
	}
	if err = database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	app, err := web.New(ctx, pool, cfg, modules.Registry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	if err = app.Auth.RecoverPassword(ctx, "bootstrap", cfg.AdminPassword, true); err != nil {
		return err
	}
	// This guarded test-only database is reused between browser runs. Keep their
	// authentication budgets independent without weakening application limits.
	_, err = pool.Exec(ctx, "TRUNCATE core.rate_limit_buckets")
	return err
}
