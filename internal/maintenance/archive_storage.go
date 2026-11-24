package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/backupplan"
	"household/internal/buildinfo"
	"household/internal/config"
	"household/internal/value"

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
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > backupplan.ArchiveLimit {
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
	reader, err := decryptArchive(ctx, src, target.ArchivePassword())
	if err != nil {
		return err
	}
	if _, err = copyArchive(ctx, io.Discard, reader); err != nil {
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
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.backup_runs
 WHERE repository_kind='offsite' AND repository_reference=$1 AND snapshot_reference=$2 AND state='complete')`,
		offsite.Label(), snapshot).Scan(&exists)
	if err != nil {
		return "", err
	}
	if exists {
		if err = checkPortable(ctx, offsite, snapshot); err != nil {
			return "", err
		}
	} else {
		size, err := sendArchive(ctx, local, offsite, snapshot)
		if err != nil {
			return "", err
		}
		if err = checkPortable(ctx, offsite, snapshot); err != nil {
			return "", err
		}
		_, err = audit.Exec(ctx, pool, `INSERT INTO core.backup_runs
 (id,state,application_version,repository_reference,repository_kind,job_id,finished_at,snapshot_reference,schema_manifest,data_added,total_bytes)
 VALUES($1,'complete',$2,$3,'offsite',$4,now(),$5,$6,$7,$7)`,
			value.UUID(), buildinfo.Label(), offsite.Label(), nullableUUID(jobID), snapshot, schemas, size)
		if err != nil {
			return "", err
		}
	}
	err = applyArchiveRetention(ctx, pool, offsite, settings.OffsiteKeepDaily, settings.OffsiteKeepWeekly, settings.OffsiteKeepMonthly)
	return snapshot, err
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

func applyArchiveRetention(ctx context.Context, pool *pgxpool.Pool, target config.BackupTarget, daily, weekly, monthly int) error {
	tx, err := audit.Begin(ctx, pool)
	if err != nil {
		return err
	}
	defer rollbackCrypto(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return err
	}
	_, zone, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT snapshot_reference,max(started_at),bool_or(state='retention_pending')
 FROM core.backup_runs WHERE repository_reference=$1 AND snapshot_reference ~ $2
 AND state IN('complete','retention_pending') GROUP BY snapshot_reference ORDER BY max(started_at) DESC,snapshot_reference`,
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
	expired := expiredArchives(points, zone, daily, weekly, monthly)
	if len(expired) == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE core.backup_runs SET state='retention_pending'
 WHERE repository_reference=$1 AND snapshot_reference=ANY($2::text[]) AND state='complete'`, target.Label(), expired); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	for _, id := range expired {
		if err = removePortable(ctx, target, backupplan.ArchiveName(id)); err != nil {
			return err
		}
		_, err = audit.Exec(ctx, pool, `UPDATE core.backup_runs SET state='expired'
 WHERE repository_reference=$1 AND snapshot_reference=$2 AND state='retention_pending'`, target.Label(), id)
		if err != nil {
			return err
		}
	}
	return nil
}

func removePortable(ctx context.Context, target config.BackupTarget, name string) error {
	if filepath.IsAbs(target.Repository) {
		err := os.Remove(filepath.Join(backupplan.ArchiveDirectory(target.Repository), name)) // #nosec G703 -- name is derived from the validated snapshot ID
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	client, bucket, key, err := archiveObject(target, name)
	if err != nil {
		return err
	}
	return archiveStorageError(client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}))
}
