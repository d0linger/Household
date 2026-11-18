//go:build windows

package documentinput

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Regression: on Windows a replacement under the same name, size, write time
// and creation time also restarts its settling period.
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	created := info.Sys().(*syscall.Win32FileAttributeData).CreationTime
	write(filepath.Join(dir, "replacement.tmp"))
	if err = os.Rename(filepath.Join(dir, "replacement.tmp"), path); err != nil {
		t.Fatal(err)
	}
	// The replacement carries the original's creation, write and size: only
	// the file ID tells them apart.
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(pathPtr, syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.SetFileTime(handle, &created, nil, nil); err != nil {
		_ = syscall.CloseHandle(handle)
		t.Fatal(err)
	}
	_ = syscall.CloseHandle(handle)
	if replaced, err := os.Stat(path); err != nil || replaced.Sys().(*syscall.Win32FileAttributeData).CreationTime != created || !replaced.ModTime().Equal(info.ModTime()) {
		t.Fatal("fixture did not reproduce matching timestamps", err)
	}
	cursor.SettlingSince = cursor.SettlingSince.Add(-watchSettleTime)
	got, next, err := Watch(t.Context(), dir, cursor)
	if err != nil || len(got) != 0 || next.Settling != "a.ready.pdf" || !next.SettlingSince.After(cursor.SettlingSince) {
		t.Fatal("a replacement must restart its settling period", got, next, err)
	}
}
