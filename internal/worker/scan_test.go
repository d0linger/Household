package worker

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryScanVisitsLargeDirectoryInBoundedBatches(t *testing.T) {
	t.Parallel()
	const total = 10451
	directory := &fakeDirectory{remaining: total}
	scan := directoryScan{directory: directory}
	t.Cleanup(func() { _ = scan.close() })
	seen := make(map[string]bool, total)
	for cycle := 0; directory.closed == 0; cycle++ {
		if cycle > total/200+1 {
			t.Fatal("directory scan restarted before reaching its end")
		}
		entries, err := scan.next(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 200 {
			t.Fatalf("unbounded cycle returned %d entries", len(entries))
		}
		for _, entry := range entries {
			if seen[entry.Name()] {
				t.Fatalf("entry %s was revisited before EOF", entry.Name())
			}
			seen[entry.Name()] = true
		}
	}
	if len(seen) != total || directory.largestRead > 200 || directory.closed != 1 {
		t.Fatalf("visited=%d max read=%d closes=%d", len(seen), directory.largestRead, directory.closed)
	}
}

func TestDirectoryScanClosesOnReadFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("synthetic directory read failure")
	directory := &fakeDirectory{readErr: want}
	scan := directoryScan{directory: directory}
	if _, err := scan.next(nil); !errors.Is(err, want) {
		t.Fatalf("read error=%v, want propagated failure", err)
	}
	if err := scan.close(); err != nil || directory.closed != 1 {
		t.Fatalf("close error=%v, closes=%d", err, directory.closed)
	}
}

func TestDirectoryScanClosesRealHandleAndStartsNextPass(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "one"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	var scan directoryScan
	t.Cleanup(func() { _ = scan.close() })
	entries, err := scan.next(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "one" {
		t.Fatalf("first pass entries=%d error=%v", len(entries), err)
	}
	handle := scan.directory
	if handle == nil {
		t.Fatal("expected cursor to remain open until its EOF read")
	}
	if entries, err = scan.next(root); err != nil || len(entries) != 0 {
		t.Fatalf("EOF entries=%d error=%v", len(entries), err)
	}
	if err = handle.Close(); err == nil {
		t.Fatal("EOF retained an open directory handle")
	}
	if entries, err = scan.next(root); err != nil || len(entries) != 1 {
		t.Fatalf("next full pass entries=%d error=%v", len(entries), err)
	}
	handle = scan.directory
	if err = scan.close(); err != nil {
		t.Fatal(err)
	}
	if err = handle.Close(); err == nil {
		t.Fatal("explicit close retained an open directory handle")
	}
	if err = scan.close(); err != nil {
		t.Fatalf("repeated close failed: %v", err)
	}
}

type fakeDirectory struct {
	remaining, position, largestRead, closed int
	readErr                                  error
}

func (d *fakeDirectory) ReadDir(n int) ([]os.DirEntry, error) {
	if d.readErr != nil {
		return nil, d.readErr
	}
	if d.closed != 0 {
		return nil, os.ErrClosed
	}
	d.largestRead = max(d.largestRead, n)
	n = min(n, d.remaining)
	entries := make([]os.DirEntry, 0, n)
	for range n {
		entries = append(entries, fakeEntry{name: fmt.Sprintf("file-%05d", d.position)})
		d.position++
	}
	d.remaining -= n
	if d.remaining == 0 {
		return entries, io.EOF
	}
	return entries, nil
}

func (d *fakeDirectory) Close() error {
	d.closed++
	return nil
}

type fakeEntry struct{ name string }

func (e fakeEntry) Name() string             { return e.name }
func (fakeEntry) IsDir() bool                { return false }
func (fakeEntry) Type() fs.FileMode          { return 0 }
func (fakeEntry) Info() (fs.FileInfo, error) { return nil, errors.New("unused fixture metadata") }
