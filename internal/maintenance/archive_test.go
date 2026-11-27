package maintenance

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/config"

	"github.com/jackc/pgx/v5/pgconn"
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

// Server and storage failures of pg_dump and pg_restore keep their own code,
// so a valid upload is never reported as an invalid archive; any other tool
// failure, such as a corrupt dump, stays unclassified.
func TestDatabaseToolFailuresKeepTheirCause(t *testing.T) {
	for stderr, want := range map[string]string{
		"pg_restore: error: could not execute query: ERROR:  could not extend file \"base/1/2\": No space left on device":                  "disk_full",
		"pg_restore: error: connection to server at \"db\" (10.0.0.2), port 5432 failed: FATAL:  the database system is shutting down":     "database_unavailable",
		"pg_restore: error: could not execute query: server closed the connection unexpectedly":                                            "database_unavailable",
		"pg_restore: error: could not execute query: ERROR:  out of memory":                                                                "database_unavailable",
		"pg_restore: error: could not execute query: ERROR:  could not write to file \"base/pgsql_tmp/t1\": No space left on device":       "disk_full",
		"pg_dump: error: connection to server on socket \"/run/postgresql/.s.PGSQL.5432\" failed: FATAL:  sorry, too many clients already": "database_unavailable",
		// Quoted dump data and context lines must not select a code.
		"pg_restore: error: could not execute query: ERROR:  invalid input syntax for type integer: \"out of memory\"":                "",
		"pg_restore: error: could not execute query: ERROR:  value too long for type character varying(5): \"could not extend file\"": "",
		"CONTEXT:  COPY items, line 1, column name: \"disk quota exceeded\"\nCommand was: COPY items FROM stdin;":                     "",
		"pg_restore: error: could not execute query: ERROR:  could not extend file \"base/1/2\": Input/output error":                  "database_storage_failed",
		"pg_restore: error: could not read input file: end of file":                                                                   "",
		"pg_restore: error: could not execute query: ERROR:  syntax error at or near \"x\"":                                           "",
		// pg_restore's COPY wrapper names a dump-controlled table tag.
		"pg_restore: error: COPY failed for table \"items\": ERROR:  out of memory":                                                                "database_unavailable",
		"pg_restore: error: COPY failed for table \"items\": ERROR:  could not extend file \"base/1/2\": Input/output error":                       "database_storage_failed",
		"pg_restore: error: COPY failed for table \"items\": ERROR:  could not extend file \"base/1/2\": No space left on device":                  "disk_full",
		"pg_restore: error: COPY failed for table \"items\": server closed the connection unexpectedly":                                            "database_unavailable",
		"pg_restore: error: COPY failed for table \"items\": ERROR:  invalid input syntax for type integer: \"out of memory\"":                     "",
		"pg_restore: error: COPY failed for table \"x\": ERROR:  out of memory\": ERROR:  invalid input syntax for type integer: \"1\"":            "",
		"pg_restore: error: COPY failed for table \"x\": out of memory\": server closed the connection unexpectedly":                               "",
		"pg_restore: error: COPY failed for table \"x\": out of memory\": ERROR:  invalid input syntax for type integer: \"1\"":                    "",
		"pg_restore: error: COPY failed for table \"x\": ERROR:  disk quota exceeded\": server closed the connection unexpectedly":                 "",
		"pg_restore: error: COPY failed for table \"x\": ERROR:  disk quota exceeded\": could not send data to server":                             "",
		"pg_restore: error: COPY failed for table \"items\": ERROR:  could not extend file \"base/1/2\": wrote only 4096 of 8192 bytes at block 3": "database_storage_failed",
		"pg_restore: error: COPY failed for table \"items\": ERROR:  could not write to file \"base/1/2\": Disk quota exceeded":                    "disk_full",
	} {
		if got := classifyDatabaseToolOutput([]byte(stderr)); got != want {
			t.Errorf("%q classified as %q, want %q", stderr, got, want)
		}
	}
	const private = "private-diagnostic-7f3a /var/lib/postgresql: No space left on device"
	err := &ToolError{Program: "pg_restore", Code: "disk_full", err: errors.New(private)}
	if ResultCode(err) != "disk_full" || strings.Contains(err.Error(), "private-diagnostic-7f3a") || strings.Contains(err.Error(), "No space") {
		t.Fatalf("classified tool error = %q, %q", ResultCode(err), err.Error())
	}
}

// An unreadable offsite password setting stops the import retry with its own
// code and keeps the path private. A canceled run and a missing offsite
// password keep their own result.
func TestImportRetryReportsAnUnreadableOffsitePassword(t *testing.T) {
	unreadable := &ToolError{Program: "archive", Code: "archive_unreadable", err: errors.New("no identity matched")}
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD", "")
	missing := filepath.Join(t.TempDir(), "private-offsite-password-path")
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD_FILE", missing)
	if _, err := offsiteRetryPassword(unreadable, "local-password-123"); ResultCode(err) != "password_unavailable" || strings.Contains(err.Error(), "private-offsite-password-path") {
		t.Fatalf("unreadable setting = %v (%s)", err, ResultCode(err))
	}
	if _, err := offsiteRetryPassword(context.Canceled, "local-password-123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run = %v", err)
	}
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD_FILE", "")
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD", "offsite-password-123")
	if fallback, err := offsiteRetryPassword(unreadable, "local-password-123"); err != nil || fallback != "offsite-password-123" {
		t.Fatalf("configured fallback = %q, %v", fallback, err)
	}
	if _, err := offsiteRetryPassword(unreadable, "offsite-password-123"); ResultCode(err) != "archive_unreadable" {
		t.Fatalf("same password retried: %v", err)
	}
}

// A database tool that cannot start reports its own code, never an invalid
// archive.
func TestDatabaseToolThatCannotStartKeepsItsCode(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := command(context.Background(), "pg_restore", []string{"--list"}, ""); ResultCode(err) != "tool_unavailable" {
		t.Fatalf("missing pg_restore = %v (%s)", err, ResultCode(err))
	}
}

// A mounted offsite path that resolves to the local repository is rejected,
// although the configured strings differ; distinct directories stay valid.
func TestOffsiteStorageMustDifferFromTheLocalDirectory(t *testing.T) {
	directory := t.TempDir()
	local := config.BackupTarget{Kind: "local", Repository: directory}
	aliased := config.BackupTarget{Kind: "offsite", Repository: directory + string(filepath.Separator) + "."}
	if err := distinctArchiveStorage(local, aliased); ResultCode(err) != "same_as_local" {
		t.Fatalf("aliased offsite path = %v", err)
	}
	separate := config.BackupTarget{Kind: "offsite", Repository: t.TempDir()}
	if err := distinctArchiveStorage(local, separate); err != nil {
		t.Fatalf("separate directory rejected: %v", err)
	}
	absent := config.BackupTarget{Kind: "offsite", Repository: filepath.Join(directory, "absent")}
	if err := distinctArchiveStorage(local, absent); err != nil {
		t.Fatalf("absent directory rejected: %v", err)
	}
}

// Only a failed local retention lets the offsite copy follow; a failure to
// write or check the new snapshot stops before the copy.
func TestOffsiteCopyFollowsOnlyAfterLocalRetentionFails(t *testing.T) {
	retention := &localRetentionError{err: &ToolError{Program: "archive", Code: "repository_unreachable", err: errors.New("delete refused")}}
	for err, want := range map[error]bool{
		nil:                                  true,
		retention:                            true,
		fmt.Errorf("wrapped: %w", retention): true,
		errors.New("check failed"):           false,
		&ToolError{Program: "archive", Code: "disk_full", err: errors.New("full")}: false,
	} {
		if got := copyAfterLocalBackup(err); got != want {
			t.Errorf("copyAfterLocalBackup(%v) = %v, want %v", err, got, want)
		}
	}
	if ResultCode(retention) != "repository_unreachable" {
		t.Fatalf("retention failure lost its code: %q", ResultCode(retention))
	}
}

// A failed local retention keeps its code when the offsite copy fails too:
// the local run is complete, so only the job's code shows the failure to local
// health. The offsite failure stays in the chain.
func TestBackupJobErrorKeepsAFailedLocalRetentionVisible(t *testing.T) {
	retention := &localRetentionError{err: &ToolError{Program: "archive", Code: "repository_unreachable", err: errors.New("delete refused")}}
	offsite := &ToolError{Program: "restic", Code: "access_denied", err: errors.New("denied")}
	for _, c := range []struct {
		name           string
		local, offsite error
		want           string
		offsiteInChain bool
	}{
		{"neither", nil, nil, "ok", false},
		{"offsite only", nil, offsite, "offsite_access_denied", true},
		{"retention only", retention, nil, "repository_unreachable", false},
		{"both", retention, offsite, "repository_unreachable", true},
	} {
		err := backupJobError(c.local, c.offsite)
		if got := ResultCode(err); got != c.want {
			t.Errorf("%s: code %q, want %q", c.name, got, c.want)
		}
		inChain := false
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			inChain = slices.ContainsFunc(joined.Unwrap(), func(e error) bool { return ResultCode(e) == "offsite_access_denied" })
		}
		if inChain != c.offsiteInChain {
			t.Errorf("%s: offsite code in chain = %v, want %v", c.name, inChain, c.offsiteInChain)
		}
		var kept *localRetentionError
		if errors.As(err, &kept) != (c.local != nil) {
			t.Errorf("%s: local retention failure lost from the chain", c.name)
		}
	}
}

func TestArchiveExtractRequiresEachMemberOnce(t *testing.T) {
	const listed, unlisted = "uploads/6b1f4c2e-8a3d-4e5f-9b70-1c2d3e4f5a6b", "uploads/0f9e8d7c-6b5a-4c3d-8e2f-1a0b9c8d7e6f"
	manifest, err := json.Marshal(Manifest{ArchiveFormat: 1, Version: "test", Created: time.Now(), UploadPath: "/uploads", Crypto: &CryptoReport{},
		Files: []File{{Key: strings.TrimPrefix(listed, "uploads/"), SHA256: strings.Repeat("0", 64), Size: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, want string
		entries    []string
	}{
		{"missing manifest", "encrypted backup lacks manifest.json", []string{"database.dump", "security-keys.json"}},
		{"duplicate dump", "unexpected entry in encrypted backup", []string{"manifest.json", "database.dump", "database.dump", "security-keys.json"}},
		{"missing listed attachment", "encrypted backup lacks an attachment its manifest lists", []string{"manifest.json", "database.dump", "security-keys.json"}},
		{"unlisted attachment", "encrypted backup contains an attachment its manifest does not list", []string{"manifest.json", "database.dump", "security-keys.json", listed, unlisted}},
		{"complete", "", []string{"manifest.json", "database.dump", listed, "security-keys.json"}},
	} {
		var data bytes.Buffer
		writer := tar.NewWriter(&data)
		for _, entry := range test.entries {
			content := []byte("x")
			if entry == "manifest.json" {
				content = manifest
			}
			if err = writer.WriteHeader(&tar.Header{Name: entry, Typeflag: tar.TypeReg, Size: int64(len(content)), Mode: 0600}); err != nil {
				t.Fatal(err)
			}
			if _, err = writer.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		err = extractArchivePlain(context.Background(), &data, t.TempDir())
		if test.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", test.name, err)
			}
			continue
		}
		if err == nil || err.Error() != test.want || storageFailure(err) {
			t.Fatalf("%s: got %v, want %q as a file defect", test.name, err, test.want)
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

func TestDatabaseServerFailureLeavesContentErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "53100"}, true},  // disk full
		{&pgconn.PgError{Code: "57P01"}, true},  // admin shutdown
		{&pgconn.PgError{Code: "08006"}, true},  // connection failure
		{&pgconn.PgError{Code: "42P01"}, false}, // a restored table is missing
		{errors.New("restored attachment records differ from the archive manifest"), false},
	} {
		if got := databaseServerFailure(fmt.Errorf("check: %w", test.err)); got != test.want {
			t.Fatalf("%v: got %v, want %v", test.err, got, test.want)
		}
	}
}

// A safety backup that does not fit an encrypted backup file by count or by
// size is written to restic instead.
func TestArchiveFitsCountsAttachmentsAndSize(t *testing.T) {
	if !archiveFits(nil) || !archiveFits([]File{{Size: backupplan.ArchiveLimit}}) {
		t.Fatal("a backup within the limits does not fit")
	}
	if archiveFits(make([]File, archiveAttachmentLimit+1)) {
		t.Fatal("too many attachments fit")
	}
	if archiveFits([]File{{Size: backupplan.ArchiveLimit}, {Size: 1}}) {
		t.Fatal("attachments beyond the size limit fit")
	}
}

func TestS3ArchiveIdentityIgnoresOnlySpelling(t *testing.T) {
	const name = "household-2026-11-27T10-00-00Z-x.tar.age"
	base, ok := s3ArchiveIdentity("s3:https://storage.example/bucket/prefix", name)
	if !ok {
		t.Fatal("no identity for a valid S3 repository")
	}
	for _, same := range []string{
		"s3:https://storage.example/bucket/prefix/",
		"s3:https://STORAGE.example:443/bucket/prefix",
	} {
		if got, _ := s3ArchiveIdentity(same, name); got != base {
			t.Errorf("%s addresses %q, want %q", same, got, base)
		}
	}
	for _, distinct := range []string{
		"s3:https://storage.example/bucket/other",
		"s3:https://storage.example/other/prefix",
		"s3:https://storage.example/bucket",
		"s3:https://storage.example:9000/bucket/prefix",
		"s3:http://storage.example/bucket/prefix",
	} {
		if got, _ := s3ArchiveIdentity(distinct, name); got == base {
			t.Errorf("%s addresses the same object as the base repository", distinct)
		}
	}
	// One explicit port served over both schemes names two destinations.
	plain, _ := s3ArchiveIdentity("s3:http://storage.example:9000/bucket/prefix", name)
	secure, _ := s3ArchiveIdentity("s3:https://storage.example:9000/bucket/prefix", name)
	if plain == secure {
		t.Error("http and https on one port address the same object")
	}
	if _, ok = s3ArchiveIdentity("/srv/offsite", name); ok {
		t.Error("a mounted path has an S3 identity")
	}
}
