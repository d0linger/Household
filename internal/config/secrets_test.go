package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecretFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSecretsFolderSuppliesUnconfiguredSecrets(t *testing.T) {
	setValidEnvironment(t)
	dir := t.TempDir()
	t.Setenv(SecretsDirVariable, dir)
	t.Setenv("HOUSEHOLD_CSRF_SECRET", "")
	t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	writeSecretFile(t, dir, "csrf_secret", strings.Repeat("f", 32)+"\n")
	writeSecretFile(t, dir, "encryption_key", strings.Repeat("k", 32))
	writeSecretFile(t, dir, "database_password", "synthetic pass/word\n")
	writeSecretFile(t, dir, "meters_ha_token", "") // empty placeholder: integration stays off
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CSRFSecret != strings.Repeat("f", 32) || cfg.EncryptionKey != strings.Repeat("k", 32) || cfg.MeterFeeds.HAToken != "" {
		t.Fatal("secrets folder values were not used")
	}
	if cfg.DatabaseURL != "postgres://household_app:synthetic%20pass%2Fword@db:5432/household?sslmode=disable" {
		t.Fatal("database URL was not built from the password with encoded credentials")
	}
	// Explicit settings keep precedence over the folder.
	t.Setenv("HOUSEHOLD_RATE_SECRET", strings.Repeat("x", 32))
	writeSecretFile(t, dir, "rate_secret", strings.Repeat("y", 32))
	if cfg, err = Load(); err != nil || cfg.RateSecret != strings.Repeat("x", 32) {
		t.Fatal("explicit setting must win over the secrets folder", err)
	}
	// A folder entry that is not a regular file is an error, not "unset".
	if err = os.Mkdir(filepath.Join(dir, "metrics_token"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(); err == nil {
		t.Fatal("unreadable folder entry must fail")
	}
}

func TestDatabaseURLSources(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	t.Setenv("HOUSEHOLD_DATABASE_PASSWORD", "synthetic-owner")
	t.Setenv("HOUSEHOLD_DATABASE_USER", "household_owner")
	t.Setenv("HOUSEHOLD_DATABASE_HOST", "127.0.0.1:55434")
	t.Setenv("HOUSEHOLD_DATABASE_NAME", "household_test")
	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != "postgres://household_owner:synthetic-owner@127.0.0.1:55434/household_test?sslmode=disable" {
		t.Fatal("database URL settings not honored", err)
	}
	restore, explicit, err := RestoreTarget(cfg.DatabaseURL)
	if err != nil || explicit || restore != "postgres://household_owner:synthetic-owner@127.0.0.1:55434/household_restore?sslmode=disable" {
		t.Fatal("restore URL not derived from the source credentials", err)
	}
	t.Setenv("HOUSEHOLD_RESTORE_DATABASE_URL", "postgres://explicit@restore/household_restore")
	if restore, explicit, err = RestoreTarget(cfg.DatabaseURL); err != nil || !explicit || restore != "postgres://explicit@restore/household_restore" {
		t.Fatal("explicit restore URL must win", err)
	}
	t.Setenv("HOUSEHOLD_RESTORE_DATABASE_URL", "")
	t.Setenv("HOUSEHOLD_DATABASE_HOST", "db")
	if _, err = Load(); err == nil {
		t.Fatal("host without port must fail")
	}
	t.Setenv("HOUSEHOLD_DATABASE_HOST", "")
	t.Setenv("HOUSEHOLD_DATABASE_URL", "postgres://synthetic@localhost/unused")
	if _, err = Load(); err == nil || strings.Contains(err.Error(), "synthetic") {
		t.Fatal("URL and password together must fail without leaking values", err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	t.Setenv("HOUSEHOLD_DATABASE_PASSWORD", "")
	if _, err = Load(); err == nil {
		t.Fatal("missing database credentials must fail")
	}
}

func TestSecretsFolderEncryptionAndOptionalFiles(t *testing.T) {
	setValidEnvironment(t)
	dir := t.TempDir()
	t.Setenv(SecretsDirVariable, dir)
	t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
	writeSecretFile(t, dir, "encryption_key", strings.Repeat("k", 32))
	writeSecretFile(t, dir, "encryption_keyring", "{}")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatal("legacy key and keyring in the folder must be refused", err)
	}
	// An explicit source disables folder discovery for the other one.
	t.Setenv("HOUSEHOLD_ENCRYPTION_KEY_FILE", filepath.Join(dir, "encryption_key"))
	if _, err := Load(); err != nil {
		t.Fatal("explicit legacy key must ignore a folder keyring", err)
	}
	t.Setenv("HOUSEHOLD_ENCRYPTION_KEY_FILE", "")
	if err := os.Remove(filepath.Join(dir, "encryption_keyring")); err != nil {
		t.Fatal(err)
	}
	scalar := make([]byte, 32)
	scalar[31] = 1
	key := base64.RawURLEncoding.EncodeToString(scalar)
	writeSecretFile(t, dir, "web_push_vapid_private_key", key+"\n")
	writeSecretFile(t, dir, "meters_ca", "")
	cfg, err := Load()
	if err != nil || cfg.WebPushPrivateKey != key || cfg.MeterFeeds.CAPEM != nil {
		t.Fatal("folder Web Push key or empty CA placeholder handled wrongly", err)
	}
}

func TestFeaturesTurnOnWhenConfigured(t *testing.T) {
	setValidEnvironment(t)
	ocrInstalled = func() bool { return true }
	scalar := make([]byte, 32)
	scalar[31] = 1
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", base64.RawURLEncoding.EncodeToString(scalar))
	t.Setenv("HOUSEHOLD_ORIGIN", "https://household.example.org")
	cfg, err := Load()
	if err != nil || !cfg.OCREnabled || !cfg.WebPushEnabled || cfg.WebPushContact != "https://household.example.org" {
		t.Fatal("installed OCR, a push key or the origin contact default were not used", err)
	}
	t.Setenv("HOUSEHOLD_OCR_ENABLED", "false")
	t.Setenv("HOUSEHOLD_WEB_PUSH_ENABLED", "false")
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_CONTACT", "mailto:ops@example.org")
	if cfg, err = Load(); err != nil || cfg.OCREnabled || cfg.WebPushEnabled || cfg.WebPushContact != "mailto:ops@example.org" {
		t.Fatal("explicit settings must win", err)
	}
	t.Setenv("HOUSEHOLD_WEB_PUSH_ENABLED", "")
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", "")
	if cfg, err = Load(); err != nil || cfg.WebPushEnabled {
		t.Fatal("Web Push must stay off without a key", err)
	}
}

// Regression: `start` hands `serve` an explicit app-role password; a mounted
// database_url in the secrets folder must not conflict with it.
func TestDatabaseSourceExplicitBeatsFolder(t *testing.T) {
	setValidEnvironment(t)
	dir := t.TempDir()
	t.Setenv(SecretsDirVariable, dir)
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	writeSecretFile(t, dir, "database_url", "postgres://folder@db:5432/household?sslmode=disable")
	t.Setenv("HOUSEHOLD_DATABASE_USER", "household_app")
	t.Setenv("HOUSEHOLD_DATABASE_PASSWORD", "explicit-app")
	cfg, err := Load()
	if err != nil || !strings.Contains(cfg.DatabaseURL, "household_app:explicit-app@") {
		t.Fatal("an explicit password must win over a folder URL", err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_PASSWORD", "")
	writeSecretFile(t, dir, "database_password", "folder-password")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatal("URL and password both only in the folder must be refused", err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_URL", "postgres://explicit@db:5432/household?sslmode=disable")
	if cfg, err = Load(); err != nil || cfg.DatabaseURL != "postgres://explicit@db:5432/household?sslmode=disable" {
		t.Fatal("an explicit URL must win over a folder password", err)
	}
}

// Regression: the restore target and its maintenance endpoint follow the
// selected source connection, never an ignored password file or default host.
func TestRestoreTargetFollowsTheSelectedSource(t *testing.T) {
	setValidEnvironment(t)
	dir := t.TempDir()
	t.Setenv(SecretsDirVariable, dir)
	writeSecretFile(t, dir, "database_password", "ignored-folder-password")
	source := "postgres://household_app:explicit@primary.example:5432/household?sslmode=require"
	t.Setenv("HOUSEHOLD_DATABASE_URL", source)
	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != source {
		t.Fatal("explicit source not selected", err)
	}
	target, explicit, err := RestoreTarget(cfg.DatabaseURL)
	if err != nil || explicit || target != "postgres://household_app:explicit@primary.example:5432/household_restore?sslmode=require" {
		t.Fatal("restore target must follow the selected source", target, err)
	}
	maintenance, err := RestoreMaintenanceURL(cfg.DatabaseURL, "super")
	if err != nil || maintenance != "postgres://postgres:super@primary.example:5432/postgres?sslmode=require" {
		t.Fatal("maintenance endpoint must follow the selected source", maintenance, err)
	}
	t.Setenv("HOUSEHOLD_RESTORE_DATABASE_HOST", "restore.example:6543")
	if target, _, err = RestoreTarget(cfg.DatabaseURL); err != nil || target != "postgres://household_app:explicit@restore.example:6543/household_restore?sslmode=require" {
		t.Fatal("restore host override", target, err)
	}
	if _, _, err = RestoreTarget("host=db user=app dbname=household"); err == nil {
		t.Fatal("a non-URL source must require an explicit restore URL")
	}
	// An explicit but empty URL file counts as unset.
	empty := filepath.Join(dir, "empty_url")
	if err = os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	t.Setenv("HOUSEHOLD_DATABASE_URL_FILE", empty)
	t.Setenv("HOUSEHOLD_DATABASE_PASSWORD", "explicit-password")
	if cfg, err = Load(); err != nil || !strings.Contains(cfg.DatabaseURL, ":explicit-password@") {
		t.Fatal("an empty explicit URL file must not conflict with a password", err)
	}
}

// Regression: an empty explicit URL file does not block a folder password, and
// the configuration is built from the value that was read.
func TestEmptyExplicitURLFileFallsBackToTheFolder(t *testing.T) {
	setValidEnvironment(t)
	dir := t.TempDir()
	t.Setenv(SecretsDirVariable, dir)
	empty := filepath.Join(t.TempDir(), "empty_url")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_URL", "")
	t.Setenv("HOUSEHOLD_DATABASE_URL_FILE", empty)
	writeSecretFile(t, dir, "database_password", "folder-password")
	cfg, err := Load()
	if err != nil || !strings.Contains(cfg.DatabaseURL, ":folder-password@") {
		t.Fatal("an empty explicit URL file must fall back to the folder password", err)
	}
}
