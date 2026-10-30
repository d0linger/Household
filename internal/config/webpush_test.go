package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebPushConfigurationBoundaries(t *testing.T) {
	scalar := make([]byte, 32)
	scalar[31] = 1
	key := base64.RawURLEncoding.EncodeToString(scalar)
	cases := []struct {
		name, enabled, contact, private string
		wantError                       bool
	}{
		{name: "disabled default"},
		{name: "enabled unconfigured", enabled: "true"},
		{name: "configured mail", enabled: "true", contact: "mailto:operator@example.invalid", private: key},
		{name: "configured HTTPS", enabled: "true", contact: "https://example.invalid/contact", private: key},
		{name: "invalid flag", enabled: "possibly", wantError: true},
		{name: "zero scalar", private: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), wantError: true},
		{name: "wrong length", private: "AQ", wantError: true},
		{name: "padded key", private: key + "=", wantError: true},
		{name: "oversized key", private: strings.Repeat("x", 129), wantError: true},
		{name: "contact injection", contact: "mailto:operator@example.invalid\r\nInjected:x", wantError: true},
		{name: "mail query", contact: "mailto:operator@example.invalid?subject=test", wantError: true},
		{name: "HTTPS credentials", contact: "https://operator@example.invalid/contact", wantError: true},
		{name: "HTTP contact", contact: "http://example.invalid", wantError: true},
		{name: "unsupported HTTPS contact port", contact: "https://example.invalid:8443/contact", wantError: true},
		{name: "fragment", contact: "https://example.invalid/#contact", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOUSEHOLD_WEB_PUSH_ENABLED", tc.enabled)
			t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_CONTACT", tc.contact)
			t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", tc.private)
			t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY_FILE", "")
			var cfg Config
			err := loadWebPush(&cfg)
			if (err != nil) != tc.wantError {
				t.Fatalf("configuration error=%v; expected error=%v", err, tc.wantError)
			}
			if !tc.wantError && (cfg.WebPushEnabled != (tc.enabled == "true") || cfg.WebPushPrivateKey != tc.private || cfg.WebPushContact != tc.contact) {
				t.Fatal("configuration did not preserve explicit values")
			}
		})
	}
}

func TestWebPushSecretFileBoundaries(t *testing.T) {
	t.Setenv("HOUSEHOLD_WEB_PUSH_ENABLED", "true")
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_CONTACT", "")
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", "")
	name := filepath.Join(t.TempDir(), "private-key")
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY_FILE", name)
	scalar := make([]byte, 32)
	scalar[31] = 1
	key := base64.RawURLEncoding.EncodeToString(scalar)
	if err := os.WriteFile(name, []byte(key+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := loadWebPush(&cfg); err != nil || cfg.WebPushPrivateKey != key {
		t.Fatal("bounded secret file should accept a terminal newline")
	}
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", key)
	if loadWebPush(&cfg) == nil {
		t.Fatal("simultaneous file and environment key must fail")
	}
	t.Setenv("HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", "")
	if err := os.WriteFile(name, []byte(strings.Repeat("x", 513)), 0600); err != nil {
		t.Fatal(err)
	}
	if loadWebPush(&cfg) == nil {
		t.Fatal("oversized private key file must fail")
	}
}
