package expenseedit

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"household/internal/value"
)

var ErrStaleLines = errors.New("expense allocation rows changed since complete read")

type LineVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type LineChanges struct {
	Save   []Line
	Delete []LineVersion
}

// ReconcileLines requires the complete saved read set, explicit removals and
// freshly locked current children of the same entry. The writer establishes
// parent identity, authorization, generation and draft state before calling it.
// It never authorizes a caller or turns a partial list into replacement intent.
func ReconcileLines(draft Draft, baseline, removed, current []LineVersion) (LineChanges, error) {
	invalid := func() (LineChanges, error) {
		return LineChanges{}, Problems{"lines": "Vollständige Kategoriezeilen und ausdrückliche Entfernungen mit aktuellen Versionen prüfen"}
	}
	if len(draft.Lines) > LineLimit || len(baseline) > LineLimit || len(removed) > LineLimit || len(current) > LineLimit {
		return invalid()
	}
	before, err := lineVersionMap(baseline)
	if err != nil {
		return invalid()
	}
	now, err := lineVersionMap(current)
	if err != nil {
		return invalid()
	}
	deletions, err := lineVersionMap(removed)
	if err != nil {
		return invalid()
	}
	if draft.EntryID == "" {
		if len(before) != 0 || len(now) != 0 || len(deletions) != 0 {
			return invalid()
		}
	} else if !value.ValidUUID(draft.EntryID) {
		return invalid()
	}
	// This must also detect legacy child writes that did not change the parent
	// version, and children added or removed since the editor's complete read.
	if !maps.Equal(before, now) {
		return LineChanges{}, ErrStaleLines
	}
	out := LineChanges{Save: make([]Line, 0, len(draft.Lines)), Delete: make([]LineVersion, 0, len(removed))}
	seenIDs, seenKeys := make(map[string]bool, len(draft.Lines)), make(map[string]bool, len(draft.Lines))
	for position, raw := range draft.Lines {
		line := raw
		line.ID, line.Key = strings.ToLower(line.ID), strings.ToLower(line.Key)
		if !value.ValidUUID(line.Key) || seenKeys[line.Key] || line.Position != position {
			return invalid()
		}
		seenKeys[line.Key] = true
		if line.ID == "" {
			if line.Version != 0 {
				return invalid()
			}
		} else {
			if !value.ValidUUID(line.ID) || seenIDs[line.ID] || before[line.ID] != line.Version || line.Version <= 0 {
				return invalid()
			}
			seenIDs[line.ID] = true
		}
		out.Save = append(out.Save, line)
	}
	for id, version := range deletions {
		if seenIDs[id] || before[id] != version || version <= 0 {
			return invalid()
		}
		out.Delete = append(out.Delete, LineVersion{ID: id, Version: version})
	}
	if len(seenIDs)+len(deletions) != len(before) {
		return invalid()
	}
	slices.SortFunc(out.Delete, func(a, b LineVersion) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func lineVersionMap(lines []LineVersion) (map[string]int64, error) {
	out := make(map[string]int64, len(lines))
	for _, line := range lines {
		id := strings.ToLower(line.ID)
		if !value.ValidUUID(id) || line.Version <= 0 || out[id] != 0 {
			return nil, Problems{"lines": "Eindeutige Kategoriezeilen mit positiven gespeicherten Versionen erforderlich"}
		}
		out[id] = line.Version
	}
	return out, nil
}
