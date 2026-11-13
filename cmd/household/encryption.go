package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"household/internal/secretbox"
)

// Key generation requires neither configured secrets nor a database connection.
func runEncryptionKeygen(args []string) error {
	if len(args) != 2 || args[0] != "--out" || args[1] == "" {
		return errors.New("usage: household encryption-keygen --out NEW_FILE")
	}
	name, err := filepath.Abs(args[1])
	if err != nil {
		return errors.New("invalid encryption key output path")
	}
	id, secret, err := secretbox.GenerateKey()
	if err != nil {
		return errors.New("encryption key generation failed")
	}
	raw, err := json.Marshal(struct {
		Version int                 `json:"version"`
		Active  string              `json:"active"`
		Keys    []map[string]string `json:"keys"`
	}{1, id, []map[string]string{{"id": id, "secret": secret}}})
	if err != nil {
		return errors.New("encryption key encoding failed")
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	// Exclusive creation rejects existing files and symlinks. Cleanup checks the
	// exact created file identity; key material is never written to stdout.
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 G703 -- explicit operator output path, exclusive private creation
	if err != nil {
		return errors.New("encryption output must be a new writable file")
	}
	info, statErr := file.Stat()
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			removeOwnedKeyFile(name, info)
		}
	}()
	if statErr != nil || !info.Mode().IsRegular() {
		return errors.New("could not inspect new encryption key file")
	}
	if _, err = file.Write(append(raw, '\n')); err != nil {
		return errors.New("could not write encryption key file")
	}
	if err = file.Sync(); err != nil {
		return errors.New("could not synchronize encryption key file")
	}
	if err = file.Close(); err != nil {
		return errors.New("could not close encryption key file")
	}
	complete = true
	return nil
}

func encryptionBatch(args []string) (int, error) {
	if len(args) == 0 {
		return 50, nil
	}
	raw := ""
	if len(args) == 2 && args[0] == "--batch" {
		raw = args[1]
	} else if len(args) == 1 && strings.HasPrefix(args[0], "--batch=") {
		raw = strings.TrimPrefix(args[0], "--batch=")
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 || strconv.Itoa(value) != raw {
		return 0, errors.New("usage: household encryption-reencrypt [--batch 1..100]")
	}
	return value, nil
}

func encryptionRetirementID(args []string) (string, error) {
	if len(args) != 1 || len(args[0]) != 64 {
		return "", errors.New("usage: household encryption-retirement-check KEY_ID")
	}
	for _, c := range args[0] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", errors.New("key identifier must contain exactly 64 lowercase hexadecimal characters")
		}
	}
	return args[0], nil
}
