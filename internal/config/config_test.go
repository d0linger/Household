package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecret(t *testing.T) {
	const name = "HOUSEHOLD_TEST_SECRET"
	cases := []struct {
		name, environment, file, want string
		missingFile, wantErr          bool
	}{
		{name: "unset"},
		{name: "environment", environment: "  preserved environment  ", want: "  preserved environment  "},
		{name: "file newline", file: "synthetic file secret\r\n", want: "synthetic file secret"},
		{name: "conflicting sources", environment: "synthetic environment secret", file: "synthetic file secret", wantErr: true},
		{name: "missing file", missingFile: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(name, tc.environment)
			t.Setenv(name+"_FILE", "")
			if tc.file != "" || tc.missingFile {
				path := filepath.Join(t.TempDir(), "secret")
				if !tc.missingFile {
					if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv(name+"_FILE", path)
			}
			got, err := secret(name)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("secret() = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic") {
				t.Fatal("secret contents appeared in an error")
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	setValidEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Production || cfg.Listen != ":8080" || cfg.Origin != "http://localhost:8100" {
		t.Fatalf("unexpected deployment defaults: production=%v listen=%q origin=%q", cfg.Production, cfg.Listen, cfg.Origin)
	}
	if cfg.MaxConnections != 10 || cfg.SessionIdle != 24*time.Hour || cfg.SessionAbsolute != 30*24*time.Hour {
		t.Fatal("connection or session limits changed")
	}
	if cfg.UploadQuota != 2<<30 || cfg.OCREnabled || len(cfg.TrustedProxies) != 0 {
		t.Fatal("upload, OCR or proxy defaults changed")
	}
	if cfg.AdminUsername != "admin" || cfg.HouseholdName != "Mein Haushalt" {
		t.Fatal("bootstrap defaults changed")
	}
}

func TestLoadOrigin(t *testing.T) {
	cases := []struct {
		name, origin, want string
		production         bool
	}{
		{name: "development", origin: "http://localhost:8100", want: "http://localhost:8100"},
		{name: "root slash", origin: "https://household.example/", want: "https://household.example", production: true},
		{name: "hostname case", origin: "https://HOUSEHOLD.Example", want: "https://household.example", production: true},
		{name: "default secure port", origin: "https://household.example:443", want: "https://household.example", production: true},
		{name: "default insecure port", origin: "http://localhost:80/", want: "http://localhost"},
		{name: "nondefault port", origin: "https://household.example:8443", want: "https://household.example:8443", production: true},
		{name: "IPv6", origin: "http://[::1]:8100/", want: "http://[::1]:8100"},
		{name: "IPv6 normalization", origin: "http://[0:0:0:0:0:0:0:1]:8100", want: "http://[::1]:8100"},
		{name: "IPv4 mapped IPv6", origin: "https://[::ffff:192.0.2.1]", want: "https://[::ffff:c000:201]", production: true},
		{name: "international domain as ASCII", origin: "https://xn--husl-loa.example", want: "https://xn--husl-loa.example", production: true},
		{name: "Unicode domain requires ASCII", origin: "https://häusl.example", production: true},
		{name: "IPv4", origin: "http://127.0.0.1:8100", want: "http://127.0.0.1:8100"},
		{name: "abbreviated IPv4", origin: "http://127.1"},
		{name: "legacy hexadecimal IPv4", origin: "http://0x7f000001"},
		{name: "hostname spaces", origin: "https://household example", production: true},
		{name: "empty DNS label", origin: "https://household..example", production: true},
		{name: "unbracketed IPv6", origin: "https://::1", production: true},
		{name: "production requires TLS", origin: "http://household.example", production: true},
		{name: "credentials", origin: "https://user:password@household.example", production: true},
		{name: "path", origin: "https://household.example/app", production: true},
		{name: "repeated slashes", origin: "https://household.example///", production: true},
		{name: "query", origin: "https://household.example/?x=1", production: true},
		{name: "empty query", origin: "https://household.example?", production: true},
		{name: "fragment", origin: "https://household.example/#section", production: true},
		{name: "empty fragment", origin: "https://household.example#", production: true},
		{name: "missing hostname", origin: "https://:443", production: true},
		{name: "invalid IPv6", origin: "https://[invalid]", production: true},
		{name: "IPv6 zone", origin: "https://[fe80::1%25eth0]", production: true},
		{name: "invalid port", origin: "https://household.example:abc", production: true},
		{name: "empty port", origin: "https://household.example:", production: true},
		{name: "zero port", origin: "https://household.example:0", production: true},
		{name: "large port", origin: "https://household.example:65536", production: true},
		{name: "relative", origin: "//household.example"},
		{name: "unsupported scheme", origin: "ftp://household.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("HOUSEHOLD_ORIGIN", tc.origin)
			if tc.production {
				t.Setenv("HOUSEHOLD_ENV", "production")
			}
			cfg, err := Load()
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted invalid origin %q", tc.origin)
				}
				return
			}
			if err != nil || cfg.Origin != tc.want {
				t.Fatalf("Load origin = %q, %v; want %q", cfg.Origin, err, tc.want)
			}
		})
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name, key, value, errorPart string
	}{
		{name: "unknown mode", key: "HOUSEHOLD_ENV", value: "staging", errorPart: "HOUSEHOLD_ENV"},
		{name: "production is default", key: "HOUSEHOLD_ENV", errorPart: "requires https"},
		{name: "database required", key: "HOUSEHOLD_DATABASE_URL", errorPart: "database URL"},
		{name: "short CSRF", key: "HOUSEHOLD_CSRF_SECRET", value: "short", errorPart: "CSRF secret"},
		{name: "blank encryption", key: "HOUSEHOLD_ENCRYPTION_KEY", value: strings.Repeat(" ", 32), errorPart: "encryption secret"},
		{name: "placeholder rate", key: "HOUSEHOLD_RATE_SECRET", value: "CHANGE-ME-" + strings.Repeat("x", 32), errorPart: "rate secret"},
		{name: "shared secrets", key: "HOUSEHOLD_ENCRYPTION_KEY", value: strings.Repeat("c", 32), errorPart: "independent"},
		{name: "invalid OCR", key: "HOUSEHOLD_OCR_ENABLED", value: "maybe", errorPart: "OCR"},
		{name: "too few connections", key: "HOUSEHOLD_DB_MAX_CONNECTIONS", value: "1", errorPart: "connection limit"},
		{name: "too many connections", key: "HOUSEHOLD_DB_MAX_CONNECTIONS", value: "101", errorPart: "connection limit"},
		{name: "fractional connections", key: "HOUSEHOLD_DB_MAX_CONNECTIONS", value: "2.5", errorPart: "connection limit"},
		{name: "connection overflow", key: "HOUSEHOLD_DB_MAX_CONNECTIONS", value: "999999999999999999999999", errorPart: "connection limit"},
		{name: "proxy requires CIDR", key: "HOUSEHOLD_TRUSTED_PROXIES", value: "127.0.0.1", errorPart: "proxy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.errorPart) {
				t.Fatalf("Load() error = %v; want %q", err, tc.errorPart)
			}
		})
	}
}

func TestLoadConfiguredLimitsAndProxies(t *testing.T) {
	for _, limit := range []string{"2", "100"} {
		t.Run("connection limit "+limit, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("HOUSEHOLD_DB_MAX_CONNECTIONS", limit)
			t.Setenv("HOUSEHOLD_TRUSTED_PROXIES", " 127.0.0.1/32, , ::1/128 ")
			t.Setenv("HOUSEHOLD_OCR_ENABLED", "true")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "127.0.0.1/32" || cfg.TrustedProxies[1].String() != "::1/128" {
				t.Fatalf("unexpected proxy networks: %v", cfg.TrustedProxies)
			}
			if !cfg.OCREnabled || (limit == "2" && cfg.MaxConnections != 2) || (limit == "100" && cfg.MaxConnections != 100) {
				t.Fatal("configured limits were not applied")
			}
		})
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HOUSEHOLD_ENV", "HOUSEHOLD_LISTEN", "HOUSEHOLD_ORIGIN", "HOUSEHOLD_ADMIN_USERNAME",
		"HOUSEHOLD_NAME", "HOUSEHOLD_UPLOAD_DIR", "HOUSEHOLD_OCR_ENABLED",
		"HOUSEHOLD_DB_MAX_CONNECTIONS", "HOUSEHOLD_TRUSTED_PROXIES", "HOUSEHOLD_DATABASE_URL",
		"HOUSEHOLD_CSRF_SECRET", "HOUSEHOLD_ENCRYPTION_KEY", "HOUSEHOLD_ENCRYPTION_KEYRING", "HOUSEHOLD_RATE_SECRET",
		"HOUSEHOLD_ADMIN_PASSWORD", "HOUSEHOLD_METRICS_TOKEN",
		"HOUSEHOLD_OFF_ENABLED", "HOUSEHOLD_OFF_USER_AGENT", "HOUSEHOLD_OFF_STAGING",
		"HOUSEHOLD_WEB_PUSH_ENABLED", "HOUSEHOLD_WEB_PUSH_VAPID_CONTACT", "HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY",
		"HOUSEHOLD_METERS_HA_URL", "HOUSEHOLD_METERS_HA_TOKEN", "HOUSEHOLD_METERS_MQTT_URL",
		"HOUSEHOLD_METERS_MQTT_USER", "HOUSEHOLD_METERS_MQTT_PASSWORD", "HOUSEHOLD_METERS_ALLOW_PLAINTEXT", "HOUSEHOLD_METERS_CA_FILE",
	} {
		t.Setenv(name, "")
		t.Setenv(name+"_FILE", "")
	}
	t.Setenv("HOUSEHOLD_ENV", "development")
	t.Setenv("HOUSEHOLD_DATABASE_URL", "postgres://synthetic@localhost/unused")
	t.Setenv("HOUSEHOLD_CSRF_SECRET", strings.Repeat("c", 32))
	t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", strings.Repeat("e", 32))
	t.Setenv("HOUSEHOLD_RATE_SECRET", strings.Repeat("r", 32))
}
