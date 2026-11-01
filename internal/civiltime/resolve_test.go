package civiltime

import (
	"testing"
	"time"
)

func TestResolvePreservesExplicitFoldAndGapPolicy(t *testing.T) {
	for _, tc := range []struct{ zone, day, clock, want, policy string }{
		{"Europe/Vienna", "2026-03-29", "02:30", "2026-03-29T01:30:00Z", "gap_forward"},
		{"Europe/Vienna", "2026-10-25", "02:30", "2026-10-25T00:30:00Z", "fold_earlier"},
		{"Australia/Lord_Howe", "2026-10-04", "02:15", "2026-10-03T15:45:00Z", "gap_forward"},
		{"Pacific/Apia", "2011-12-30", "09:00", "2011-12-30T19:00:00Z", "gap_forward"},
		{"UTC", "2026-01-01", "09:00", "2026-01-01T09:00:00Z", "exact"},
	} {
		t.Run(tc.zone+tc.day, func(t *testing.T) {
			location, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			day, err := time.Parse(time.DateOnly, tc.day)
			if err != nil {
				t.Fatal(err)
			}
			got, policy, err := Resolve(day, tc.clock, location)
			if err != nil || got.Format(time.RFC3339) != tc.want || policy != tc.policy {
				t.Fatal(got, policy, err)
			}
		})
	}
	if _, _, err := Resolve(time.Now(), "09:00", nil); err == nil {
		t.Fatal("missing zone accepted")
	}
	if _, _, err := Resolve(time.Now(), "9:00", time.UTC); err == nil {
		t.Fatal("ambiguous clock form accepted")
	}
}
