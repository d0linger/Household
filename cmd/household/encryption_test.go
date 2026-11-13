package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"household/internal/secretbox"
)

func TestEncryptionKeygenExclusivePrivateAndNoConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "encryption.json")
	t.Setenv("HOUSEHOLD_DATABASE_URL", "invalid-database-configuration")
	previous := os.Args
	os.Args = []string{"household", "encryption-keygen", "--out", path}
	t.Cleanup(func() { os.Args = previous })
	readOutput, writeOutput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousOutput := os.Stdout
	os.Stdout = writeOutput
	t.Cleanup(func() { os.Stdout = previousOutput; _ = readOutput.Close(); _ = writeOutput.Close() })
	var logs bytes.Buffer
	if err := run(slog.New(slog.NewJSONHandler(&logs, nil))); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatal("key generation emitted logs")
	}
	os.Stdout = previousOutput
	if err = writeOutput.Close(); err != nil {
		t.Fatal(err)
	}
	printed, err := io.ReadAll(readOutput)
	if err != nil || len(printed) != 0 {
		t.Fatal("key generation emitted stdout", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := secretbox.ParseConfig(raw)
	if err != nil || len(ring.ReadableIDs()) != 1 || ring.LegacyID() != "" {
		t.Fatal("generated ring must be one active key without invented legacy binding", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("generated key file must be private", err)
		}
	}
	if err = runEncryptionKeygen([]string{"--out", path}); err == nil {
		t.Fatal("existing key overwritten")
	}
	still, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, still) {
		t.Fatal("existing key bytes changed")
	}
	for _, args := range [][]string{nil, {"--out"}, {"--out", path, "extra"}, {"--unknown", path}, {"--out", ""}} {
		if runEncryptionKeygen(args) == nil {
			t.Fatal("invalid keygen arguments accepted")
		}
	}
}

func TestEncryptionKeygenRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	if runEncryptionKeygen([]string{"--out", dir}) == nil {
		t.Fatal("directory output accepted")
	}
	target, link := filepath.Join(dir, "existing"), filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		// Windows without Developer Mode denies symlink creation. Existing-file
		// rejection is still mandatory in the ordinary cross-platform contract.
		if runtime.GOOS == "windows" {
			t.Log("host does not permit constructing a symlink; Linux campaign covers this branch")
			return
		}
		t.Fatal(err)
	}
	if runEncryptionKeygen([]string{"--out", link}) == nil {
		t.Fatal("symlink output accepted")
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "keep" {
		t.Fatal("symlink target changed")
	}
}

func TestEncryptionCommandArgumentsFailBeforeConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--batch", "0"}, {"--batch", "101"}, {"--batch", "01"}, {"--batch", "+1"}, {"--batch", "1", "--batch", "2"}, {"--unknown"}, {"extra"}} {
		if _, err := encryptionBatch(args); err == nil {
			t.Fatal("unsafe batch arguments accepted", args)
		}
	}
	for _, tc := range []struct {
		args []string
		want int
	}{{nil, 50}, {[]string{"--batch", "1"}, 1}, {[]string{"--batch=100"}, 100}} {
		if got, err := encryptionBatch(tc.args); err != nil || got != tc.want {
			t.Fatal("valid batch rejected", got, err)
		}
	}
	for _, args := range [][]string{nil, {"v1"}, {strings.Repeat("A", 64)}, {strings.Repeat("a", 64), "extra"}} {
		if _, err := encryptionRetirementID(args); err == nil {
			t.Fatal("invalid retirement ID accepted")
		}
	}
	if _, err := encryptionRetirementID([]string{strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOUSEHOLD_DATABASE_URL", "invalid-database-configuration")
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	os.Args = []string{"household", "encryption-reencrypt", "--batch", "0"}
	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil || !strings.Contains(err.Error(), "usage: household encryption-reencrypt") {
		t.Fatal("batch validation did not precede config/database", err)
	}
}
