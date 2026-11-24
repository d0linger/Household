package backupplan

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

const (
	archiveMarker           = "be47d8ac916bb1"
	archiveStamp            = "20060102150405"
	ArchiveLimit            = 32 << 30
	ArchiveReferencePattern = "^[0-9a-f]{36}" + archiveMarker + "[0-9]{14}$"
)

// ArchiveID keeps the existing 64-hex restore and confirmation contract.
func ArchiveID(at time.Time) string {
	var nonce [18]byte
	rand.Read(nonce[:])
	return hex.EncodeToString(nonce[:]) + archiveMarker + at.UTC().Format(archiveStamp)
}

// ArchiveName derives a safe filename from an archive reference.
func ArchiveName(id string) string {
	if !ValidSnapshot(id) || id[36:50] != archiveMarker {
		return ""
	}
	at, err := time.Parse(archiveStamp, id[50:])
	if err != nil {
		return ""
	}
	return "household-" + at.Format("2006-01-02T15-04-05Z") + "-" + id[:36] + ".tar.age"
}

// ExchangeDirectory contains encrypted imports and short-lived downloads only.
func ExchangeDirectory() string {
	if dir := os.Getenv("HOUSEHOLD_BACKUP_EXCHANGE"); filepath.IsAbs(dir) {
		return dir
	}
	return "/data/backup-exchange"
}

// ArchiveDirectory is mounted read-only in the web container.
func ArchiveDirectory(repository string) string {
	if repository == "" {
		repository = "/data/backups"
	}
	return filepath.Join(repository, "portable")
}

// ExchangeName names an import or download without trusting a browser filename.
func ExchangeName(kind, id string) string {
	if (kind != "import" && kind != "download") || ArchiveName(id) == "" {
		return ""
	}
	return kind + "-" + id
}
