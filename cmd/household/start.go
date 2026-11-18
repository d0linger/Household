package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"household/internal/config"
	"household/internal/database"
	"household/internal/maintenance"
)

// startOnlyVariables carry setup credentials that `serve` must never inherit.
var startOnlyVariables = []string{"POSTGRES_PASSWORD", "HOUSEHOLD_OWNER_PASSWORD", "HOUSEHOLD_APP_PASSWORD", "HOUSEHOLD_DATABASE_USER", "HOUSEHOLD_DATABASE_PASSWORD", "HOUSEHOLD_DATABASE_URL"}

// setupCredentials are the passwords `setup` and `start` work with.
type setupCredentials struct{ superuser, owner, app string }

func readSetupCredentials() (setupCredentials, error) {
	var c setupCredentials
	var err error
	if c.superuser, err = config.Secret("POSTGRES_PASSWORD"); err != nil {
		return c, err
	}
	if c.owner, err = config.Secret("HOUSEHOLD_OWNER_PASSWORD"); err != nil {
		return c, err
	}
	if c.app, err = config.Secret("HOUSEHOLD_APP_PASSWORD"); err != nil {
		return c, err
	}
	if c.owner == "" || c.app == "" {
		return c, errors.New("setup needs HOUSEHOLD_OWNER_PASSWORD and HOUSEHOLD_APP_PASSWORD; create .env with household init")
	}
	return c, nil
}

// setupDatabase creates the roles as the PostgreSQL superuser (when
// POSTGRES_PASSWORD is set) and migrates as the owner, on
// HOUSEHOLD_DATABASE_HOST/HOUSEHOLD_DATABASE_NAME.
func setupDatabase(ctx context.Context, c setupCredentials) error {
	ownerURL, err := config.ConnectionURL("household_owner", c.owner)
	if err != nil {
		return err
	}
	if c.superuser != "" {
		superuserURL, err := config.ConnectionURL("postgres", c.superuser)
		if err != nil {
			return err
		}
		if err = database.Bootstrap(ctx, superuserURL, c.owner, c.app); err != nil {
			return err
		}
	}
	return database.Migrate(ctx, ownerURL)
}

// runSetup prepares the database without starting the app.
func runSetup() error {
	c, err := readSetupCredentials()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	setupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return setupDatabase(setupCtx, c)
}

// runStart is the single-container entry point: setup, then it replaces
// itself with `household serve` running as the app role, without the
// superuser or owner password in its environment.
func runStart() error {
	c, err := readSetupCredentials()
	if err != nil {
		return err
	}
	// Validate the complete serve configuration before touching the database.
	environment := serveEnvironment(os.Environ(), c.app)
	if err = replaceEnvironment(environment); err != nil {
		return err
	}
	if _, err = config.Load(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	setupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err = setupDatabase(setupCtx, c); err != nil {
		return err
	}
	cancel()
	stop()
	return execServe(environment)
}

// serveEnvironment drops every setup credential and connects serve as the app role.
func serveEnvironment(current []string, appPassword string) []string {
	result := make([]string, 0, len(current)+2)
	for _, entry := range current {
		name, _, _ := strings.Cut(entry, "=")
		if startOnly(name) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "HOUSEHOLD_DATABASE_USER=household_app", "HOUSEHOLD_DATABASE_PASSWORD="+appPassword)
}

func startOnly(name string) bool {
	for _, variable := range startOnlyVariables {
		if name == variable || name == variable+"_FILE" {
			return true
		}
	}
	return false
}

func replaceEnvironment(environment []string) error {
	os.Clearenv()
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(name, value); err != nil {
			return errors.New("could not prepare the serve environment")
		}
	}
	return nil
}

// withRestoreTarget creates a fresh, uniquely named restore database on the
// server of the source connection, prepares the Household roles in it, runs
// verify against it with the target's credentials, and always drops it again.
func withRestoreTarget(ctx context.Context, source, target, superuser string, verify func(target string) error) error {
	owner, err := config.Secret("HOUSEHOLD_OWNER_PASSWORD")
	if err != nil {
		return err
	}
	app, err := config.Secret("HOUSEHOLD_APP_PASSWORD")
	if err != nil || owner == "" || app == "" {
		return errors.New("restore preparation needs HOUSEHOLD_OWNER_PASSWORD and HOUSEHOLD_APP_PASSWORD")
	}
	maintenanceURL, err := config.RestoreMaintenanceURL(source, superuser)
	if err != nil {
		return err
	}
	return database.WithAutomaticRestoreDatabase(ctx, maintenanceURL, func(name string) error {
		restoreURL, err := config.WithDatabase(maintenanceURL, name)
		if err != nil {
			return err
		}
		runTarget, err := config.WithDatabase(target, name)
		if err != nil {
			return err
		}
		if err = maintenance.ValidateRestoreTarget(source, runTarget); err != nil {
			return err
		}
		setupCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err = database.Bootstrap(setupCtx, restoreURL, owner, app); err != nil {
			return err
		}
		return verify(runTarget)
	})
}
