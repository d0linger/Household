package documentinput

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Watch consumes only producer-renamed *.ready.pdf/jpg/png files. It never moves or deletes input files.
func Watch(ctx context.Context, dir string, cursor Cursor) ([]Candidate, Cursor, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, cursor, errors.New("document watch folder unavailable")
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(".")
	if err != nil {
		return nil, cursor, err
	}
	defer func() { _ = f.Close() }()
	// Bounded lexical batches make progress without retaining all directory names.
	chosen := []string{}
	scanned := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, cursor, err
		}
		entries, e := f.ReadDir(200)
		for _, entry := range entries {
			scanned++
			name := entry.Name()
			if scanned > 20000 {
				return nil, cursor, errors.New("watch folder inventory exceeds 20000 entries")
			}
			ext := strings.ToLower(filepath.Ext(name))
			if name <= cursor.After || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(strings.TrimSuffix(strings.ToLower(name), ext), ".ready") || !slices.Contains([]string{".pdf", ".jpg", ".jpeg", ".png"}, ext) {
				continue
			}
			chosen = append(chosen, name)
			slices.Sort(chosen)
			if len(chosen) > 10 {
				chosen = chosen[:10]
			}
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, cursor, e
		}
	}
	out := []Candidate{}
	next := cursor
	for _, name := range chosen {
		if err = ctx.Err(); err != nil {
			return nil, cursor, err
		}
		info, e := root.Lstat(name)
		if e != nil {
			return nil, cursor, e
		}
		if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 10<<20 || time.Since(info.ModTime()) < 2*time.Second {
			continue
		}
		in, e := root.Open(name)
		if e != nil {
			return nil, cursor, e
		}
		before, e := in.Stat()
		if e != nil {
			_ = in.Close()
			return nil, cursor, e
		}
		raw, e := io.ReadAll(io.LimitReader(in, (10<<20)+1))
		after, statErr := in.Stat()
		_ = in.Close()
		if e != nil || statErr != nil || len(raw) > 10<<20 {
			return nil, cursor, errors.New("watch input read limit reached")
		}
		if !os.SameFile(info, before) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return nil, cursor, errors.New("watch input changed during read")
		}
		out = append(out, Candidate{Key: name, Name: name, Bytes: raw})
		next.After = name
	}
	if len(chosen) == 0 {
		next.After = ""
	}
	return out, next, nil
}
