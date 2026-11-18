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
