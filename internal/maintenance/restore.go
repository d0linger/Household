package maintenance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/config"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrLiveKeyMissing refuses a snapshot whose protected records the current
	// encryption key ring cannot read: sign-in factors would stop working.
	ErrLiveKeyMissing = errors.New("the current encryption keys cannot read this snapshot")
	// ErrInsufficientSpace refuses a restore that would fill the work volume.
	ErrInsufficientSpace = errors.New("not enough free space for the restore")
	// ErrDrainTimeout reports that the web process did not pause in time.
	ErrDrainTimeout     = errors.New("the app did not pause in time")
	errSuperuserMissing = &ToolError{Program: "postgres", Code: "superuser_missing", err: errors.New("a live restore needs POSTGRES_PASSWORD")}
	errRestoreBusy      = &ToolError{Program: "restore", Code: "restore_busy", err: errors.New("another live restore operation is running")}
)

// restoreJournal records a live restore outside the database being replaced,
// so an interrupted swap can be completed or undone after a restart.
type restoreJournal struct {
	Job       string    `json:"job"`
	Phase     string    `json:"phase"`
	Live      string    `json:"live"`
	Stage     string    `json:"stage"`
	Previous  string    `json:"previous"`
	Directory string    `json:"directory"`
	Moved     string    `json:"moved"`
	Snapshot  string    `json:"snapshot"`
	Target    string    `json:"target"`
	Requester string    `json:"requester,omitempty"`
	Started   time.Time `json:"started"`
}

// drainIdlePolls is how many one-second polls without any app session prove
// a stopped web process; it exceeds the presence session's reconnect time.
const drainIdlePolls = 15

func journalPath() string { return filepath.Join(workDirectory(), "restore-journal.json") }

func writeJournal(j restoreJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	temporary := journalPath() + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600) // #nosec G304 G703 -- fixed name in the operator work directory
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporary, journalPath()); err != nil { // #nosec G703 -- fixed names in the operator work directory
		return err
	}
	return syncDirectory(workDirectory())
}

func syncDirectory(path string) error {
	dir, err := os.Open(path) // #nosec G304 -- operator work directory
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil {
		// Windows cannot sync directories (developer tests only); the Linux
		// container can, so any failure there is real.
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	return nil
}

func readJournal() (*restoreJournal, error) {
	raw, err := os.ReadFile(journalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j restoreJournal
	if err = json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("unreadable restore journal: %w", err)
	}
	if !database.IsLiveRestoreStage(j.Stage) || j.Previous != "" && !database.IsLiveRestorePrevious(j.Previous) || j.Live == "" || database.IsReservedRestoreDatabase(j.Live) {
		return nil, errors.New("restore journal names unexpected databases")
	}
	return &j, nil
}

func removeJournal() { _ = os.Remove(journalPath()) }

// PendingRestore reports whether an interrupted live restore or rollback
// left its journal for recovery.
func PendingRestore() (bool, error) {
	j, err := readJournal()
	return j != nil, err
}

func databaseName(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" || strings.Contains(name, "/") {
		return "", errors.New("database URL has no database name")
	}
	return name, nil
}

func randomHex(bytes int) (string, error) {
	random := make([]byte, bytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

// setPhase records a public restore phase; state changes only when given.
func (s *Service) setPhase(ctx context.Context, id, state, phase string) error {
	pool := s.Pool()
	if pool == nil {
		return nil
	}
	_, err := pool.Exec(ctx, "UPDATE core.backup_jobs SET state=coalesce(nullif($2,''),state),phase=$3,heartbeat_at=now() WHERE id=$1", id, state, phase)
	return err
}

func superuserConn(ctx context.Context, maintenanceURL string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(maintenanceURL)
	if err != nil {
		return nil, errors.New("parse restore maintenance configuration")
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["application_name"] = "household-restore"
	return pgx.ConnectConfig(ctx, cfg)
}

func exists(ctx context.Context, conn *pgx.Conn, name string) (bool, error) {
	var found bool
	err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&found)
	return found, err
}

func allowConnections(ctx context.Context, conn *pgx.Conn, name string, allow bool) error {
	_, err := conn.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s WITH ALLOW_CONNECTIONS %t", pgx.Identifier{name}.Sanitize(), allow))
	return err
}

// renameDatabase renames from → to after ending every session on from. A
// session ended by pg_terminate_backend can linger briefly, so it retries.
func renameDatabase(ctx context.Context, conn *pgx.Conn, from, to string) error {
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		if _, err = conn.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()", from); err != nil {
			return err
		}
		if _, err = conn.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{from}.Sanitize()+" RENAME TO "+pgx.Identifier{to}.Sanitize()); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return err
}

func dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if !database.IsLiveRestoreStage(name) && !database.IsLiveRestorePrevious(name) {
		return errors.New("refusing to drop a database outside live restore maintenance")
	}
	_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// restore performs a requested live restore. Preparation never touches the
// live database; only after the stage passed every check does the web process
// pause and the databases swap. The replaced database stays available.
func (s *Service) restore(ctx context.Context, job Job) error {
	unlock, err := acquireRestoreLock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.restoreLocked(ctx, job)
}

// restoreLocked performs a live restore while the caller holds the restore lock.
func (s *Service) restoreLocked(ctx context.Context, job Job) (err error) {
	if s.Superuser == "" {
		return errSuperuserMissing
	}
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), job.ID)
	stop := s.keepAlive(ctx, job.ID)
	defer stop()
	liveName, err := databaseName(s.Config.DatabaseURL)
	if err != nil {
		return err
	}
	if database.IsReservedRestoreDatabase(liveName) {
		return errors.New("the live database has a reserved restore name")
	}
	serverURL, err := config.ServerMaintenanceURL(s.Config.DatabaseURL, s.Superuser)
	if err != nil {
		return err
	}
	target, err := s.target(job.Target)
	if err != nil {
		return err
	}
	suffix, err := randomHex(4)
	if err != nil {
		return err
	}
	journal := restoreJournal{Job: job.ID, Phase: "preparing", Live: liveName, Snapshot: job.Snapshot, Target: job.Target, Started: time.Now().UTC(),
		Stage:    "household_restore_job_" + strings.ReplaceAll(job.ID, "-", "")[:12],
		Previous: fmt.Sprintf("household_pre_restore_%d_%s", time.Now().Unix(), suffix)}
	if job.RequestedBy != nil {
		journal.Requester = *job.RequestedBy
	}
	if err = writeJournal(journal); err != nil {
		return err
	}
	conn, err := superuserConn(ctx, serverURL)
	if err != nil {
		removeJournal()
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	restored, err := s.prepareStage(ctx, conn, serverURL, job, target, &journal)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		_ = dropDatabase(cleanupCtx, conn, journal.Stage)
		cancel()
		if restored != nil {
			_ = restored.Remove() // before the switch; the work volume is reused
		}
		removeJournal()
		return err
	}
	journal.Phase, journal.Directory = "prepared", restored.Directory
	journal.Moved = filepath.Join(workDirectory(), "pre_restore_"+strings.TrimPrefix(journal.Previous, "household_pre_restore_"))
	if err = writeJournal(journal); err != nil {
		return s.abortBeforeSwap(ctx, conn, journal, restored, err)
	}
	if err = s.drain(ctx, job.ID, liveName); err != nil {
		return s.abortBeforeSwap(ctx, conn, journal, restored, err)
	}
	// Writers have stopped: copy the operational state again, so a backup job
	// cancelled or a feed revoked during preparation stays that way.
	if err = s.refreshOperationalState(ctx, journal.Stage, job.ID); err != nil {
		return s.abortBeforeSwap(ctx, conn, journal, restored, err)
	}
	if err = s.swap(ctx, conn, &journal); err != nil {
		return err // swap undid itself or left the journal for recovery
	}
	return s.completeSwap(ctx, conn, journal, &restored.Manifest)
}

// prepareStage takes a safety backup, restores the snapshot into the stage
// database, checks it with the archived and the live keys, and carries over
// operational state the snapshot cannot know.
func (s *Service) prepareStage(ctx context.Context, conn *pgx.Conn, serverURL string, job Job, target config.BackupTarget, journal *restoreJournal) (*restoredSnapshot, error) {
	pool := s.Pool()
	if err := s.setPhase(ctx, job.ID, "preparing", "safety_backup"); err != nil {
		return nil, err
	}
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var needed int64
	if err = pool.QueryRow(ctx, "SELECT coalesce(max(total_bytes),0) FROM core.backup_runs WHERE snapshot_reference=$1 OR repository_kind='local' AND state='complete'", job.Snapshot).Scan(&needed); err != nil {
		return nil, err
	}
	if free, known := freeBytes(workDirectory()); known && free < uint64(needed)*2+512<<20 { // #nosec G115 -- needed is non-negative
		return nil, ErrInsufficientSpace
	}
	local, err := config.LocalBackupTarget()
	if err != nil {
		return nil, err
	}
	settings, _, err := loadSettings(ctx, pool)
	if err != nil {
		return nil, err
	}
	if _, _, err = backupLocked(ctx, pool, s.Config, local, settings, job.ID, false); err != nil {
		return nil, err
	}
	if err = s.setPhase(ctx, job.ID, "", "restoring"); err != nil {
		return nil, err
	}
	if err = dropDatabase(ctx, conn, journal.Stage); err != nil {
		return nil, err
	}
	if _, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{journal.Stage}.Sanitize()); err != nil {
		return nil, err
	}
	owner, err := config.Secret("HOUSEHOLD_OWNER_PASSWORD")
	if err != nil {
		return nil, err
	}
	app, err := config.Secret("HOUSEHOLD_APP_PASSWORD")
	if err != nil || owner == "" || app == "" {
		return nil, errors.New("a live restore needs HOUSEHOLD_OWNER_PASSWORD and HOUSEHOLD_APP_PASSWORD")
	}
	stageSuperuser, err := config.WithDatabase(serverURL, journal.Stage)
	if err != nil {
		return nil, err
	}
	if err = database.PrepareRestoreDatabase(ctx, stageSuperuser, owner, app); err != nil {
		return nil, err
	}
	if err = sameDatabaseSettings(ctx, conn, journal.Live, journal.Stage); err != nil {
		return nil, err
	}
	stageURL, err := config.WithDatabase(s.Config.DatabaseURL, journal.Stage)
	if err != nil {
		return nil, err
	}
	restoreCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	restored, err := restoreVerified(restoreCtx, s.Config, target, job.Snapshot, stageURL, true)
	if err != nil {
		return restored, err
	}
	if err = s.setPhase(ctx, job.ID, "", "checking"); err != nil {
		return restored, err
	}
	stage, err := database.Open(ctx, stageURL, 2)
	if err != nil {
		return restored, err
	}
	defer stage.Close()
	if err = liveKeysReadStage(ctx, stage, s.Config); err != nil {
		return restored, err
	}
	if err = carryOperationalState(ctx, pool, stage); err != nil {
		return restored, err
	}
	if err = database.Ready(ctx, stage); err != nil {
		return restored, err
	}
	return restored, nil
}

// refreshOperationalState repeats carryOperationalState after the drain and
// before the first rename; a failure aborts the restore before the swap. The
// stage then shows its own restore as switching, so a web process that
// connects to it after the rename stays paused until the uploads match and
// completeSwap records the outcome.
func (s *Service) refreshOperationalState(ctx context.Context, stageName, job string) error {
	stageURL, err := config.WithDatabase(s.Config.DatabaseURL, stageName)
	if err != nil {
		return err
	}
	stage, err := database.Open(ctx, stageURL, 2)
	if err != nil {
		return err
	}
	defer stage.Close()
	if err = carryOperationalState(ctx, s.Pool(), stage); err != nil {
		return err
	}
	tag, err := stage.Exec(ctx, "UPDATE core.backup_jobs SET state='swapping',phase='switching',heartbeat_at=now() WHERE id=$1 AND kind='restore'", job)
	if err == nil && tag.RowsAffected() != 1 {
		err = errors.New("the restore job is missing from the restored database")
	}
	return err
}

// sameDatabaseSettings requires identical database-level settings, owner and
// privileges, so readiness behaves the same after the rename.
func sameDatabaseSettings(ctx context.Context, conn *pgx.Conn, live, stage string) error {
	var same bool
	err := conn.QueryRow(ctx, `SELECT l.datdba=s.datdba AND l.encoding=s.encoding AND l.datcollate=s.datcollate AND l.datctype=s.datctype
 AND coalesce((SELECT array_agg(a ORDER BY a) FROM unnest(l.datacl::text[]) a),'{}')=coalesce((SELECT array_agg(a ORDER BY a) FROM unnest(s.datacl::text[]) a),'{}')
 AND NOT EXISTS(SELECT 1 FROM pg_db_role_setting r WHERE r.setdatabase IN(l.oid,s.oid))
 FROM pg_database l,pg_database s WHERE l.datname=$1 AND s.datname=$2`, live, stage).Scan(&same)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("live or stage database missing")
	}
	if err != nil {
		return err
	}
	if !same {
		return &ToolError{Program: "postgres", Code: "database_settings_differ", err: errors.New("live and restored database settings differ")}
	}
	return nil
}

// liveKeysReadStage proves the running key ring can read every protected
// record of the restored data. The archived bundle verified the snapshot
// itself; the app will run with the live ring afterwards.
func liveKeysReadStage(ctx context.Context, stage *pgxpool.Pool, cfg config.Config) error {
	ring, err := cfg.Keyring()
	if err != nil {
		return err
	}
	tx, err := stage.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollbackCrypto(tx)
	if _, err = verifyProtected(ctx, tx, ring); err != nil {
		return fmt.Errorf("%w: %v", ErrLiveKeyMissing, err)
	}
	return tx.Commit(ctx)
}

// carryOperationalState makes the stage continue where the live installation
// stands: backup history, settings and work survive the restore; sessions and
// challenges end; calendar feeds and push connections that were revoked after
// the snapshot stay revoked.
func carryOperationalState(ctx context.Context, live, stage *pgxpool.Pool) error {
	var jobs, runs, settings string
	var feeds, mealFeeds [][]byte
	var pushes []string
	err := live.QueryRow(ctx, `SELECT coalesce((SELECT jsonb_agg(to_jsonb(j)) FROM core.backup_jobs j),'[]')::text,
 coalesce((SELECT jsonb_agg(to_jsonb(r)) FROM core.backup_runs r),'[]')::text,
 coalesce((SELECT jsonb_agg(to_jsonb(s)) FROM core.backup_settings s),'[]')::text,
 coalesce((SELECT array_agg(token_hash) FROM chores.calendar_feeds WHERE revoked_at IS NULL AND expires_at>now()),'{}'),
 coalesce((SELECT array_agg(token_hash) FROM mealplan.calendar_feeds WHERE revoked_at IS NULL AND expires_at>now()),'{}'),
 coalesce((SELECT array_agg(id::text) FROM core.push_subscriptions),'{}')`).Scan(&jobs, &runs, &settings, &feeds, &mealFeeds, &pushes)
	if err != nil {
		return err
	}
	tx, err := audit.Begin(ctx, stage)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	statements := []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM core.backup_runs", nil},
		{"DELETE FROM core.backup_jobs", nil},
		{"DELETE FROM core.backup_settings", nil},
		// Every requester and editor reference must exist in the restored users.
		{`INSERT INTO core.backup_jobs SELECT (jsonb_populate_record(NULL::core.backup_jobs,j
  ||jsonb_build_object('household_id',(SELECT id FROM core.households WHERE singleton=1))
  ||CASE WHEN EXISTS(SELECT 1 FROM core.users u WHERE u.id::text=j->>'requested_by') THEN '{}'::jsonb ELSE '{"requested_by":null}'::jsonb END)).*
  FROM jsonb_array_elements($1::jsonb) j`, []any{jobs}},
		{`INSERT INTO core.backup_runs SELECT (jsonb_populate_record(NULL::core.backup_runs,r)).* FROM jsonb_array_elements($1::jsonb) r`, []any{runs}},
		{`INSERT INTO core.backup_settings SELECT (jsonb_populate_record(NULL::core.backup_settings,s
  ||jsonb_build_object('household_id',(SELECT id FROM core.households WHERE singleton=1))
  ||CASE WHEN EXISTS(SELECT 1 FROM core.users u WHERE u.id::text=s->>'updated_by') THEN '{}'::jsonb ELSE '{"updated_by":null}'::jsonb END)).*
  FROM jsonb_array_elements($1::jsonb) s LIMIT 1`, []any{settings}},
		{"DELETE FROM core.sessions", nil},
		{"DELETE FROM core.auth_challenges", nil},
		{"UPDATE chores.calendar_feeds SET revoked_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE revoked_at IS NULL AND NOT token_hash=ANY($1::bytea[])", []any{feeds}},
		// Meal plan feeds accept revocation only from a signed-in issuer; the owner
		// suspends that guard inside this transaction to revoke stale feeds.
		{"ALTER TABLE mealplan.calendar_feeds DISABLE TRIGGER calendar_feed_change", nil},
		{"UPDATE mealplan.calendar_feeds SET revoked_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE revoked_at IS NULL AND NOT token_hash=ANY($1::bytea[])", []any{mealFeeds}},
		{"ALTER TABLE mealplan.calendar_feeds ENABLE TRIGGER calendar_feed_change", nil},
		{"DELETE FROM core.push_subscriptions WHERE NOT id::text=ANY($1::text[])", []any{pushes}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			return fmt.Errorf("carry operational state: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// drain asks the web process to pause and waits for its confirmation. A
// running web process always holds a presence session on the live database
// (Server.HoldPresence, reconnecting within seconds), so only a database
// without any app session for drainIdlePolls consecutive seconds counts as a
// stopped web process that needs no confirmation.
func (s *Service) drain(ctx context.Context, id, live string) error {
	if err := s.setPhase(ctx, id, "awaiting_drain", "pausing_app"); err != nil {
		return err
	}
	timeout := s.DrainTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	idle := 0
	for time.Now().Before(deadline) {
		var acknowledged bool
		var appSessions int
		err := s.Pool().QueryRow(ctx, `SELECT app_drained_at IS NOT NULL,(SELECT count(*) FROM pg_stat_activity WHERE datname=$2 AND usename='household_app')
 FROM core.backup_jobs WHERE id=$1`, id, live).Scan(&acknowledged, &appSessions)
		if err != nil {
			return err
		}
		if appSessions == 0 {
			idle++
		} else {
			idle = 0
		}
		if acknowledged || idle >= drainIdlePolls {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return ErrDrainTimeout
}

// abortBeforeSwap ends a restore whose live database was never touched.
func (s *Service) abortBeforeSwap(ctx context.Context, conn *pgx.Conn, j restoreJournal, restored *restoredSnapshot, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	_ = allowConnections(cleanupCtx, conn, j.Live, true)
	_ = dropDatabase(cleanupCtx, conn, j.Stage)
	if restored != nil {
		_ = restored.Remove() // before the switch; the work volume is reused
	}
	removeJournal()
	return cause
}

// swap replaces the live database with the stage. Each step is journaled
// first; a failure before the second rename puts the original back.
func (s *Service) swap(ctx context.Context, conn *pgx.Conn, j *restoreJournal) error {
	if err := s.setPhase(ctx, j.Job, "swapping", "switching"); err != nil {
		return err
	}
	s.Close()
	j.Phase = "swapping"
	if err := writeJournal(*j); err != nil {
		return s.undoSwap(ctx, conn, *j, err)
	}
	if err := allowConnections(ctx, conn, j.Live, false); err != nil {
		return s.undoSwap(ctx, conn, *j, err)
	}
	if err := renameDatabase(ctx, conn, j.Live, j.Previous); err != nil {
		return s.undoSwap(ctx, conn, *j, err)
	}
	if err := renameDatabase(ctx, conn, j.Stage, j.Live); err != nil {
		return s.undoSwap(ctx, conn, *j, err)
	}
	if err := allowConnections(ctx, conn, j.Live, true); err != nil {
		return err
	}
	j.Phase = "swapped"
	return writeJournal(*j)
}

// undoSwap restores the original live database name and connections, marks
// the job failed in it and removes the stage.
func (s *Service) undoSwap(ctx context.Context, conn *pgx.Conn, j restoreJournal, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	liveExists, err := exists(cleanupCtx, conn, j.Live)
	if err == nil && !liveExists {
		if previousExists, _ := exists(cleanupCtx, conn, j.Previous); previousExists {
			err = renameDatabase(cleanupCtx, conn, j.Previous, j.Live)
		}
	}
	if err == nil {
		err = allowConnections(cleanupCtx, conn, j.Live, true)
	}
	if err != nil {
		// Keep the journal: the next start completes the undo.
		return errors.Join(cause, err)
	}
	_ = dropDatabase(cleanupCtx, conn, j.Stage)
	s.removeRestored(j)
	if reopenErr := s.reopen(cleanupCtx); reopenErr == nil {
		s.finish(cleanupCtx, j.Job, cause, "")
	}
	return cause
}

// completeSwap reconciles the uploads with the restored metadata and records
// the outcome in the new live database. It is idempotent: recovery repeats
// it. The job row marked complete in the same transaction as the
// restore_completed event is the durable completion marker: once it exists,
// only cleanup remains, and uploads are never reconciled again (the resumed
// app may have accepted new files meanwhile). manifest is loaded from the
// restored directory when nil and the restore is not complete yet.
func (s *Service) completeSwap(ctx context.Context, conn *pgx.Conn, j restoreJournal, manifest *Manifest) error {
	if err := s.reopen(ctx); err != nil {
		return err
	}
	var committed bool
	if err := s.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_jobs WHERE id=$1 AND kind='restore' AND state='complete'
 AND previous_database IS NOT DISTINCT FROM nullif($2,''))`, j.Job, j.Previous).Scan(&committed); err != nil {
		return err
	}
	if committed {
		s.cleanupCompleted(j)
		return nil
	}
	if manifest == nil {
		loaded, err := restoredManifest(j.Directory)
		if err != nil {
			return err
		}
		manifest = &loaded
	}
	j.Phase = "uploads"
	if err := writeJournal(j); err != nil {
		return err
	}
	staged, err := restoreUploadPath(j.Directory, manifest.UploadPath)
	if err != nil {
		return err
	}
	if err = reconcileUploads(s.Config.UploadDir, staged, j.Moved, manifest.Files); err != nil {
		return err
	}
	j.Phase = "finishing"
	if err = writeJournal(j); err != nil {
		return err
	}
	pool := s.Pool()
	auditCtx := audit.WithRequest(audit.WithSystem(ctx, "operator"), j.Job)
	tx, err := audit.Begin(auditCtx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(auditCtx, `UPDATE core.backup_jobs SET state='complete',result_code='ok',phase=NULL,previous_database=$2,finished_at=now(),heartbeat_at=now()
 WHERE id=$1`, j.Job, j.Previous); err != nil {
		return err
	}
	if err = audit.Event(auditCtx, tx, "core", "backup_jobs", j.Job, "restore_completed", nil); err != nil {
		return err
	}
	if err = tx.Commit(auditCtx); err != nil {
		return err
	}
	// The outcome is recorded; nothing after this point may report the
	// restore as failed. A restart before the cleanup finds the marker.
	s.cleanupCompleted(j)
	s.Log.Info("live restore completed", "job", j.Job, "snapshot", j.Snapshot, "previous_database", j.Previous)
	s.alert(ctx, Job{ID: j.Job, Kind: "restore", Target: j.Target}, nil)
	return nil
}

// cleanupCompleted removes the restored snapshot directory and the journal of
// a restore whose outcome the live database records. Failures are logged:
// the restore itself succeeded, and a later start retries the cleanup.
func (s *Service) cleanupCompleted(j restoreJournal) {
	j.Phase = "committed"
	if err := writeJournal(j); err != nil {
		s.Log.Warn("restore journal could not be marked committed", "job", j.Job, "error", err)
	}
	s.removeRestored(j)
}

// removeRestored deletes the restored snapshot directory and then the
// journal. If the directory cannot be removed, the journal stays, so the
// next start finds the leftover and retries; the outcome is unaffected.
func (s *Service) removeRestored(j restoreJournal) {
	if j.Directory != "" {
		if err := (&restoredSnapshot{Directory: j.Directory}).Remove(); err != nil {
			s.Log.Warn("restored snapshot directory could not be removed; cleanup is retried on the next start", "job", j.Job, "error", err)
			return
		}
	}
	removeJournal()
}

// reconcileUploads makes the upload folder match the restored metadata:
// restored files are copied in, and every UUID-named file the restored
// metadata does not reference (or references with other content) is moved
// to moved, so a rollback can put it back.
func reconcileUploads(uploadDir, staged, moved string, files []File) error {
	if err := os.MkdirAll(moved, 0700); err != nil { // #nosec G703 -- operator work directory
		return err
	}
	live, err := os.OpenRoot(uploadDir)
	if err != nil {
		return err
	}
	defer func() { _ = live.Close() }()
	want := make(map[string]File, len(files))
	for _, f := range files {
		if !value.ValidUUID(f.Key) {
			return errors.New("invalid backup storage key")
		}
		want[f.Key] = f
	}
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !value.ValidUUID(name) {
			continue
		}
		if f, ok := want[name]; ok && fileMatches(live, name, f) {
			continue
		}
		if err = moveFile(filepath.Join(uploadDir, name), filepath.Join(moved, name)); err != nil {
			return err
		}
	}
	source, err := os.OpenRoot(staged)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	for _, f := range files {
		if fileMatches(live, f.Key, f) {
			continue
		}
		if err = copyInto(source, live, f.Key); err != nil {
			return err
		}
		if !fileMatches(live, f.Key, f) {
			return errors.New("restored attachment integrity mismatch")
		}
	}
	return nil
}

func fileMatches(root *os.Root, name string, f File) bool {
	file, _, err := openRegular(root, name)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return err == nil && size == f.Size && hex.EncodeToString(hash.Sum(nil)) == f.SHA256
}

func copyInto(source, target *os.Root, name string) error {
	in, _, err := openRegular(source, name)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	temporary := name + ".restore"
	out, err := target.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = target.Remove(temporary)
		return err
	}
	return target.Rename(temporary, name)
}

// moveFile renames within a volume and copies across volumes.
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil { // #nosec G703 -- validated UUID names below operator folders
		return nil
	}
	opened, err := os.OpenFile(from, os.O_RDONLY|nonblockingRead, 0) // #nosec G304 -- validated UUID name below the upload folder
	if err != nil {
		return err
	}
	in, _, err := regularFile(opened)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600) // #nosec G304 -- validated UUID name below the work folder
	if err != nil {
		_ = in.Close()
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	_ = in.Close()
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Remove(from) // #nosec G703 -- validated UUID name below the upload folder
}

// RecoverRestore completes or undoes a live restore interrupted by a crash
// or restart, using its journal. Without a journal it only fails restore
// work whose service stopped.
func (s *Service) RecoverRestore(ctx context.Context) error {
	unlock, err := acquireRestoreLock()
	if errors.Is(err, errRestoreBusy) {
		return nil // another process is restoring right now
	}
	if err != nil {
		return err
	}
	defer unlock()
	return s.recoverRestoreLocked(ctx)
}

// recoverRestoreLocked is RecoverRestore for a caller holding the restore lock.
func (s *Service) recoverRestoreLocked(ctx context.Context) error {
	j, err := readJournal()
	if err != nil {
		return err
	}
	if j == nil {
		if pool := s.Pool(); pool != nil {
			_, err = pool.Exec(ctx, `UPDATE core.backup_jobs SET state='failed',result_code='interrupted',phase=NULL,finished_at=now()
 WHERE kind='restore' AND state IN('running','preparing','awaiting_drain','swapping')`)
		}
		return err
	}
	if j.Phase == "committed" {
		// The restored database already records the outcome; only cleanup is left.
		s.removeRestored(*j)
		return nil
	}
	if s.Superuser == "" {
		return errSuperuserMissing
	}
	serverURL, err := config.ServerMaintenanceURL(s.Config.DatabaseURL, s.Superuser)
	if err != nil {
		return err
	}
	conn, err := superuserConn(ctx, serverURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if j.Phase == "rollback" {
		s.Log.Warn("completing interrupted rollback", "job", j.Job)
		return s.finishRollback(ctx, conn, *j)
	}
	liveExists, err := exists(ctx, conn, j.Live)
	if err != nil {
		return err
	}
	stageExists, err := exists(ctx, conn, j.Stage)
	if err != nil {
		return err
	}
	previousExists, err := exists(ctx, conn, j.Previous)
	if err != nil {
		return err
	}
	interrupted := &ToolError{Program: "restore", Code: "interrupted", err: errors.New("live restore interrupted")}
	switch {
	case liveExists && previousExists && !stageExists:
		// Both renames happened: finish the uploads and the record.
		if err = allowConnections(ctx, conn, j.Live, true); err != nil {
			return err
		}
		s.Log.Warn("completing interrupted live restore", "job", j.Job)
		return s.completeSwap(ctx, conn, *j, nil)
	case !liveExists && previousExists:
		s.Log.Warn("undoing interrupted live restore", "job", j.Job)
	default:
		// The live database was never renamed.
		s.Log.Warn("abandoning interrupted live restore before the switch", "job", j.Job)
	}
	// undoSwap reports the restore's own cause once the undo succeeded (and
	// records it on the job); recovery itself succeeded then.
	if err = s.undoSwap(ctx, conn, *j, interrupted); err != interrupted { //nolint:errorlint // identity marks a completed undo
		return err
	}
	return nil
}

// restoredManifest rereads the manifest of a restored snapshot directory.
func restoredManifest(directory string) (Manifest, error) {
	var manifest Manifest
	found := ""
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error { // #nosec G703 -- private restore directory from the journal
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected symlink in restore snapshot")
		}
		if entry.Name() == "manifest.json" {
			if found != "" {
				return errors.New("multiple restore manifests")
			}
			found = path
		}
		return nil
	})
	if err != nil {
		return manifest, err
	}
	if found == "" {
		return manifest, errors.New("restore manifest missing")
	}
	raw, err := os.ReadFile(found) // #nosec G304 -- found inside the private restore directory
	if err != nil {
		return manifest, err
	}
	return manifest, json.Unmarshal(raw, &manifest)
}

// discardPreviousLocked drops every database and upload folder kept by earlier
// live restores, after an administrator accepted the restored state.
// The caller (Tick) holds the restore lock.
func (s *Service) discardPreviousLocked(ctx context.Context) error {
	if s.Superuser == "" {
		return errSuperuserMissing
	}
	serverURL, err := config.ServerMaintenanceURL(s.Config.DatabaseURL, s.Superuser)
	if err != nil {
		return err
	}
	conn, err := superuserConn(ctx, serverURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	// A previous attempt may have dropped a database but failed to clear its
	// reference or remove its upload folder; reconcile both first.
	if err = s.reconcileKeptState(ctx, conn); err != nil {
		return err
	}
	// Stage databases are kept only by a rollback; no restore runs while
	// this holds the restore lock, so every remaining stage is discardable.
	// Bounded batches; references are cleared only for databases actually
	// dropped, so a failure never hides a database that still exists.
	for round := 0; round < 20; round++ {
		rows, err := conn.Query(ctx, `SELECT datname FROM pg_database WHERE starts_with(datname,'household_pre_restore_') OR starts_with(datname,'household_restore_job_')
 ORDER BY datname LIMIT 50`)
		if err != nil {
			return err
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		dropped := make([]string, 0, len(names))
		for _, name := range names {
			if !database.IsLiveRestoreStage(name) && !database.IsLiveRestorePrevious(name) {
				continue
			}
			if err = dropDatabase(ctx, conn, name); err != nil {
				return errors.Join(err, s.clearPrevious(ctx, dropped))
			}
			dropped = append(dropped, name)
		}
		if err = s.clearPrevious(ctx, dropped); err != nil {
			return err
		}
		if len(dropped) < len(names) || len(names) < 50 {
			if len(dropped) < len(names) {
				return errors.New("some kept restore databases have unexpected names and were left in place")
			}
			// Folders are found by name on disk, so one whose removal failed
			// is retried by the next discard although its database is gone.
			return s.reconcileKeptState(ctx, conn)
		}
	}
	return errors.New("kept restore databases remain after the discard limit; repeat the request")
}

// reconcileKeptState clears kept-state references to databases that no
// longer exist and removes moved-upload folders whose database is gone.
// Folder removal failures are reported; the folder stays discoverable.
func (s *Service) reconcileKeptState(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE starts_with(datname,'household_pre_restore_')")
	if err != nil {
		return err
	}
	existing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE core.backup_jobs SET previous_database=NULL WHERE previous_database IS NOT NULL AND NOT previous_database=ANY($1::text[])", existing); err != nil {
		return err
	}
	kept := make(map[string]bool, len(existing))
	for _, name := range existing {
		kept[name] = true
	}
	entries, err := os.ReadDir(workDirectory())
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), "pre_restore_")
		name := "household_pre_restore_" + suffix
		if !ok || !entry.IsDir() || !database.IsLiveRestorePrevious(name) || kept[name] {
			continue
		}
		if err = os.RemoveAll(filepath.Join(workDirectory(), entry.Name())); err != nil { // #nosec G703 -- validated reserved name below the work directory
			failures = append(failures, fmt.Errorf("remove kept uploads of %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) clearPrevious(ctx context.Context, dropped []string) error {
	if len(dropped) == 0 {
		return nil
	}
	_, err := s.Pool().Exec(ctx, "UPDATE core.backup_jobs SET previous_database=NULL WHERE previous_database=ANY($1::text[])", dropped)
	return err
}

// Rollback switches back to the database a live restore replaced. The web
// process must be stopped; restored uploads moved aside are put back.
func (s *Service) Rollback(ctx context.Context) error {
	if s.Superuser == "" {
		return errSuperuserMissing
	}
	unlock, err := acquireRestoreLock()
	if err != nil {
		return err
	}
	defer unlock()
	if pending, readErr := readJournal(); readErr != nil || pending != nil {
		if readErr != nil {
			return readErr
		}
		if pending.Phase != "rollback" {
			return errors.New("an interrupted live restore is pending; restart the backup service to complete it first")
		}
		serverURL, urlErr := config.ServerMaintenanceURL(s.Config.DatabaseURL, s.Superuser)
		if urlErr != nil {
			return urlErr
		}
		conn, connErr := superuserConn(ctx, serverURL)
		if connErr != nil {
			return connErr
		}
		defer func() { _ = conn.Close(context.Background()) }()
		return s.finishRollback(ctx, conn, *pending)
	}
	if s.Pool() == nil {
		return errors.New("the live database is not reachable")
	}
	var job, previous string
	err = s.Pool().QueryRow(ctx, `SELECT id,previous_database FROM core.backup_jobs WHERE kind='restore' AND state='complete'
 AND previous_database IS NOT NULL ORDER BY finished_at DESC LIMIT 1`).Scan(&job, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no restore with a kept previous database")
	}
	if err != nil {
		return err
	}
	live, err := databaseName(s.Config.DatabaseURL)
	if err != nil {
		return err
	}
	serverURL, err := config.ServerMaintenanceURL(s.Config.DatabaseURL, s.Superuser)
	if err != nil {
		return err
	}
	conn, err := superuserConn(ctx, serverURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var appSessions int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND usename='household_app'", live).Scan(&appSessions); err != nil {
		return err
	}
	if appSessions > 0 {
		return errors.New("stop the app before a rollback: docker compose stop app")
	}
	if found, err := exists(ctx, conn, previous); err != nil || !found {
		return errors.Join(errors.New("the previous database no longer exists"), err)
	}
	away := "household_restore_job_" + strings.ReplaceAll(job, "-", "")[:12]
	if found, _ := exists(ctx, conn, away); found {
		return errors.New("a staging database of this restore still exists")
	}
	// Journal every name before the first rename, so an interruption at any
	// point is completed by the next start (RecoverRestore → finishRollback).
	j := restoreJournal{Job: job, Phase: "rollback", Live: live, Stage: away, Previous: previous, Started: time.Now().UTC(),
		Moved: filepath.Join(workDirectory(), "pre_restore_"+strings.TrimPrefix(previous, "household_pre_restore_"))}
	if err = writeJournal(j); err != nil {
		return err
	}
	s.Close()
	return s.finishRollback(ctx, conn, j)
}

// finishRollback drives a journaled rollback to its end from whatever state
// an interruption left: restored database moved aside, previous database
// back under the live name, uploads moved back, restore job ended. Every step
// is idempotent; the journal stays until all of them succeeded.
func (s *Service) finishRollback(ctx context.Context, conn *pgx.Conn, j restoreJournal) error {
	state := func() (live, away, previous bool, err error) {
		if live, err = exists(ctx, conn, j.Live); err != nil {
			return
		}
		if away, err = exists(ctx, conn, j.Stage); err != nil {
			return
		}
		previous, err = exists(ctx, conn, j.Previous)
		return
	}
	live, away, previous, err := state()
	if err != nil {
		return err
	}
	if live && previous && !away {
		if err = renameDatabase(ctx, conn, j.Live, j.Stage); err != nil {
			return err
		}
		live, away = false, true
	}
	if !live && away && previous {
		if err = renameDatabase(ctx, conn, j.Previous, j.Live); err != nil {
			return err // the journal stays; the next start retries this step
		}
		live, previous = true, false
	}
	if !live || !away || previous {
		return errors.New("rollback found an unexpected set of databases; inspect them before retrying")
	}
	if err = allowConnections(ctx, conn, j.Live, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(j.Moved)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// The restore moved no uploads aside.
	case err != nil:
		return err
	default:
		for _, entry := range entries {
			if value.ValidUUID(entry.Name()) && entry.Type().IsRegular() {
				if err = moveFile(filepath.Join(j.Moved, entry.Name()), filepath.Join(s.Config.UploadDir, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	if err = s.reopen(ctx); err != nil {
		return err
	}
	// The previous database still shows the restore as switching: end it, so
	// the app does not wait for it, and keep the restored data discardable.
	if _, err = s.Pool().Exec(ctx, `UPDATE core.backup_jobs SET state='failed',result_code='rolled_back',phase=NULL,finished_at=now()
 WHERE kind='restore' AND state IN('running','preparing','awaiting_drain','swapping')`); err != nil {
		return err
	}
	removeJournal()
	s.Log.Info("live restore rolled back", "job", j.Job, "restored_database", j.Stage)
	return nil
}
