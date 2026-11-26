package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/config"
	"household/internal/database"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Service) importArchive(ctx context.Context, local config.BackupTarget, job Job) error {
	exchange := backupplan.ExchangeDirectory()
	name := backupplan.ExchangeName("import", job.Snapshot)
	source, err := openArchiveFile(exchange, name)
	if err != nil {
		return archiveStorageError(err)
	}
	defer func() { _ = source.Close() }()
	defer func() { _ = os.Remove(filepath.Join(exchange, name)) }() // #nosec G703 -- validated import reference
	unlock, err := lockRepository(ctx, s.Pool())
	if err != nil {
		return err
	}
	defer unlock()
	stage, err := os.MkdirTemp(workDirectory(), "archive-import-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }() // #nosec G703 -- exact private work directory
	// The app can write to Exchange. Keep a stable copy under the worker's
	// private root so the published bytes always come from the tested archive.
	const stagedName = "import.age"
	if _, err = writeArchiveFile(stage, stagedName, func(f *os.File) error {
		_, copyErr := copyArchive(ctx, f, source)
		return copyErr
	}); err != nil {
		return err
	}
	staged, err := openArchiveFile(stage, stagedName)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()
	passphrase := local.ArchivePassword()
	plain, err := decryptArchive(ctx, staged, passphrase)
	if err != nil {
		// An offsite file uses the offsite password. Read it without the
		// transport settings, which a replacement installation may lack.
		fallback, configErr := config.OffsiteArchivePassword()
		if configErr != nil || fallback == "" || fallback == passphrase || contextFailure(err) {
			return err
		}
		passphrase = fallback
		if _, err = staged.Seek(0, io.SeekStart); err != nil {
			return err
		}
		plain, err = decryptArchive(ctx, staged, passphrase)
		if err != nil {
			return err
		}
	}
	// Archive authentication happens before executing the PostgreSQL dump.
	// The restore uses the configured installation passwords only.
	var manifest Manifest
	// content holds a failure while the archive's own content was checked.
	// Preparing, connecting to and removing the trial database are not part
	// of it, so their failures keep their own result.
	var content error
	err = CheckRestore(ctx, s.Config.DatabaseURL, s.Superuser, func(targetURL string) error {
		result, restoreErr := restoreWithPayload(ctx, targetURL, true, func(dir string) error {
			return extractArchivePlain(ctx, plain, dir)
		})
		if result != nil {
			defer func() { _ = result.Remove() }()
		}
		if restoreErr != nil {
			content = restoreErr
			return restoreErr
		}
		verified, openErr := database.Open(ctx, targetURL, 2)
		if openErr != nil {
			return openErr
		}
		defer verified.Close()
		if openErr = liveKeysReadStage(ctx, verified, s.Config); openErr != nil {
			content = openErr
			return openErr
		}
		if openErr = attachmentsMatchManifest(ctx, verified, result.Manifest.Files); openErr != nil {
			content = openErr
			return openErr
		}
		manifest = result.Manifest
		if manifest.Created.IsZero() {
			content = errors.New("archive does not record its capture date")
			return content
		}
		return nil
	})
	if err != nil {
		// Only a payload that fails validation is an invalid archive. A full or
		// unwritable restore volume and an unavailable trial database keep their
		// own error, so the operator can fix them and upload the same file again.
		if content != nil && ResultCode(content) == "failed" && !contextFailure(content) &&
			!storageFailure(content) && !databaseServerFailure(content) {
			return &ToolError{Program: "archive", Code: "archive_invalid", err: err}
		}
		return err
	}
	if _, err = staged.Seek(0, io.SeekStart); err != nil {
		return err
	}
	size, err := writeArchiveFile(backupplan.ArchiveDirectory(local.Repository), backupplan.ArchiveName(job.Snapshot), func(f *os.File) error {
		if passphrase != local.ArchivePassword() {
			return reencryptArchive(ctx, f, staged, passphrase, local.ArchivePassword())
		}
		_, copyErr := copyArchive(ctx, f, staged)
		return copyErr
	})
	if err != nil {
		return err
	}
	schemas, err := json.Marshal(manifest.Schemas)
	if err != nil {
		return err
	}
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), job.ID)
	_, err = audit.Exec(ctx, s.Pool(), `INSERT INTO core.backup_runs
 (id,state,application_version,repository_reference,repository_kind,job_id,started_at,finished_at,verified_at,snapshot_reference,schema_manifest,total_bytes)
 VALUES($1,'complete',$2,$3,'local',$4,$5,now(),now(),$6,$7,$8)`,
		value.UUID(), manifest.Version, local.Label(), job.ID, manifest.Created, job.Snapshot, schemas, size)
	return err
}

// prepareArchiveDownload verifies the remote file while streaming its encrypted
// bytes to the exchange. The file becomes visible only after a successful probe.
func (s *Service) prepareArchiveDownload(ctx context.Context, target config.BackupTarget, snapshot string) error {
	pool := s.Pool()
	unlock, err := lockRepository(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	if err = knownSnapshot(ctx, pool, target, snapshot); err != nil {
		return err
	}
	source, err := openPortable(ctx, target, snapshot)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	_, err = writeArchiveFile(backupplan.ExchangeDirectory(), backupplan.DownloadName(snapshot, target.Label()), func(f *os.File) error {
		secured := &archiveWriter{Writer: f, remaining: backupplan.ArchiveLimit}
		return CheckRestore(ctx, s.Config.DatabaseURL, s.Superuser, func(targetURL string) error {
			result, verifyErr := restoreWithPayload(ctx, targetURL, false, func(dir string) error {
				return extractArchive(ctx, io.TeeReader(source, secured), target.ArchivePassword(), dir)
			})
			if result != nil {
				_ = result.Remove()
			}
			return verifyErr
		})
	})
	if err != nil {
		return err
	}
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	_, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET verified_at=now() WHERE snapshot_reference=$1 AND repository_reference=$2 AND state='complete'", snapshot, target.Label())
	return err
}

func cleanArchiveExchange(now time.Time) {
	directory := backupplan.ExchangeDirectory()
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !backupplan.ManagedExchangeName(entry.Name()) {
			continue
		}
		if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > 6*time.Hour {
			_ = os.Remove(filepath.Join(directory, entry.Name())) // #nosec G703 -- exact managed exchange filename
		}
	}
}

// databaseServerFailure reports a failed connection or a PostgreSQL error of
// the server itself (connection, resources, operator intervention, system),
// which a restored archive cannot cause.
func databaseServerFailure(err error) bool {
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || len(pgErr.Code) < 2 {
		return false
	}
	switch pgErr.Code[:2] {
	case "08", "53", "57", "58":
		return true
	}
	return false
}

// storageFailure reports a filesystem error of the worker's own volumes.
func storageFailure(err error) bool {
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	var syscallErr *os.SyscallError
	return errors.As(err, &pathErr) || errors.As(err, &linkErr) || errors.As(err, &syscallErr)
}

// attachmentsMatchManifest requires that the restored attachment rows are
// exactly the files the manifest lists, by key, hash and size. The writer builds
// the manifest from the same query, so only a crafted archive can differ.
func attachmentsMatchManifest(ctx context.Context, db *pgxpool.Pool, files []File) error {
	rows, err := db.Query(ctx, attachmentInventoryQuery)
	if err != nil {
		return err
	}
	restored, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (File, error) {
		var f File
		err := row.Scan(&f.Key, &f.SHA256, &f.Size)
		return f, err
	})
	if err != nil {
		return err
	}
	listed := slices.Clone(files)
	byKey := func(a, b File) int { return strings.Compare(a.Key, b.Key) }
	slices.SortFunc(restored, byKey)
	slices.SortFunc(listed, byKey)
	if !slices.EqualFunc(restored, listed, func(a, b File) bool {
		return a.Key == b.Key && a.SHA256 == b.SHA256 && a.Size == b.Size
	}) {
		return errors.New("restored attachment records differ from the archive manifest")
	}
	return nil
}
