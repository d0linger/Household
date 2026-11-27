//go:build unix

package maintenance

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe without a writer is rejected at once instead of blocking the
// open of a recorded archive, an attachment or a restored file.
func TestNamedPipesAreRejectedWithoutBlocking(t *testing.T) {
	directory := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(directory, "pipe"), 0600); err != nil {
		t.Skipf("named pipes unavailable: %v", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err = os.WriteFile(filepath.Join(directory, "regular"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"regular file": func() error {
			f, info, err := openRegular(root, "regular")
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			raw, err := io.ReadAll(f)
			if err != nil || string(raw) != "synthetic" || info.Size() != int64(len(raw)) {
				return errors.New("regular file did not read back")
			}
			return nil
		},
		"openRegular": func() error {
			f, _, err := openRegular(root, "pipe")
			if err == nil {
				_ = f.Close()
			}
			if !errors.Is(err, errNotRegularFile) {
				return errors.New("pipe was not reported as a non-regular file")
			}
			return nil
		},
		"openArchiveFile": func() error {
			f, err := openArchiveFile(directory, "pipe")
			if err == nil {
				_ = f.Close()
				return errors.New("pipe opened as an archive")
			}
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("pipe reported as a missing archive")
			}
			return nil
		},
		"addArchiveFile": func() error {
			if err := addArchiveFile(context.Background(), tar.NewWriter(io.Discard), root, "pipe", "uploads/pipe", nil); err == nil {
				return errors.New("pipe added to an archive")
			}
			return nil
		},
		"fileMatches": func() error {
			if fileMatches(root, "pipe", File{}) {
				return errors.New("pipe matched a manifest file")
			}
			return nil
		},
	}
	for name, check := range checks {
		done := make(chan error, 1)
		go func() { done <- check() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s blocked on a named pipe without a writer", name)
		}
	}
}
