package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SecretsDirVariable names the optional folder of mounted secret files. The
// container images set it to /run/secrets, so Compose only has to mount a
// secret under its conventional name instead of wiring every *_FILE setting.
const SecretsDirVariable = "HOUSEHOLD_SECRETS_DIR"

// conventionalSecretName maps HOUSEHOLD_CSRF_SECRET to csrf_secret.
func conventionalSecretName(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "HOUSEHOLD_"))
}

// conventionalPath returns the secret file for name inside the secrets folder.
// Missing and empty files mean "not configured", so placeholder files created
// by household init leave an optional integration off.
func conventionalPath(name string) (string, error) {
	dir := os.Getenv(SecretsDirVariable)
	if dir == "" {
		return "", nil
	}
	// security: the folder comes solely from deployment configuration and the
	// file name from a fixed setting name, never from request input.
	info, err := os.Stat(filepath.Join(dir, conventionalSecretName(name))) // #nosec G703 -- trusted operator secret folder
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s secret file could not be read", name)
	}
	if info.Size() == 0 {
		return "", nil
	}
	return filepath.Join(dir, conventionalSecretName(name)), nil
}

// explicitlyConfigured reports whether name or name_FILE is set.
func explicitlyConfigured(name string) bool {
	return os.Getenv(name) != "" || os.Getenv(name+"_FILE") != ""
}

// Configured reports whether a secret setting has a value, a _FILE path or a
// file in the secrets folder, with the same precedence Secret uses.
func Configured(name string) (bool, error) { return configured(name) }

// configured reports whether name has an explicit or conventional source.
func configured(name string) (bool, error) {
	if explicitlyConfigured(name) {
		return true, nil
	}
	path, err := conventionalPath(name)
	return path != "", err
}

// Secret reads a secret setting from its value, its _FILE path or the secrets
// folder, in that order. An empty result means unconfigured.
func Secret(name string) (string, error) { return secret(name) }

// ConnectionURL builds a PostgreSQL URL for user and password on
// HOUSEHOLD_DATABASE_HOST (default db:5432) and HOUSEHOLD_DATABASE_NAME
// (default household). Credentials are percent-encoded, so maintenance tools
// and pgx parse the same connection.
func ConnectionURL(user, password string) (string, error) {
	return connectionURL("HOUSEHOLD_DATABASE_HOST", "db:5432", env("HOUSEHOLD_DATABASE_NAME", "household"), user, password)
}

func connectionURL(hostVariable, defaultHost, databaseName, user, password string) (string, error) {
	host := env(hostVariable, defaultHost)
	if name, port, err := net.SplitHostPort(host); err != nil || name == "" || port == "" {
		return "", fmt.Errorf("%s must be host:port", hostVariable)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: host, Path: "/" + databaseName, RawQuery: "sslmode=disable"}
	return u.String(), nil
}

// databaseURL builds the URL from HOUSEHOLD_DATABASE_PASSWORD when no complete
// URL is configured. An empty result means unconfigured.
func databaseURL(hostVariable, defaultHost, databaseName string) (string, error) {
	password, err := secret("HOUSEHOLD_DATABASE_PASSWORD")
	if err != nil || password == "" {
		return "", err
	}
	return connectionURL(hostVariable, defaultHost, databaseName, env("HOUSEHOLD_DATABASE_USER", "household_app"), password)
}

func loadDatabaseURL(c *Config) error {
	var err error
	if c.DatabaseURL, err = secret("HOUSEHOLD_DATABASE_URL"); err != nil {
		return err
	}
	passwordConfigured, err := configured("HOUSEHOLD_DATABASE_PASSWORD")
	if err != nil {
		return err
	}
	if c.DatabaseURL != "" && passwordConfigured {
		return errors.New("database URL and database password are mutually exclusive")
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL, err = databaseURL("HOUSEHOLD_DATABASE_HOST", "db:5432", env("HOUSEHOLD_DATABASE_NAME", "household"))
	}
	return err
}

// RestoreDatabaseURL returns the disposable restore target: an explicit
// HOUSEHOLD_RESTORE_DATABASE_URL, or household_restore on
// HOUSEHOLD_RESTORE_DATABASE_HOST (default: the application's database
// server) with the same database user and password as the source.
func RestoreDatabaseURL() (string, error) {
	target, err := secret("HOUSEHOLD_RESTORE_DATABASE_URL")
	if err != nil || target != "" {
		return target, err
	}
	return databaseURL("HOUSEHOLD_RESTORE_DATABASE_HOST", env("HOUSEHOLD_DATABASE_HOST", "db:5432"), "household_restore")
}

// RestoreSuperuserURLs address the restore server as postgres: its
// maintenance database (to create and drop household_restore) and
// household_restore itself (to prepare the roles).
func RestoreSuperuserURLs(password string) (maintenance, restore string, err error) {
	host := env("HOUSEHOLD_DATABASE_HOST", "db:5432")
	if maintenance, err = connectionURL("HOUSEHOLD_RESTORE_DATABASE_HOST", host, "postgres", "postgres", password); err != nil {
		return "", "", err
	}
	restore, err = connectionURL("HOUSEHOLD_RESTORE_DATABASE_HOST", host, "household_restore", "postgres", password)
	return maintenance, restore, err
}
