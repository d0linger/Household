//go:build unix

package documentinput

import (
	"errors"
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
