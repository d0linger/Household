//go:build integration

package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"
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
	var newest string
	if err := f.owner.QueryRow(ctx, "SELECT snapshot_reference FROM core.backup_runs WHERE repository_reference=$1 AND state='complete'", offsite.Label()).Scan(&newest); err != nil || newest != offsiteIDs[2] {
		t.Fatalf("offsite retention kept %q, %v; want only the newest archive under offsite_keep_daily=1", newest, err)
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

// The file of an earlier failed offsite copy is removed before a new copy,
// also when that copy fails, so stale attempts cannot keep taking the space
// later copies need.
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
