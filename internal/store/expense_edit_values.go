package store

import (
	"slices"
	"strings"

	"household/internal/expenseedit"
	"household/internal/value"
)

// Read-set ordering is not user intent; final category-row ordering is. Copy
// and canonicalize only the former before binding durable request identity.
func canonicalExpenseBasis(input []expenseedit.LineVersion) ([]expenseedit.LineVersion, error) {
	if len(input) > expenseedit.LineLimit {
		return nil, expenseedit.Problems{"lines": "Vollständige Zeilenbasis auf100Einträge begrenzen"}
	}
	out := make([]expenseedit.LineVersion, len(input))
	for index, line := range input {
		line.ID = strings.ToLower(line.ID)
		if !value.ValidUUID(line.ID) || line.Version <= 0 {
			return nil, expenseedit.Problems{"lines": "Eindeutige vollständige Zeilenbasis mit aktuellen Versionen erforderlich"}
		}
		out[index] = line
	}
	slices.SortFunc(out, func(a, b expenseedit.LineVersion) int { return strings.Compare(a.ID, b.ID) })
	for index := 1; index < len(out); index++ {
		if out[index-1].ID == out[index].ID {
			return nil, expenseedit.Problems{"lines": "Eine gespeicherte Zeile darf nur einmal in der Basis vorkommen"}
		}
	}
	return out, nil
}
