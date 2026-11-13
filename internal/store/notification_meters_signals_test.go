package store

import (
	"slices"
	"testing"
	"time"
)

func TestMeterActualHourlyCoverageRequiresContiguousMeasuredHistory(t *testing.T) {
	end := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	history := make([]meterHistoryHour, 169)
	for n := range history {
		through := end.Add(-time.Duration(n) * time.Hour)
		history[n] = meterHistoryHour{from: through.Add(-time.Hour), through: through, covered: 3600, quantity: true}
	}
	for _, test := range []struct {
		name   string
		change func([]meterHistoryHour) []meterHistoryHour
		latest time.Time
		want   bool
	}{
		{"six complete days", func(h []meterHistoryHour) []meterHistoryHour { return h[:144] }, end, true},
		{"current partial hour", func(h []meterHistoryHour) []meterHistoryHour { return h }, end.Add(45 * time.Minute), true},
		{"insufficient days", func(h []meterHistoryHour) []meterHistoryHour { return h[:143] }, end, false},
		{"stale aggregate tail", func(h []meterHistoryHour) []meterHistoryHour { return h }, end.Add(time.Hour), false},
		{"future aggregate", func(h []meterHistoryHour) []meterHistoryHour { return h }, end.Add(-time.Minute), false},
		{"missing hour", func(h []meterHistoryHour) []meterHistoryHour { return append(h[:70], h[71:]...) }, end, false},
		{"estimated coverage", func(h []meterHistoryHour) []meterHistoryHour { h[80].estimated = 1; return h }, end, false},
		{"missing quantity", func(h []meterHistoryHour) []meterHistoryHour { h[30].quantity = false; return h }, end, false},
		{"partial actual coverage", func(h []meterHistoryHour) []meterHistoryHour { h[20].covered = 3599; return h }, end, false},
		{"overlapping hour", func(h []meterHistoryHour) []meterHistoryHour {
			h[12].through = h[12].through.Add(time.Minute)
			return h
		}, end, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := meterHasActualHours(test.change(slices.Clone(history)), test.latest); got != test.want {
				t.Fatalf("measured history = %v, want %v", got, test.want)
			}
		})
	}
}
