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
	"slices"
	"strings"

	"household/internal/webpush"
)

// generatedSecrets are random values every installation needs.
var generatedSecrets = []string{"postgres_password", "owner_password", "app_password", "csrf_secret", "encryption_key", "rate_secret", "bootstrap_password", "restic_password"}

// databaseSecrets are mounted into the test PostgreSQL container, whose postgres
// user differs from the host user. They are readable inside the private 0700
// folder, so other host users still cannot reach them; every other secret stays
// owner-only.
var databaseSecrets = []string{"postgres_password", "owner_password", "app_password"}

func secretMode(name string) os.FileMode {
	if slices.Contains(databaseSecrets, name) {
		return 0o644
	}
	return 0o600
}

// runInit prints a complete .env with fresh secrets to standard output, or
// with --files creates the .local/secrets folder that the isolated test
// database (compose.test.yaml) and local test tooling read. It runs before configuration and
// database access, and never prints secrets to a terminal.
func runInit(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	files := flags.Bool("files", false, "create .local/secrets instead of printing .env")
	development := flags.Bool("development", false, "local development settings")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("usage: household init [--development] > .env, or household init --files")
	}
	if !*files {
		if terminal(out) {
			return errors.New("refusing to print secrets to the terminal; run: household init > .env")
		}
		content, err := environmentFile(*development)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, content)
		return err
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
		if err = writeNewFile(filepath.Join(dir, name), value, secretMode(name)); err != nil {
			return err
		}
	}
	complete = true
	_, err := fmt.Fprintln(out, "Test secrets created in .local/secrets. Never commit or share these files.")
	return err
}

func randomSecret() (string, error) {
	raw := make([]byte, 36)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("random secret generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func writeNewFile(name, content string, mode os.FileMode) error {
	// security: fixed names below the folder this run created; O_EXCL never follows
	// or replaces an existing file.
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 G703 -- fixed init output path, exclusive creation
	if err != nil {
		return fmt.Errorf("could not create %s", filepath.Base(name))
	}
	// Set the mode explicitly: scripts/initialize.sh runs with umask 077.
	if mode != 0o600 {
		if err = file.Chmod(mode); err != nil { // #nosec G302 -- database secrets only, inside the private 0700 folder
			_ = file.Close()
			return fmt.Errorf("could not set permissions of %s", filepath.Base(name))
		}
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

// terminal reports whether out is an interactive terminal.
func terminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// environmentFile renders the single .env used by compose.yaml.
func environmentFile(development bool) (string, error) {
	values := map[string]string{}
	for _, name := range []string{"POSTGRES_PASSWORD", "HOUSEHOLD_OWNER_PASSWORD", "HOUSEHOLD_APP_PASSWORD", "HOUSEHOLD_CSRF_SECRET", "HOUSEHOLD_RATE_SECRET", "HOUSEHOLD_ENCRYPTION_KEY", "HOUSEHOLD_ADMIN_PASSWORD", "RESTIC_PASSWORD"} {
		value, err := randomSecret()
		if err != nil {
			return "", err
		}
		values[name] = value
	}
	private, _, err := webpush.GenerateKeys()
	if err != nil {
		return "", errors.New("push key generation failed")
	}
	values["HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY"] = private
	origin := "HOUSEHOLD_ORIGIN=https://household.example.org"
	if development {
		origin = "HOUSEHOLD_ENV=development\nHOUSEHOLD_ORIGIN=http://localhost:8100"
	}
	var b strings.Builder
	b.WriteString(`# Household-Einstellungen, erzeugt mit "household init".
# Diese Datei enthält Passwörter und den Verschlüsselungsschlüssel: sicher aufbewahren,
# zusammen mit den Backups sichern und niemals teilen. Ohne sie sind die Daten verloren.

# 1. Adresse, unter der Household im Browser erreichbar ist (HTTPS über einen Reverse Proxy).
#    Nur zum lokalen Ausprobieren: HOUSEHOLD_ENV=development und HOUSEHOLD_ORIGIN=http://localhost:8100
`)
	b.WriteString(origin + "\n")
	b.WriteString(`# Netzwerk des Reverse Proxys in CIDR-Schreibweise; leer vertraut keinem Proxy.
HOUSEHOLD_TRUSTED_PROXIES=
# Port auf diesem Rechner (nur 127.0.0.1) und Name des Haushalts.
HOUSEHOLD_PORT=8100
HOUSEHOLD_NAME=Mein Haushalt

# 2. Erste Anmeldung: Benutzer "admin" mit diesem Passwort, danach sofort ändern.
HOUSEHOLD_ADMIN_USERNAME=admin
`)
	b.WriteString("HOUSEHOLD_ADMIN_PASSWORD=" + values["HOUSEHOLD_ADMIN_PASSWORD"] + "\n")
	b.WriteString("\n# 3. Automatisch erzeugt. Nicht ändern, sonst sind Daten oder Backups nicht mehr lesbar.\n")
	for _, name := range []string{"POSTGRES_PASSWORD", "HOUSEHOLD_OWNER_PASSWORD", "HOUSEHOLD_APP_PASSWORD", "HOUSEHOLD_CSRF_SECRET", "HOUSEHOLD_RATE_SECRET", "HOUSEHOLD_ENCRYPTION_KEY", "HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY", "RESTIC_PASSWORD"} {
		b.WriteString(name + "=" + values[name] + "\n")
	}
	b.WriteString(`
# 4. Optional. Eine Funktion ist aktiv, sobald ihre Zeilen ausgefüllt und ohne "#" sind.
# Dokumente per E-Mail (IMAP über TLS):
# HOUSEHOLD_DOCUMENT_IMAP_ADDRESS=imap.example.org:993
# HOUSEHOLD_DOCUMENT_IMAP_USER=scanner@example.org
# HOUSEHOLD_DOCUMENT_IMAP_PASSWORD=
# HOUSEHOLD_DOCUMENT_IMAP_MAILBOX=INBOX
# Dokumente aus Paperless-ngx (Tag-Nummer, nur so markierte Dokumente):
# HOUSEHOLD_DOCUMENT_PAPERLESS_URL=https://paperless.example.org
# HOUSEHOLD_DOCUMENT_PAPERLESS_TOKEN=
# HOUSEHOLD_DOCUMENT_PAPERLESS_TAG=1
# Zählerstände aus Home Assistant:
# HOUSEHOLD_METERS_HA_URL=https://homeassistant.example.org
# HOUSEHOLD_METERS_HA_TOKEN=
# Zählerstände über MQTT (TLS):
# HOUSEHOLD_METERS_MQTT_URL=mqtts://mqtt.example.org:8883
# HOUSEHOLD_METERS_MQTT_USER=household
# HOUSEHOLD_METERS_MQTT_PASSWORD=
# Kontakt für Push-Dienste; leer verwendet die Adresse aus Abschnitt 1.
# HOUSEHOLD_WEB_PUSH_VAPID_CONTACT=mailto:ich@example.org
# Externe Sicherung (S3-kompatibel, restic-REST oder eingebundener Pfad), nur für den Sicherungsdienst.
# Eigenes, langes Passwort wählen und getrennt vom Server aufbewahren.
# HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY=s3:https://s3.eu-central-1.amazonaws.com/mein-bucket/household
# HOUSEHOLD_BACKUP_OFFSITE_PASSWORD=
# HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID=
# HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY=
# HOUSEHOLD_BACKUP_S3_REGION=eu-central-1
`)
	return b.String(), nil
}
