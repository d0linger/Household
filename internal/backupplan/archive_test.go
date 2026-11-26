package backupplan

import (
	"strings"
	"testing"
	"time"
)

func TestArchiveNamesRoundTripAndExchangeNames(t *testing.T) {
	id := ArchiveID(time.Date(2026, 11, 24, 14, 5, 27, 0, time.UTC))
	name := ArchiveName(id)
	if got := ArchiveIDFromName(name); got != id {
		t.Fatalf("ArchiveIDFromName(%q) = %q, want %q", name, got, id)
	}
	for _, bad := range []string{"", ".partial-x", "household-2026-11-24T14-05-27Z-" + strings.Repeat("g", 36) + ".tar.age", strings.TrimSuffix(name, ".age")} {
		if got := ArchiveIDFromName(bad); got != "" {
			t.Fatalf("ArchiveIDFromName(%q) = %q, want empty", bad, got)
		}
	}
	first, second := DownloadName(id, "s3:https://a.example/bucket"), DownloadName(id, "s3:https://b.example/bucket")
	if first == "" || second == "" || first == second || DownloadName(id, "") != "" || DownloadName("x", "label") != "" {
		t.Fatalf("download names are not bound to the repository: %q %q", first, second)
	}
	for _, managed := range []string{first, second, ExchangeName("import", id), ".partial-123"} {
		if !ManagedExchangeName(managed) {
			t.Fatalf("%q is not recognized as a managed exchange file", managed)
		}
	}
	for _, foreign := range []string{"download-" + id, "notes.txt", "import-x", first + "0", strings.ToUpper(first)} {
		if ManagedExchangeName(foreign) {
			t.Fatalf("%q was treated as a managed exchange file", foreign)
		}
	}
}
