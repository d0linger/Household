package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitCreatesSecretsOnceWithoutPrintingThem(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := runInit([]string{"--files"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range generatedSecrets {
		raw, err := os.ReadFile(filepath.Join(".local", "secrets", name))
		if err != nil || len(raw) != 48 || strings.Contains(out.String(), string(raw)) {
			t.Fatalf("%s missing, malformed or printed", name)
		}
		if runtime.GOOS != "windows" {
			if info, _ := os.Stat(filepath.Join(".local", "secrets", name)); info.Mode().Perm() != secretMode(name) {
				t.Fatalf("%s has mode %v, want %v", name, info.Mode().Perm(), secretMode(name))
			}
		}
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(".local", "secrets")); info.Mode().Perm() != 0o700 {
			t.Fatal("the secrets folder must stay owner-only")
		}
		if secretMode("csrf_secret") != 0o600 || secretMode("app_password") != 0o644 {
			t.Fatal("only the database passwords may be readable by the PostgreSQL container")
		}
	}
	if _, err := os.Stat(".env"); !os.IsNotExist(err) {
		t.Fatal("--files must not write .env")
	}
	before, _ := os.ReadFile(filepath.Join(".local", "secrets", "csrf_secret"))
	if err := runInit([]string{"--files"}, &out); err == nil {
		t.Fatal("a second run must refuse existing secrets")
	}
	if after, _ := os.ReadFile(filepath.Join(".local", "secrets", "csrf_secret")); !bytes.Equal(before, after) {
		t.Fatal("existing secrets were changed")
	}
}

func TestInitRejectsArguments(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if runInit([]string{"extra"}, &out) == nil || runInit([]string{"--files", "extra"}, &out) == nil {
		t.Fatal("unexpected arguments must fail")
	}
	if _, err := os.Stat(".local"); !os.IsNotExist(err) {
		t.Fatal("rejected runs must not create files")
	}
}

func TestInitPrintsCompleteEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := runInit(nil, &out); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			values[name] = value
		}
	}
	for _, name := range []string{"POSTGRES_PASSWORD", "HOUSEHOLD_OWNER_PASSWORD", "HOUSEHOLD_APP_PASSWORD", "HOUSEHOLD_CSRF_SECRET", "HOUSEHOLD_RATE_SECRET", "HOUSEHOLD_ENCRYPTION_KEY", "HOUSEHOLD_ADMIN_PASSWORD", "RESTIC_PASSWORD"} {
		if len(values[name]) != 48 {
			t.Fatalf("%s missing or malformed", name)
		}
	}
	if len(values["HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY"]) != 43 || values["HOUSEHOLD_ORIGIN"] != "https://household.example.org" {
		t.Fatal("Web Push key or origin placeholder missing")
	}
	if values["POSTGRES_PASSWORD"] == values["HOUSEHOLD_OWNER_PASSWORD"] {
		t.Fatal("secrets must be independent")
	}
	if _, err := os.Stat(".local"); !os.IsNotExist(err) {
		t.Fatal("printing .env must not create files")
	}
	out.Reset()
	if err := runInit([]string{"--development"}, &out); err != nil || !strings.Contains(out.String(), "HOUSEHOLD_ENV=development\nHOUSEHOLD_ORIGIN=http://localhost:8100\n") {
		t.Fatal("development settings missing", err)
	}
}
