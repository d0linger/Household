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
	"strings"
	"time"

	"household/internal/config"
	"household/internal/database"
	"household/internal/value"

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
}
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("operator command output exceeded limit")
	}
	return b.Buffer.Write(p)
}

func command(ctx context.Context, program string, args []string, dsn string) ([]byte, error) {
	if program != "pg_dump" && program != "pg_restore" && program != "restic" {
		return nil, errors.New("unsupported operator tool")
	}
	// security: executable is the compiled allowlist above; arguments never pass a shell.
	cmd := exec.CommandContext(ctx, program, args...) // #nosec G204 G702 -- fixed operator tool allowlist; no shell
	env := os.Environ()
	if dsn != "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, errors.New("invalid maintenance database URL")
		}
		password, _ := u.User.Password()
		env = append(env, "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(), "PGPASSWORD="+password, "PGDATABASE="+strings.TrimPrefix(u.Path, "/"), "PGSSLMODE="+u.Query().Get("sslmode"))
		if u.Query().Get("sslmode") == "" {
			env = append(env, "PGSSLMODE=require")
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
func Backup(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) (string, error) {
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
	if _, err = pool.Exec(ctx, "INSERT INTO core.backup_runs(id,state,application_version,repository_reference) VALUES($1,'running','0.1.0',$2)", run, os.Getenv("RESTIC_REPOSITORY")); err != nil {
		return "", err
	}
	finished := false
	defer func() {
		if !finished {
			failureCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = pool.Exec(failureCtx, "UPDATE core.backup_runs SET state='failed',finished_at=now(),failure_reason='operator backup failed' WHERE id=$1", run)
		}
	}()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout=0"); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return "", err
	}
	manifest := Manifest{Version: "0.1.0", Created: time.Now().UTC(), UploadPath: cfg.UploadDir, Schemas: map[string]int{}, Files: []File{}}
	for _, schema := range database.Schemas {
		manifest.Schemas[schema] = database.ExpectedVersion(schema)
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
	if _, err = command(ctx, "pg_dump", []string{"--format=custom", "--file=" + filepath.Join(dir, "database.dump"), "--no-owner"}, cfg.DatabaseURL); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(dir, "manifest.json"), raw); err != nil {
		return "", err
	}
	// The repository encrypts this key bundle; recovering TOTP requires the original key.
	keyring, err := json.Marshal(map[string]string{"csrf_secret": cfg.CSRFSecret, "encryption_key": cfg.EncryptionKey, "rate_secret": cfg.RateSecret})
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(dir, "security-keys.json"), keyring); err != nil {
		return "", err
	}
	output, err := command(ctx, "restic", []string{"backup", "--json", "--host", "household", "--tag", "household", "--tag", "v0.1.0", dir, cfg.UploadDir}, "")
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
	if _, err = pool.Exec(ctx, "UPDATE core.backup_runs SET state='complete',finished_at=now(),snapshot_reference=$2,schema_manifest=$3 WHERE id=$1", run, snapshot, schemas); err != nil {
		return "", err
	}
	finished = true
	// Invalidate eligibility before repository pruning, behind the same barrier as purge.
	// A failed or interrupted retention run therefore cannot leave a deleted snapshot eligible.
	retention, err := pool.Begin(ctx)
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
	if _, err = pool.Exec(ctx, "UPDATE core.backup_runs SET state=CASE WHEN snapshot_reference=ANY($2::text[]) THEN 'complete' ELSE 'expired' END WHERE repository_reference=$1 AND state='retention_pending'", os.Getenv("RESTIC_REPOSITORY"), ids); err != nil {
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
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	target, err := url.Parse(targetURL)
	if err != nil || target.Path != "/household_restore" {
		return errors.New("restore verification requires a separate database named household_restore")
	}
	source, err := url.Parse(cfg.DatabaseURL)
	if err != nil || source.Host == target.Host && source.Path == target.Path {
		return errors.New("restore target must differ from source")
	}
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
			manifestPath = path
		}
		if entry.Name() == "database.dump" {
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
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if _, err = command(ctx, "pg_restore", []string{"--clean", "--if-exists", "--no-owner", "--exit-on-error", "--dbname=" + strings.TrimPrefix(target.Path, "/"), dumpPath}, targetURL); err != nil {
		return err
	}
	restored, err := database.Open(ctx, targetURL, 2)
	if err != nil {
		return err
	}
	defer restored.Close()
	if err = database.Ready(ctx, restored); err != nil {
		return err
	}
	// Verify every metadata-referenced attachment against the restored bytes.
	uploadRelative := strings.TrimLeft(filepath.ToSlash(manifest.UploadPath), "/")
	if filepath.IsAbs(uploadRelative) || strings.Contains(uploadRelative, "..") {
		return errors.New("invalid upload path in backup manifest")
	}
	root, err := os.OpenRoot(filepath.Join(dir, filepath.FromSlash(uploadRelative)))
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
	_, err = pool.Exec(ctx, "UPDATE core.backup_runs SET verified_at=now() WHERE snapshot_reference=$1 AND state='complete'", snapshot)
	return err
}
