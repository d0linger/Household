package notification

import (
	"testing"
	"time"
)

func TestCalendarStageCivilDatesAcrossDST(t *testing.T) {
	for _, tc := range []struct {
		day     string
		advance int
		want    string
	}{
		{"2026-03-29", 0, "2026-03-29T07:00:00Z"}, {"2026-03-29", 7, "2026-03-22T08:00:00Z"},
		{"2026-10-25", 0, "2026-10-25T08:00:00Z"}, {"2026-10-25", 7, "2026-10-18T07:00:00Z"},
		{"2028-03-01", 1, "2028-02-29T08:00:00Z"},
	} {
		got, err := CalendarStage(tc.day, "Europe/Vienna", "09:00", tc.advance)
		if err != nil || got.UTC().Format(time.RFC3339) != tc.want {
			t.Fatalf("%s before%d: %v %v", tc.day, tc.advance, got, err)
		}
	}
	for _, tc := range []struct {
		day, zone, clock string
		advance          int
	}{
		{"2026-02-30", "Europe/Vienna", "09:00", 0}, {"2026-3-1", "Europe/Vienna", "09:00", 0},
		{"2026-03-01", "Unknown/Zone", "09:00", 0}, {"2026-03-01", "UTC", "bad", 0},
		{"2026-03-01", "UTC", "09:00", -1}, {"2026-03-01", "UTC", "09:00", 366},
	} {
		if _, err := CalendarStage(tc.day, tc.zone, tc.clock, tc.advance); err == nil {
			t.Fatal("invalid calendar accepted", tc)
		}
	}
}
