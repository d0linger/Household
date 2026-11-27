//go:build integration && testtools

package maintenance

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"
)

// writeSyntheticArchive writes age ciphertext of synthetic bytes, which
// checkPortable authenticates without a backup inside.
func writeSyntheticArchive(t *testing.T, path, password string) []byte {
	t.Helper()
	encrypted := syntheticArchive(t, password)
	if err := os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	return encrypted
}

func syntheticArchive(t *testing.T, password string) []byte {
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

// objectStore is an in-process stand-in for the few S3 object requests the
// archive code makes. It does not check signatures.
type objectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	writes  int
}

func (s *objectStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		data, ok := s.objects[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			}
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"synthetic"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	case http.MethodPut:
		s.writes++
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"synthetic"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		s.writes++
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

// Two spellings of one S3 prefix address the same object. A copy under one
// never replaces the object a run under the other needs, and retention under
// one never deletes it.
func TestOffsiteCopyNeverReplacesAnObjectAnotherSpellingNeeds(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	ctx := f.ctx
	t.Setenv("HOUSEHOLD_BACKUP_WORK", t.TempDir())
	store := &objectStore{objects: map[string][]byte{}}
	server := httptest.NewServer(store)
	t.Cleanup(server.Close)
	local := config.BackupTargetForTests("local", t.TempDir(), "synthetic-local-password")
	repository := "s3:" + server.URL + "/household/archives"
	offsite := config.S3BackupTargetForTests(repository, "synthetic-offsite-password")
	snapshot := backupplan.ArchiveID(time.Now().Add(-time.Hour))
	key := "/household/archives/" + backupplan.ArchiveName(snapshot)
	if err := os.Mkdir(backupplan.ArchiveDirectory(local.Repository), 0700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticArchive(t, filepath.Join(backupplan.ArchiveDirectory(local.Repository), backupplan.ArchiveName(snapshot)), "synthetic-local-password")
	// The same prefix with a trailing slash completed its copy under another
	// password.
	if _, err := f.owner.Exec(ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,snapshot_reference,repository_kind)
 VALUES('complete','test',$1,$2,'offsite')`, repository+"/", snapshot); err != nil {
		t.Fatal(err)
	}
	other := syntheticArchive(t, "synthetic-other-password")
	store.objects[key] = other
	err := sendPortableCopy(ctx, f.owner, local, offsite, snapshot, []byte(`{}`), "")
	if ResultCode(err) != "archive_shared" {
		t.Fatalf("copy over another spelling's object: %v (code %s, %d writes), want archive_shared", err, ResultCode(err), store.writes)
	}
	if store.writes != 0 || !bytes.Equal(store.objects[key], other) {
		t.Fatalf("copy changed another spelling's object: %d writes", store.writes)
	}
	// An object that this target's password opens is reused unchanged.
	same := syntheticArchive(t, "synthetic-offsite-password")
	store.objects[key] = same
	if err = sendPortableCopy(ctx, f.owner, local, offsite, snapshot, []byte(`{}`), ""); err != nil {
		t.Fatalf("copy that can reuse the shared object: %v", err)
	}
	var size int64
	if err = f.owner.QueryRow(ctx, `SELECT total_bytes FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2 AND state='complete'`,
		offsite.Label(), snapshot).Scan(&size); err != nil || size != int64(len(same)) {
		t.Fatalf("reused copy recorded with size %d, %v; want %d", size, err, len(same))
	}
	// Retention under this spelling keeps the object the other one needs.
	if _, err = f.owner.Exec(ctx, `UPDATE core.backup_runs SET state='retention_pending' WHERE repository_reference=$1 AND snapshot_reference=$2`,
		offsite.Label(), snapshot); err != nil {
		t.Fatal(err)
	}
	if err = finishArchiveRetention(ctx, f.owner, offsite, snapshot); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.owner.QueryRow(ctx, `SELECT state FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference=$2`,
		offsite.Label(), snapshot).Scan(&state); err != nil || state != "retention_pending" {
		t.Fatalf("pending row is %q, %v; want retention_pending", state, err)
	}
	if store.writes != 0 || !bytes.Equal(store.objects[key], same) {
		t.Fatalf("retention changed an object another spelling needs: %d writes", store.writes)
	}
}
