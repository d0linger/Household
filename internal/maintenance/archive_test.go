package maintenance

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	for _, header := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "uploads/link", Typeflag: tar.TypeSymlink, Linkname: "manifest.json"},
	} {
		var data bytes.Buffer
		writer := tar.NewWriter(&data)
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := extractArchivePlain(context.Background(), &data, t.TempDir()); err == nil {
			t.Fatalf("accepted unapproved entry %q", header.Name)
		}
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
