package maintenance

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"household/internal/backupplan"
	"household/internal/config"
	"household/internal/value"

	"filippo.io/age"
)

const (
	archiveWorkFactor    = 17
	archiveMetadataLimit = 16 << 20
)

type archiveReader struct {
	context.Context
	io.Reader
}

type archiveWriter struct {
	io.Writer
	remaining int64
}

func (w *archiveWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errors.New("encrypted backup exceeds archive limit")
	}
	n, err := w.Writer.Write(b)
	w.remaining -= int64(n)
	return n, err
}

func (r archiveReader) Read(b []byte) (int, error) {
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(b)
}

func portableBackups(local config.BackupTarget) bool {
	if os.Getenv("HOUSEHOLD_BACKUP_FORMAT") == "restic" {
		return false
	}
	remote, _ := config.OffsiteBackupTarget(local)
	return remote == nil || !strings.HasPrefix(remote.Repository, "rest:")
}

func encryptArchive(dst io.Writer, password string) (io.WriteCloser, error) {
	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(archiveWorkFactor)
	return age.Encrypt(dst, recipient)
}

func decryptArchive(ctx context.Context, src io.Reader, password string) (io.Reader, error) {
	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	identity.SetMaxWorkFactor(archiveWorkFactor)
	read, err := age.Decrypt(archiveReader{Context: ctx, Reader: src}, identity)
	if err != nil {
		return nil, &ToolError{Program: "archive", Code: "archive_unreadable", err: err}
	}
	return read, nil
}

func copyArchive(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	size, err := io.Copy(dst, io.LimitReader(archiveReader{Context: ctx, Reader: src}, backupplan.ArchiveLimit+1))
	if err == nil && size > backupplan.ArchiveLimit {
		err = errors.New("archive exceeds configured format limit")
	}
	return size, err
}

// writeArchiveFile publishes an encrypted file only after close and fsync.
func writeArchiveFile(directory, name string, write func(*os.File) error) (int64, error) {
	if err := os.MkdirAll(directory, 0700); err != nil { // #nosec G703 -- fixed or operator-approved storage location
		return 0, err
	}
	tmp, err := os.CreateTemp(directory, ".partial-")
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if err = write(tmp); err != nil {
		return 0, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() > backupplan.ArchiveLimit {
		return 0, errors.New("encrypted backup exceeds archive limit")
	}
	if err = tmp.Sync(); err != nil {
		return 0, err
	}
	if err = tmp.Close(); err != nil {
		return 0, err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(directory, name)); err != nil { // #nosec G703 -- safe names derived from backup or job IDs
		return 0, err
	}
	return info.Size(), syncDirectory(directory)
}

func writePortable(ctx context.Context, target config.BackupTarget, id, stage, sourceUploads string, files []File) (int64, error) {
	return writeArchiveFile(backupplan.ArchiveDirectory(target.Repository), backupplan.ArchiveName(id), func(f *os.File) error {
		enc, err := encryptArchive(&archiveWriter{Writer: f, remaining: backupplan.ArchiveLimit}, target.ArchivePassword())
		if err != nil {
			return err
		}
		writer := tar.NewWriter(enc)
		stageRoot, err := os.OpenRoot(stage)
		if err != nil {
			return err
		}
		defer func() { _ = stageRoot.Close() }()
		for _, name := range []string{"manifest.json", "database.dump", "security-keys.json"} {
			if err = addArchiveFile(ctx, writer, stageRoot, name, name, nil); err != nil {
				return err
			}
		}
		if len(files) > 0 {
			uploads, openErr := os.OpenRoot(sourceUploads)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = uploads.Close() }()
			for _, attachment := range files {
				if !value.ValidUUID(attachment.Key) {
					return errors.New("invalid attachment key")
				}
				if err = addArchiveFile(ctx, writer, uploads, attachment.Key, "uploads/"+attachment.Key, &attachment); err != nil {
					return err
				}
			}
		}
		return errors.Join(writer.Close(), enc.Close())
	})
}

func addArchiveFile(ctx context.Context, dst *tar.Writer, root *os.Root, key, name string, expected *File) error {
	f, err := root.Open(key)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > backupplan.ArchiveLimit || expected != nil && expected.Size != info.Size() {
		return errors.New("unexpected backup file size or type")
	}
	if err = dst.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		return err
	}
	if expected == nil {
		_, err = copyArchive(ctx, dst, f)
		return err
	}
	hash := sha256.New()
	size, err := copyArchive(ctx, io.MultiWriter(dst, hash), f)
	if err != nil {
		return err
	}
	if size != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("attachment changed while backing up")
	}
	return nil
}

// extractArchive authenticates the complete stream before pg_restore can run.
// Only the fixed backup payload and UUID-named attachments can be written.
func extractArchive(ctx context.Context, src io.Reader, password, directory string) error {
	plaintext, err := decryptArchive(ctx, src, password)
	if err != nil {
		return err
	}
	return extractArchivePlain(ctx, plaintext, directory)
}

func extractArchivePlain(ctx context.Context, plaintext io.Reader, directory string) error {
	reader := tar.NewReader(plaintext)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err = root.Mkdir("uploads", 0700); err != nil {
		return err
	}
	remaining := int64(backupplan.ArchiveLimit)
	for entries := 0; ; entries++ {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		if entries >= 100003 || header.Typeflag != tar.TypeReg ||
			header.Size < 0 || header.Size > remaining || !allowedArchiveEntry(header.Name, header.Size) {
			return errors.New("unexpected entry in encrypted backup")
		}
		remaining -= header.Size
		file, openErr := root.OpenFile(filepath.FromSlash(header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return openErr
		}
		size, copyErr := copyArchive(ctx, file, reader)
		if err = errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
		if size != header.Size {
			return io.ErrUnexpectedEOF
		}
	}
	var trailer [1]byte
	if n, readErr := plaintext.Read(trailer[:]); n != 0 || readErr != io.EOF {
		return fmt.Errorf("encrypted backup has an incomplete or unexpected trailer: %w", errors.Join(readErr, io.ErrUnexpectedEOF))
	}
	meta, err := root.ReadFile("manifest.json")
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(meta, &manifest); err != nil {
		return err
	}
	if manifest.ArchiveFormat != 1 || manifest.UploadPath != "/uploads" || manifest.Crypto == nil ||
		manifest.Created.IsZero() || manifest.Version == "" {
		return errors.New("unsupported Household archive manifest")
	}
	return nil
}

func allowedArchiveEntry(name string, size int64) bool {
	switch name {
	case "manifest.json", "security-keys.json":
		return size <= archiveMetadataLimit
	case "database.dump":
		return size > 0
	default:
		key, found := strings.CutPrefix(name, "uploads/")
		return found && value.ValidUUID(key)
	}
}
