//go:build integration

package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The 180-day job cleanup must keep an import job while its local run exists,
// even an expired one: scheduling and health read every run, and only the job
// tells an imported capture date apart from a backup attempt.
func TestJobCleanupKeepsImportProvenance(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	var household string
	if err := f.owner.QueryRow(ctx, "INSERT INTO core.households(name) VALUES('Synthetic backup job cleanup') RETURNING id").Scan(&household); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-200 * 24 * time.Hour)
	job := func(kind string, snapshot *string) string {
		t.Helper()
		var id string
		if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_jobs(household_id,kind,target,snapshot_reference,state,created_at,expires_at,started_at,finished_at)
 VALUES($1,$2,'local',$3,'complete',$4,$4,$4,$4) RETURNING id`, household, kind, snapshot, old).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	run := func(jobID, state, snapshot string) string {
		t.Helper()
		var id string
		if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(started_at,finished_at,state,application_version,repository_reference,snapshot_reference,repository_kind,job_id)
 VALUES($1,$1,$2,'test','/data/backups',$3,'local',$4) RETURNING id`, old, state, snapshot, jobID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	imported, tested := strings.Repeat("a", 64), strings.Repeat("b", 64)
	importJob := job("verify", &imported)
	importRun := run(importJob, "expired", imported)
	backupJob := job("backup", nil)
	backupRun := run(backupJob, "expired", strings.Repeat("c", 64))
	probeJob := job("verify", &tested)

	service := &Service{pool: f.owner, now: time.Now}
	if err := service.failStale(ctx); err != nil {
		t.Fatal(err)
	}
	var importKept, backupKept, probeKept, stillImported bool
	if err := f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_jobs WHERE id=$1),EXISTS(SELECT 1 FROM core.backup_jobs WHERE id=$2),
 EXISTS(SELECT 1 FROM core.backup_jobs WHERE id=$3),EXISTS(SELECT 1 FROM core.backup_runs r WHERE r.id=$4 AND `+backupplan.ImportedRun+`)`,
		importJob, backupJob, probeJob, importRun).Scan(&importKept, &backupKept, &probeKept, &stillImported); err != nil {
		t.Fatal(err)
	}
	if !importKept || !stillImported {
		t.Fatalf("expired import lost its provenance: job kept %v, still imported %v", importKept, stillImported)
	}
	if backupKept || probeKept {
		t.Fatalf("cleanup kept old jobs without provenance: backup %v, restore test %v", backupKept, probeKept)
	}
	var detached bool
	if err := f.owner.QueryRow(ctx, "SELECT job_id IS NULL FROM core.backup_runs WHERE id=$1", backupRun).Scan(&detached); err != nil || !detached {
		t.Fatalf("expired backup run kept its deleted job: %v, %v", detached, err)
	}
}

// Archive retention reads the quotas committed when it prunes, for the local
// and the offsite repository, not values a caller read before the backup.
func TestArchiveRetentionUsesTheCommittedQuotas(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	var household string
	if err := f.owner.QueryRow(ctx, "INSERT INTO core.households(name,timezone) VALUES('Synthetic retention quotas','UTC') RETURNING id").Scan(&household); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_settings(household_id,keep_daily,keep_weekly,keep_monthly,offsite_keep_daily,offsite_keep_weekly,offsite_keep_monthly)
 VALUES($1,3,1,1,1,1,1) ON CONFLICT(household_id) DO UPDATE SET keep_daily=3,keep_weekly=1,keep_monthly=1,offsite_keep_daily=1,offsite_keep_weekly=1,offsite_keep_monthly=1`, household); err != nil {
		t.Fatal(err)
	}
	// Three captures on consecutive days of one ISO week and month.
	captures := []time.Time{
		time.Date(2026, 1, 6, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 7, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 8, 3, 0, 0, 0, time.UTC),
	}
	setup := func(kind string) (config.BackupTarget, []string) {
		t.Helper()
		target := config.BackupTarget{Kind: kind, Repository: t.TempDir()}
		if err := os.MkdirAll(backupplan.ArchiveDirectory(target.Repository), 0700); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, at := range captures {
			id := backupplan.ArchiveID(at)
			if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(started_at,finished_at,state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES($1,$1,'complete','test',$2,$3,$4)`, at, target.Label(), id, kind); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return target, ids
	}
	kept := func(target config.BackupTarget) int {
		t.Helper()
		var count int
		if err := f.owner.QueryRow(ctx, "SELECT count(*) FROM core.backup_runs WHERE repository_reference=$1 AND state='complete'", target.Label()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	local, _ := setup("local")
	offsite, offsiteIDs := setup("offsite")
	if err := applyArchiveRetention(ctx, f.owner, local, ""); err != nil {
		t.Fatal(err)
	}
	if got := kept(local); got != 3 {
		t.Fatalf("local retention kept %d of three daily archives, want the committed keep_daily=3", got)
	}
	if err := applyArchiveRetention(ctx, f.owner, offsite, ""); err != nil {
		t.Fatal(err)
	}
	var complete int
	var newest string
	if err := f.owner.QueryRow(ctx, "SELECT count(*),coalesce(max(snapshot_reference),'') FROM core.backup_runs WHERE repository_reference=$1 AND state='complete'", offsite.Label()).Scan(&complete, &newest); err != nil || complete != 1 || newest != offsiteIDs[2] {
		t.Fatalf("offsite retention kept %d runs up to %q, %v; want only the newest archive under offsite_keep_daily=1", complete, newest, err)
	}
	// A retention change committed after the backup started governs the prune.
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_settings SET keep_daily=1 WHERE household_id=$1", household); err != nil {
		t.Fatal(err)
	}
	if err := applyArchiveRetention(ctx, f.owner, local, ""); err != nil {
		t.Fatal(err)
	}
	if got := kept(local); got != 1 {
		t.Fatalf("local retention kept %d archives after keep_daily=1 was saved, want 1", got)
	}
}

// Before a new archive is written, reclaim retries pending local deletions.
// It keeps files that any run of this repository label references, also an
// offsite run of a repository that became the local one, and removes files
// that no run references.
func TestReclaimFinishesPendingDeletionsAndKeepsReferencedFiles(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(local.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	file := func(id string) string {
		t.Helper()
		path := filepath.Join(directory, backupplan.ArchiveName(id))
		if err := os.WriteFile(path, []byte("synthetic archive"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		return path
	}
	run := func(state, kind, id string) {
		t.Helper()
		if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES($1,'test',$2,$3,$4)`, state, local.Label(), id, kind); err != nil {
			t.Fatal(err)
		}
	}
	pending := backupplan.ArchiveID(old.Add(-48 * time.Hour))
	formerOffsite := backupplan.ArchiveID(old.Add(-24 * time.Hour))
	unreferenced := backupplan.ArchiveID(old)
	pendingFile, formerOffsiteFile, unreferencedFile := file(pending), file(formerOffsite), file(unreferenced)
	run("retention_pending", "local", pending)
	run("complete", "offsite", formerOffsite)
	if err := reclaimArchives(ctx, f.owner, local, time.Now()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{pendingFile: false, formerOffsiteFile: true, unreferencedFile: false} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", filepath.Base(path), err == nil, want)
		}
	}
	var state string
	if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", local.Label(), pending).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("pending deletion recorded as %q, %v; want expired", state, err)
	}
}

// An older backup worker expired every run of its repository label that its
// own retention did not list, also portable archives. Reclaim registers such a
// present file again instead of deleting it as unreferenced.
func TestReclaimRegistersArchivesAnOlderWorkerExpired(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(local.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	id := backupplan.ArchiveID(old.Add(-24 * time.Hour))
	path := filepath.Join(directory, backupplan.ArchiveName(id))
	if err := os.WriteFile(path, []byte("synthetic archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	expire := func(daysAgo int) string {
		t.Helper()
		var run string
		if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,started_at)
 VALUES('expired','test',$1,$2,'local',now()-interval '1 day'*$3) RETURNING id`, local.Label(), id, daysAgo).Scan(&run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	earlier, newest := expire(2), expire(1)
	if err := reclaimArchives(ctx, f.owner, local, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive of an expired run deleted: %v", err)
	}
	for run, want := range map[string]string{newest: "complete", earlier: "expired"} {
		var state string
		if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE id=$1", run).Scan(&state); err != nil || state != want {
			t.Errorf("run state = %q, %v; want %s", state, err, want)
		}
	}
}

// Pending deletions of other snapshots are retried before an offsite copy is
// staged, also when that copy then fails.
func TestOffsiteCopyFinishesOtherPendingDeletionsFirst(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	expired := backupplan.ArchiveID(time.Now().Add(-72 * time.Hour))
	expiredFile := filepath.Join(directory, backupplan.ArchiveName(expired))
	if err := os.WriteFile(expiredFile, []byte("synthetic expired copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('retention_pending','test',$1,$2,'offsite')`, offsite.Label(), expired); err != nil {
		t.Fatal(err)
	}
	// The local file of the new snapshot is missing, so this copy fails.
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), backupplan.ArchiveID(time.Now()), []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(expiredFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending deletion kept its file before the copy: %v", err)
	}
	var state string
	if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", offsite.Label(), expired).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("pending deletion recorded as %q, %v; want expired", state, err)
	}
}

// A failed copy of a snapshot can leave its file under the final name. A new
// copy of the same snapshot removes it before staging, so the mount does not
// have to hold both files.
func TestOffsiteCopyRemovesTheStaleFileOfTheSameSnapshot(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now())
	stale := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.WriteFile(stale, []byte("synthetic copy that failed its check"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,failure_reason)
 VALUES('failed','test',$1,$2,'offsite','offsite copy failed')`, offsite.Label(), snapshot); err != nil {
		t.Fatal(err)
	}
	// The local run of the snapshot does not protect the offsite file.
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'local')`, local.Label(), snapshot); err != nil {
		t.Fatal(err)
	}
	// The local file is missing, so the new copy fails after the removal.
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), snapshot, []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file of the same snapshot kept: %v", err)
	}
}

// Callers waiting for the repository lock hold no pool connection, so the
// holder and other requests can still use a small pool.
func TestRepositoryLockWaitersLeaveThePoolFree(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	settings := f.owner.Config()
	settings.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, stopWaiter := context.WithCancel(ctx)
	waiter := make(chan error, 1)
	go func() {
		release, err := lockRepository(waitCtx, pool)
		if err == nil {
			release()
		}
		waiter <- err
	}()
	// The holder's session last ran the same statement, so a second session
	// showing it is the waiter contending for the lock.
	contended := false
	for deadline := time.Now().Add(10 * time.Second); !contended && time.Now().Before(deadline); {
		var sessions int
		if err = f.owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
 WHERE datname=current_database() AND query LIKE 'SELECT pg_try_advisory_lock(721607137)%'`).Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		contended = sessions >= 2
		if !contended {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !contended {
		stopWaiter()
		waitErr := <-waiter
		unlock()
		t.Fatalf("the waiter never contended for the lock (waiter returned %v)", waitErr)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	var one int
	err = pool.QueryRow(queryCtx, "SELECT 1").Scan(&one)
	cancel()
	stopWaiter()
	if waitErr := <-waiter; !errors.Is(waitErr, context.Canceled) {
		t.Errorf("waiter returned %v, want cancellation", waitErr)
	}
	unlock()
	if err != nil || one != 1 {
		t.Fatalf("pool blocked while a caller waited for the lock: %v", err)
	}
	release, err := lockRepository(ctx, pool)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	release()
}

// The file of an earlier failed offsite copy is removed before a new copy,
// also when that copy fails, so stale attempts cannot keep taking the space
// later copies need.
// An offsite path can resolve to a directory that another offsite label also
// uses. A file that a complete run of that label references is kept.
func TestOffsiteCopyKeepsAFileAnotherOffsiteLabelReferences(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now())
	shared := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.WriteFile(shared, []byte("synthetic copy of another offsite label"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test','offsite:/another/path/to/the/same/mount',$1,'offsite')`, snapshot); err != nil {
		t.Fatal(err)
	}
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), snapshot, []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("file of another offsite label removed: %v", err)
	}
}

// A worker that stopped during a write leaves its partial file on the mount.
// The next copy removes it once the grace period passed, and keeps a partial
// file that may still be written.
func TestOffsiteCopyRemovesInterruptedPartialFiles(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	interrupted, recent := filepath.Join(directory, ".partial-interrupted"), filepath.Join(directory, ".partial-recent")
	for _, path := range []string{interrupted, recent} {
		if err := os.WriteFile(path, []byte("synthetic partial copy"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * archiveReclaimGrace)
	if err := os.Chtimes(interrupted, old, old); err != nil {
		t.Fatal(err)
	}
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), backupplan.ArchiveID(time.Now()), []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(interrupted); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted partial file kept: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent partial file removed: %v", err)
	}
}

func TestOffsiteCopyRemovesEarlierAttemptsWhenItFails(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	earlier := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	stale := filepath.Join(directory, backupplan.ArchiveName(earlier))
	if err := os.WriteFile(stale, []byte("synthetic partial upload"), 0600); err != nil {
		t.Fatal(err)
	}
	var attempt string
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,failure_reason)
 VALUES('failed','test',$1,$2,'offsite','offsite copy failed') RETURNING id`, offsite.Label(), earlier).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	// The local file of the new snapshot is missing, so this copy fails.
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), backupplan.ArchiveID(time.Now()), nil, ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("earlier attempt's file kept after a failed copy: %v", err)
	}
	var cleared bool
	if err := f.owner.QueryRow(ctx, "SELECT snapshot_reference IS NULL FROM core.backup_runs WHERE id=$1", attempt).Scan(&cleared); err != nil || !cleared {
		t.Fatalf("earlier attempt still references its file: %v, %v", cleared, err)
	}
}

// A mounted offsite target must show the mounted filesystem: Household never
// creates the repository path, and once an archive copy was recorded there a
// missing archive directory means the mount is absent.
func TestMountedArchiveDirectoryRequiresTheMountedFilesystem(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	mounted := t.TempDir()
	if _, err := mountedArchiveDirectory(ctx, f.owner, config.BackupTarget{Kind: "offsite", Repository: filepath.Join(mounted, "absent")}); ResultCode(err) != "repository_unreachable" {
		t.Fatalf("absent repository path: %v", err)
	}
	target := config.BackupTarget{Kind: "offsite", Repository: mounted}
	directory, err := mountedArchiveDirectory(ctx, f.owner, target)
	if err != nil || directory != backupplan.ArchiveDirectory(mounted) {
		t.Fatalf("first copy to an existing path: %q, %v", directory, err)
	}
	if _, err = f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite')`, target.Label(), backupplan.ArchiveID(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err = mountedArchiveDirectory(ctx, f.owner, target); !errors.Is(err, errArchiveDirectoryMissing) || ResultCode(err) != "repository_unreachable" {
		t.Fatalf("recorded copies without their directory: %v", err)
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := mountedArchiveDirectory(ctx, f.owner, target); err != nil || got != directory {
		t.Fatalf("mounted directory with recorded copies: %q, %v", got, err)
	}
}
