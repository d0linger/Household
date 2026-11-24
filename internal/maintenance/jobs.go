package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"household/internal/alerts"
	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/buildinfo"
	"household/internal/config"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is the owner backup service loop (`household backup-loop`). It is
// the only process with repository credentials: it performs scheduled work and
// the backup work administrators request in the app, after validating each
// request again with its own privileges.
type Service struct {
	Config    config.Config
	Log       *slog.Logger
	Superuser string
	// DrainTimeout bounds how long a live restore waits for the web process.
	DrainTimeout time.Duration
	// Alerts reports failures and restores to an optional external channel.
	Alerts *alerts.Sender
	// Recovered reports that NewService completed an interrupted restore or
	// rollback before it could open the live database.
	Recovered bool

	mu   sync.Mutex
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService opens the owner pool of the backup service.
func NewService(ctx context.Context, cfg config.Config, log *slog.Logger, superuser string) (*Service, error) {
	channels, err := alerts.Load(config.Secret)
	if err != nil {
		return nil, err
	}
	service := &Service{Config: cfg, Log: log, Superuser: superuser, DrainTimeout: 2 * time.Minute, now: time.Now}
	if channels.Enabled() {
		service.Alerts = alerts.NewSender(channels)
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL, 4)
	if err != nil {
		// An interrupted swap can leave no database under the live name. The
		// journal recovery needs only the superuser connection; it puts the
		// live name back first, then the pool opens.
		pending, journalErr := readJournal()
		if journalErr != nil || pending == nil {
			return nil, errors.Join(err, journalErr)
		}
		if recoverErr := service.RecoverRestore(ctx); recoverErr != nil {
			return nil, errors.Join(err, recoverErr)
		}
		service.Recovered = true
		if service.Pool() == nil {
			if err = service.reopen(ctx); err != nil {
				return nil, err
			}
		}
		return service, nil
	}
	service.pool = pool
	return service, nil
}

// Pool returns the current owner pool; a live restore replaces it.
func (s *Service) Pool() *pgxpool.Pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pool
}

// Close releases the owner pool.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
}

func (s *Service) reopen(ctx context.Context) error {
	pool, err := database.Open(ctx, s.Config.DatabaseURL, 4)
	if err != nil {
		return err
	}
	s.mu.Lock()
	old := s.pool
	s.pool = pool
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// tickBudget bounds one tick's work: a backup with its offsite copy, a
// restore check or a live restore. It is far above normal durations and
// exists so a stalled restic or PostgreSQL call cannot hold the locks forever.
const tickBudget = 6 * time.Hour

// Run completes an interrupted live restore, then ticks every 30 seconds
// until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if err := s.RecoverRestore(ctx); err != nil {
		s.Log.Error("interrupted restore recovery failed", "code", ResultCode(err))
	}
	go s.reportLiveness(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Tick records the service heartbeat and performs at most one unit of work:
// a requested job first, otherwise scheduled work.
func (s *Service) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	// Another process (an operator's `household restore`) may be restoring:
	// then this loop leaves the database alone until it finished. The lock is
	// held for the whole tick, through claiming and executing work, so an
	// operator restore can never start between this check and the work.
	unlock, err := acquireRestoreLock()
	if err != nil {
		if !errors.Is(err, errRestoreBusy) {
			s.Log.Error("restore lock unavailable", "error", err)
		}
		return
	}
	defer unlock()
	// Bound the whole unit of work, so a stalled tool eventually releases the
	// repository and restore locks; large backups get generous time.
	ctx, cancelWork := context.WithTimeout(ctx, tickBudget)
	defer cancelWork()
	// A live restore that failed mid-switch leaves its journal: finish or undo it first.
	if journal, err := readJournal(); err != nil || journal != nil || s.Pool() == nil {
		if err == nil && journal != nil {
			err = s.recoverRestoreLocked(ctx)
		}
		if err == nil && s.Pool() == nil {
			err = s.reopen(ctx)
		}
		if err != nil {
			s.Log.Error("restore recovery pending", "code", ResultCode(err))
			return
		}
	}
	tickCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = s.heartbeat(tickCtx)
	if err == nil {
		err = s.failStale(tickCtx)
	}
	cancel()
	if err != nil {
		s.Log.Error("backup service heartbeat failed", "error", err)
		return
	}
	job, err := s.claim(ctx)
	if err != nil {
		s.Log.Error("backup request claim failed", "error", err)
		return
	}
	if job != nil {
		s.perform(ctx, *job)
		return
	}
	if err = s.scheduled(ctx); err != nil && ctx.Err() == nil {
		s.Log.Error("scheduled backup work failed", "code", ResultCode(err))
	}
}

// reportLiveness refreshes the service heartbeat every minute while the
// service runs, independent of Tick, which a long backup or restore check
// can block for hours. It stops with ctx, so service_seen_at then expires.
// While a live restore has closed the pool there is nothing to report to.
func (s *Service) reportLiveness(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.Pool() == nil {
			continue
		}
		beat, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := s.heartbeat(beat); err != nil && ctx.Err() == nil {
			s.Log.Warn("backup service heartbeat failed", "error", err)
		}
		cancel()
	}
}

// repositoryLabels returns the nonsecret labels of the configured local and
// offsite repositories; an unconfigured or invalid one is empty.
func repositoryLabels() (string, string) {
	local, err := config.LocalBackupTarget()
	if err != nil {
		return "", ""
	}
	offsiteLabel := ""
	if offsite, offsiteErr := config.OffsiteBackupTarget(local); offsiteErr == nil && offsite != nil {
		offsiteLabel = offsite.Label()
	}
	return local.Label(), offsiteLabel
}

// heartbeat lets the app show that the service runs and which repositories
// it has, without ever giving the app their credentials.
func (s *Service) heartbeat(ctx context.Context) error {
	localLabel, offsiteLabel := repositoryLabels()
	// A live restore can close the pool at any moment; read it once.
	pool := s.Pool()
	if pool == nil {
		return nil
	}
	_, err := pool.Exec(ctx, `INSERT INTO core.backup_settings(household_id,service_seen_at,service_version,local_label,offsite_label)
 SELECT id,now(),$1,nullif($2,''),nullif($3,'') FROM core.households WHERE singleton=1
 ON CONFLICT(household_id) DO UPDATE SET service_seen_at=excluded.service_seen_at,service_version=excluded.service_version,
  local_label=excluded.local_label,offsite_label=excluded.offsite_label`, buildinfo.Label(), localLabel, offsiteLabel)
	return err
}

// failStale ends claimed work whose service stopped heartbeating. Tick calls
// it holding the restore lock and after any journal was recovered, so a
// restore job in an active state without a recent heartbeat is orphaned too
// (for example a crash before its journal was written).
func (s *Service) failStale(ctx context.Context) error {
	_, err := s.Pool().Exec(ctx, `UPDATE core.backup_jobs SET state='failed',result_code='interrupted',phase=NULL,finished_at=now()
 WHERE state IN('running','preparing','awaiting_drain','swapping') AND coalesce(heartbeat_at,started_at,created_at)<now()-interval '5 minutes'`)
	if err != nil {
		return err
	}
	_, err = s.Pool().Exec(ctx, `UPDATE core.backup_jobs SET state='failed',result_code='expired',finished_at=now()
 WHERE state='requested' AND expires_at<now()`)
	if err != nil {
		return err
	}
	_, err = s.Pool().Exec(ctx, "DELETE FROM core.backup_jobs WHERE finished_at<now()-interval '180 days'")
	return err
}

// Job is one claimed unit of backup work.
type Job struct {
	ID               string
	Kind             string
	Target           string
	Snapshot         string
	RequestedBy      *string
	RequestedSession *string
	Confirmation     *string
	CreatedAt        time.Time
}

// claim takes the oldest request, validates it with owner privileges and
// marks it running, or refuses it with a reason code.
func (s *Service) claim(ctx context.Context) (*Job, error) {
	pool := s.Pool()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var job Job
	var snapshot *string
	err = tx.QueryRow(ctx, `SELECT id,kind,target,snapshot_reference,requested_by,requested_session,confirmation,created_at FROM core.backup_jobs
 WHERE state='requested' AND (expires_at IS NULL OR expires_at>=now()) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&job.ID, &job.Kind, &job.Target, &snapshot, &job.RequestedBy, &job.RequestedSession, &job.Confirmation, &job.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if snapshot != nil {
		job.Snapshot = *snapshot
	}
	refusal, err := validateRequest(ctx, tx, job)
	if err != nil {
		return nil, err
	}
	if refusal != "" {
		if _, err = tx.Exec(ctx, "UPDATE core.backup_jobs SET state='failed',result_code=$2,finished_at=now() WHERE id=$1", job.ID, refusal); err != nil {
			return nil, err
		}
		s.Log.Warn("backup request refused", "job", job.ID, "kind", job.Kind, "code", refusal)
		return nil, tx.Commit(ctx)
	}
	state := "running"
	if job.Kind == "restore" {
		state = "preparing"
	}
	// The repository labels tie the attempt to the repositories it used, so a
	// replaced repository's history never counts for the current one.
	localLabel, offsiteLabel := repositoryLabels()
	if _, err = tx.Exec(ctx, "UPDATE core.backup_jobs SET state=$2,phase='started',started_at=now(),heartbeat_at=now(),local_label=nullif($3,''),offsite_label=nullif($4,'') WHERE id=$1",
		job.ID, state, localLabel, offsiteLabel); err != nil {
		return nil, err
	}
	return &job, tx.Commit(ctx)
}

// validateRequest repeats the app's checks with owner privileges, because the
// web role is the less trusted side: the requester must still be an active
// administrator, the request fresh, and a restore confirmed for exactly this
// snapshot from a recently re-authenticated session.
func validateRequest(ctx context.Context, tx pgx.Tx, job Job) (string, error) {
	if job.RequestedBy == nil {
		return "requester_missing", nil
	}
	var admin, fresh bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
  WHERE m.user_id=$1 AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password),
 EXISTS(SELECT 1 FROM core.sessions s WHERE s.id=$2 AND s.user_id=$1 AND s.revoked_at IS NULL AND s.absolute_expires_at>now()
  AND s.reauthenticated_at>=$3::timestamptz-interval '10 minutes')`, *job.RequestedBy, job.RequestedSession, job.CreatedAt).Scan(&admin, &fresh)
	if err != nil {
		return "", err
	}
	switch {
	case !admin:
		return "requester_not_admin", nil
	case job.Kind == "verify" || job.Kind == "restore":
		if !backupplan.ValidSnapshot(job.Snapshot) {
			return "snapshot_invalid", nil
		}
	}
	// Restoring and discarding the rollback copy both end a recovery path, so
	// both need a recently re-authenticated session; only a restore also needs
	// the typed snapshot confirmation.
	if (job.Kind == "restore" || job.Kind == "discard_previous") && !fresh {
		return "reauthentication_required", nil
	}
	if job.Kind == "restore" && (job.Confirmation == nil || !backupplan.ConfirmationMatches(job.Snapshot, *job.Confirmation)) {
		return "confirmation_mismatch", nil
	}
	return "", nil
}

// keepAlive refreshes a claimed job's heartbeat until stop is called.
func (s *Service) keepAlive(ctx context.Context, id string) func() {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				beat, cancel := context.WithTimeout(ctx, 5*time.Second)
				if pool := s.Pool(); pool != nil {
					_, _ = pool.Exec(beat, "UPDATE core.backup_jobs SET heartbeat_at=now() WHERE id=$1 AND finished_at IS NULL", id)
				}
				cancel()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func (s *Service) finish(ctx context.Context, id string, err error, snapshot string) {
	state, code := "complete", "ok"
	if err != nil {
		state, code = "failed", ResultCode(err)
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	pool := s.Pool()
	if pool == nil {
		// A live restore closed the pool and left its journal; recovery records it.
		s.Log.Error("backup job result deferred to restore recovery", "job", id, "code", code)
		return
	}
	if _, updateErr := pool.Exec(finishCtx, "UPDATE core.backup_jobs SET state=$2,result_code=$3,result_snapshot=nullif($4,''),phase=NULL,finished_at=now(),heartbeat_at=now() WHERE id=$1",
		id, state, code, snapshot); updateErr != nil {
		s.Log.Error("backup job result could not be recorded", "job", id, "error", updateErr)
	}
}

// reportBackupPhase updates progress independently of the backup outcome.
func reportBackupPhase(ctx context.Context, pool *pgxpool.Pool, jobID, phase string) {
	if jobID == "" || pool == nil {
		return
	}
	reportContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := pool.Exec(reportContext, "UPDATE core.backup_jobs SET phase=$2 WHERE id=$1 AND finished_at IS NULL AND kind<>'restore'", jobID, phase); err != nil {
		slog.Default().Warn("could not update backup phase", "job", jobID, "phase", phase)
	}
}

// perform runs one claimed job and records its outcome.
func (s *Service) perform(ctx context.Context, job Job) {
	stop := s.keepAlive(ctx, job.ID)
	defer stop()
	var err error
	snapshot := ""
	switch job.Kind {
	case "backup":
		var result BackupResult
		result, err = RunBackup(ctx, s.Pool(), s.Config, BackupOptions{Offsite: job.Target == "both", JobID: job.ID})
		snapshot = result.Snapshot
		if err == nil && result.OffsiteErr != nil {
			err = &ToolError{Program: "restic", Code: "offsite_" + ResultCode(result.OffsiteErr), err: result.OffsiteErr}
		}
	case "verify":
		reportBackupPhase(ctx, s.Pool(), job.ID, "restoring")
		err = s.verify(ctx, job.Target, job.Snapshot)
	case "offsite_test":
		reportBackupPhase(ctx, s.Pool(), job.ID, "connecting_offsite")
		err = TestOffsite(ctx, s.Pool())
	case "restore":
		stop()
		// Claimed jobs run inside Tick, which holds the restore lock.
		err = s.restoreLocked(ctx, job)
		if err == nil {
			return // the restore recorded its own outcome in the new database
		}
	case "discard_previous":
		err = s.discardPreviousLocked(ctx)
	default:
		err = errors.New("unsupported backup work")
	}
	if err != nil {
		s.Log.Error("backup work failed", "job", job.ID, "kind", job.Kind, "code", ResultCode(err))
		s.alert(ctx, job, err)
	} else {
		s.Log.Info("backup work completed", "job", job.ID, "kind", job.Kind, "snapshot", snapshot)
	}
	s.finish(ctx, job.ID, err, snapshot)
}

func (s *Service) target(kind string) (config.BackupTarget, error) {
	local, err := config.LocalBackupTarget()
	if err != nil || kind == "local" {
		return local, err
	}
	offsite, err := config.OffsiteBackupTarget(local)
	if err != nil {
		return config.BackupTarget{}, err
	}
	if offsite == nil {
		return config.BackupTarget{}, ErrOffsiteUnconfigured
	}
	return *offsite, nil
}

// verify restores one snapshot into a disposable database and marks it verified.
func (s *Service) verify(ctx context.Context, kind, snapshot string) error {
	target, err := s.target(kind)
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return CheckRestore(verifyCtx, s.Config.DatabaseURL, s.Superuser, func(targetURL string) error {
		return VerifyRestore(verifyCtx, s.Pool(), s.Config, target, snapshot, targetURL)
	})
}

// RestoreNow performs an operator's live restore from the command line. The
// operator is trusted like the owner credentials they hold; the typed
// confirmation was checked by the caller.
func (s *Service) RestoreNow(ctx context.Context, id, target, snapshot, confirmation string) error {
	_, err := s.Pool().Exec(ctx, `INSERT INTO core.backup_jobs(id,household_id,kind,target,snapshot_reference,confirmation,state,phase,started_at,heartbeat_at)
 SELECT $1,id,'restore',$2,$3,$4,'preparing','started',now(),now() FROM core.households WHERE singleton=1`, id, target, snapshot, confirmation)
	if err != nil {
		return err
	}
	err = s.restore(ctx, Job{ID: id, Kind: "restore", Target: target, Snapshot: snapshot, Confirmation: &confirmation, CreatedAt: time.Now()})
	if err != nil {
		s.finish(ctx, id, err, "")
	}
	return err
}

// startScheduled records scheduled work as a job, so administrators see one
// history of requested and automatic work. offsiteOnly marks a copy of an
// existing local snapshot: the job records no local repository, so it never
// counts as a local backup attempt (backup jobs name only local or both).
func (s *Service) startScheduled(ctx context.Context, kind, target, snapshot string, offsiteOnly bool) (string, error) {
	id := value.UUID()
	localLabel, offsiteLabel := repositoryLabels()
	if offsiteOnly {
		localLabel = ""
	}
	_, err := s.Pool().Exec(ctx, `INSERT INTO core.backup_jobs(id,household_id,kind,target,snapshot_reference,state,phase,started_at,heartbeat_at,local_label,offsite_label)
 SELECT $1,id,$2,$3,nullif($4,''),'running','scheduled',now(),now(),nullif($5,''),nullif($6,'') FROM core.households WHERE singleton=1`, id, kind, target, snapshot, localLabel, offsiteLabel)
	return id, err
}

// scheduled performs the first due scheduled work: the local backup (with its
// offsite copy when that is due as well), an offsite copy of the newest local
// snapshot, or the restore probe.
func (s *Service) scheduled(ctx context.Context) error {
	pool := s.Pool()
	settings, zone, err := loadSettings(ctx, pool)
	if err != nil {
		return err
	}
	now := s.clock()
	local, err := config.LocalBackupTarget()
	if err != nil {
		return err
	}
	offsite, offsiteErr := config.OffsiteBackupTarget(local)
	offsiteActive := settings.OffsiteEnabled && offsite != nil && offsiteErr == nil
	lastLocal, err := lastAttempt(ctx, pool, "local", local.Label())
	if err != nil {
		return err
	}
	localDue, err := backupplan.Due(now, zone, settings.LocalTime, settings.LocalIntervalHours, lastLocal)
	if err != nil {
		return err
	}
	offsiteDue := false
	if offsiteActive {
		lastOffsite, err := lastAttempt(ctx, pool, "offsite", offsite.Label())
		if err != nil {
			return err
		}
		if offsiteDue, err = backupplan.Due(now, zone, settings.LocalTime, settings.OffsiteIntervalHours, lastOffsite); err != nil {
			return err
		}
	}
	switch {
	case localDue:
		target := "local"
		if offsiteDue {
			target = "both"
		}
		id, err := s.startScheduled(ctx, "backup", target, "", false)
		if err != nil {
			return err
		}
		s.perform(ctx, Job{ID: id, Kind: "backup", Target: target})
		return nil
	case offsiteDue:
		id, err := s.startScheduled(ctx, "backup", "both", "", true)
		if err != nil {
			return err
		}
		stop := s.keepAlive(ctx, id)
		unlock, err := lockRepository(ctx, pool)
		snapshot := ""
		if err == nil {
			snapshot, err = copyLatestOffsite(audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID()), pool, local, *offsite, settings, id)
			unlock()
		}
		stop()
		if err != nil {
			// Only the offsite copy ran: its failure must not fail the local target.
			err = &ToolError{Program: "restic", Code: "offsite_" + ResultCode(err), err: err}
			s.Log.Error("backup work failed", "job", id, "kind", "backup", "code", ResultCode(err))
			s.alert(ctx, Job{ID: id, Kind: "backup", Target: "offsite"}, err)
		}
		s.finish(ctx, id, err, snapshot)
		return nil
	}
	return s.scheduledProbe(ctx, settings, now)
}

// scheduledProbe verifies the newest restorable snapshot when the configured
// probe interval passed since the last probe attempt.
func (s *Service) scheduledProbe(ctx context.Context, settings backupplan.Settings, now time.Time) error {
	if settings.ProbeIntervalDays == 0 || s.Superuser == "" {
		return nil
	}
	pool := s.Pool()
	target, err := s.target(settings.ProbeTarget)
	if err != nil {
		return nil // an unconfigured offsite probe target is shown on the status page
	}
	var lastProbe *time.Time
	// Only checks that actually started count; a cancelled request never ran.
	if err = pool.QueryRow(ctx, "SELECT max(started_at) FROM core.backup_jobs WHERE kind='verify' AND target=$1 AND started_at IS NOT NULL", settings.ProbeTarget).Scan(&lastProbe); err != nil {
		return err
	}
	if lastProbe != nil && now.Sub(*lastProbe) < time.Duration(settings.ProbeIntervalDays)*24*time.Hour {
		return nil
	}
	rows, err := pool.Query(ctx, `SELECT snapshot_reference,schema_manifest FROM core.backup_runs
 WHERE repository_reference=$1 AND state='complete' AND snapshot_reference IS NOT NULL ORDER BY started_at DESC LIMIT 20`, target.Label())
	if err != nil {
		return err
	}
	type candidate struct {
		snapshot string
		schemas  map[string]int
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (candidate, error) {
		var c candidate
		err := row.Scan(&c.snapshot, &c.schemas)
		return c, err
	})
	if err != nil {
		return err
	}
	snapshot := ""
	for _, c := range candidates {
		if backupplan.SchemaCompatible(c.schemas, currentSchemas()) {
			snapshot = c.snapshot
			break
		}
	}
	if snapshot == "" {
		return nil
	}
	id, err := s.startScheduled(ctx, "verify", settings.ProbeTarget, snapshot, false)
	if err != nil {
		return err
	}
	s.perform(ctx, Job{ID: id, Kind: "verify", Target: settings.ProbeTarget, Snapshot: snapshot})
	return nil
}

// lastAttempt is the newest start of work for one target: a run of that
// repository, or a backup job that covered it, so a job that failed before it
// created a run (lock, settings, repository access) still waits for the next
// slot instead of retrying every tick.
func lastAttempt(ctx context.Context, pool *pgxpool.Pool, kind, label string) (*time.Time, error) {
	var last *time.Time
	err := pool.QueryRow(ctx, `SELECT greatest(
 (SELECT max(started_at) FROM core.backup_runs WHERE repository_kind=$1 AND repository_reference=$2),
 (SELECT max(started_at) FROM core.backup_jobs WHERE kind='backup' AND started_at IS NOT NULL AND (target='both' OR $1='local')
  AND CASE WHEN $1='local' THEN local_label ELSE offsite_label END=$2))`, kind, label).Scan(&last)
	return last, err
}

// HealthReport is the backup service's own view for `household backup-health`.
type HealthReport struct {
	Local   backupplan.Health `json:"local"`
	Offsite backupplan.Health `json:"offsite"`
	Healthy bool              `json:"healthy"`
}

// CheckHealth evaluates both targets against their schedules. A target that
// never ran is healthy until its first slot has passed with grace.
func CheckHealth(ctx context.Context, pool *pgxpool.Pool) (HealthReport, error) {
	settings, zone, err := loadSettings(ctx, pool)
	if err != nil {
		return HealthReport{}, err
	}
	now := time.Now()
	evaluate := func(kind string, enabled bool, interval int) (backupplan.Health, error) {
		var since, success, ended *time.Time
		var failed bool
		if err := pool.QueryRow(ctx, backupplan.HealthQuery, kind).Scan(&since, &success, &failed, &ended); err != nil {
			return backupplan.Health{}, err
		}
		return backupplan.Evaluate(now, zone, settings.LocalTime, interval, enabled, since, success, failed), nil
	}
	report := HealthReport{}
	if report.Local, err = evaluate("local", true, settings.LocalIntervalHours); err != nil {
		return report, err
	}
	if report.Offsite, err = evaluate("offsite", settings.OffsiteEnabled, settings.OffsiteIntervalHours); err != nil {
		return report, err
	}
	report.Healthy = report.Local.State != backupplan.StateFailed && report.Local.State != backupplan.StateOverdue &&
		report.Offsite.State != backupplan.StateFailed && report.Offsite.State != backupplan.StateOverdue
	return report, nil
}

// alertTitles names each kind of failed work in operator alerts.
var alertTitles = map[string]string{
	"backup":           "Sicherung fehlgeschlagen",
	"verify":           "Probewiederherstellung fehlgeschlagen",
	"offsite_test":     "Externes Sicherungsziel nicht erreichbar",
	"restore":          "Wiederherstellung fehlgeschlagen",
	"discard_previous": "Früheren Stand verwerfen fehlgeschlagen",
}

// alert sends a failed or completed unit of work to the external channel.
// Only the kind, target and public reason code leave the installation.
func (s *Service) alert(ctx context.Context, job Job, err error) {
	if s.Alerts == nil {
		return
	}
	message := alerts.Message{Critical: job.Kind == "backup" || job.Kind == "restore"}
	if err == nil {
		message.Title = "Wiederherstellung abgeschlossen"
		message.Body = "Household wurde aus einer Sicherung wiederhergestellt. Alle Benutzer melden sich neu an; der frühere Stand bleibt aufbewahrt, bis ein Administrator ihn verwirft."
	} else {
		message.Title = alertTitles[job.Kind]
		if message.Title == "" {
			message.Title = "Sicherungsauftrag fehlgeschlagen"
		}
		message.Body = "Ziel: " + job.Target + "\nGrund: " + ResultCode(err) +
			"\nDetails stehen in der App unter Einstellungen → Sicherung und im Protokoll des Dienstes backup."
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if sendErr := s.Alerts.Send(sendCtx, message); sendErr != nil {
		s.Log.Warn("operator alert could not be delivered", "error", sendErr)
	}
}
