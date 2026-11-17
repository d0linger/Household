package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"household/internal/webpush"
)

// generatedSecrets are random values every installation needs.
var generatedSecrets = []string{"postgres_password", "owner_password", "app_password", "csrf_secret", "encryption_key", "rate_secret", "bootstrap_password", "restic_password"}

// placeholderSecrets stay empty until the operator enables that integration;
// an empty file counts as not configured, but Compose can already mount it.
var placeholderSecrets = []string{"document_imap_password", "document_paperless_token", "meters_ha_token", "meters_mqtt_password", "meters_ca.pem"}

const developmentEnvironment = "HOUSEHOLD_ENV=development\nHOUSEHOLD_ORIGIN=http://localhost:8100\nHOUSEHOLD_PORT=8100\nHOUSEHOLD_NAME=Mein Haushalt\n"

// runInit creates every secret file and a starting .env in the current folder.
// It runs before configuration and database access and never prints secrets.
func runInit(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	development := flags.Bool("development", false, "write a local development .env")
	template := flags.String("env-template", ".env.example", "settings template copied to .env")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("usage: household init [--development] [--env-template FILE]")
	}
	dir := filepath.Join(".local", "secrets")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return errors.New("could not create .local")
	}
	// Exclusive creation: existing secrets are never replaced.
	if err := os.Mkdir(dir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("secrets already exist; preserve them and use the recovery runbook")
		}
		return errors.New("could not create .local/secrets")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir) // #nosec G703 -- removes only the folder this run created exclusively
		}
	}()
	for _, name := range generatedSecrets {
		value, err := randomSecret()
		if err != nil {
			return err
		}
		if err = writeNewFile(filepath.Join(dir, name), value); err != nil {
			return err
		}
	}
	private, public, err := webpush.GenerateKeys()
	if err != nil {
		return errors.New("push key generation failed")
	}
	if err = writeNewFile(filepath.Join(dir, "web_push_private_key"), private+"\n"); err != nil {
		return err
	}
	if err = writeNewFile(filepath.Join(dir, "web_push_public_key"), public+"\n"); err != nil {
		return err
	}
	for _, name := range placeholderSecrets {
		if err = writeNewFile(filepath.Join(dir, name), ""); err != nil {
			return err
		}
	}
	if err = writeEnvironment(*development, *template); err != nil {
		return err
	}
	complete = true
	_, err = fmt.Fprintln(out, "Secrets created in .local/secrets. Sign in with .local/secrets/bootstrap_password and change it immediately. Never commit or share these files.")
	return err
}

func randomSecret() (string, error) {
	raw := make([]byte, 36)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("random secret generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func writeNewFile(name, content string) error {
	// security: fixed names below the folder this run created; O_EXCL never follows
	// or replaces an existing file.
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 G703 -- fixed init output path, exclusive creation
	if err != nil {
		return fmt.Errorf("could not create %s", filepath.Base(name))
	}
	if _, err = io.WriteString(file, content); err != nil {
		_ = file.Close()
		return fmt.Errorf("could not write %s", filepath.Base(name))
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("could not write %s", filepath.Base(name))
	}
	return file.Close()
}

// writeEnvironment keeps an existing .env and otherwise starts one.
func writeEnvironment(development bool, template string) error {
	if _, err := os.Lstat(".env"); err == nil {
		return nil
	}
	content := developmentEnvironment
	if !development {
		// security: the template path is an explicit local CLI argument.
		raw, err := os.ReadFile(template) // #nosec G304 G703 -- operator-selected local template
		if err != nil {
			return fmt.Errorf("could not read %s; copy a settings template to .env yourself", template)
		}
		content = string(raw)
	}
	file, err := os.OpenFile(".env", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("could not create .env")
	}
	if _, err = io.WriteString(file, content); err != nil {
		_ = file.Close()
		return errors.New("could not write .env")
	}
	return file.Close()
}
