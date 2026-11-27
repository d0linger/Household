package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/buildinfo"
	"household/internal/config"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func archiveObject(target config.BackupTarget, name string) (*minio.Client, string, string, error) {
	s3, ok := target.S3Connection()
	if !ok {
		return nil, "", "", errors.New("backup storage is not S3")
	}
	repository, err := url.Parse(strings.TrimPrefix(target.Repository, "s3:"))
	if err != nil {
		return nil, "", "", err
	}
	_, prefix, _ := strings.Cut(strings.TrimPrefix(repository.Path, "/"), "/")
	client, err := minio.New(s3.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3.AccessKey, s3.SecretKey, ""),
		Secure: s3.Secure, Region: s3.Region, BucketLookup: minio.BucketLookupPath,
	})
	return client, s3.Bucket, path.Join(prefix, name), err
}

// s3ArchiveIdentity names the object that archiveObject addresses for name
// under an s3: repository, independent of its spelling: the host is compared
// without case and with its default port, and a trailing slash or a repeated
// separator in the prefix names the same key.
func s3ArchiveIdentity(repository, name string) (string, bool) {
	raw, found := strings.CutPrefix(repository, "s3:")
	if !found || name == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	bucket, prefix, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	if bucket == "" || port == "" {
		return "", false
	}
	return strings.ToLower(u.Hostname()) + ":" + port + "/" + bucket + "/" + path.Join(prefix, name), true
}

func archiveStorageError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	response := minio.ToErrorResponse(err)
	code := "repository_unreachable"
	switch {
	case errors.Is(err, os.ErrNotExist), response.Code == "NoSuchKey", response.Code == "NotFound":
		code = "archive_missing"
	case response.Code == "NoSuchBucket":
		code = "bucket_missing"
	case response.StatusCode == 401 || response.StatusCode == 403:
		code = "access_denied"
	}
	return &ToolError{Program: "archive", Code: code, err: err}
}

func openArchiveFile(directory, name string) (*os.File, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		// A missing or unmounted directory is a storage failure, not proof
		// that one archive is missing, so the error does not wrap ErrNotExist.
		return nil, fmt.Errorf("open backup storage directory: %v", err)
	}
	defer func() { _ = root.Close() }()
	f, info, err := openRegular(root, name)
	if errors.Is(err, errNotRegularFile) {
		return nil, errors.New("unexpected encrypted backup size or type")
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > backupplan.ArchiveLimit {
		_ = f.Close()
		return nil, errors.New("unexpected encrypted backup size or type")
	}
	return f, nil
}

func openPortable(ctx context.Context, target config.BackupTarget, id string) (io.ReadCloser, error) {
	name := backupplan.ArchiveName(id)
	if name == "" {
		return nil, errors.New("invalid encrypted backup reference")
	}
	if filepath.IsAbs(target.Repository) {
		file, err := openArchiveFile(backupplan.ArchiveDirectory(target.Repository), name)
		return file, archiveStorageError(err)
	}
	client, bucket, key, err := archiveObject(target, name)
	if err != nil {
		return nil, archiveStorageError(err)
	}
	obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, archiveStorageError(err)
	}
	info, err := obj.Stat()
	if err != nil || info.Size > backupplan.ArchiveLimit {
		_ = obj.Close()
		if err == nil {
			err = errors.New("encrypted object exceeds archive limit")
		}
		return nil, archiveStorageError(err)
	}
	return obj, nil
}

func checkPortable(ctx context.Context, target config.BackupTarget, id string) error {
	src, err := openPortable(ctx, target, id)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	source := &sourceReader{Reader: src}
	reader, err := decryptArchive(ctx, source, target.ArchivePassword())
	if err != nil {
		return err
	}
	if _, err = copyArchive(ctx, io.Discard, reader); err != nil {
		if contextFailure(err) {
			return err
		}
		// The password opened the file and the storage returned every byte, so
		// the failed authentication is a defect of the stored file itself.
		if source.err == nil && !errors.Is(err, errArchiveTooLarge) {
			err = errors.Join(errArchiveCorrupt, err)
		}
		return &ToolError{Program: "archive", Code: "archive_unreadable", err: err}
	}
	return nil
}

func reencryptArchive(ctx context.Context, dst io.Writer, src io.Reader, from, to string) error {
	reader, err := decryptArchive(ctx, src, from)
	if err != nil {
		return err
	}
	encrypted, err := encryptArchive(&archiveWriter{Writer: dst, remaining: backupplan.ArchiveLimit}, to)
	if err != nil {
		return err
	}
	if _, err = copyArchive(ctx, encrypted, reader); err != nil {
		return err
	}
	return encrypted.Close()
}

func sendArchive(ctx context.Context, local, offsite config.BackupTarget, id string) (int64, error) {
	source, err := openPortable(ctx, local, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = source.Close() }()
	name := backupplan.ArchiveName(id)
	write := func(f *os.File) error {
		return reencryptArchive(ctx, f, source, local.ArchivePassword(), offsite.ArchivePassword())
	}
	if filepath.IsAbs(offsite.Repository) {
		return writeArchiveFile(backupplan.ArchiveDirectory(offsite.Repository), name, write)
	}
	// Stage ciphertext so S3 gets a bounded upload with a known length.
	stage, err := os.MkdirTemp(workDirectory(), "archive-copy-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(stage) }() // #nosec G703 -- this job's exact temporary directory
	size, err := writeArchiveFile(stage, name, write)
	if err != nil {
		return 0, err
	}
	staged, err := openArchiveFile(stage, name)
	if err != nil {
		return 0, err
	}
	defer func() { _ = staged.Close() }()
	client, bucket, key, err := archiveObject(offsite, name)
	if err != nil {
		return 0, archiveStorageError(err)
	}
	_, err = client.PutObject(ctx, bucket, key, staged, size, minio.PutObjectOptions{ContentType: "application/octet-stream", NumThreads: 1})
	return size, archiveStorageError(err)
}

func copyPortableLocked(ctx context.Context, pool *pgxpool.Pool, local, offsite config.BackupTarget, settings backupplan.Settings, snapshot string, schemas []byte, jobID string) (string, error) {
	if filepath.IsAbs(offsite.Repository) {
		if err := distinctArchiveStorage(local, offsite); err != nil {
			return "", err
		}
		directory, err := mountedArchiveDirectory(ctx, pool, offsite)
		if err != nil {
			return "", err
		}
		// A worker that stopped during a write can leave its partial file on
		// the mount. This is best effort, like the cleanups below.
		if err = removeStalePartials(directory, time.Now()); err != nil {
			slog.Default().Warn("could not remove interrupted offsite writes before the copy", "code", ResultCode(err))
		}
	}
	// Pending deletions and files of earlier failed copies are removed first,
	// so they cannot take the space this copy needs. This is best effort: a
	// refused delete does not block the copy, and the cleanup after the copy
	// reports it. The pending runs are decided again first; without that
	// decision nothing is deleted, because a pending row may be one the
	// quotas keep.
	decideErr := decideArchives(ctx, pool, offsite, "", false)
	if decideErr != nil {
		slog.Default().Warn("could not decide pending offsite deletions before the copy", "code", ResultCode(decideErr))
	} else if err := finishArchiveRetention(ctx, pool, offsite, ""); err != nil {
		slog.Default().Warn("could not finish pending offsite deletions before the copy", "code", ResultCode(err))
	}
	// A pending deletion of this snapshot finishes before its name is reused.
	// If the decision failed, a pending row of this snapshot stops the copy.
	if decideErr != nil {
		var pending bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs
 WHERE repository_reference=$1 AND snapshot_reference=$2 AND state='retention_pending')`, offsite.Label(), snapshot).Scan(&pending); err != nil || pending {
			return "", errors.Join(decideErr, err)
		}
	} else if err := finishArchiveRetention(ctx, pool, offsite, snapshot); err != nil {
		return "", err
	}
	if err := reconcileOffsiteAttempts(ctx, pool, offsite, snapshot); err != nil {
		slog.Default().Warn("could not remove earlier offsite copy attempts before the copy", "code", ResultCode(err))
	}
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs
 WHERE repository_kind='offsite' AND repository_reference=$1 AND snapshot_reference=$2 AND state='complete')`,
		offsite.Label(), snapshot).Scan(&exists)
	if err != nil {
		return "", err
	}
	if exists {
		err = checkPortable(ctx, offsite, snapshot)
		var tool *ToolError
		if errors.As(err, &tool) && (tool.Code == "archive_missing" || errors.Is(err, errArchiveCorrupt)) {
			// The recorded copy is confirmed absent or corrupt: send it again
			// from the local file and keep the existing run. Re-encryption
			// authenticates the whole local file before anything is published,
			// so a defective source never replaces the copy. Access, password
			// and connection failures are returned as such.
			corrupt := errors.Is(err, errArchiveCorrupt)
			_, err = sendArchive(ctx, local, offsite, snapshot)
			if corrupt && outOfSpace(err) && filepath.IsAbs(offsite.Repository) {
				err = replaceCorruptCopy(ctx, local, offsite, snapshot)
			}
			if err == nil {
				err = checkPortable(ctx, offsite, snapshot)
			}
		}
		// A readable file is not yet a durable one: an earlier publication
		// may have failed its directory sync after the rename.
		if err == nil && filepath.IsAbs(offsite.Repository) {
			err = syncDirectory(backupplan.ArchiveDirectory(offsite.Repository))
		}
		if err != nil {
			return "", err
		}
	} else if err = sendPortableCopy(ctx, pool, local, offsite, snapshot, schemas, jobID); err != nil {
		return "", err
	}
	err = errors.Join(reconcileOffsiteAttempts(ctx, pool, offsite, snapshot),
		applyArchiveRetention(ctx, pool, offsite, ""))
	// Retention can expire the new copy when newer captures already fill its
	// calendar buckets. That copy is gone, so it is not reported as a success.
	var retained bool
	if retainedErr := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs
 WHERE repository_kind='offsite' AND repository_reference=$1 AND snapshot_reference=$2 AND state='complete')`,
		offsite.Label(), snapshot).Scan(&retained); retainedErr != nil {
		return "", errors.Join(err, retainedErr)
	}
	if !retained {
		// The lost copy is the outcome; a cleanup failure stays a secondary cause.
		return "", errors.Join(&ToolError{Program: "archive", Code: "copy_not_retained", err: errCopyNotRetained}, err)
	}
	return snapshot, err
}

var errCopyNotRetained = errors.New("offsite retention removed the copied snapshot in the same run")

func outOfSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// replaceCorruptCopy repairs a mounted copy whose replacement does not fit
// beside it. The copy is confirmed corrupt, so it holds no restorable data.
// The local file is authenticated first, and the corrupt copy is removed only
// then, so a defective source never costs the offsite copy.
func replaceCorruptCopy(ctx context.Context, local, offsite config.BackupTarget, snapshot string) error {
	if err := checkPortable(ctx, local, snapshot); err != nil {
		return err
	}
	if err := removePortable(ctx, offsite, backupplan.ArchiveName(snapshot)); err != nil {
		return err
	}
	_, err := sendArchive(ctx, local, offsite, snapshot)
	return err
}

// sendPortableCopy records the copy as running before anything is uploaded, so
// an upload whose check or registration fails stays referenced by a run and
// reconcileOffsiteAttempts can remove it later.
func sendPortableCopy(ctx context.Context, pool *pgxpool.Pool, local, offsite config.BackupTarget, snapshot string, schemas []byte, jobID string) error {
	run := value.UUID()
	tag, err := audit.Exec(ctx, pool, `INSERT INTO core.backup_runs
 (id,state,application_version,repository_reference,repository_kind,job_id,snapshot_reference,schema_manifest)
 SELECT $1,'running',$2,$3,'offsite',$4,$5,$6 WHERE NOT EXISTS(SELECT 1 FROM core.backup_runs
 WHERE repository_kind='offsite' AND repository_reference=$3 AND snapshot_reference=$5 AND state IN('complete','retention_pending'))`,
		run, buildinfo.Label(), offsite.Label(), nullableUUID(jobID), snapshot, schemas)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("offsite copy of this snapshot is already recorded")
	}
	finished := false
	defer func() {
		if !finished {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_, _ = audit.Exec(failureCtx, pool, "UPDATE core.backup_runs SET state='failed',finished_at=now(),failure_reason='offsite copy failed' WHERE id=$1 AND state='running'", run)
		}
	}()
	// A file that a run of another label needs is never replaced.
	shared, err := sharedArchiveFile(ctx, pool, offsite, snapshot, "complete", "retention_pending")
	if err != nil {
		return err
	}
	var size int64
	if shared {
		size, err = reuseSharedCopy(ctx, offsite, snapshot)
	} else {
		removeStaleCopy(ctx, pool, offsite, snapshot)
		if size, err = sendArchive(ctx, local, offsite, snapshot); err == nil {
			err = checkPortable(ctx, offsite, snapshot)
		}
	}
	if err != nil {
		return err
	}
	if _, err = audit.Exec(ctx, pool, "UPDATE core.backup_runs SET state='complete',finished_at=now(),data_added=$2,total_bytes=$2 WHERE id=$1", run, size); err != nil {
		return err
	}
	finished = true
	return nil
}

var errArchiveShared = errors.New("another offsite path needs the existing backup file, and this target's password does not open it")

// reuseSharedCopy keeps the file or object that a run of another repository
// label still needs, such as one written through another mount point of the
// same share or another spelling of the same S3 prefix. It counts as this target's copy when this target's password
// opens it. Otherwise the copy fails and the file stays as it is.
func reuseSharedCopy(ctx context.Context, offsite config.BackupTarget, snapshot string) (int64, error) {
	if err := checkPortable(ctx, offsite, snapshot); err != nil {
		if ResultCode(err) == "archive_unreadable" && !contextFailure(err) {
			return 0, errors.Join(&ToolError{Program: "archive", Code: "archive_shared", err: errArchiveShared}, err)
		}
		return 0, err
	}
	name := backupplan.ArchiveName(snapshot)
	if !filepath.IsAbs(offsite.Repository) {
		client, bucket, key, err := archiveObject(offsite, name)
		if err != nil {
			return 0, archiveStorageError(err)
		}
		info, err := client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
		return info.Size, archiveStorageError(err)
	}
	info, err := os.Stat(filepath.Join(backupplan.ArchiveDirectory(offsite.Repository), name)) // #nosec G703 -- name derived from a validated snapshot ID
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// removeStaleCopy removes the file an earlier failed copy of this snapshot
// left under its final name, when it failed after the rename. Its run was
// just recorded as running, so no complete or pending row references that
// file. Removing it first keeps a mount from needing room for both the stale
// file and the new partial file. The caller reuses a file that another
// repository label still needs instead, and this check keeps it as well. This
// is best effort: publishing replaces the file anyway, and that write reports
// a destination that refuses changes.
func removeStaleCopy(ctx context.Context, pool *pgxpool.Pool, offsite config.BackupTarget, snapshot string) {
	if !filepath.IsAbs(offsite.Repository) {
		return
	}
	if shared, err := sharedArchiveFile(ctx, pool, offsite, snapshot, "complete", "retention_pending"); err != nil || shared {
		return
	}
	name := backupplan.ArchiveName(snapshot)
	if _, err := os.Lstat(filepath.Join(backupplan.ArchiveDirectory(offsite.Repository), name)); err != nil { // #nosec G703 -- name derived from a validated snapshot ID
		return
	}
	if err := removePortable(ctx, offsite, name); err != nil {
		slog.Default().Warn("could not remove the earlier failed copy of this snapshot", "code", ResultCode(err))
	}
}

// sharedArchiveFile reports whether a run of another repository label in one
// of states needs the archive file or object of snapshot under target. Another
// configured path can name the same directory, such as a second mount point of
// one filesystem, so the files are compared, not the labels. Another spelling
// of an S3 repository can name the same object, so S3 labels are compared by
// the object they address. A label whose
// path does not resolve here cannot name this file; any other failure to
// compare is returned, and callers then keep the file.
func sharedArchiveFile(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, snapshot string, states ...string) (bool, error) {
	targets, err := sharingTargets(ctx, pool, target, snapshot, states...)
	return len(targets) > 0, err
}

// sharingTargets returns the repositories of other labels with a run of
// snapshot in one of states whose archive file or object is the one of target.
func sharingTargets(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, snapshot string, states ...string) ([]config.BackupTarget, error) {
	name := backupplan.ArchiveName(snapshot)
	if _, ok := target.S3Connection(); ok {
		return s3SharingTargets(ctx, pool, target, snapshot, states)
	}
	if !filepath.IsAbs(target.Repository) || name == "" {
		return nil, nil
	}
	own, err := os.Lstat(filepath.Join(backupplan.ArchiveDirectory(target.Repository), name)) // #nosec G703 -- name derived from a validated snapshot ID
	if absentPath(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `SELECT DISTINCT repository_kind,repository_reference FROM core.backup_runs
 WHERE repository_reference<>$1 AND snapshot_reference=$2 AND state=ANY($3::text[]) ORDER BY 1,2`, target.Label(), snapshot, states)
	if err != nil {
		return nil, err
	}
	others, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (config.BackupTarget, error) {
		var other config.BackupTarget
		return other, row.Scan(&other.Kind, &other.Repository)
	})
	if err != nil {
		return nil, err
	}
	var sharing []config.BackupTarget
	for _, other := range others {
		if !filepath.IsAbs(other.Repository) {
			continue
		}
		// The label is a repository path this service recorded.
		info, err := os.Lstat(filepath.Join(backupplan.ArchiveDirectory(other.Repository), name)) // #nosec G703 -- recorded repository path and validated name
		if absentPath(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if os.SameFile(own, info) {
			sharing = append(sharing, other)
		}
	}
	return sharing, nil
}

// s3SharingTargets returns the other S3 labels with a run of snapshot in one of
// states that address the object of target. They are returned with the
// credentials of target, which reach the same bucket, so their pending runs
// can be decided. An object that is gone is needed by no label.
func s3SharingTargets(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, snapshot string, states []string) ([]config.BackupTarget, error) {
	own, ok := s3ArchiveIdentity(target.Repository, backupplan.ArchiveName(snapshot))
	if !ok {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `SELECT DISTINCT repository_reference FROM core.backup_runs
 WHERE repository_kind='offsite' AND repository_reference<>$1 AND repository_reference LIKE 's3:%' AND snapshot_reference=$2
 AND state=ANY($3::text[]) ORDER BY 1`, target.Label(), snapshot, states)
	if err != nil {
		return nil, err
	}
	labels, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var sharing []config.BackupTarget
	for _, label := range labels {
		if identity, ok := s3ArchiveIdentity(label, backupplan.ArchiveName(snapshot)); ok && identity == own {
			sharing = append(sharing, target.WithRepository(label))
		}
	}
	if len(sharing) == 0 {
		return nil, nil
	}
	present, err := archivePresent(ctx, target, snapshot)
	if err != nil || !present {
		return nil, err
	}
	return sharing, nil
}

// absentPath reports a path that names no file, including one below a
// component that is not a directory.
func absentPath(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

var errArchiveStorageAliased = errors.New("the mounted offsite path is the local backup directory")

// distinctArchiveStorage rejects a mounted offsite repository that resolves to
// the local repository, or whose archive directory is the local one, although
// the configured paths differ. A copy there would replace each local archive
// with one encrypted under the offsite password. Distinct directories on the
// same filesystem stay valid.
func distinctArchiveStorage(local, offsite config.BackupTarget) error {
	if !filepath.IsAbs(local.Repository) || !filepath.IsAbs(offsite.Repository) {
		return nil
	}
	for _, paths := range [][2]string{{local.Repository, offsite.Repository},
		{backupplan.ArchiveDirectory(local.Repository), backupplan.ArchiveDirectory(offsite.Repository)}} {
		first, err := os.Stat(paths[0])
		if err != nil {
			continue
		}
		second, err := os.Stat(paths[1])
		if err == nil && os.SameFile(first, second) {
			return &ToolError{Program: "archive", Code: "same_as_local", err: errArchiveStorageAliased}
		}
	}
	return nil
}

// reconcileOffsiteAttempts removes archives left by earlier copies that never
// completed, then clears their reference. Under the repository lock no other
// copy runs, so a running row is stale. A snapshot with a complete or pending
// row keeps its file, also when its completion committed despite an error. A
// file that a run of another label needs keeps the attempt and its reference,
// so the cleanup resumes once that run no longer needs it.
func reconcileOffsiteAttempts(ctx context.Context, pool *pgxpool.Pool, offsite config.BackupTarget, current string) error {
	rows, err := pool.Query(ctx, `SELECT r.id,r.snapshot_reference FROM core.backup_runs r
 WHERE r.repository_kind='offsite' AND r.repository_reference=$1 AND r.state IN('running','failed')
 AND r.snapshot_reference ~ $2 AND r.snapshot_reference<>$3 AND NOT EXISTS(SELECT 1 FROM core.backup_runs c
 WHERE c.repository_reference=r.repository_reference AND c.snapshot_reference=r.snapshot_reference AND c.state IN('complete','retention_pending'))
 ORDER BY r.started_at`, offsite.Label(), backupplan.ArchiveReferencePattern, current)
	if err != nil {
		return err
	}
	type attempt struct{ id, snapshot string }
	attempts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (attempt, error) {
		var a attempt
		err := row.Scan(&a.id, &a.snapshot)
		return a, err
	})
	if err != nil {
		return err
	}
	// A failed delete keeps that attempt's reference for the next run and does
	// not stop the cleanup of later attempts.
	var failures []error
	for _, a := range attempts {
		shared, sharedErr := sharedArchiveFile(ctx, pool, offsite, a.snapshot, "complete", "retention_pending")
		if sharedErr != nil || shared {
			failures = append(failures, sharedErr)
			continue
		}
		if err = removePortable(ctx, offsite, backupplan.ArchiveName(a.snapshot)); err != nil {
			failures = append(failures, err)
			continue
		}
		_, err = audit.Exec(ctx, pool, `UPDATE core.backup_runs SET state='failed',finished_at=coalesce(finished_at,now()),
 failure_reason=coalesce(failure_reason,'offsite copy failed'),snapshot_reference=NULL WHERE id=$1`, a.id)
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

type archiveRetentionPoint struct {
	id      string
	at      time.Time
	pending bool
}

// Retain the newest archive in each requested calendar bucket, taking the
// union of all three rules. The caller provides newest-first order.
func expiredArchives(points []archiveRetentionPoint, zone *time.Location, daily, weekly, monthly int) []string {
	days, weeks, months := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var expired []string
	for _, point := range points {
		at := point.at.In(zone)
		year, week := at.ISOWeek()
		d, w, m := at.Format("2006-01-02"), fmt.Sprintf("%d-%d", year, week), at.Format("2006-01")
		keepDay, keepWeek, keepMonth := len(days) < daily && !days[d], len(weeks) < weekly && !weeks[w], len(months) < monthly && !months[m]
		if point.pending || !keepDay && !keepWeek && !keepMonth {
			expired = append(expired, point.id)
			continue
		}
		if keepDay {
			days[d] = true
		}
		if keepWeek {
			weeks[w] = true
		}
		if keepMonth {
			months[m] = true
		}
	}
	return expired
}

// keepFirst moves the protected point to the front and keeps the remaining
// newest-first order.
func keepFirst(points []archiveRetentionPoint, keep string) []archiveRetentionPoint {
	i := slices.IndexFunc(points, func(p archiveRetentionPoint) bool { return p.id == keep && !p.pending })
	if keep == "" || i <= 0 {
		return points
	}
	return slices.Concat(points[i:i+1], points[:i], points[i+1:])
}

// keep names an archive this run wrote; it claims its calendar buckets first,
// so a new local backup is never expired by the run that created it.
//
// The quotas are read in the pruning transaction, so a retention change saved
// during a backup governs the prune that follows it.
func applyArchiveRetention(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, keep string) error {
	return retainArchives(ctx, pool, target, keep, true)
}

// retainArchives decides the pending deletions of target again and finishes
// the ones that stay selected. With prune, it first marks the archives that
// the quotas no longer keep.
//
// A pending row does not prove that portable retention selected its archive:
// a version without portable files marks every complete run of its label
// pending before restic decides, and a run stopped then leaves them pending.
// So a pending archive that the quotas keep and whose file is present becomes
// complete again. A pending row whose file is gone, because its deletion ran
// but was not recorded, stays pending and is recorded expired.
func retainArchives(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, keep string, prune bool) error {
	if err := decideArchives(ctx, pool, target, keep, prune); err != nil {
		return err
	}
	return finishArchiveRetention(ctx, pool, target, "")
}

// decideArchives is the transaction of retainArchives: it restores the
// pending archives that the quotas keep and, with prune, marks the expired
// ones. It removes no file.
func decideArchives(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, keep string, prune bool) error {
	tx, err := audit.Begin(ctx, pool)
	if err != nil {
		return err
	}
	defer rollbackCrypto(tx)
	// The settings row is held until the prune commits. A save that committed
	// earlier is read here; a later save waits (an update blocks on this row,
	// a first insert on the household row's key lock) and governs the next run.
	// It is locked before the household row, which a save's audit entry may
	// reference, so the two never wait on each other.
	if _, err = tx.Exec(ctx, "SELECT 1 FROM core.backup_settings FOR SHARE"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return err
	}
	settings, zone, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	daily, weekly, monthly := settings.KeepDaily, settings.KeepWeekly, settings.KeepMonthly
	if target.Kind == "offsite" {
		daily, weekly, monthly = settings.OffsiteKeepDaily, settings.OffsiteKeepWeekly, settings.OffsiteKeepMonthly
	}
	// Points are keyed by capture time: the local run's start (for an import,
	// the archive's own capture date), not the time an offsite copy finished.
	rows, err := tx.Query(ctx, `SELECT r.snapshot_reference,coalesce((SELECT min(l.started_at) FROM core.backup_runs l
  WHERE l.repository_kind='local' AND l.snapshot_reference=r.snapshot_reference),max(r.started_at)) captured,
 bool_or(r.state='retention_pending')
 FROM core.backup_runs r WHERE r.repository_reference=$1 AND r.snapshot_reference ~ $2
 AND r.state IN('complete','retention_pending') GROUP BY r.snapshot_reference ORDER BY captured DESC,r.snapshot_reference`,
		target.Label(), backupplan.ArchiveReferencePattern)
	if err != nil {
		return err
	}
	var points []archiveRetentionPoint
	for rows.Next() {
		var point archiveRetentionPoint
		if err = rows.Scan(&point.id, &point.at, &point.pending); err != nil {
			rows.Close()
			return err
		}
		if backupplan.ArchiveName(point.id) != "" {
			points = append(points, point)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	restored, err := keptPendingArchives(ctx, target, points, keep, zone, daily, weekly, monthly)
	if err != nil {
		return err
	}
	if len(restored) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE core.backup_runs SET state='complete'
 WHERE repository_reference=$1 AND snapshot_reference=ANY($2::text[]) AND state='retention_pending'`, target.Label(), restored); err != nil {
			return err
		}
		for i := range points {
			if slices.Contains(restored, points[i].id) {
				points[i].pending = false
			}
		}
	}
	if prune {
		if expired := expiredArchives(keepFirst(points, keep), zone, daily, weekly, monthly); len(expired) > 0 {
			if _, err = tx.Exec(ctx, `UPDATE core.backup_runs SET state='retention_pending'
 WHERE repository_reference=$1 AND snapshot_reference=ANY($2::text[]) AND state='complete'`, target.Label(), expired); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if len(restored) > 0 {
		slog.Default().Warn("kept pending backup files that the retention quotas keep", "kind", target.Kind, "count", len(restored))
	}
	return nil
}

// keptPendingArchives returns the pending archives that the quotas keep when
// pending rows are not taken as selected, and whose file is present.
func keptPendingArchives(ctx context.Context, target config.BackupTarget, points []archiveRetentionPoint, keep string, zone *time.Location, daily, weekly, monthly int) ([]string, error) {
	present := map[string]bool{}
	decided := make([]archiveRetentionPoint, len(points))
	for i, point := range points {
		decided[i] = archiveRetentionPoint{id: point.id, at: point.at}
		if !point.pending {
			continue
		}
		found, err := archivePresent(ctx, target, point.id)
		if err != nil {
			return nil, err
		}
		present[point.id] = found
		// A pending row whose file is gone holds no restore point, so it
		// stays selected and claims no calendar bucket.
		decided[i].pending = !found
	}
	expired := expiredArchives(keepFirst(decided, keep), zone, daily, weekly, monthly)
	var kept []string
	for _, point := range points {
		if point.pending && present[point.id] && !slices.Contains(expired, point.id) {
			kept = append(kept, point.id)
		}
	}
	return kept, nil
}

// archivePresent reports whether the archive file of id exists at target. A
// destination that cannot be read returns its error, so nothing is decided.
func archivePresent(ctx context.Context, target config.BackupTarget, id string) (bool, error) {
	file, err := openPortable(ctx, target, id)
	if err == nil {
		return true, file.Close()
	}
	if ResultCode(err) == "archive_missing" {
		return false, nil
	}
	return false, err
}

// finishArchiveRetention removes the file of every archive marked for
// retention on this repository, or only of one snapshot, and then marks its
// rows expired. It also completes a retention an earlier run left unfinished.
// A file that a complete run of another label needs is kept, and its row stays
// pending until that run no longer needs it. A pending run of another label
// that shares the file is decided again first, so one that its quotas keep is
// complete again and keeps the file. A pending run that stays selected does
// not keep it, so two labels that both expire a file never wait for each other.
func finishArchiveRetention(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, only string) error {
	rows, err := pool.Query(ctx, `SELECT DISTINCT snapshot_reference FROM core.backup_runs
 WHERE repository_reference=$1 AND snapshot_reference ~ $2 AND state='retention_pending' AND ($3='' OR snapshot_reference=$3) ORDER BY 1`,
		target.Label(), backupplan.ArchiveReferencePattern, only)
	if err != nil {
		return err
	}
	pending, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	// One file that cannot be removed keeps only its own row pending; the
	// others are still removed, and the errors are returned together.
	var failures []error
	for _, id := range pending {
		name := backupplan.ArchiveName(id)
		if name == "" {
			continue
		}
		if err = decideSharingLabels(ctx, pool, target, id); err != nil {
			failures = append(failures, err)
			continue
		}
		shared, sharedErr := sharedArchiveFile(ctx, pool, target, id, "complete")
		if sharedErr != nil || shared {
			failures = append(failures, sharedErr)
			continue
		}
		if err = removePortable(ctx, target, name); err != nil {
			failures = append(failures, err)
			continue
		}
		_, err = audit.Exec(ctx, pool, `UPDATE core.backup_runs SET state='expired'
 WHERE repository_reference=$1 AND snapshot_reference=$2 AND state='retention_pending'`, target.Label(), id)
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// decideSharingLabels decides the pending deletions of every other label
// whose pending run of snapshot shares the file of target, without removing
// any of their files.
func decideSharingLabels(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, snapshot string) error {
	others, err := sharingTargets(ctx, pool, target, snapshot, "retention_pending")
	if err != nil {
		return err
	}
	for _, other := range others {
		if err = decideArchives(ctx, pool, other, "", false); err != nil {
			return err
		}
	}
	return nil
}

func removePortable(ctx context.Context, target config.BackupTarget, name string) error {
	if filepath.IsAbs(target.Repository) {
		// Open the directory first: an unmounted destination must keep the
		// deletion pending. Only a file missing inside it counts as removed.
		root, err := os.OpenRoot(backupplan.ArchiveDirectory(target.Repository))
		if err != nil {
			return &ToolError{Program: "archive", Code: "repository_unreachable", err: err}
		}
		defer func() { _ = root.Close() }()
		if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Persist the unlink before the caller records the deletion, so a
		// crash cannot leave an expired archive that nothing retries. A file
		// that is already gone may still have an unsynced removal.
		return syncRootDirectory(root)
	}
	client, bucket, key, err := archiveObject(target, name)
	if err != nil {
		return err
	}
	return archiveStorageError(client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}))
}

var errArchiveDirectoryMissing = errors.New("the offsite backup directory is missing although copies were recorded there; is the destination mounted?")

// mountedArchiveDirectory returns the archive directory of a mounted offsite
// target once the path plausibly shows the mounted filesystem. Household never
// creates the repository path itself, so an absent mount point is not replaced
// by a new directory. Once an archive copy was recorded there, its archive
// directory must exist as well: if it is missing, the mount is absent and the
// path shows the filesystem underneath. Only the archive directory of a first
// copy is created. Callers hold the repository lock.
func mountedArchiveDirectory(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget) (string, error) {
	unreachable := func(err error) error {
		return &ToolError{Program: "archive", Code: "repository_unreachable", err: err}
	}
	info, err := os.Stat(target.Repository)
	if err != nil {
		return "", unreachable(err)
	}
	if !info.IsDir() {
		return "", unreachable(errors.New("the offsite backup path is not a directory"))
	}
	directory := backupplan.ArchiveDirectory(target.Repository)
	if _, err = os.Stat(directory); err == nil {
		return directory, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", unreachable(err)
	}
	var recorded bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs WHERE repository_kind='offsite'
 AND repository_reference=$1 AND state IN('complete','retention_pending','expired') AND snapshot_reference ~ $2)`,
		target.Label(), backupplan.ArchiveReferencePattern).Scan(&recorded); err != nil {
		return "", err
	}
	if recorded {
		return "", unreachable(errArchiveDirectoryMissing)
	}
	return directory, nil
}

// syncRootDirectory syncs the directory a root opened, so entries removed
// through it are durable.
func syncRootDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil && runtime.GOOS != "windows" {
		// Windows cannot sync directories (developer tests only).
		return err
	}
	return nil
}

// archiveReclaimGrace keeps recently written files out of reclamation. Writers
// hold the repository lock as well; the grace period only adds a margin.
const archiveReclaimGrace = time.Hour

// reclaimArchives frees local archive space before a new archive is written.
// It first retries deletions that an earlier retention left pending, so a
// file whose deletion failed once cannot keep blocking every later write. It
// then removes files that no backup run references: a published archive whose
// registration or check failed, an import that was never recorded, and
// partial files of an interrupted write. A run of any kind protects its file
// by the repository label it records, so the rows of an offsite repository
// that became the local one keep their archives. A run of another label whose
// path names the same file, such as an offsite mount that is now the local
// one under another path, protects it as well. The local archive directory
// is always a filesystem path. Callers hold the repository lock.
func reclaimArchives(ctx context.Context, pool *pgxpool.Pool, local config.BackupTarget, now time.Time) error {
	if !filepath.IsAbs(local.Repository) && local.Repository != "" {
		return nil
	}
	// Pending deletions finish before the directory is read, so a file removed
	// here is not seen below with its now expired row. A failed delete keeps
	// its row pending and its file protected below.
	failures := []error{retainArchives(ctx, pool, local, "", false)}
	directory := backupplan.ArchiveDirectory(local.Repository)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(failures...)
	}
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	candidates := map[string]string{}
	for _, entry := range entries {
		if !reclaimable(entry, now) {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".partial-") {
			failures = append(failures, removeArchiveEntry(directory, name))
		} else if id := backupplan.ArchiveIDFromName(name); id != "" {
			candidates[id] = name
		}
	}
	if len(candidates) > 0 {
		ids := make([]string, 0, len(candidates))
		for id := range candidates {
			ids = append(ids, id)
		}
		rows, queryErr := pool.Query(ctx, `SELECT DISTINCT snapshot_reference FROM core.backup_runs
 WHERE (repository_kind='local' OR repository_reference=$2) AND snapshot_reference=ANY($1::text[])
 AND state IN('complete','retention_pending')`, ids, local.Label())
		if queryErr != nil {
			return queryErr
		}
		referenced, collectErr := pgx.CollectRows(rows, pgx.RowTo[string])
		if collectErr != nil {
			return collectErr
		}
		for _, id := range referenced {
			delete(candidates, id)
		}
		revived, reviveErr := reviveExpiredArchives(ctx, pool, local, slices.Collect(maps.Keys(candidates)))
		if reviveErr != nil {
			return errors.Join(append(failures, reviveErr)...)
		}
		for _, id := range revived {
			delete(candidates, id)
		}
		for id, name := range candidates {
			shared, sharedErr := sharedArchiveFile(ctx, pool, local, id, "complete", "retention_pending")
			if sharedErr != nil || shared {
				failures = append(failures, sharedErr)
				continue
			}
			failures = append(failures, removeArchiveEntry(directory, name))
		}
	}
	return errors.Join(failures...)
}

// reclaimable reports a regular file old enough to be reclaimed.
func reclaimable(entry os.DirEntry, now time.Time) bool {
	info, err := entry.Info()
	return err == nil && !entry.IsDir() && info.Mode().IsRegular() && now.Sub(info.ModTime()) >= archiveReclaimGrace
}

// removeStalePartials removes partial files of interrupted writes. A write in
// progress keeps changing its file, and callers hold the repository lock.
func removeStalePartials(directory string, now time.Time) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") && reclaimable(entry, now) {
			failures = append(failures, removeArchiveEntry(directory, entry.Name()))
		}
	}
	return errors.Join(failures...)
}

// reviveExpiredArchives registers again the portable archives that an older
// backup worker marked expired. Its retention handled only its own snapshots
// and expired every other run of the repository label, so after a rollback and
// a new upgrade a present file can have only an expired row. This version
// marks a run expired only after its file is gone, so portable retention never
// selected such a file. The newest of its runs becomes complete again, and the
// next retention decides whether it stays.
func reviveExpiredArchives(ctx context.Context, pool *pgxpool.Pool, local config.BackupTarget, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := audit.Begin(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, `UPDATE core.backup_runs SET state='complete' WHERE id IN(
 SELECT DISTINCT ON (snapshot_reference) id FROM core.backup_runs
 WHERE repository_reference=$1 AND snapshot_reference=ANY($2::text[]) AND state='expired'
 ORDER BY snapshot_reference, started_at DESC) RETURNING snapshot_reference`, local.Label(), ids)
	if err != nil {
		return nil, err
	}
	revived, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if len(revived) > 0 {
		slog.Default().Warn("registered portable archives again that an older backup worker marked expired", "count", len(revived))
	}
	return revived, nil
}

func removeArchiveEntry(directory, name string) error {
	err := os.Remove(filepath.Join(directory, name)) // #nosec G703 -- a managed filename read from the archive directory
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
