//go:build integration

package maintenance

import (
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
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
