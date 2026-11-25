package maintenance

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"
)

func TestPortableArchiveRoundTripAndAuthentication(t *testing.T) {
	ctx := context.Background()
	timestamp := time.Date(2026, 11, 24, 14, 5, 27, 0, time.UTC)
	id := backupplan.ArchiveID(timestamp)
	name := backupplan.ArchiveName(id)
	if !backupplan.ValidSnapshot(id) || !strings.HasPrefix(name, "household-2026-11-24T14-05-27Z-") || backupplan.ArchiveName(strings.Repeat("a", 64)) != "" {
		t.Fatalf("unexpected archive identity %q / %q", id, name)
	}
	stage, uploads, repo := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("RESTIC_REPOSITORY", "/data/backups")
	t.Setenv("RESTIC_PASSWORD", "test backup file encryption key")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	target, err := config.LocalBackupTarget()
	if err != nil {
		t.Fatal(err)
	}
	target.Repository = repo
	uploadID := "2b0979e6-0c00-43ca-9893-ae8c7cf2633c"
	attachment := []byte("synthetic archive attachment")
	digest := sha256.Sum256(attachment)
	files := []File{{Key: uploadID, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(attachment))}}
	manifest := Manifest{ArchiveFormat: 1, Version: "test", Created: timestamp, UploadPath: "/uploads", Crypto: &CryptoReport{}, Files: files}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		filepath.Join(stage, "manifest.json"): raw, filepath.Join(stage, "database.dump"): []byte("test pg dump"),
		filepath.Join(stage, "security-keys.json"): []byte("test escrow"), filepath.Join(uploads, uploadID): attachment,
	} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = writePortable(ctx, target, id, stage, uploads, files); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(backupplan.ArchiveDirectory(repo), name))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, attachment) || bytes.Contains(encrypted, []byte("test escrow")) {
		t.Fatal("encrypted file contains plaintext")
	}
	restored := t.TempDir()
	if err = extractArchive(ctx, bytes.NewReader(encrypted), target.ArchivePassword(), restored); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(restored, "uploads", uploadID))
	if err != nil || !bytes.Equal(data, attachment) {
		t.Fatalf("restored attachment: %q, %v", data, err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if err = extractArchive(ctx, bytes.NewReader(encrypted), target.ArchivePassword(), t.TempDir()); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	if err = extractArchive(ctx, bytes.NewReader(encrypted), "wrong backup password", t.TempDir()); ResultCode(err) != "archive_unreadable" {
		t.Fatalf("wrong password classification: %v", err)
	}
}

func TestArchiveExtractRejectsUnapprovedEntries(t *testing.T) {
	// With tarinsecurepath=0 archive/tar rejects "../escape" itself; allow it
	// here so the test exercises Household's own entry check.
	t.Setenv("GODEBUG", "tarinsecurepath=1")
	// The symlink has a valid upload name, so only the type check can reject it.
	for _, header := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "uploads/2b0979e6-0c00-43ca-9893-ae8c7cf2633c", Typeflag: tar.TypeSymlink, Linkname: "manifest.json"},
	} {
		var data bytes.Buffer
		writer := tar.NewWriter(&data)
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		err := extractArchivePlain(context.Background(), &data, t.TempDir())
		if err == nil || err.Error() != "unexpected entry in encrypted backup" {
			t.Fatalf("entry %q: got %v, want the unapproved-entry rejection", header.Name, err)
		}
	}
}

func TestArchiveHeaderAllowsEntriesOverEightGiB(t *testing.T) {
	writer := tar.NewWriter(io.Discard)
	if err := writer.WriteHeader(archiveHeader("database.dump", 9<<30)); err != nil {
		t.Fatalf("a 9 GiB dump needs a header: %v", err)
	}
}

func TestArchiveWriterAppliesReaderEntryRules(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), make([]byte, archiveMetadataLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err = addArchiveFile(context.Background(), tar.NewWriter(io.Discard), root, "manifest.json", "manifest.json", nil); err == nil {
		t.Fatal("writer accepted a manifest the reader rejects")
	}
	small := t.TempDir()
	for _, name := range []string{"manifest.json", "database.dump", "security-keys.json"} {
		if err = os.WriteFile(filepath.Join(small, name), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RESTIC_REPOSITORY", "/data/backups")
	t.Setenv("RESTIC_PASSWORD", "test backup file encryption key")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	target, err := config.LocalBackupTarget()
	if err != nil {
		t.Fatal(err)
	}
	target.Repository = t.TempDir()
	files := make([]File, archiveAttachmentLimit+1)
	_, err = writePortable(context.Background(), target, backupplan.ArchiveID(time.Now()), small, small, files)
	if err == nil || !strings.Contains(err.Error(), "attachments") {
		t.Fatalf("writer accepted more attachments than the reader extracts: %v", err)
	}
	entries, err := os.ReadDir(backupplan.ArchiveDirectory(target.Repository))
	if err != nil {
		t.Fatalf("read the archive directory of the rejected backup: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a rejected archive left files: %v", entries)
	}
}

func TestArchiveRetentionCalendarBuckets(t *testing.T) {
	zone := time.FixedZone("test", 3600)
	points := []archiveRetentionPoint{
		{id: "new", at: time.Date(2026, 11, 24, 23, 20, 0, 0, time.UTC)},
		{id: "duplicate-day", at: time.Date(2026, 11, 24, 23, 1, 0, 0, time.UTC)},
		{id: "prior-day", at: time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)},
		{id: "prior-week", at: time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)},
		{id: "prior-month", at: time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)},
		{id: "stale", at: time.Date(2026, 9, 24, 20, 0, 0, 0, time.UTC)},
	}
	if got := expiredArchives(points, zone, 2, 2, 2); !slices.Equal(got, []string{"duplicate-day", "stale"}) {
		t.Fatalf("unexpected expiry: %v", got)
	}
}

func TestArchiveRetentionKeepsTheArchiveThisRunWrote(t *testing.T) {
	zone := time.UTC
	// An imported archive captured later the same day sorts ahead of the new
	// backup; the new backup still claims its buckets and stays.
	points := []archiveRetentionPoint{
		{id: "imported", at: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)},
		{id: "new", at: time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)},
		{id: "older", at: time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)},
	}
	if got := expiredArchives(keepFirst(points, "new"), zone, 1, 1, 1); !slices.Equal(got, []string{"imported", "older"}) {
		t.Fatalf("unexpected expiry with the new archive protected: %v", got)
	}
	if got := expiredArchives(keepFirst(points, ""), zone, 1, 1, 1); !slices.Equal(got, []string{"new", "older"}) {
		t.Fatalf("without protection the newest capture claims the buckets: %v", got)
	}
	if got := keepFirst(points, "missing"); !slices.Equal(got, points) {
		t.Fatalf("an unknown keep reference reordered points: %v", got)
	}
}

func TestArchiveExtractRequiresEachMemberOnce(t *testing.T) {
	for name, entries := range map[string][]string{
		"missing manifest": {"database.dump", "security-keys.json"},
		"duplicate dump":   {"manifest.json", "database.dump", "database.dump", "security-keys.json"},
	} {
		var data bytes.Buffer
		writer := tar.NewWriter(&data)
		for _, entry := range entries {
			if err := writer.WriteHeader(&tar.Header{Name: entry, Typeflag: tar.TypeReg, Size: 1, Mode: 0600}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		err := extractArchivePlain(context.Background(), &data, t.TempDir())
		if err == nil || storageFailure(err) {
			t.Fatalf("%s: got %v, want a file defect rather than a storage failure", name, err)
		}
	}
}

func TestCheckPortableConfirmsCorruptionOnlyWhenTheFileOpens(t *testing.T) {
	ctx := context.Background()
	stage, repo := t.TempDir(), t.TempDir()
	t.Setenv("RESTIC_REPOSITORY", "/data/backups")
	t.Setenv("RESTIC_PASSWORD", "test backup file encryption key")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	target, err := config.LocalBackupTarget()
	if err != nil {
		t.Fatal(err)
	}
	target.Repository = repo
	for _, name := range []string{"manifest.json", "database.dump", "security-keys.json"} {
		if err = os.WriteFile(filepath.Join(stage, name), []byte("synthetic "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id := backupplan.ArchiveID(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	if _, err = writePortable(ctx, target, id, stage, stage, nil); err != nil {
		t.Fatal(err)
	}
	if err = checkPortable(ctx, target, id); err != nil {
		t.Fatalf("intact file: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = checkPortable(canceled, target, id); ResultCode(err) != "canceled" {
		t.Fatalf("canceled check: got %q (%v), want canceled", ResultCode(err), err)
	}
	t.Setenv("RESTIC_PASSWORD", "another backup file encryption key")
	wrong, err := config.LocalBackupTarget()
	if err != nil {
		t.Fatal(err)
	}
	wrong.Repository = repo
	if err = checkPortable(ctx, wrong, id); ResultCode(err) != "archive_unreadable" || errors.Is(err, errArchiveCorrupt) {
		t.Fatalf("wrong password must stay ambiguous: %q, %v", ResultCode(err), err)
	}
	path := filepath.Join(backupplan.ArchiveDirectory(repo), backupplan.ArchiveName(id))
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if err = os.WriteFile(path, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	if err = checkPortable(ctx, target, id); ResultCode(err) != "archive_unreadable" || !errors.Is(err, errArchiveCorrupt) {
		t.Fatalf("tampered content must be confirmed corrupt: %q, %v", ResultCode(err), err)
	}
}
