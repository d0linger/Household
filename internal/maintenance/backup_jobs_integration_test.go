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

// retentionQuotas creates the household with the same quotas for the local
// and the offsite repository.
func retentionQuotas(t *testing.T, f restoreBarcodeFixture, daily, weekly, monthly int) {
	t.Helper()
	var household string
	if err := f.owner.QueryRow(f.ctx, "INSERT INTO core.households(name,timezone) VALUES('Synthetic retention','UTC') RETURNING id").Scan(&household); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_settings(household_id,keep_daily,keep_weekly,keep_monthly,offsite_keep_daily,offsite_keep_weekly,offsite_keep_monthly)
 VALUES($1,$2,$3,$4,$2,$3,$4)`, household, daily, weekly, monthly); err != nil {
		t.Fatal(err)
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

// Before a new archive is written, reclaim retries pending local deletions
// that the quotas still select. It keeps files that any run of this repository
// label references, also an offsite run of a repository that became the local
// one, and removes files that no run references.
func TestReclaimFinishesPendingDeletionsAndKeepsReferencedFiles(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	retentionQuotas(t, f, 1, 1, 1)
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
	// The newer archive claims every bucket, so the quotas expire this one.
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_runs SET started_at=now()-interval '60 days' WHERE snapshot_reference=$1", pending); err != nil {
		t.Fatal(err)
	}
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
	retentionQuotas(t, f, 1, 1, 1)
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
	// A newer copy claims every bucket, so the quotas expire the pending one.
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,started_at)
 VALUES('retention_pending','test',$1,$2,'offsite',now()-interval '60 days'),('complete','test',$1,$3,'offsite',now()-interval '1 hour')`,
		offsite.Label(), expired, backupplan.ArchiveID(time.Now().Add(-time.Hour))); err != nil {
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
func TestOffsiteCopyKeepsAFileAnotherLabelShares(t *testing.T) {
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
	// Another configured path names the same directory.
	var other string
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite') RETURNING id`, aliasPath(offsite.Repository), snapshot).Scan(&other); err != nil {
		t.Fatal(err)
	}
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), snapshot, []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("file of another offsite label removed: %v", err)
	}
	// A label whose directory is a different one does not keep this file.
	distinct := t.TempDir()
	distinctFile := filepath.Join(backupplan.ArchiveDirectory(distinct), backupplan.ArchiveName(snapshot))
	if err := os.Mkdir(backupplan.ArchiveDirectory(distinct), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(distinctFile, []byte("synthetic copy in another directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_runs SET repository_reference=$2 WHERE id=$1", other, distinct); err != nil {
		t.Fatal(err)
	}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), snapshot, []byte(`{}`), ""); err == nil {
		t.Fatal("copy without a local archive succeeded")
	}
	if _, err := os.Stat(shared); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file kept for a label of another directory: %v", err)
	}
	if _, err := os.Stat(distinctFile); err != nil {
		t.Fatalf("file in the other directory changed: %v", err)
	}
}

// aliasPath names directory by another path string.
func aliasPath(directory string) string { return directory + string(filepath.Separator) + "." }

// The cleanup of an earlier failed copy keeps a file that a run of another
// label needs, and keeps the attempt, so it resumes once that run is gone.
func TestOffsiteAttemptCleanupKeepsAFileAnotherLabelNeeds(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	file := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.WriteFile(file, []byte("synthetic copy that another label completed"), 0600); err != nil {
		t.Fatal(err)
	}
	var attempt, other string
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,failure_reason)
 VALUES('failed','test',$1,$2,'offsite','offsite copy failed') RETURNING id`, offsite.Label(), snapshot).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite') RETURNING id`, aliasPath(offsite.Repository), snapshot).Scan(&other); err != nil {
		t.Fatal(err)
	}
	cleared := func() bool {
		t.Helper()
		var cleared bool
		if err := f.owner.QueryRow(ctx, "SELECT snapshot_reference IS NULL FROM core.backup_runs WHERE id=$1", attempt).Scan(&cleared); err != nil {
			t.Fatal(err)
		}
		return cleared
	}
	if err := reconcileOffsiteAttempts(ctx, f.owner, offsite, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil || cleared() {
		t.Fatalf("cleanup removed a file another label completed: %v, attempt cleared %v", err, cleared())
	}
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_runs SET state='expired' WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	if err := reconcileOffsiteAttempts(ctx, f.owner, offsite, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) || !cleared() {
		t.Fatalf("cleanup did not resume: %v, attempt cleared %v", err, cleared())
	}
}

// A pending deletion keeps a file that a complete run of another label needs,
// and stays pending. A pending run of the other label that its quotas expire
// does not keep it, so two labels never wait for each other.
func TestPendingDeletionKeepsAFileAnotherLabelCompleted(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	retentionQuotas(t, f, 1, 1, 1)
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	file := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.WriteFile(file, []byte("synthetic copy that another label completed"), 0600); err != nil {
		t.Fatal(err)
	}
	var pending, other string
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('retention_pending','test',$1,$2,'offsite') RETURNING id`, offsite.Label(), snapshot).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite') RETURNING id`, aliasPath(offsite.Repository), snapshot).Scan(&other); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		t.Helper()
		var state string
		if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE id=$1", pending).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	if err := finishArchiveRetention(ctx, f.owner, offsite, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil || state() != "retention_pending" {
		t.Fatalf("pending deletion removed a file another label completed: %v, state %s", err, state())
	}
	// The other label expires the archive as well: a newer archive fills its
	// quotas. Its pending run then does not keep the file.
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_runs SET state='retention_pending' WHERE id=$1", other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite')`, aliasPath(offsite.Repository), backupplan.ArchiveID(time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := finishArchiveRetention(ctx, f.owner, offsite, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) || state() != "expired" {
		t.Fatalf("pending deletion waited for a pending run of another label that its quotas expire: %v, state %s", err, state())
	}
}

// Two labels can share one mounted file. When one label expires it, a pending
// run of the other label that its own quotas keep is decided again first, so
// the file stays. When both labels expire it, the file is removed and both
// runs are recorded expired, whichever label finishes first.
func TestPendingDeletionKeepsAFileAnotherLabelsQuotasKeep(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	retentionQuotas(t, f, 1, 1, 1)
	first := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	second := config.BackupTarget{Kind: "offsite", Repository: aliasPath(first.Repository)}
	directory := backupplan.ArchiveDirectory(first.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	shared := backupplan.ArchiveID(time.Now().Add(-60 * 24 * time.Hour))
	file := filepath.Join(directory, backupplan.ArchiveName(shared))
	if err := os.WriteFile(file, []byte("synthetic archive two labels share"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(target config.BackupTarget, state, snapshot string, age time.Duration) {
		t.Helper()
		if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,started_at)
 VALUES($1,'test',$2,$3,'offsite',now()-interval '1 second'*$4)`, state, target.Label(), snapshot, int64(age.Seconds())); err != nil {
			t.Fatal(err)
		}
	}
	state := func(target config.BackupTarget) string {
		t.Helper()
		var state string
		if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", target.Label(), shared).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	// The first label has a newer archive, so its quotas expire the shared one.
	// The second label holds the shared archive alone, as a pending run that a
	// version without portable files left, and its quotas keep it.
	run(first, "complete", shared, 60*24*time.Hour)
	run(first, "complete", backupplan.ArchiveID(time.Now().Add(-time.Hour)), time.Hour)
	run(second, "retention_pending", shared, 60*24*time.Hour)
	if err := applyArchiveRetention(ctx, f.owner, first, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil || state(first) != "retention_pending" || state(second) != "complete" {
		t.Fatalf("expiry removed a file another label keeps: %v, states %s and %s", err, state(first), state(second))
	}
	// Once the second label expires the archive as well, the file goes.
	run(second, "complete", backupplan.ArchiveID(time.Now().Add(-2*time.Hour)), 2*time.Hour)
	if err := applyArchiveRetention(ctx, f.owner, second, ""); err != nil {
		t.Fatal(err)
	}
	if err := finishArchiveRetention(ctx, f.owner, first, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) || state(first) != "expired" || state(second) != "expired" {
		t.Fatalf("labels that both expire a file kept it: %v, states %s and %s", err, state(first), state(second))
	}
}

// A version without portable files marks every complete run of its label
// pending before restic decides; a run stopped then leaves portable archives
// pending that no retention selected. Retention and reclaim decide them again:
// an archive the quotas keep stays, one they expire is removed, and a pending
// row whose file is already gone is recorded expired.
func TestRetentionKeepsPendingArchivesTheQuotasKeep(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	retentionQuotas(t, f, 1, 1, 1)
	for _, kind := range []string{"local", "offsite"} {
		target := config.BackupTarget{Kind: kind, Repository: t.TempDir()}
		directory := backupplan.ArchiveDirectory(target.Repository)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-3 * time.Hour)
		pending := func(age time.Duration, withFile bool) (string, string) {
			t.Helper()
			id := backupplan.ArchiveID(time.Now().Add(-age))
			path := filepath.Join(directory, backupplan.ArchiveName(id))
			if withFile {
				if err := os.WriteFile(path, []byte("synthetic archive"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,started_at)
 VALUES('retention_pending','test',$1,$2,$3,now()-interval '1 second'*$4)`, target.Label(), id, kind, int64(age.Seconds())); err != nil {
				t.Fatal(err)
			}
			return id, path
		}
		kept, keptFile := pending(time.Hour, true)
		gone, _ := pending(25*time.Hour, false)
		expired, expiredFile := pending(60*24*time.Hour, true)
		var err error
		if kind == "local" {
			err = reclaimArchives(ctx, f.owner, target, time.Now())
		} else {
			err = applyArchiveRetention(ctx, f.owner, target, "")
		}
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for path, want := range map[string]bool{keptFile: true, expiredFile: false} {
			if _, err := os.Stat(path); (err == nil) != want {
				t.Errorf("%s: %s present=%v, want %v", kind, filepath.Base(path), err == nil, want)
			}
		}
		for id, want := range map[string]string{kept: "complete", gone: "expired", expired: "expired"} {
			var state string
			if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", target.Label(), id).Scan(&state); err != nil || state != want {
				t.Errorf("%s: run of %s is %q, %v; want %s", kind, id, state, err, want)
			}
		}
	}
}

// A failed decision of pending deletions stops the cleanup before an offsite
// copy: a pending row of the snapshot being copied may be one the quotas
// keep, so its file is not deleted on the strength of the pending row alone.
func TestOffsiteCopyKeepsAPendingFileWhenTheDecisionFails(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	offsite := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(offsite.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	// A directory under the archive name cannot be opened as an archive, so
	// the presence check of the pending row fails.
	entry := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.Mkdir(entry, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('retention_pending','test',$1,$2,'offsite')`, offsite.Label(), snapshot); err != nil {
		t.Fatal(err)
	}
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	if _, err := copyPortableLocked(ctx, f.owner, local, offsite, backupplan.Defaults(), snapshot, []byte(`{}`), ""); err == nil {
		t.Fatal("copy succeeded although its pending deletion was not decided")
	}
	var state string
	if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", offsite.Label(), snapshot).Scan(&state); err != nil || state != "retention_pending" {
		t.Fatalf("pending row is %q, %v; want retention_pending", state, err)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("undecided pending deletion removed its entry: %v", err)
	}
}

// A pending row whose file is gone claims no calendar bucket when pending
// rows are decided again, so it cannot push out a present archive of the
// same period.
func TestRetentionKeepsAPresentPendingArchiveBesideAMissingOne(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	retentionQuotas(t, f, 1, 1, 1)
	target := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(target.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	// Both points lie in one fixed past day, week and month.
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-72 * time.Hour)
	present, missing := backupplan.ArchiveID(day.Add(10*time.Hour)), backupplan.ArchiveID(day.Add(11*time.Hour))
	presentFile := filepath.Join(directory, backupplan.ArchiveName(present))
	if err := os.WriteFile(presentFile, []byte("synthetic archive"), 0600); err != nil {
		t.Fatal(err)
	}
	for id, at := range map[string]time.Time{present: day.Add(10 * time.Hour), missing: day.Add(11 * time.Hour)} {
		if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind,started_at)
 VALUES('retention_pending','test',$1,$2,'offsite',$3)`, target.Label(), id, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyArchiveRetention(ctx, f.owner, target, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(presentFile); err != nil {
		t.Fatalf("a missing pending archive pushed out the present one: %v", err)
	}
	for id, want := range map[string]string{present: "complete", missing: "expired"} {
		var state string
		if err := f.owner.QueryRow(ctx, "SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2", target.Label(), id).Scan(&state); err != nil || state != want {
			t.Errorf("run of %s is %q, %v; want %s", id, state, err, want)
		}
	}
}

// An offsite mount can become the local repository under another path. A
// complete offsite run of that path still needs its file, so reclaim keeps
// it, and removes it once no run needs it.
func TestReclaimKeepsAFileAnOffsiteAliasNeeds(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	local := config.BackupTarget{Kind: "local", Repository: t.TempDir()}
	directory := backupplan.ArchiveDirectory(local.Repository)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	file := filepath.Join(directory, backupplan.ArchiveName(snapshot))
	if err := os.WriteFile(file, []byte("synthetic offsite copy"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	var run string
	if err := f.owner.QueryRow(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite') RETURNING id`, aliasPath(local.Repository), snapshot).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err := reclaimArchives(ctx, f.owner, local, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("reclaim removed a file an offsite alias needs: %v", err)
	}
	if _, err := f.owner.Exec(ctx, "UPDATE core.backup_runs SET state='expired' WHERE id=$1", run); err != nil {
		t.Fatal(err)
	}
	if err := reclaimArchives(ctx, f.owner, local, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reclaim kept a file no run needs: %v", err)
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
