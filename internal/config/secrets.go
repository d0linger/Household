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

// explicitValue reads name or name_FILE only. An explicit file that is empty
// counts as unset, like an empty file in the secrets folder.
func explicitValue(name string) (string, error) {
	value, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && path != "" {
		return "", fmt.Errorf("both %s and file are configured", name)
	}
	if path != "" {
		return readSecretFile(name, path, 32<<10)
	}
	if len(value) > 32<<10 {
		return "", fmt.Errorf("%s secret exceeds its bounded format", name)
	}
	return value, nil
}

// folderValue reads only the conventional file in the secrets folder.
func folderValue(name string) (string, error) {
	path, err := conventionalPath(name)
	if err != nil || path == "" {
		return "", err
	}
	return readSecretFile(name, path, 32<<10)
}

// loadDatabaseURL selects the database credential once and builds the URL from
// exactly the value it read. Explicit settings (value or _FILE) form one tier
// and the secrets folder another: a non-empty explicit value disables folder
// discovery, and two non-empty values in the selected tier are refused.
func loadDatabaseURL(c *Config) error {
	databaseURL, err := explicitValue("HOUSEHOLD_DATABASE_URL")
	if err != nil {
		return err
	}
	password, err := explicitValue("HOUSEHOLD_DATABASE_PASSWORD")
	if err != nil {
		return err
	}
	if databaseURL == "" && password == "" {
		if databaseURL, err = folderValue("HOUSEHOLD_DATABASE_URL"); err != nil {
			return err
		}
		if password, err = folderValue("HOUSEHOLD_DATABASE_PASSWORD"); err != nil {
			return err
		}
	}
	if databaseURL != "" && password != "" {
		return errors.New("database URL and database password are mutually exclusive")
	}
	if databaseURL != "" || password == "" {
		c.DatabaseURL = databaseURL
		return nil
	}
	c.DatabaseURL, err = connectionURL("HOUSEHOLD_DATABASE_HOST", "db:5432", env("HOUSEHOLD_DATABASE_NAME", "household"), env("HOUSEHOLD_DATABASE_USER", "household_app"), password)
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

// ServerMaintenanceURL addresses the server of source itself (never the
// restore server override) as postgres in its maintenance database. A live
// restore renames databases, which is only possible within that server.
func ServerMaintenanceURL(source, superuser string) (string, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" || u.User == nil {
		return "", errors.New("a live restore needs a postgres:// database URL")
	}
	u.User = url.UserPassword("postgres", superuser)
	u.Path, u.RawPath = "/postgres", ""
	stripTargetQuery(u)
	return u.String(), nil
}

// connectionTargetKeys are query parameters pgx applies after the URL's
// authority and path; they could change the role, database or server.
var connectionTargetKeys = []string{"user", "password", "database", "dbname", "host", "hostaddr", "port", "service", "servicefile", "passfile"}

// stripTargetQuery removes every query parameter that could override the
// identity or target set on a derived maintenance URL; transport options
// such as sslmode stay.
func stripTargetQuery(u *url.URL) {
	query := u.Query()
	for key := range query {
		for _, target := range connectionTargetKeys {
			if strings.EqualFold(key, target) {
				query.Del(key)
			}
		}
	}
	u.RawQuery = query.Encode()
}

// WithDatabase returns raw with its database replaced by name.
func WithDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Host == "" {
		return "", errors.New("restore checks need postgres:// URLs; set HOUSEHOLD_RESTORE_DATABASE_URL")
	}
	u.Path, u.RawPath = "/"+name, ""
	// Only the database may change here; a query override must not move it elsewhere.
	query := u.Query()
	for key := range query {
		if strings.EqualFold(key, "database") || strings.EqualFold(key, "dbname") {
			query.Del(key)
		}
	}
	u.RawQuery = query.Encode()
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
