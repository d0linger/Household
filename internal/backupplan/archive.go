package backupplan

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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

// ExchangeName names an uploaded import without trusting a browser filename.
func ExchangeName(kind, id string) string {
	if kind != "import" || ArchiveName(id) == "" {
		return ""
	}
	return kind + "-" + id
}

// DownloadName names a prepared offsite download. It includes the repository
// label, so a file prepared from one offsite target is never served for another.
func DownloadName(id, label string) string {
	if ArchiveName(id) == "" || label == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(label))
	return "download-" + id + "-" + hex.EncodeToString(sum[:8])
}

// ManagedExchangeName reports whether the worker created or accepts this
// exchange filename, so stale copies can be removed.
func ManagedExchangeName(name string) bool {
	if strings.HasPrefix(name, ".partial-") {
		return true
	}
	if id, ok := strings.CutPrefix(name, "import-"); ok {
		return ExchangeName("import", id) != ""
	}
	rest, ok := strings.CutPrefix(name, "download-")
	if !ok || len(rest) != 64+1+16 || rest[64] != '-' {
		return false
	}
	_, err := hex.DecodeString(rest[65:])
	return ArchiveName(rest[:64]) != "" && err == nil && strings.ToLower(rest[65:]) == rest[65:]
}

// ArchiveIDFromName reverses ArchiveName for a file in an archive directory.
func ArchiveIDFromName(name string) string {
	rest, ok := strings.CutPrefix(name, "household-")
	if !ok {
		return ""
	}
	rest, ok = strings.CutSuffix(rest, ".tar.age")
	if !ok || len(rest) != len("2006-01-02T15-04-05Z")+1+36 || rest[20] != '-' {
		return ""
	}
	at, err := time.Parse("2006-01-02T15-04-05Z", rest[:20])
	if err != nil {
		return ""
	}
	id := rest[21:] + archiveMarker + at.Format(archiveStamp)
	if ArchiveName(id) != name {
		return ""
	}
	return id
}
