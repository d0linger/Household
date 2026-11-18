//go:build unix && !solaris && !illumos

package documentinput

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Regression: a FIFO that replaced a checked inbox file is refused at once
// instead of blocking the shared worker in open(2).
func TestOpenWatchCandidateRefusesFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "scan.ready.pdf"), 0o600); err != nil {
		t.Skip("FIFOs are unavailable here:", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invoice.ready.pdf"), []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	done := make(chan error, 1)
	go func() {
		in, _, err := openWatchCandidate(root, "scan.ready.pdf")
		if in != nil {
			_ = in.Close()
		}
		done <- err
	}()
	select {
	case err = <-done:
		if !errors.Is(err, errWatchNotRegular) {
			t.Fatal("FIFO candidate was not refused as non-regular:", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO candidate blocked")
	}
	in, info, err := openWatchCandidate(root, "invoice.ready.pdf")
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("regular candidate refused:", err)
	}
	_ = in.Close()
}

// Regression: FIFOs that sort first never fill the ten-file batch and starve a
// valid inbox file behind them.
func TestWatchSkipsNonRegularEntriesBeforeTheBatchLimit(t *testing.T) {
	dir := t.TempDir()
	for index := range 12 {
		if err := syscall.Mkfifo(filepath.Join(dir, fmt.Sprintf("a%02d.ready.pdf", index)), 0o600); err != nil {
			t.Skip("FIFOs are unavailable here:", err)
		}
	}
	name := filepath.Join(dir, "z.ready.pdf")
	if err := os.WriteFile(name, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
	candidates, _, err := Watch(t.Context(), dir, Cursor{})
	if err != nil || len(candidates) != 1 || candidates[0].Name != "z.ready.pdf" {
		t.Fatal("the valid inbox file was starved by non-regular entries", candidates, err)
	}
}

// Regression: oversized ready files are reported and passed, so they never
// hold back a valid file behind them.
func TestWatchPassesOversizedFilesWithTheCursor(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Minute)
	write := func(name string, size int) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 10 {
		write(fmt.Sprintf("a%02d.ready.pdf", index), (10<<20)+1)
	}
	write("z.ready.pdf", 8)
	first, cursor, err := Watch(t.Context(), dir, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 10 || cursor.After != "a09.ready.pdf" {
		t.Fatal("the first poll must report all ten oversized files and pass them", len(first), cursor.After)
	}
	for index, candidate := range first {
		if candidate.Name != fmt.Sprintf("a%02d.ready.pdf", index) || candidate.Rejected == "" || candidate.Bytes != nil {
			t.Fatalf("oversized file %d was not reported as a byte-less rejection: %+v", index, candidate)
		}
	}
	second, _, err := Watch(t.Context(), dir, cursor)
	if err != nil || len(second) != 1 || second[0].Name != "z.ready.pdf" || second[0].Rejected != "" || len(second[0].Bytes) != 8 {
		t.Fatal("the valid file behind ten oversized files was not imported", second, err)
	}
}

// Regression: a producer clock that runs ahead cannot hold the inbox; the
// settling period runs from the worker's first observation.
func TestWatchSettlesFutureTimestampsFromFirstObservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ready.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	first, cursor, err := Watch(t.Context(), dir, Cursor{})
	if err != nil || len(first) != 0 || cursor.Settling != "a.ready.pdf" || cursor.SettlingSince.IsZero() {
		t.Fatal("a file with a future timestamp must first settle", first, cursor, err)
	}
	cursor.SettlingSince = cursor.SettlingSince.Add(-watchSettleTime)
	second, cursor, err := Watch(t.Context(), dir, cursor)
	if err != nil || len(second) != 1 || second[0].Name != "a.ready.pdf" || cursor.Settling != "" {
		t.Fatal("a file unchanged since its first observation must be read", second, cursor, err)
	}
}

// Regression: a replacement under the same name, size and time settles anew;
// names that are not valid UTF-8 are ignored instead of blocking the inbox.
func TestWatchRestartsSettlingForAReplacementAndIgnoresInvalidNames(t *testing.T) {
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
	// Replace atomically: the new file exists before the old one goes away, so
	// it cannot reuse the old inode.
	write(filepath.Join(dir, "replacement.tmp"))
	if err = os.Rename(filepath.Join(dir, "replacement.tmp"), path); err != nil {
		t.Fatal(err)
	}
	cursor.SettlingSince = cursor.SettlingSince.Add(-watchSettleTime)
	got, next, err := Watch(t.Context(), dir, cursor)
	if err != nil || len(got) != 0 || next.Settling != "a.ready.pdf" || !next.SettlingSince.After(cursor.SettlingSince) {
		t.Fatal("a replacement must restart its settling period", got, next, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "a\xff.ready.pdf")
	if err = os.WriteFile(invalid, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Skip("this filesystem refuses non-UTF-8 names:", err)
	}
	valid := filepath.Join(dir, "z.ready.pdf")
	if err = os.WriteFile(valid, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	for _, name := range []string{invalid, valid} {
		if err = os.Chtimes(name, old, old); err != nil {
			t.Fatal(err)
		}
	}
	got, next, err = Watch(t.Context(), dir, Cursor{})
	if err != nil || len(got) != 1 || got[0].Name != "z.ready.pdf" || next.After != "z.ready.pdf" {
		t.Fatal("an invalid name must be ignored, not block or enter the cursor", got, next, err)
	}
}
