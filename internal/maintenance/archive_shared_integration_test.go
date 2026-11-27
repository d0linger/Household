//go:build integration && testtools

package maintenance

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"
)

// writeSyntheticArchive writes age ciphertext of synthetic bytes, which
// checkPortable authenticates without a backup inside.
func writeSyntheticArchive(t *testing.T, path, password string) []byte {
	t.Helper()
	var encrypted bytes.Buffer
	w, err := encryptArchive(&encrypted, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("synthetic archive content")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, encrypted.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return encrypted.Bytes()
}

// A copy never replaces a mounted file that a run of another label still
// needs. It reuses the file when its own password opens it, and otherwise
// fails and leaves the file as it is.
func TestOffsiteCopyNeverReplacesAFileAnotherLabelNeeds(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	local := config.BackupTargetForTests("local", t.TempDir(), "synthetic-local-password")
	offsite := config.BackupTargetForTests("offsite", t.TempDir(), "synthetic-offsite-password")
	snapshot := backupplan.ArchiveID(time.Now().Add(-time.Hour))
	name := backupplan.ArchiveName(snapshot)
	var err error
	for _, dir := range []string{backupplan.ArchiveDirectory(local.Repository), backupplan.ArchiveDirectory(offsite.Repository)} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeSyntheticArchive(t, filepath.Join(backupplan.ArchiveDirectory(local.Repository), name), "synthetic-local-password")
	file := filepath.Join(backupplan.ArchiveDirectory(offsite.Repository), name)
	// Another label names the same directory and completed its copy under a
	// different password.
	if _, err = f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite')`, aliasPath(offsite.Repository), snapshot); err != nil {
		t.Fatal(err)
	}
	other := writeSyntheticArchive(t, file, "synthetic-other-password")
	err = sendPortableCopy(ctx, f.owner, local, offsite, snapshot, []byte(`{}`), "")
	if ResultCode(err) != "archive_shared" {
		t.Fatalf("copy over another label's file: %v (code %s), want archive_shared", err, ResultCode(err))
	}
	if kept, readErr := os.ReadFile(file); readErr != nil || !bytes.Equal(kept, other) {
		t.Fatalf("copy replaced another label's file: %v", readErr)
	}
	// A shared file that this target's password opens is reused unchanged.
	same := writeSyntheticArchive(t, file, "synthetic-offsite-password")
	if err = sendPortableCopy(ctx, f.owner, local, offsite, snapshot, []byte(`{}`), ""); err != nil {
		t.Fatalf("copy that can reuse the shared file: %v", err)
	}
	if kept, readErr := os.ReadFile(file); readErr != nil || !bytes.Equal(kept, same) {
		t.Fatalf("copy rewrote a shared file it could reuse: %v", readErr)
	}
	var complete bool
	if err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2 AND state='complete')`,
		offsite.Label(), snapshot).Scan(&complete); err != nil || !complete {
		t.Fatalf("reused copy not recorded complete: %v", err)
	}
}
