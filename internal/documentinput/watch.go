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
	"unicode/utf8"
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
			// Known non-regular entries (FIFOs, sockets, devices, links) never fill
			// the ten-file batch; openWatchCandidate still checks later swaps.
			// Names that are not valid UTF-8 cannot be stored losslessly in the
			// JSON cursor or as a document name, so they are ignored entirely.
			if name <= cursor.After || !utf8.ValidString(name) || !entry.Type().IsRegular() || !strings.HasSuffix(strings.TrimSuffix(strings.ToLower(name), ext), ".ready") || !slices.Contains([]string{".pdf", ".jpg", ".jpeg", ".png"}, ext) {
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
		if !info.Mode().IsRegular() {
			continue
		}
		// A file that may still be written stops this batch, so the cursor never
		// passes it; a later poll picks it up.
		if !watchSettled(cursor, name, info, time.Now()) {
			if !sameSettlingObservation(cursor, name, info) {
				next.SettlingSince = time.Now()
			}
			next.Settling, next.SettlingSize, next.SettlingMod, next.SettlingFile = name, info.Size(), info.ModTime(), watchFileIdentity(info)
			break
		}
		if next.Settling == name {
			next.Settling, next.SettlingSize, next.SettlingMod, next.SettlingSince, next.SettlingFile = "", 0, time.Time{}, time.Time{}, ""
		}
		in, before, e := openWatchCandidate(root, name)
		if errors.Is(e, errWatchNotRegular) {
			continue
		}
		if e != nil {
			return nil, cursor, e
		}
		// Decide on the opened file: a replacement after Lstat is retried later.
		if !os.SameFile(info, before) || before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) {
			_ = in.Close()
			break
		}
		// An empty or oversized file can never be imported: report it and move
		// the cursor past it, so it cannot hold back the files behind it.
		if before.Size() < 1 || before.Size() > 10<<20 {
			_ = in.Close()
			out = append(out, Candidate{Key: name, Name: name, Rejected: "watch input is empty or larger than 10 MiB"})
			next.After = name
			continue
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

var errWatchNotRegular = errors.New("watch input is not a regular file")

// watchSettleTime is how long a file must stay unchanged before it is read.
const watchSettleTime = 2 * time.Second

// watchSettled reports whether name may be read: its modification time is at
// least watchSettleTime in the past, or the worker first saw it unchanged at
// least that long ago (a producer clock running ahead).
func watchSettled(cursor Cursor, name string, info os.FileInfo, now time.Time) bool {
	if age := now.Sub(info.ModTime()); age >= watchSettleTime {
		return true
	}
	return sameSettlingObservation(cursor, name, info) && !cursor.SettlingSince.IsZero() && now.Sub(cursor.SettlingSince) >= watchSettleTime
}

// sameSettlingObservation reports whether info is the file the cursor last
// observed settling: same name, size, time and (where available) identity.
func sameSettlingObservation(cursor Cursor, name string, info os.FileInfo) bool {
	return cursor.Settling == name && cursor.SettlingSize == info.Size() && cursor.SettlingMod.Equal(info.ModTime()) &&
		cursor.SettlingFile == watchFileIdentity(info)
}

// openWatchCandidate opens name below root without blocking and checks the
// opened descriptor itself: the earlier Lstat only saw a replaceable pathname.
func openWatchCandidate(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	in, err := root.OpenFile(name, os.O_RDONLY|watchOpenFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := in.Stat()
	if err != nil {
		_ = in.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = in.Close()
		return nil, nil, errWatchNotRegular
	}
	return in, info, nil
}
