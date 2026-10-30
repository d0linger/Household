package main

import (
	"crypto/ecdh"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPushKeygenCreatesMatchingExclusiveFiles(t *testing.T) {
	dir := t.TempDir()
	privatePath, publicPath := filepath.Join(dir, "private"), filepath.Join(dir, "public")
	if err := runPushKeygen([]string{"--private-out", privatePath, "--public-out", publicPath}); err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{privatePath, publicPath} {
			info, statErr := os.Stat(name)
			if statErr != nil || info.Mode().Perm() != 0600 {
				t.Fatal("both generated key files must remain owner-readable only")
			}
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(private)))
	if err != nil {
		t.Fatal("invalid private key encoding")
	}
	key, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) != strings.TrimSpace(string(public)) {
		t.Fatal("public key must derive from the generated private key")
	}
	if runPushKeygen([]string{"--private-out", privatePath, "--public-out", publicPath}) == nil {
		t.Fatal("key generation must not overwrite existing files")
	}
	still, err := os.ReadFile(privatePath)
	if err != nil || string(still) != string(private) {
		t.Fatal("existing private key changed")
	}
}

func TestPushKeygenRollbackAndArgumentBoundaries(t *testing.T) {
	dir := t.TempDir()
	privatePath, publicPath := filepath.Join(dir, "private"), filepath.Join(dir, "public")
	if err := os.WriteFile(publicPath, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if runPushKeygen([]string{"--private-out", privatePath, "--public-out", publicPath}) == nil {
		t.Fatal("existing public output must fail")
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatal("failed pair must remove only its newly created private file")
	}
	for _, args := range [][]string{nil, {"--private-out", privatePath}, {"--private-out", privatePath, "--public-out", privatePath}, {"--unknown"}} {
		if runPushKeygen(args) == nil {
			t.Fatal("incomplete or conflicting arguments must fail")
		}
	}
}
