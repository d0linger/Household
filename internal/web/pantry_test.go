package web

import (
	"testing"
	"time"
)

func TestPantryDayEnd(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ day, end string }{
		{"2026-03-29", "2026-03-29T22:00:00Z"},
		{"2026-10-25", "2026-10-25T23:00:00Z"},
		{"2026-01-01", "2026-01-01T23:00:00Z"},
	} {
		t.Run(tc.day, func(t *testing.T) {
			end, err := pantryDayEnd(tc.day, "Europe/Vienna", now)
			if err != nil || end.UTC().Format(time.RFC3339) != tc.end {
				t.Fatalf("day cutoff: %v/%v", end, err)
			}
		})
	}
	for _, invalid := range []string{"", "2026-02-30", "2026-1-1", "2027-01-01", "1899-12-31", "2026-01-01T00:00:00Z"} {
		if _, err := pantryDayEnd(invalid, "Europe/Vienna", now); err == nil {
			t.Fatalf("accepted invalid date %q", invalid)
		}
	}
}
