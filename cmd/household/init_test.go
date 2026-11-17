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
	if err := os.WriteFile(".env.example", []byte("HOUSEHOLD_ORIGIN=https://household.example.org\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInit(nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range generatedSecrets {
		raw, err := os.ReadFile(filepath.Join(".local", "secrets", name))
		if err != nil || len(raw) != 48 || strings.Contains(out.String(), string(raw)) {
			t.Fatalf("%s missing, malformed or printed", name)
		}
		if runtime.GOOS != "windows" {
			if info, _ := os.Stat(filepath.Join(".local", "secrets", name)); info.Mode().Perm() != 0o600 {
				t.Fatalf("%s must be owner-only", name)
			}
		}
	}
	for _, name := range append([]string{"web_push_private_key", "web_push_public_key"}, placeholderSecrets...) {
		if _, err := os.Stat(filepath.Join(".local", "secrets", name)); err != nil {
			t.Fatalf("%s missing", name)
		}
	}
	if env, err := os.ReadFile(".env"); err != nil || !strings.Contains(string(env), "household.example.org") {
		t.Fatal(".env was not copied from the template")
	}
	before, _ := os.ReadFile(filepath.Join(".local", "secrets", "csrf_secret"))
	if err := runInit(nil, &out); err == nil {
		t.Fatal("a second run must refuse existing secrets")
	}
	if after, _ := os.ReadFile(filepath.Join(".local", "secrets", "csrf_secret")); !bytes.Equal(before, after) {
		t.Fatal("existing secrets were changed")
	}
}

func TestInitDevelopmentAndCleanupOnFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := runInit([]string{"--env-template", "missing.example"}, &out); err == nil {
		t.Fatal("missing template must fail")
	}
	if _, err := os.Stat(filepath.Join(".local", "secrets")); !os.IsNotExist(err) {
		t.Fatal("a failed run must not leave partial secrets")
	}
	if err := runInit([]string{"--development"}, &out); err != nil {
		t.Fatal(err)
	}
	if env, err := os.ReadFile(".env"); err != nil || !strings.Contains(string(env), "HOUSEHOLD_ENV=development") {
		t.Fatal("development .env not written")
	}
	if runInit([]string{"extra"}, &out) == nil {
		t.Fatal("unexpected arguments must fail")
	}
}
