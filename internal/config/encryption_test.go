package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"household/internal/secretbox"
)

func encryptionFixture(t *testing.T, secret string, legacy bool) string {
	t.Helper()
	ring, err := secretbox.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ring.ExportConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !legacy {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "legacy")
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
	}
	return string(raw)
}

func TestEncryptionRingSelectionRetirementAndFixtureFallback(t *testing.T) {
	for _, fromFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment", true: "file"}[fromFile], func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			secret := "synthetic operator encryption material 0123456789"
			raw := encryptionFixture(t, secret, false)
			if fromFile {
				file := filepath.Join(t.TempDir(), "ring.json")
				if err := os.WriteFile(file, []byte(raw+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING_FILE", file)
			} else {
				t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", raw)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			keys, err := cfg.Keyring()
			if err != nil || keys != cfg.EncryptionKeys || keys.LegacyID() != "" || cfg.EncryptionKey != "" {
				t.Fatal("ring configuration still depends on legacy environment", err)
			}
			if err := keys.ValidateIndependent(secret); err == nil {
				t.Fatal("loaded ring lost actual key material")
			}
		})
	}
	legacy := "  synthetic exact fixture encryption material 0123456789 \t"
	fixture := Config{EncryptionKey: legacy}
	keys, err := fixture.Keyring()
	if err != nil || keys.ActiveID() == "" || keys.ActiveID() != keys.LegacyID() {
		t.Fatal("fixture fallback unavailable", err)
	}
	plain, err := secretbox.New(strings.TrimSpace(legacy))
	if err != nil || plain.ActiveID() == keys.ActiveID() {
		t.Fatal("fixture fallback normalized original string", err)
	}
}

func TestEncryptionRingRejectsConflictingAndSharedSecrets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*testing.T)
	}{
		{"old environment and ring", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, strings.Repeat("z", 32), true))
		}},
		{"old file and ring", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY_FILE", "unread-old-key")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, strings.Repeat("z", 32), true))
		}},
		{"ring environment and file", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, strings.Repeat("z", 32), true))
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING_FILE", "unread-ring")
		}},
		{"empty ring file", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			file := filepath.Join(t.TempDir(), "empty")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING_FILE", file)
		}},
		{"ring same csrf", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, strings.Repeat("c", 32), true))
		}},
		{"ring same rate", func(t *testing.T) {
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, strings.Repeat("r", 32), true))
		}},
		{"ring same vapid", func(t *testing.T) {
			key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEY", "")
			t.Setenv("HOUSEHOLD_ENCRYPTION_KEYRING", encryptionFixture(t, key, true))
			t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", key)
		}},
		{"vapid same csrf", func(t *testing.T) {
			key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			t.Setenv("HOUSEHOLD_CSRF_SECRET", key)
			t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", key)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setValidEnvironment(t)
			tc.configure(t)
			if _, err := Load(); err == nil {
				t.Fatal("conflicting configuration accepted")
			}
		})
	}
}

func TestBoundedSecretReadsPreserveWhitespaceAndHidePaths(t *testing.T) {
	const name = "HOUSEHOLD_TEST_BOUNDED_SECRET"
	t.Setenv(name, "")
	t.Setenv(name+"_FILE", "")
	t.Setenv(name, strings.Repeat("x", 32<<10+1))
	if _, err := secret(name); err == nil {
		t.Fatal("oversized environment accepted")
	}
	t.Setenv(name, "")
	file := filepath.Join(t.TempDir(), "private-path-never-report")
	if err := os.WriteFile(file, bytes.Repeat([]byte{'x'}, 32<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(name+"_FILE", file)
	if _, err := secret(name); err == nil || strings.Contains(err.Error(), file) {
		t.Fatal("oversized file accepted or path disclosed")
	}
	t.Setenv(name+"_FILE", file+"-missing")
	if _, err := secret(name); err == nil || strings.Contains(err.Error(), "private-path-never-report") {
		t.Fatal("missing file path disclosed")
	}
	t.Setenv(name+"_FILE", filepath.Dir(file))
	if _, err := secret(name); err == nil {
		t.Fatal("nonregular secret accepted")
	}
	t.Setenv(name+"_FILE", "")
	t.Setenv(name, "  retained environment value  ")
	if value, err := secret(name); err != nil || value != "  retained environment value  " {
		t.Fatal("environment whitespace changed")
	}
}
