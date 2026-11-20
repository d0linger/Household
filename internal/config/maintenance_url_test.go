package config

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// A query parameter applied after the URL's authority and path must not move
// the live-restore maintenance connection to another role, database or server.
func TestServerMaintenanceURLIgnoresTargetOverrides(t *testing.T) {
	source := "postgres://household_owner:owner@db:5432/household?sslmode=require&user=household_owner&password=owner" +
		"&database=household&dbname=household&host=elsewhere&port=6543&Port=7654&application_name=web"
	derived, err := ServerMaintenanceURL(source, "superuser-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(derived)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "postgres" || cfg.Password != "superuser-secret" || cfg.Database != "postgres" || cfg.Host != "db" || cfg.Port != 5432 {
		t.Fatalf("maintenance connection = %s@%s:%d/%s", cfg.User, cfg.Host, cfg.Port, cfg.Database)
	}
	if cfg.TLSConfig == nil {
		t.Fatal("transport options such as sslmode must stay")
	}
	stage, err := WithDatabase(source, "household_restore_job_0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err = pgx.ParseConfig(stage); err != nil || cfg.Database != "household_restore_job_0123456789ab" {
		t.Fatalf("stage database = %q, %v", cfg.Database, err)
	}
}
