package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Bootstrap prepares a fresh PostgreSQL database for Household as the
// superuser, matching deploy/postgres/init.sh: it creates the owner and app
// login roles (or updates their passwords), hands the current database to the
// owner and limits CONNECT and public-schema CREATE. Every step is idempotent,
// so it runs safely on every start. Passwords never appear in errors.
func Bootstrap(ctx context.Context, superuserURL, ownerPassword, appPassword string) error {
	if ownerPassword == "" || appPassword == "" || ownerPassword == appPassword {
		return errors.New("database setup needs two different role passwords")
	}
	cfg, err := pgx.ParseConfig(superuserURL)
	if err != nil {
		return errors.New("parse database setup configuration")
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["search_path"] = "pg_catalog"
	cfg.RuntimeParams["application_name"] = "household-setup"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect as database superuser: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, role := range []struct{ name, password string }{{"household_owner", ownerPassword}, {"household_app", appPassword}} {
		var exists bool
		if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect role %s: %w", role.name, err)
		}
		template := "CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD %L"
		if exists {
			template = "ALTER ROLE %I PASSWORD %L"
		}
		if err = execFormatted(ctx, conn, template, role.name, role.password); err != nil {
			return fmt.Errorf("set up role %s", role.name)
		}
	}
	for _, template := range []string{
		"ALTER DATABASE %I OWNER TO household_owner",
		"REVOKE ALL ON DATABASE %I FROM PUBLIC",
		"GRANT CONNECT ON DATABASE %I TO household_app,household_owner",
	} {
		var statement string
		if err = conn.QueryRow(ctx, "SELECT format($1::text, current_database())", template).Scan(&statement); err != nil {
			return fmt.Errorf("prepare database setup: %w", err)
		}
		if _, err = conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("database setup: %w", err)
		}
	}
	if _, err = conn.Exec(ctx, "REVOKE CREATE ON SCHEMA public FROM PUBLIC"); err != nil {
		return fmt.Errorf("restrict public schema: %w", err)
	}
	return nil
}

// execFormatted lets the server quote the role name and password with format().
func execFormatted(ctx context.Context, conn *pgx.Conn, template, name, password string) error {
	var statement string
	if err := conn.QueryRow(ctx, "SELECT format($1::text, $2::text, $3::text)", template, name, password).Scan(&statement); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, statement)
	return err
}

// RestoreDatabase is the only database CreateRestoreDatabase and
// DropRestoreDatabase touch; restore verification refuses any other target.
const RestoreDatabase = "household_restore"

// CreateRestoreDatabase creates an empty household_restore on the server
// behind maintenanceURL (a superuser connection to its maintenance database),
// replacing any leftover copy from an interrupted verification.
func CreateRestoreDatabase(ctx context.Context, maintenanceURL string) error {
	if err := DropRestoreDatabase(ctx, maintenanceURL); err != nil {
		return err
	}
	return execMaintenance(ctx, maintenanceURL, "CREATE DATABASE "+pgx.Identifier{RestoreDatabase}.Sanitize())
}

// DropRestoreDatabase removes household_restore and its connections.
func DropRestoreDatabase(ctx context.Context, maintenanceURL string) error {
	return execMaintenance(ctx, maintenanceURL, "DROP DATABASE IF EXISTS "+pgx.Identifier{RestoreDatabase}.Sanitize()+" WITH (FORCE)")
}

func execMaintenance(ctx context.Context, maintenanceURL, statement string) error {
	cfg, err := pgx.ParseConfig(maintenanceURL)
	if err != nil {
		return errors.New("parse restore maintenance configuration")
	}
	if cfg.Database == RestoreDatabase {
		return errors.New("restore maintenance must not connect to the restore database")
	}
	cfg.ConnectTimeout = 5 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect for restore maintenance: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err = conn.Exec(ctx, statement); err != nil {
		return fmt.Errorf("restore database maintenance: %w", err)
	}
	return nil
}
