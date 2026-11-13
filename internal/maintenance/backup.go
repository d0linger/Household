// Package maintenance provides operator-only backup, restore verification, and purge.
package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/config"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type File struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version    string         `json:"version"`
	Created    time.Time      `json:"created"`
	UploadPath string         `json:"upload_path"`
	Schemas    map[string]int `json:"schemas"`
	Files      []File         `json:"files"`
	Crypto     *CryptoReport  `json:"crypto,omitempty"`
}
type limitedBuffer struct{ buffer bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > 4<<20-b.buffer.Len() {
		return 0, errors.New("operator command output exceeded limit")
	}
	return b.buffer.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

var databaseEnvironmentKeys = map[string]string{
	"sslmode":         "PGSSLMODE",
	"sslrootcert":     "PGSSLROOTCERT",
	"sslcert":         "PGSSLCERT",
	"sslkey":          "PGSSLKEY",
	"connect_timeout": "PGCONNECT_TIMEOUT",
}

// Maintenance tools and pgx must address the same database. Reject connection
// overrides instead of silently dropping URI parameters interpreted by pgx.
func maintenanceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("maintenance requires an explicit postgres URI with user, host and database")
	}
	authority, _, _ := strings.Cut(raw[strings.Index(raw, "://")+3:], "/")
	if strings.ContainsAny(raw, " \t\r\n") || strings.Contains(u.RawQuery, "+") || strings.Contains(u.Hostname(), ",") || strings.Count(authority, "@") != 1 {
		return nil, errors.New("maintenance database URI requires one explicit host and percent-encoded values")
	}
	if u.Path == "" || u.Path == "/" || strings.Contains(strings.TrimPrefix(u.Path, "/"), "/") || strings.ContainsAny(u.Hostname()+u.Path+u.User.Username(), "\x00") {
		return nil, errors.New("invalid maintenance database URI")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("maintenance database URI requires an explicit valid port")
	}
	parameters, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid maintenance database parameters")
	}
	if u.RawQuery != "" {
		for _, parameter := range strings.Split(u.RawQuery, "&") {
			if strings.Count(parameter, "=") != 1 {
				return nil, errors.New("invalid maintenance database parameter encoding")
			}
		}
	}
	for key, values := range parameters {
		if _, allowed := databaseEnvironmentKeys[key]; !allowed || len(values) != 1 || values[0] == "" || strings.ContainsRune(values[0], 0) {
			return nil, errors.New("unsupported or repeated maintenance database parameter")
		}
	}
	if mode := parameters.Get("sslmode"); mode != "" {
		switch mode {
		case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return nil, errors.New("invalid maintenance database TLS mode")
		}
	}
	if timeout := parameters.Get("connect_timeout"); timeout != "" {
		if _, err := strconv.ParseUint(timeout, 10, 32); err != nil {
			return nil, errors.New("invalid maintenance connection timeout")
		}
	}
	return u, nil
}

func databaseEnvironment(existing []string, raw string) ([]string, error) {
	u, err := maintenanceURL(raw)
	if err != nil {
		return nil, err
	}
	env := make([]string, 0, len(existing)+10)
	for _, entry := range existing {
		// Remove inherited connection defaults, including PGHOSTADDR/PGSERVICE,
		// so they cannot redirect the explicitly validated connection.
		if len(entry) < 2 || !strings.EqualFold(entry[:2], "PG") {
			env = append(env, entry)
		}
	}
	password, _ := u.User.Password()
	env = append(env, "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(), "PGPASSWORD="+password, "PGDATABASE="+strings.TrimPrefix(u.Path, "/"))
	parameters := u.Query()
	if parameters.Get("sslmode") == "" {
		env = append(env, "PGSSLMODE=require")
	}
	for key, variable := range databaseEnvironmentKeys {
		if value := parameters.Get(key); value != "" {
			env = append(env, variable+"="+value)
		}
	}
	return env, nil
}

func command(ctx context.Context, program string, args []string, dsn string) ([]byte, error) {
	if program != "pg_dump" && program != "pg_restore" && program != "restic" {
		return nil, errors.New("unsupported operator tool")
	}
	// security: executable is the compiled allowlist above; arguments never pass a shell.
	cmd := exec.CommandContext(ctx, program, args...) // #nosec G204 G702 -- fixed operator tool allowlist; no shell
	env := os.Environ()
	if dsn != "" {
		var err error
		env, err = databaseEnvironment(env, dsn)
		if err != nil {
			return nil, err
		}
	}
	cmd.Env = env
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed (%v); inspect the operator tool configuration", program, err)
	}
	return out.Bytes(), nil
}

// security: all callers pass files inside an operator-created private temporary directory.
func writePrivate(path string, data []byte) error { return os.WriteFile(path, data, 0600) } // #nosec G703 -- trusted maintenance path

func newManifest(uploadDirectory string, created time.Time) (Manifest, error) {
	if uploadDirectory == "" {
		return Manifest{}, errors.New("backup upload directory is required")
	}
	uploadPath, err := filepath.Abs(uploadDirectory)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{Version: "0.1.0", Created: created.UTC(), UploadPath: uploadPath, Schemas: map[string]int{}, Files: []File{}}
	for _, schema := range database.Schemas {
		manifest.Schemas[schema] = database.ExpectedVersion(schema)
	}
	return manifest, nil
}

func Backup(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) (string, error) {
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	if _, err := maintenanceURL(cfg.DatabaseURL); err != nil {
		return "", err
	}
	manifest, err := newManifest(cfg.UploadDir, time.Now())
	if err != nil {
		return "", err
	}
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return "", err
	}
	defer unlock()
	if os.Getenv("RESTIC_REPOSITORY") == "" || os.Getenv("RESTIC_PASSWORD_FILE") == "" {
		return "", errors.New("encrypted restic repository and password file required")
	}
	if _, err := command(ctx, "restic", []string{"snapshots", "--json"}, ""); err != nil {
		if _, err = command(ctx, "restic", []string{"init"}, ""); err != nil {
			return "", err
		}
	}
	base := os.Getenv("HOUSEHOLD_BACKUP_WORK")
	if base == "" {
		base = "/work"
	}
	if err := os.MkdirAll(base, 0700); err != nil { // #nosec G703 -- trusted operator backup-work directory
		return "", err
	}
	dir, err := os.MkdirTemp(base, "snapshot-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }() // #nosec G703 -- exact private directory returned by MkdirTemp; never an HTTP path
	run := value.UUID()
	if _, err = audit.Exec(ctx, pool, "INSERT INTO core.backup_runs(id,state,application_version,repository_reference) VALUES($1,'running','0.1.0',$2)", run, os.Getenv("RESTIC_REPOSITORY")); err != nil {
		return "", err
	}
	finished := false
	defer func() {
		if !finished {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = audit.Exec(failureCtx, pool, "UPDATE core.backup_runs SET state='failed',finished_at=now(),failure_reason='operator backup failed' WHERE id=$1", run)
		}
	}()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return "", err
	}
	defer rollbackCrypto(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout=0"); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, "SELECT storage_key,encode(sha256,'hex'),size_bytes FROM core.attachments WHERE state='ready' UNION ALL SELECT d.storage_key,encode(d.sha256,'hex'),d.size_bytes FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.state='ready' ORDER BY storage_key")
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var f File
		if err = rows.Scan(&f.Key, &f.SHA256, &f.Size); err != nil {
			rows.Close()
			return "", err
		}
		manifest.Files = append(manifest.Files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	ring, err := cfg.Keyring()
	if err != nil {
		return "", err
	}
	inventory, err := verifyProtected(ctx, tx, ring)
	if err != nil {
		return "", err
	}
	manifest.Crypto = &inventory
	var exported string
	if err = tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&exported); err != nil {
		return "", err
	}
	if _, err = command(ctx, "pg_dump", []string{"--format=custom", "--file=" + filepath.Join(dir, "database.dump"), "--no-owner", "--snapshot=" + exported}, cfg.DatabaseURL); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(dir, "manifest.json"), raw); err != nil {
		return "", err
	}
	// Escrow is private and covered by restic encryption. Its keys verified the
	// same repeatable-read snapshot imported by pg_dump, including retained keys.
	keyring, err := encodeSecurityBundle(cfg, ring)
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(dir, "security-keys.json"), keyring); err != nil {
		return "", err
	}
	for i := range keyring {
		keyring[i] = 0
	}
	output, err := command(ctx, "restic", []string{"backup", "--json", "--host", "household", "--tag", "household", "--tag", "v0.1.0", dir, manifest.UploadPath}, "")
	if err != nil {
		return "", err
	}
	snapshot := ""
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if json.Unmarshal(line, &event) == nil && event.SnapshotID != "" {
			snapshot = event.SnapshotID
		}
	}
	if snapshot == "" {
		return "", errors.New("backup tool returned no snapshot reference")
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	if _, err = command(ctx, "restic", []string{"check", "--read-data"}, ""); err != nil {
		return "", err
	}
	schemas, _ := json.Marshal(manifest.Schemas)
	if _, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state='complete',finished_at=now(),snapshot_reference=$2,schema_manifest=$3 WHERE id=$1", run, snapshot, schemas); err != nil {
		return "", err
	}
	finished = true
	// Invalidate eligibility before repository pruning, behind the same barrier as purge.
	// A failed or interrupted retention run therefore cannot leave a deleted snapshot eligible.
	retention, err := audit.Begin(ctx, pool)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = retention.Rollback(ctx) }()
	if _, err = retention.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return snapshot, err
	}
	if _, err = retention.Exec(ctx, "UPDATE core.backup_runs SET state='retention_pending' WHERE repository_reference=$1 AND state='complete'", os.Getenv("RESTIC_REPOSITORY")); err != nil {
		return snapshot, err
	}
	if err = retention.Commit(ctx); err != nil {
		return snapshot, err
	}
	// Empty grouping deliberately ignores random temporary paths, container hosts and version tags.
	if _, err = command(ctx, "restic", []string{"forget", "--tag", "household", "--group-by", "", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--prune"}, ""); err != nil {
		return snapshot, err
	}
	remaining, err := command(ctx, "restic", []string{"snapshots", "--tag", "household", "--json"}, "")
	if err != nil {
		return snapshot, err
	}
	var snapshots []struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(remaining, &snapshots); err != nil {
		return snapshot, err
	}
	ids := []string{}
	for _, item := range snapshots {
		ids = append(ids, item.ID)
	}
	if _, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state=CASE WHEN snapshot_reference=ANY($2::text[]) THEN 'complete' ELSE 'expired' END WHERE repository_reference=$1 AND state='retention_pending'", os.Getenv("RESTIC_REPOSITORY"), ids); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func lockRepository(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(721607137)"); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, unlockErr := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock(721607137)"); unlockErr != nil {
			_ = conn.Hijack().Close(unlockCtx)
			return
		}
		conn.Release()
	}, nil
}

func VerifyRestore(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, snapshot, targetURL string) error {
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	if err := validateRestoreTarget(cfg.DatabaseURL, targetURL); err != nil {
		return err
	}
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	var known bool
	if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.backup_runs WHERE snapshot_reference=$1 AND state='complete')", snapshot).Scan(&known); err != nil {
		return err
	}
	if !known {
		return errors.New("snapshot is not a completed Household backup")
	}
	base := os.Getenv("HOUSEHOLD_BACKUP_WORK")
	if base == "" {
		base = "/work"
	}
	dir, err := os.MkdirTemp(base, "restore-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }() // #nosec G703 -- exact private directory returned by MkdirTemp; never an HTTP path
	if _, err = command(ctx, "restic", []string{"restore", snapshot, "--target", dir}, ""); err != nil {
		return err
	}
	manifestPath, dumpPath := "", ""
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error { // #nosec G703 -- root is the newly created private restore directory; symlinks rejected
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected symlink in restore snapshot")
		}
		if entry.Name() == "manifest.json" {
			if manifestPath != "" {
				return errors.New("multiple restore manifests")
			}
			manifestPath = path
		}
		if entry.Name() == "database.dump" {
			if dumpPath != "" {
				return errors.New("multiple restore database dumps")
			}
			dumpPath = path
		}
		return nil
	})
	if err != nil {
		return err
	}
	if manifestPath == "" || dumpPath == "" {
		return errors.New("restore manifest or database dump missing")
	}
	if filepath.Dir(manifestPath) != filepath.Dir(dumpPath) {
		return errors.New("restore manifest and dump are not from one bundle")
	}
	ring, err := readSecurityBundle(filepath.Join(filepath.Dir(manifestPath), "security-keys.json"))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if _, err = command(ctx, "pg_restore", []string{"--clean", "--if-exists", "--no-owner", "--exit-on-error", "--dbname=household_restore", dumpPath}, targetURL); err != nil {
		return err
	}
	restored, err := database.Open(ctx, targetURL, 2)
	if err != nil {
		return err
	}
	defer restored.Close()
	if manifest.Schemas["shopping"] == 8 {
		if err = normalizeRestoreBarcodeConstraint(ctx, restored); err != nil {
			return err
		}
	}
	if err = database.Ready(ctx, restored); err != nil {
		return err
	}
	protected, err := restored.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollbackCrypto(protected)
	inventory, err := verifyProtected(ctx, protected, ring)
	if err != nil {
		return err
	}
	if manifest.Crypto != nil {
		want, _ := json.Marshal(manifest.Crypto)
		got, _ := json.Marshal(inventory)
		if !bytes.Equal(want, got) {
			return errors.New("restored protected inventory differs from backup snapshot")
		}
	}
	if err = protected.Commit(ctx); err != nil {
		return err
	}
	if drift, checkErr := PantryProjection(ctx, restored, false); checkErr != nil {
		return checkErr
	} else if drift != 0 {
		return errors.New("restored stock projection differs from journal")
	}
	// Verify every metadata-referenced attachment against the restored bytes.
	uploadPath, err := restoreUploadPath(dir, manifest.UploadPath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(uploadPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, f := range manifest.Files {
		if !value.ValidUUID(f.Key) {
			return errors.New("invalid backup storage key")
		}
		file, err := root.Open(f.Key)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if size != f.Size || hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			return errors.New("restored attachment integrity mismatch")
		}
	}
	var count int
	if err = restored.QueryRow(ctx, "SELECT (SELECT count(*) FROM core.attachments WHERE state='ready')+(SELECT count(*) FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.state='ready')").Scan(&count); err != nil {
		return err
	}
	if count != len(manifest.Files) {
		return errors.New("restored attachment metadata count mismatch")
	}
	_, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET verified_at=now() WHERE snapshot_reference=$1 AND state='complete'", snapshot)
	return err
}

func validateRestoreTarget(sourceURL, targetURL string) error {
	target, err := maintenanceURL(targetURL)
	if err != nil {
		return err
	}
	if target.Path != "/household_restore" {
		return errors.New("restore verification requires a separate database named household_restore")
	}
	source, err := maintenanceURL(sourceURL)
	if err != nil {
		return err
	}
	// The disposable name is reserved regardless of host spelling, aliases or
	// query parameters; it can never be the installation being verified.
	if source.Path == "/household_restore" {
		return errors.New("restore source cannot use the reserved household_restore database")
	}
	return nil
}

func restoreUploadPath(directory, uploadPath string) (string, error) {
	// Restic restores absolute POSIX paths below its target directory. Reject
	// ambiguous Windows spellings and traversal before opening that local root.
	relative := strings.TrimLeft(uploadPath, "/")
	if relative == "" || strings.ContainsAny(relative, "\\:\x00") || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return "", errors.New("invalid upload path in backup manifest")
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == ".." {
			return "", errors.New("invalid upload path in backup manifest")
		}
	}
	return filepath.Join(directory, filepath.FromSlash(relative)), nil
}
