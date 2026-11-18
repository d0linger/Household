//go:build windows

package documentinput

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: on Windows a replacement under the same name, size and time
// also restarts its settling period.
func TestWatchRestartsSettlingForAReplacementOnWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ready.pdf")
	future := time.Now().Add(time.Hour)
	write := func(target string) {
		if err := os.WriteFile(target, []byte("%PDF-1.4"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(target, future, future); err != nil {
			t.Fatal(err)
		}
	}
	write(path)
	_, cursor, err := Watch(t.Context(), dir, Cursor{})
	if err != nil || cursor.Settling != "a.ready.pdf" || cursor.SettlingFile == "" {
		t.Fatal("first observation", cursor, err)
	}
	time.Sleep(20 * time.Millisecond) // a later creation time
	write(filepath.Join(dir, "replacement.tmp"))
	if err = os.Rename(filepath.Join(dir, "replacement.tmp"), path); err != nil {
		t.Fatal(err)
	}
	cursor.SettlingSince = cursor.SettlingSince.Add(-watchSettleTime)
	got, next, err := Watch(t.Context(), dir, cursor)
	if err != nil || len(got) != 0 || next.Settling != "a.ready.pdf" || !next.SettlingSince.After(cursor.SettlingSince) {
		t.Fatal("a replacement must restart its settling period", got, next, err)
	}
}
