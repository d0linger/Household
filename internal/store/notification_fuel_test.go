package store

import (
	"testing"
	"time"
)

func TestFuelNotificationCalendarStages(t *testing.T) {
	for _, tc := range []struct {
		day   string
		stage int
		want  string
	}{
		{"2026-03-29", 0, "2026-03-29T07:00:00Z"},
		{"2026-03-29", 7, "2026-03-22T08:00:00Z"},
		{"2026-10-25", 0, "2026-10-25T08:00:00Z"},
		{"2026-10-25", 7, "2026-10-18T07:00:00Z"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			got, err := fuelDateEvent(tc.day, "Europe/Vienna", tc.stage)
			if err != nil || got.Format(time.RFC3339) != tc.want {
				t.Fatalf("event=%s error=%v want=%s", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		day, zone string
		stage     int
	}{
		{"2026-02-30", "UTC", 0}, {"2026-03-29", "missing/zone", 0}, {"2026-03-29", "UTC", 1},
	} {
		if _, err := fuelDateEvent(tc.day, tc.zone, tc.stage); err == nil {
			t.Fatalf("accepted invalid calendar input %+v", tc)
		}
	}
}

func TestFuelNotificationDateHistoryAndFreshness(t *testing.T) {
	day := "2026-10-25"
	event, err := fuelDateEvent(day, "Europe/Vienna", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := event.Add(-time.Hour)
	source := fuelNotificationSource{id: "00000000-0000-4000-8000-000000000001", user: "00000000-0000-4000-8000-000000000002",
		zone: "Europe/Vienna", version: 1, active: true, since: &before, updated: before, activated: before, service: &day}
	for _, tc := range []struct {
		name  string
		now   time.Time
		alter func(*fuelNotificationSource)
		want  int
	}{
		{"future", event.Add(-time.Second), nil, 0},
		{"due", event, nil, 1},
		{"last fresh second", event.Add(24*time.Hour - time.Second), nil, 1},
		{"expired", event.Add(24 * time.Hour), nil, 0},
		{"unassigned", event, func(s *fuelNotificationSource) { s.since = nil }, 0},
		{"archived", event, func(s *fuelNotificationSource) { s.active = false }, 0},
		{"new assignment", event.Add(time.Minute), func(s *fuelNotificationSource) { late := event.Add(time.Second); s.since = &late }, 0},
		{"retroactive edit", event.Add(time.Minute), func(s *fuelNotificationSource) { s.updated = event.Add(time.Second) }, 0},
		{"reactivation", event.Add(time.Minute), func(s *fuelNotificationSource) { s.activated = event.Add(time.Second) }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := source
			if tc.alter != nil {
				tc.alter(&candidate)
			}
			got, err := fuelDateIntents(candidate, "00000000-0000-4000-8000-000000000003", "vehicles", 1, tc.now)
			if err != nil || len(got) != tc.want {
				t.Fatalf("intents=%d err=%v want=%d", len(got), err, tc.want)
			}
			for _, intent := range got {
				if err := intent.Validate(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
