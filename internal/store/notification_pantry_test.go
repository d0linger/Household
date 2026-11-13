package store

import (
	"testing"
	"time"
)

func TestPantryDigestIntentTimesLocalDayAndSourceFreshness(t *testing.T) {
	parse := func(t *testing.T, raw string) time.Time {
		t.Helper()
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	for _, tc := range []struct {
		name, day, event, available, expiry string
		dayHours                            int
		deliverable                         bool
	}{
		{"spring day has 23 hours", "2026-03-29", "2026-03-29T04:00:00+02:00", "2026-03-29T08:00:00+02:00", "2026-03-30T00:00:00+02:00", 23, true},
		{"autumn day has 25 hours", "2026-10-25", "2026-10-25T04:00:00+01:00", "2026-10-25T08:00:00+01:00", "2026-10-26T00:00:00+01:00", 25, true},
		{"shorter source freshness wins", "2026-03-29", "2026-03-28T11:00:00+01:00", "2026-03-29T09:00:00+02:00", "2026-03-29T12:00:00+02:00", 23, true},
		{"midnight boundary cannot deliver yesterday", "2026-03-29", "2026-03-29T04:00:00+02:00", "2026-03-30T00:00:00+02:00", "2026-03-30T00:00:00+02:00", 23, false},
		{"late quiet window cannot revive yesterday", "2026-10-25", "2026-10-25T04:00:00+01:00", "2026-10-26T00:00:01+01:00", "2026-10-26T00:00:00+01:00", 25, false},
		{"future source remains not before event", "2026-03-29", "2026-03-29T09:00:00+02:00", "2026-03-29T08:00:00+02:00", "2026-03-30T00:00:00+02:00", 23, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, available, wantExpiry := parse(t, tc.event), parse(t, tc.available), parse(t, tc.expiry)
			ref := pantryNotificationReference{Kind: "low_stock", ID: "22222222-2222-4222-8222-222222222222", Episode: 1,
				EventAt: event, Cutoff: event.Add(-time.Minute), ExpiresAt: event.Add(24 * time.Hour)}
			d := pantryNotificationDigest{Schema: 1, Household: "33333333-3333-4333-8333-333333333333", User: "44444444-4444-4444-8444-444444444444",
				Generation: 1, Day: tc.day, Timezone: "Europe/Vienna", References: []pantryNotificationReference{ref}}
			gotEvent, notBefore, expiry, err := pantryDigestIntentTimes(d, available)
			if err != nil {
				t.Fatal(err)
			}
			wantNotBefore := available
			if event.After(wantNotBefore) {
				wantNotBefore = event
			}
			if !gotEvent.Equal(event) || !notBefore.Equal(wantNotBefore) || !expiry.Equal(wantExpiry) {
				t.Fatalf("incorrect daily envelope: event=%s not_before=%s expires=%s", gotEvent, notBefore, expiry)
			}
			if expiry.After(notBefore) != tc.deliverable {
				t.Fatal("daily group crossed its delivery window", expiry, notBefore)
			}
			if d.References[0] != ref || expiry.After(ref.ExpiresAt) {
				t.Fatal("daily envelope changed or extended original source freshness")
			}
			location, err := time.LoadLocation(d.Timezone)
			if err != nil {
				t.Fatal(err)
			}
			day, err := time.ParseInLocation(time.DateOnly, d.Day, location)
			if err != nil || day.AddDate(0, 0, 1).Sub(day) != time.Duration(tc.dayHours)*time.Hour {
				t.Fatal("fixture did not exercise the intended DST day", err)
			}
		})
	}
}

func TestPantryDigestIntentTimesRejectsInvalidCalendar(t *testing.T) {
	for _, tc := range []struct{ name, day, zone string }{
		{"invalid timezone", "2026-03-29", "Invalid/Household"},
		{"invalid day", "2026-02-30", "Europe/Vienna"},
		{"timestamp is not a calendar day", "2026-03-29T00:00:00Z", "UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := pantryDigestIntentTimes(pantryNotificationDigest{Day: tc.day, Timezone: tc.zone}, time.Time{}); err == nil {
				t.Fatal("invalid grouping calendar accepted")
			}
		})
	}
}
