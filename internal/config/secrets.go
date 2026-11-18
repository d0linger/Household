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

// explicitValue reports whether name or name_FILE holds a value. An explicit
// file that is empty counts as unset, like an empty file in the secrets folder.
func explicitValue(name string) (bool, error) {
	if !explicitlyConfigured(name) {
		return false, nil
	}
	value, err := secret(name)
	return value != "", err
}

func loadDatabaseURL(c *Config) error {
	urlConfigured, err := explicitValue("HOUSEHOLD_DATABASE_URL")
	if err != nil {
		return err
	}
	passwordConfigured, err := explicitValue("HOUSEHOLD_DATABASE_PASSWORD")
	if err != nil {
		return err
	}
	if urlConfigured && passwordConfigured {
		return errors.New("database URL and database password are mutually exclusive")
	}
	if !urlConfigured && !passwordConfigured {
		// Only the secrets folder names a source: refuse both rather than guess.
		// An explicit source (such as the app-role handoff of `start`) disables
		// folder discovery for the other one, like the encryption key sources.
		if urlConfigured, err = configured("HOUSEHOLD_DATABASE_URL"); err != nil {
			return err
		}
		if passwordConfigured, err = configured("HOUSEHOLD_DATABASE_PASSWORD"); err != nil {
			return err
		}
		if urlConfigured && passwordConfigured {
			return errors.New("database URL and database password are mutually exclusive")
		}
	}
	if urlConfigured {
		c.DatabaseURL, err = secret("HOUSEHOLD_DATABASE_URL")
		return err
	}
	c.DatabaseURL, err = databaseURL("HOUSEHOLD_DATABASE_HOST", "db:5432", env("HOUSEHOLD_DATABASE_NAME", "household"))
	return err
}

// RestoreDatabase is the database an explicit or derived restore check uses.
const RestoreDatabase = "household_restore"

// RestoreTarget resolves the restore target in one read: an explicit
// HOUSEHOLD_RESTORE_DATABASE_URL (explicit=true), or household_restore on the
// server of the selected source connection (HOUSEHOLD_RESTORE_DATABASE_HOST
// overrides the server) with the same credentials. Deriving never reads an
// ignored password source or a default host.
func RestoreTarget(source string) (target string, explicit bool, err error) {
	if target, err = secret("HOUSEHOLD_RESTORE_DATABASE_URL"); err != nil || target != "" {
		return target, target != "", err
	}
	target, err = restoreServer(source, "", "", RestoreDatabase)
	return target, false, err
}

// RestoreMaintenanceURL addresses the restore server of source as postgres
// with the superuser password, in its postgres maintenance database.
func RestoreMaintenanceURL(source, superuser string) (string, error) {
	return restoreServer(source, "postgres", superuser, "postgres")
}

// WithDatabase returns raw with its database replaced by name.
func WithDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" {
		return "", errors.New("restore checks need postgres:// URLs; set HOUSEHOLD_RESTORE_DATABASE_URL")
	}
	u.Path, u.RawPath = "/"+name, ""
	return u.String(), nil
}

// restoreServer derives a URL on the server of source. An empty user keeps the
// source's credentials.
func restoreServer(source, user, password, database string) (string, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" || u.User == nil {
		return "", errors.New("the restore target cannot be derived from this database URL; set HOUSEHOLD_RESTORE_DATABASE_URL")
	}
	if host := os.Getenv("HOUSEHOLD_RESTORE_DATABASE_HOST"); host != "" {
		if name, port, err := net.SplitHostPort(host); err != nil || name == "" || port == "" {
			return "", errors.New("HOUSEHOLD_RESTORE_DATABASE_HOST must be host:port")
		}
		u.Host = host
	}
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	u.Path, u.RawPath = "/"+database, ""
	return u.String(), nil
}
