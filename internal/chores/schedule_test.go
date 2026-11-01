package chores

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCivilScheduleProjectsCalendarDatesWithoutMonthlyDrift(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rule     Schedule
		from, to string
		want     []string
	}{
		{"month31", Schedule{Kind: "monthly", StartsOn: "2024-01-31", MonthDay: 31}, "2024-01-01", "2024-04-30", []string{"2024-01-31", "2024-02-29", "2024-03-31", "2024-04-30"}},
		{"month30", Schedule{Kind: "monthly", StartsOn: "2025-01-30", MonthDay: 30}, "2025-01-01", "2025-03-31", []string{"2025-01-30", "2025-02-28", "2025-03-30"}},
		{"weekAnchor", Schedule{Kind: "weekly", StartsOn: "2026-10-28", Interval: 2, Weekdays: []int{1, 5}}, "2026-10-28", "2026-11-15", []string{"2026-10-30", "2026-11-09", "2026-11-13"}},
		{"season", Schedule{Kind: "monthly", StartsOn: "2026-01-31", MonthDay: 31, Months: []int{2, 4}}, "2026-01-01", "2026-05-01", []string{"2026-02-28", "2026-04-30"}},
		{"endInclusive", Schedule{Kind: "days", StartsOn: "2026-01-01", Interval: 2, EndsOn: "2026-01-05"}, "2026-01-01", "2026-01-10", []string{"2026-01-01", "2026-01-03", "2026-01-05"}},
		{"once", Schedule{Kind: "once", StartsOn: "2026-02-02"}, "2026-02-01", "2026-02-10", []string{"2026-02-02"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.Timezone = "Europe/Vienna"
			projection, err := Project(tc.rule, tc.from, tc.to, 100)
			if err != nil {
				t.Fatal(err)
			}
			actual := []string{}
			for _, slot := range projection.Slots {
				actual = append(actual, slot.Day)
			}
			if !reflect.DeepEqual(actual, tc.want) || projection.More {
				t.Fatalf("dates %v, want %v; more=%v", actual, tc.want, projection.More)
			}
		})
	}
}

func TestCivilScheduleDSTPolicyResolvesFoldsGapsAndWholeDays(t *testing.T) {
	for _, tc := range []struct{ zone, day, clock, want string }{
		{"Europe/Vienna", "2026-03-29", "02:30", "2026-03-29T01:30:00Z"},
		{"Europe/Vienna", "2026-10-25", "02:30", "2026-10-25T00:30:00Z"},
		{"Australia/Lord_Howe", "2026-10-04", "02:15", "2026-10-03T15:45:00Z"},
		{"Pacific/Apia", "2011-12-30", "09:00", "2011-12-30T19:00:00Z"},
	} {
		t.Run(tc.zone+tc.day, func(t *testing.T) {
			location, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			for range 20 {
				instant, _, err := ResolveLocal(tc.day, tc.clock, location)
				if err != nil || instant.UTC().Format(time.RFC3339) != tc.want {
					t.Fatalf("resolve %v %v, want %s", instant, err, tc.want)
				}
			}
		})
	}
}

func TestCivilScheduleCalendarDaysAndActualCompletionRemainDistinct(t *testing.T) {
	rule := Schedule{Kind: "days", Interval: 1, StartsOn: "2026-03-28", LocalTime: "09:00", Timezone: "Europe/Vienna"}
	p, err := Project(rule, "2026-03-28", "2026-03-30", 10)
	if err != nil || len(p.Slots) != 3 {
		t.Fatal(p, err)
	}
	if p.Slots[1].DueAt.Sub(p.Slots[0].DueAt) != 23*time.Hour {
		t.Fatal("DST day was treated as 24 hours")
	}
	rule.Anchor = "completion"
	actual := time.Date(2026, 4, 8, 20, 0, 0, 0, time.UTC)
	next, err := AfterCompletion(rule, actual)
	if err != nil || next.Day != "2026-04-09" || next.DueAt.UTC().Format(time.RFC3339) != "2026-04-09T07:00:00Z" {
		t.Fatal("successor ignored actual completion", next, err)
	}
	rule.EndsOn = "2026-04-08"
	if _, err = AfterCompletion(rule, actual); !errors.Is(err, ErrNoOccurrence) {
		t.Fatal("finite completion rule extended", err)
	}
}

func TestCivilScheduleBoundsAndInvalidCombinations(t *testing.T) {
	valid := Schedule{Kind: "days", StartsOn: "2026-01-01", Timezone: "Europe/Vienna"}
	for _, change := range []func(*Schedule){func(r *Schedule) { r.Timezone = "Not/AZone" }, func(r *Schedule) { r.StartsOn = "2026-02-30" }, func(r *Schedule) { r.Interval = 367 },
		func(r *Schedule) { r.Policy = "guess" }, func(r *Schedule) { r.Anchor = "completion"; r.Months = []int{1} }, func(r *Schedule) { r.Kind = "weekly" },
		func(r *Schedule) { r.Weekdays = []int{1} }, func(r *Schedule) { r.Months = []int{1, 1} }, func(r *Schedule) { r.LocalTime = "9:00" }, func(r *Schedule) { r.Kind = "rrule" }} {
		rule := valid
		change(&rule)
		if _, err := Normalize(rule); err == nil {
			t.Fatalf("invalid rule accepted: %+v", rule)
		}
	}
	if _, err := Project(valid, "2026-01-01", "2027-01-02", 10); err == nil {
		t.Fatal("unbounded window accepted")
	}
	p, err := Project(valid, "2026-01-01", "2026-01-10", 2)
	if err != nil || len(p.Slots) != 2 || !p.More {
		t.Fatal("bounded projection hid backlog", p, err)
	}
	location, _ := time.LoadLocation("UTC")
	if _, err = DayBoundary("2201-01-01", location); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ResolveLocal("2201-01-01", "00:00", location); err == nil {
		t.Fatal("exclusive boundary accepted as occurrence date")
	}
}
