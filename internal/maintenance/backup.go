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
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/buildinfo"
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
	ArchiveFormat int            `json:"archive_format,omitempty"`
	Version       string         `json:"version"`
	Created       time.Time      `json:"created"`
	UploadPath    string         `json:"upload_path"`
	Schemas       map[string]int `json:"schemas"`
	Files         []File         `json:"files"`
	Crypto        *CryptoReport  `json:"crypto,omitempty"`
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
	if program != "pg_dump" && program != "pg_restore" {
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
	if err := cmd.Start(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A missing or unstartable tool says nothing about the dump.
		return nil, &ToolError{Program: program, Code: "tool_unavailable",
			err: fmt.Errorf("%s could not start (%v); inspect the backup service image", program, err)}
	}
	if err := cmd.Wait(); err != nil {
		// A canceled or expired run keeps its own result; the killed tool
		// reports nothing about the dump.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		failure := fmt.Errorf("%s failed (%v); inspect the operator tool configuration", program, err)
		// The diagnostics only select a stable code; they are never returned.
		if code := classifyDatabaseToolOutput(stderr.Bytes()); code != "" {
			return nil, &ToolError{Program: program, Code: code, err: failure}
		}
		return nil, failure
	}
	return out.Bytes(), nil
}

// classifyDatabaseToolOutput recognizes pg_dump and pg_restore failures of the
// database server or its storage, which no dump content causes: a full disk,
// another failure to extend a database file, and a server that refuses,
// drops or cannot serve the connection. Every other failure, including a
// corrupt or incompatible dump, returns "". A full disk needs an explicit
// space or quota error.
//
// Only the start of each diagnostic message is matched, plus the system error
// that ends a failed file operation. Server messages can quote dump data, as
// in invalid input syntax for type integer: "out of memory", and that data
// must not select a code.
func classifyDatabaseToolOutput(stderr []byte) string {
	found := map[string]bool{}
	for line := range strings.Lines(strings.ToLower(string(stderr))) {
		found[databaseDiagnostic(strings.TrimSpace(line))] = true
	}
	for _, code := range []string{"disk_full", "database_storage_failed", "database_unavailable"} {
		if found[code] {
			return code
		}
	}
	return ""
}

// databaseDiagnostic classifies one lowercased stderr line.
func databaseDiagnostic(line string) string {
	// The tool's own prefix, such as "pg_restore: error: ".
	if program, rest, found := strings.Cut(line, ": "); found && !strings.Contains(program, " ") {
		if level, message, found := strings.Cut(rest, ": "); found && (level == "error" || level == "fatal") {
			line = message
		}
	}
	line = strings.TrimPrefix(line, "could not execute query: ")
	if rest, found := strings.CutPrefix(line, `copy failed for table "`); found {
		message, ok := copyFailureMessage(rest)
		if !ok {
			return ""
		}
		line = message
	}
	if strings.HasPrefix(line, "could not connect to server") || strings.HasPrefix(line, "connection to server ") {
		return "database_unavailable"
	}
	for _, severity := range []string{"error:", "fatal:", "panic:"} {
		if rest, found := strings.CutPrefix(line, severity); found {
			line = strings.TrimSpace(rest)
			break
		}
	}
	starts := func(markers ...string) bool {
		return slices.ContainsFunc(markers, func(marker string) bool { return strings.HasPrefix(line, marker) })
	}
	switch {
	case starts("no space left on device", "disk quota exceeded"),
		strings.HasSuffix(line, ": no space left on device"), strings.HasSuffix(line, ": disk quota exceeded"):
		return "disk_full"
	case starts("could not extend file"):
		// Without a space error this is a storage failure, such as an I/O error.
		return "database_storage_failed"
	case starts("server closed the connection unexpectedly", "terminating connection", "the database system is",
		"sorry, too many clients", "too many clients", "remaining connection slots", "out of memory", "out of shared memory"):
		return "database_unavailable"
	}
	return ""
}

// copyFailureMessage returns the server message of pg_restore's wrapper
// `COPY failed for table "<tag>": <message>`. The tag comes from the dump, so
// the message is split off only at the single quote-colon before a server
// severity, or, for a client message without severity, at the only
// quote-colon of the line. Any other line reports false and stays
// unclassified, so a crafted tag cannot select a code.
func copyFailureMessage(rest string) (string, bool) {
	separators, message := 0, ""
	for _, severity := range []string{"error:", "fatal:", "panic:"} {
		marker := `": ` + severity
		separators += strings.Count(rest, marker)
		if i := strings.Index(rest, marker); i >= 0 {
			message = rest[i+len(`": `):]
		}
	}
	switch {
	case separators == 1:
		return message, true
	case separators == 0 && strings.Count(rest, `": `) == 1:
		_, message, _ = strings.Cut(rest, `": `)
		return message, true
	}
	return "", false
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
	manifest := Manifest{Version: buildinfo.Label(), Created: created.UTC(), UploadPath: uploadPath, Schemas: map[string]int{}, Files: []File{}}
	for _, schema := range database.Schemas {
		manifest.Schemas[schema] = database.ExpectedVersion(schema)
	}
	return manifest, nil
}

// Backup creates one local snapshot with the household's retention settings.
// It is the operator CLI `household backup`; scheduled and requested work uses
// RunBackup. The repository lock is taken before any configuration or tool
// access, so a canceled call never reaches restic.
func Backup(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) (string, error) {
	result, err := RunBackup(ctx, pool, cfg, BackupOptions{})
	return result.Snapshot, err
}

// BackupOptions selects the work of one backup run.
type BackupOptions struct {
	// Offsite copies the new snapshot to the offsite repository when true.
	Offsite bool
	// JobID links the runs to a requested job.
	JobID string
}

// BackupResult reports the produced snapshots. OffsiteErr is set when the
// local snapshot succeeded but its offsite copy did not.
type BackupResult struct {
	Snapshot        string
	OffsiteSnapshot string
	OffsiteErr      error
}

// RunOperatorBackup is RunBackup for the `household backup` command. It holds
// the restore lock for the whole backup, as the service's tick does, so an
// operator backup can never finish between a live restore's state copy and
// its database swap and leave its run in the replaced database. A busy lock
// (a tick or a restore) is waited for up to five minutes.
func RunOperatorBackup(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, options BackupOptions) (BackupResult, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		unlock, err := acquireRestoreLock()
		if err == nil {
			defer unlock()
			return RunBackup(ctx, pool, cfg, options)
		}
		if !errors.Is(err, errRestoreBusy) || time.Now().After(deadline) {
			return BackupResult{}, err
		}
		select {
		case <-ctx.Done():
			return BackupResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// attachmentInventoryQuery selects the files a backup carries. Every restore
// check, including an archive import, compares the restored rows with the
// manifest using the same query.
const attachmentInventoryQuery = `SELECT storage_key,encode(sha256,'hex'),size_bytes FROM core.attachments WHERE state='ready' UNION ALL SELECT d.storage_key,encode(d.sha256,'hex'),d.size_bytes FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.state='ready' ORDER BY storage_key`

// RunBackup creates a local snapshot and optionally copies it offsite.
func RunBackup(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, options BackupOptions) (BackupResult, error) {
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	if _, err := maintenanceURL(cfg.DatabaseURL); err != nil {
		return BackupResult{}, err
	}
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return BackupResult{}, err
	}
	defer unlock()
	local, err := config.LocalBackupTarget()
	if err != nil {
		return BackupResult{}, err
	}
	settings, _, err := loadSettings(ctx, pool)
	if err != nil {
		return BackupResult{}, err
	}
	// Every archive is either referenced by a run or removed here, including
	// one whose earlier registration or check failed. A file that cannot be
	// removed must not stop new backups: the failure is logged and the backup
	// still runs. If the destination refuses new files as well, writing the
	// new archive reports that.
	if err = reclaimArchives(ctx, pool, local, time.Now()); err != nil {
		slog.Default().Warn("could not reclaim unreferenced backup files", "code", ResultCode(err))
	}
	reportBackupPhase(ctx, pool, options.JobID, "saving_local")
	snapshot, schemas, err := backupLocked(ctx, pool, cfg, local, settings, options.JobID, true)
	result := BackupResult{Snapshot: snapshot}
	if !copyAfterLocalBackup(err) || !options.Offsite {
		return result, err
	}
	// A failed local retention leaves the new snapshot complete and checked,
	// and retention keeps its file, so the copy still runs. The retention
	// failure is returned beside the offsite outcome.
	offsite, offsiteErr := config.OffsiteBackupTarget(local)
	if offsiteErr == nil && offsite == nil {
		offsiteErr = ErrOffsiteUnconfigured
	}
	if offsiteErr == nil {
		reportBackupPhase(ctx, pool, options.JobID, "copying_offsite")
		result.OffsiteSnapshot, offsiteErr = copyOffsiteLocked(ctx, pool, local, *offsite, settings, snapshot, schemas, options.JobID)
	}
	result.OffsiteErr = offsiteErr
	return result, err
}

// localRetentionError marks a failed retention after the new local snapshot
// was written, checked and recorded complete.
type localRetentionError struct{ err error }

func (e *localRetentionError) Error() string { return e.err.Error() }
func (e *localRetentionError) Unwrap() error { return e.err }

// copyAfterLocalBackup reports whether the offsite copy can follow a local
// backup that returned err: only after success or a failed local retention.
func copyAfterLocalBackup(err error) bool {
	var retention *localRetentionError
	return err == nil || errors.As(err, &retention)
}

// ErrOffsiteUnconfigured reports offsite work without an offsite repository.
var ErrOffsiteUnconfigured = errors.New("no offsite repository is configured")

// backupLocked creates one local snapshot under the repository lock. retain
// applies the configured retention afterwards; a live restore's safety backup
// passes false, because a same-day prune could forget the very snapshot that
// is being restored.
func backupLocked(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, local config.BackupTarget, settings backupplan.Settings, jobID string, retain bool) (string, []byte, error) {
	manifest, err := newManifest(cfg.UploadDir, time.Now())
	if err != nil {
		return "", nil, err
	}
	portable := portableBackups()
	sourceUploads := manifest.UploadPath
	if portable {
		manifest.ArchiveFormat = 1
		manifest.UploadPath = "/uploads"
	} else {
		if err = ensureRepository(ctx, local, nil); err != nil {
			return "", nil, err
		}
	}
	base := workDirectory()
	if err = os.MkdirAll(base, 0700); err != nil { // #nosec G703 -- trusted operator backup-work directory
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, "snapshot-")
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }() // #nosec G703 -- exact private directory returned by MkdirTemp; never an HTTP path
	run := value.UUID()
	if _, err = audit.Exec(ctx, pool, "INSERT INTO core.backup_runs(id,state,application_version,repository_reference,repository_kind,job_id) VALUES($1,'running',$2,$3,'local',$4)",
		run, buildinfo.Label(), local.Label(), nullableUUID(jobID)); err != nil {
		return "", nil, err
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
		return "", nil, err
	}
	defer rollbackCrypto(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return "", nil, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL idle_in_transaction_session_timeout=0"); err != nil {
		return "", nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return "", nil, err
	}
	rows, err := tx.Query(ctx, attachmentInventoryQuery)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var f File
		if err = rows.Scan(&f.Key, &f.SHA256, &f.Size); err != nil {
			rows.Close()
			return "", nil, err
		}
		manifest.Files = append(manifest.Files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	ring, err := cfg.Keyring()
	if err != nil {
		return "", nil, err
	}
	inventory, err := verifyProtected(ctx, tx, ring)
	if err != nil {
		return "", nil, err
	}
	manifest.Crypto = &inventory
	var exported string
	if err = tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&exported); err != nil {
		return "", nil, err
	}
	if _, err = command(ctx, "pg_dump", []string{"--format=custom", "--file=" + filepath.Join(dir, "database.dump"), "--no-owner", "--snapshot=" + exported}, cfg.DatabaseURL); err != nil {
		return "", nil, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err = writePrivate(filepath.Join(dir, "manifest.json"), raw); err != nil {
		return "", nil, err
	}
	// Escrow is private and covered by backup encryption. Its keys verified the
	// same repeatable-read snapshot imported by pg_dump, including retained keys.
	keyring, err := encodeSecurityBundle(cfg, ring)
	if err != nil {
		return "", nil, err
	}
	if err = writePrivate(filepath.Join(dir, "security-keys.json"), keyring); err != nil {
		return "", nil, err
	}
	for i := range keyring {
		keyring[i] = 0
	}
	var summary backupSummary
	if portable {
		summary.SnapshotID = backupplan.ArchiveID(manifest.Created)
		summary.TotalBytes, err = writePortable(ctx, local, summary.SnapshotID, dir, sourceUploads, manifest.Files)
		summary.DataAdded = summary.TotalBytes
	} else {
		var output []byte
		output, err = restic(ctx, local, nil, "backup", "--json", "--host", "household", "--tag", "household", "--tag", "v"+buildinfo.Release(), dir, manifest.UploadPath)
		summary = parseBackupSummary(output)
	}
	if err != nil {
		return "", nil, err
	}
	snapshot := summary.SnapshotID
	if snapshot == "" {
		return "", nil, errors.New("backup tool returned no snapshot reference")
	}
	if err = tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	reportBackupPhase(ctx, pool, jobID, "checking_local")
	if portable {
		err = checkPortable(ctx, local, snapshot)
	} else {
		_, err = restic(ctx, local, nil, "check", "--read-data")
	}
	if err != nil {
		return "", nil, err
	}
	schemas, _ := json.Marshal(manifest.Schemas)
	if _, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state='complete',finished_at=now(),snapshot_reference=$2,schema_manifest=$3,data_added=$4,total_bytes=$5 WHERE id=$1",
		run, snapshot, schemas, max(summary.DataAdded, 0), max(summary.TotalBytes, 0)); err != nil {
		return "", nil, err
	}
	finished = true
	if !retain {
		return snapshot, schemas, nil
	}
	if portable {
		err = applyArchiveRetention(ctx, pool, local, snapshot)
	} else {
		err = applyRetention(ctx, pool, local, settings.KeepDaily, settings.KeepWeekly, settings.KeepMonthly)
	}
	if err != nil {
		return snapshot, schemas, &localRetentionError{err: err}
	}
	return snapshot, schemas, nil
}

// applyRetention invalidates eligibility before repository pruning, behind
// the same barrier as purge: a failed or interrupted retention run can never
// leave a deleted snapshot eligible. Remaining snapshots are then reconciled.
func applyRetention(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, daily, weekly, monthly int) error {
	retention, err := audit.Begin(ctx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = retention.Rollback(ctx) }()
	if _, err = retention.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return err
	}
	if _, err = retention.Exec(ctx, "UPDATE core.backup_runs SET state='retention_pending' WHERE repository_reference=$1 AND state='complete' AND snapshot_reference !~ $2", target.Label(), backupplan.ArchiveReferencePattern); err != nil {
		return err
	}
	if err = retention.Commit(ctx); err != nil {
		return err
	}
	if err = forget(ctx, target, daily, weekly, monthly); err != nil {
		// A failed prune may have removed some snapshots; reconciliation marks
		// exactly the surviving ones complete again, so they stay usable.
		return errors.Join(err, reconcileRuns(ctx, pool, target))
	}
	return reconcileRuns(ctx, pool, target)
}

// reconcileRuns marks runs complete exactly when their snapshot still exists.
func reconcileRuns(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget) error {
	snapshots, err := listSnapshots(ctx, target)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(snapshots))
	for _, item := range snapshots {
		ids = append(ids, item.ID)
	}
	_, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state=CASE WHEN snapshot_reference=ANY($2::text[]) THEN 'complete' ELSE 'expired' END WHERE repository_reference=$1 AND state='retention_pending' AND snapshot_reference !~ $3", target.Label(), ids, backupplan.ArchiveReferencePattern)
	return err
}

func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// lockRepository takes the repository lock on its own connection, outside
// the pool: waiters must not hold pool connections that the lock holder needs
// for its work. Closing that connection ends the session and releases the
// lock, also when a cancelled query acquired it on the server.
func lockRepository(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	wait, stopWaiting := context.WithTimeout(ctx, 5*time.Minute)
	defer stopWaiting()
	conn, err := pgx.ConnectConfig(wait, pool.Config().ConnConfig)
	if err != nil {
		return nil, err
	}
	closeConn := func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}
	// Wait independently of PostgreSQL's shorter statement_timeout. A trial
	// restore may legitimately overlap the scheduled check of another target.
	retry := time.NewTicker(time.Second)
	defer retry.Stop()
	for {
		var acquired bool
		if err = conn.QueryRow(wait, "SELECT pg_try_advisory_lock(721607137)").Scan(&acquired); err != nil {
			closeConn()
			return nil, err
		}
		if acquired {
			break
		}
		select {
		case <-wait.Done():
			closeConn()
			return nil, wait.Err()
		case <-retry.C:
		}
	}
	return closeConn, nil
}

// VerifyRestore restores one completed snapshot of target into a disposable
// database (household_restore or an automatic check's own database), verifies
// schema, protected records, stock projection and every attachment, and then
// marks the snapshot verified.
func VerifyRestore(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, target config.BackupTarget, snapshot, targetURL string) error {
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	if err := validAnyRestoreTarget(cfg.DatabaseURL, targetURL); err != nil {
		return err
	}
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	// "latest" is resolved under the same lock, so retention cannot remove
	// the chosen snapshot between choosing and checking it.
	if snapshot == "latest" {
		err = pool.QueryRow(ctx, `SELECT snapshot_reference FROM core.backup_runs WHERE repository_reference=$1 AND state='complete'
 AND snapshot_reference IS NOT NULL ORDER BY started_at DESC LIMIT 1`, target.Label()).Scan(&snapshot)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("no completed snapshot to check")
		}
		if err != nil {
			return fmt.Errorf("look up the latest snapshot: %w", err)
		}
	}
	return verifyRestoreLocked(ctx, pool, cfg, target, snapshot, targetURL)
}

func verifyRestoreLocked(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, target config.BackupTarget, snapshot, targetURL string) error {
	if err := knownSnapshot(ctx, pool, target, snapshot); err != nil {
		return err
	}
	restored, err := restoreVerified(ctx, cfg, target, snapshot, targetURL, false)
	if restored != nil {
		_ = restored.Remove() // a disposable check directory; nothing to recover
	}
	if err != nil {
		return err
	}
	_, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET verified_at=now() WHERE snapshot_reference=$1 AND repository_reference=$2 AND state='complete'", snapshot, target.Label())
	return err
}

func knownSnapshot(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, snapshot string) error {
	var known bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.backup_runs WHERE snapshot_reference=$1 AND repository_reference=$2 AND state='complete')", snapshot, target.Label()).Scan(&known); err != nil {
		return err
	}
	if !known {
		return errUnknownSnapshot
	}
	return nil
}

var errUnknownSnapshot = errors.New("snapshot is not a completed Household backup")

// ErrRestoreIncompatible refuses a snapshot whose schema differs from this
// release: it must be verified and restored with its own release.
var ErrRestoreIncompatible = errors.New("snapshot schema differs from this release")

// restoredSnapshot is a verified restore: the restored database at URL and
// the snapshot's attachments below Uploads, inside a private work directory.
type restoredSnapshot struct {
	Directory string
	Uploads   string
	Manifest  Manifest
}

// Remove deletes the private work directory.
// Remove deletes the restored snapshot directory. Callers that keep a
// journal keep it when this fails, so the next start retries the cleanup.
func (r *restoredSnapshot) Remove() error {
	return os.RemoveAll(r.Directory) // #nosec G703 -- exact private directory returned by MkdirTemp
}

// restoreVerified restores snapshot from target into the empty database at
// targetURL and performs every restore check. With requireCompatible, a
// snapshot of another schema release is refused before anything is restored.
// The caller removes the returned directory.
func restoreVerified(ctx context.Context, cfg config.Config, target config.BackupTarget, snapshot, targetURL string, requireCompatible bool) (*restoredSnapshot, error) {
	return restoreWithPayload(ctx, targetURL, requireCompatible, func(dir string) error {
		if backupplan.ArchiveName(snapshot) != "" {
			source, err := openPortable(ctx, target, snapshot)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			return extractArchive(ctx, source, target.ArchivePassword(), dir)
		}
		_, err := restic(ctx, target, nil, "restore", snapshot, "--target", dir)
		return err
	})
}

func restoreWithPayload(ctx context.Context, targetURL string, requireCompatible bool, extract func(string) error) (*restoredSnapshot, error) {
	dir, err := os.MkdirTemp(workDirectory(), "restore-check-")
	if err != nil {
		return nil, err
	}
	result := &restoredSnapshot{Directory: dir}
	if err = extract(dir); err != nil {
		return result, err
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
		return result, err
	}
	if manifestPath == "" || dumpPath == "" {
		return result, errors.New("restore manifest or database dump missing")
	}
	if filepath.Dir(manifestPath) != filepath.Dir(dumpPath) {
		return result, errors.New("restore manifest and dump are not from one bundle")
	}
	ring, err := readSecurityBundle(filepath.Join(filepath.Dir(manifestPath), "security-keys.json"))
	if err != nil {
		return result, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result.Manifest); err != nil {
		return result, err
	}
	manifest := result.Manifest
	if requireCompatible && !backupplan.SchemaCompatible(manifest.Schemas, currentSchemas()) {
		return result, ErrRestoreIncompatible
	}
	// Restore into the database this check validated; the connection itself comes from the URL.
	targetDB, err := maintenanceURL(targetURL)
	if err != nil {
		return result, err
	}
	if _, err = command(ctx, "pg_restore", []string{"--clean", "--if-exists", "--no-owner", "--exit-on-error", "--dbname=" + strings.TrimPrefix(targetDB.Path, "/"), dumpPath}, targetURL); err != nil {
		return result, err
	}
	restored, err := database.Open(ctx, targetURL, 2)
	if err != nil {
		return result, err
	}
	defer restored.Close()
	if manifest.Schemas["shopping"] == 8 {
		if err = normalizeRestoreBarcodeConstraint(ctx, restored); err != nil {
			return result, err
		}
	}
	if err = database.Ready(ctx, restored); err != nil {
		return result, err
	}
	protected, err := restored.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer rollbackCrypto(protected)
	inventory, err := verifyProtected(ctx, protected, ring)
	if err != nil {
		return result, err
	}
	if manifest.Crypto != nil {
		want, _ := json.Marshal(manifest.Crypto)
		got, _ := json.Marshal(inventory)
		if !bytes.Equal(want, got) {
			return result, errors.New("restored protected inventory differs from backup snapshot")
		}
	}
	if err = protected.Commit(ctx); err != nil {
		return result, err
	}
	if drift, checkErr := PantryProjection(ctx, restored, false); checkErr != nil {
		return result, checkErr
	} else if drift != 0 {
		return result, errors.New("restored stock projection differs from journal")
	}
	// Verify every metadata-referenced attachment against the restored bytes.
	uploadPath, err := restoreUploadPath(dir, manifest.UploadPath)
	if err != nil {
		return result, err
	}
	result.Uploads = uploadPath
	root, err := os.OpenRoot(uploadPath)
	if err != nil {
		if len(manifest.Files) == 0 && errors.Is(err, os.ErrNotExist) {
			// An installation without attachments may back up an empty folder
			// that restic restores as nothing at all.
			if err = os.MkdirAll(uploadPath, 0700); err != nil { // #nosec G703 -- below the private restore directory
				return result, err
			}
			root, err = os.OpenRoot(uploadPath)
		}
		if err != nil {
			return result, err
		}
	}
	defer func() { _ = root.Close() }()
	for _, f := range manifest.Files {
		if !value.ValidUUID(f.Key) {
			return result, errors.New("invalid backup storage key")
		}
		file, _, err := openRegular(root, f.Key)
		if err != nil {
			return result, err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return result, copyErr
		}
		if closeErr != nil {
			return result, closeErr
		}
		if size != f.Size || hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			return result, errors.New("restored attachment integrity mismatch")
		}
	}
	// The restored rows must name exactly the files the manifest lists, with
	// their sizes and hashes, before a restore point counts as verified.
	if err = attachmentsMatchManifest(ctx, restored, manifest.Files); err != nil {
		return result, err
	}
	return result, nil
}

// currentSchemas returns this binary's schema versions.
func currentSchemas() map[string]int {
	schemas := make(map[string]int, len(database.Schemas))
	for _, schema := range database.Schemas {
		schemas[schema] = database.ExpectedVersion(schema)
	}
	return schemas
}

// ValidateRestoreTarget accepts only household_restore, the name of explicit
// and derived targets. Automatic per-run names are refused here: a configured
// target with such a name could be dropped by another automatic check's cleanup
// of interrupted runs. It also rejects a source with a reserved name.
// restore-check runs it before it creates or drops anything.
func ValidateRestoreTarget(sourceURL, targetURL string) error {
	return validateRestoreTarget(sourceURL, targetURL, func(name string) bool { return name == "household_restore" })
}

// ValidateAutomaticRestoreTarget accepts only an automatic check's own
// per-run database name.
func ValidateAutomaticRestoreTarget(sourceURL, targetURL string) error {
	return validateRestoreTarget(sourceURL, targetURL, database.IsAutomaticRestoreDatabase)
}

// validAnyRestoreTarget is VerifyRestore's own guard: either kind of
// disposable target, chosen and checked by its caller.
func validAnyRestoreTarget(sourceURL, targetURL string) error {
	return validateRestoreTarget(sourceURL, targetURL, func(name string) bool {
		return name == "household_restore" || database.IsAutomaticRestoreDatabase(name)
	})
}

func validateRestoreTarget(sourceURL, targetURL string, allowed func(string) bool) error {
	target, err := maintenanceURL(targetURL)
	if err != nil {
		return err
	}
	if !allowed(strings.TrimPrefix(target.Path, "/")) {
		return errors.New("restore verification requires a separate database named household_restore")
	}
	source, err := maintenanceURL(sourceURL)
	if err != nil {
		return err
	}
	// The disposable names are reserved regardless of host spelling, aliases or
	// query parameters; they can never be the installation being verified. Other
	// names, such as household_restored, remain valid sources.
	if name := strings.TrimPrefix(source.Path, "/"); database.IsReservedRestoreDatabase(name) {
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
