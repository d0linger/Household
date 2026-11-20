package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/buildinfo"
	"household/internal/config"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the read subset shared by pools and transactions.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadSettings reads the household's backup schedule and time zone. A missing
// settings row (a household created after Core42) uses the defaults.
func loadSettings(ctx context.Context, q querier) (backupplan.Settings, *time.Location, error) {
	s := backupplan.Defaults()
	var zone string
	var localTime time.Time
	err := q.QueryRow(ctx, `SELECT h.timezone,coalesce(s.local_time,'03:00'::time),coalesce(s.local_interval_hours,24),coalesce(s.keep_daily,7),
 coalesce(s.keep_weekly,4),coalesce(s.keep_monthly,12),coalesce(s.offsite_enabled,false),coalesce(s.offsite_interval_hours,24),
 coalesce(s.offsite_keep_daily,7),coalesce(s.offsite_keep_weekly,4),coalesce(s.offsite_keep_monthly,12),
 coalesce(s.probe_interval_days,7),coalesce(s.probe_target,'local')
 FROM core.households h LEFT JOIN core.backup_settings s ON s.household_id=h.id WHERE h.singleton=1`).Scan(
		&zone, &localTime, &s.LocalIntervalHours, &s.KeepDaily, &s.KeepWeekly, &s.KeepMonthly, &s.OffsiteEnabled, &s.OffsiteIntervalHours,
		&s.OffsiteKeepDaily, &s.OffsiteKeepWeekly, &s.OffsiteKeepMonthly, &s.ProbeIntervalDays, &s.ProbeTarget)
	if errors.Is(err, pgx.ErrNoRows) {
		location, _ := time.LoadLocation("Europe/Vienna")
		return s, location, nil
	}
	if err != nil {
		return s, nil, err
	}
	s.LocalTime = localTime.Format("15:04")
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
	}
	return s, location, nil
}

// copyOffsiteLocked copies one local snapshot to the offsite repository,
// checks a rotating sixth of its data, records the run and applies the
// offsite retention. The repository lock is held by the caller.
func copyOffsiteLocked(ctx context.Context, pool *pgxpool.Pool, local, offsite config.BackupTarget, settings backupplan.Settings, snapshot string, schemas []byte, jobID string) (string, error) {
	if err := ensureRepository(ctx, offsite, &local); err != nil {
		return "", err
	}
	before, err := listSnapshots(ctx, offsite)
	if err != nil {
		return "", err
	}
	if existing := offsiteCopyOf(before, snapshot); existing != "" {
		// Already offsite: no new restore point, so no new run that would make
		// the target look freshly backed up. The rotating data check still runs.
		return existing, checkOffsite(ctx, pool, offsite)
	}
	var sourceBytes *int64
	if err = pool.QueryRow(ctx, "SELECT max(total_bytes) FROM core.backup_runs WHERE repository_kind='local' AND snapshot_reference=$1", snapshot).Scan(&sourceBytes); err != nil {
		return "", err
	}
	run := value.UUID()
	if _, err = audit.Exec(ctx, pool, "INSERT INTO core.backup_runs(id,state,application_version,repository_reference,repository_kind,job_id) VALUES($1,'running',$2,$3,'offsite',$4)",
		run, buildinfo.Label(), offsite.Label(), nullableUUID(jobID)); err != nil {
		return "", err
	}
	finished := false
	defer func() {
		if !finished {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = audit.Exec(failureCtx, pool, "UPDATE core.backup_runs SET state='failed',finished_at=now(),failure_reason='offsite copy failed' WHERE id=$1", run)
		}
	}()
	if _, err = restic(ctx, offsite, &local, "copy", snapshot); err != nil {
		return "", err
	}
	snapshots, err := listSnapshots(ctx, offsite)
	if err != nil {
		return "", err
	}
	copied := offsiteCopyOf(snapshots, snapshot)
	if copied == "" {
		return "", &ToolError{Program: "restic", Code: "copy_missing", err: errors.New("copied snapshot not found offsite")}
	}
	if err = checkOffsite(ctx, pool, offsite); err != nil {
		return "", err
	}
	if _, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state='complete',finished_at=now(),snapshot_reference=$2,schema_manifest=$3,total_bytes=$4 WHERE id=$1",
		run, copied, schemas, sourceBytes); err != nil {
		return "", err
	}
	finished = true
	if err = applyRetention(ctx, pool, offsite, settings.OffsiteKeepDaily, settings.OffsiteKeepWeekly, settings.OffsiteKeepMonthly); err != nil {
		return copied, err
	}
	return copied, nil
}

// offsiteCopyOf returns the offsite snapshot that copies the local snapshot.
func offsiteCopyOf(snapshots []resticSnapshot, local string) string {
	copied := ""
	for _, item := range snapshots {
		if item.Original == local || item.ID == local {
			copied = item.ID
		}
	}
	return copied
}

// offsiteCheckParts splits the offsite data check: each copy reads one part,
// so the whole repository is read once per six runs instead of every day.
const offsiteCheckParts = 6

// checkOffsite verifies the repository structure and one rotating part of the data.
func checkOffsite(ctx context.Context, pool *pgxpool.Pool, offsite config.BackupTarget) error {
	var cursor int
	if err := pool.QueryRow(ctx, "SELECT coalesce((SELECT s.offsite_check_cursor FROM core.backup_settings s JOIN core.households h ON h.id=s.household_id WHERE h.singleton=1),0)").Scan(&cursor); err != nil {
		return err
	}
	part := cursor%offsiteCheckParts + 1
	if _, err := restic(ctx, offsite, nil, "check", fmt.Sprintf("--read-data-subset=%d/%d", part, offsiteCheckParts)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "UPDATE core.backup_settings s SET offsite_check_cursor=$1 FROM core.households h WHERE h.id=s.household_id AND h.singleton=1", part%offsiteCheckParts)
	return err
}

// TestOffsite opens (or initializes) the offsite repository without copying data.
func TestOffsite(ctx context.Context, pool *pgxpool.Pool) error {
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	local, err := config.LocalBackupTarget()
	if err != nil {
		return err
	}
	offsite, err := config.OffsiteBackupTarget(local)
	if err != nil {
		return err
	}
	if offsite == nil {
		return ErrOffsiteUnconfigured
	}
	if _, err = restic(ctx, *offsite, nil, "cat", "config"); err == nil {
		// An existing target is only read.
		_, err = listSnapshots(ctx, *offsite)
		return err
	}
	// A new target takes the local chunker parameters, so the local
	// repository must exist first, also before the very first backup.
	if err = ensureRepository(ctx, local, nil); err != nil {
		return err
	}
	if err = ensureRepository(ctx, *offsite, &local); err != nil {
		return err
	}
	_, err = listSnapshots(ctx, *offsite)
	return err
}

// copyLatestOffsite copies the newest complete local snapshot that is not
// offsite yet. It is used when the offsite schedule is due between local runs.
func copyLatestOffsite(ctx context.Context, pool *pgxpool.Pool, local, offsite config.BackupTarget, settings backupplan.Settings, jobID string) (string, error) {
	var snapshot, schemas string
	err := pool.QueryRow(ctx, `SELECT snapshot_reference,schema_manifest::text FROM core.backup_runs
 WHERE repository_kind='local' AND repository_reference=$1 AND state='complete' ORDER BY started_at DESC LIMIT 1`, local.Label()).Scan(&snapshot, &schemas)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNoLocalSnapshot
	}
	if err != nil {
		return "", err
	}
	return copyOffsiteLocked(ctx, pool, local, offsite, settings, snapshot, []byte(schemas), jobID)
}

var errNoLocalSnapshot = errors.New("no completed local snapshot to copy")
