package notification

import (
	"testing"
	"time"
)

func fixtureIntent() Intent {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	return Intent{Schema: 1, HouseholdID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222",
		Module: "recipes", Generation: 1, ResourceType: "cooking_timers", ResourceID: "33333333-3333-4333-8333-333333333333",
		Revision: 1, Occurrence: "deadline", Category: "cooking_timer", EventAt: now, NotBefore: now.Add(time.Minute), ExpiresAt: now.Add(2 * time.Minute)}
}

func TestIntentRejectsUntrustedReferencesAndExcessFreshness(t *testing.T) {
	if err := fixtureIntent().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Intent){
		"schema":                func(i *Intent) { i.Schema = 2 },
		"category":              func(i *Intent) { i.Category = " arbitrary " },
		"module":                func(i *Intent) { i.Module = "documents" },
		"household":             func(i *Intent) { i.HouseholdID = "other" },
		"resource":              func(i *Intent) { i.ResourceID = "../../secret" },
		"revision":              func(i *Intent) { i.Revision = 0 },
		"unsafe integer":        func(i *Intent) { i.Revision = MaxRevision + 1 },
		"generation":            func(i *Intent) { i.Generation = 0 },
		"time order":            func(i *Intent) { i.NotBefore = i.EventAt.Add(-time.Second) },
		"empty window":          func(i *Intent) { i.ExpiresAt = i.NotBefore },
		"timer freshness":       func(i *Intent) { i.ExpiresAt = i.NotBefore.Add(61 * time.Second) },
		"payload in occurrence": func(i *Intent) { i.Occurrence = "<script>secret</script>" },
		"oversized occurrence": func(i *Intent) {
			for len(i.Occurrence) < 161 {
				i.Occurrence += "x"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			i := fixtureIntent()
			mutate(&i)
			if i.Validate() == nil {
				t.Fatal("invalid reference accepted")
			}
		})
	}
	core := fixtureIntent()
	core.Module = ""
	core.Generation = 0
	core.Category = "test"
	if err := core.Validate(); err != nil {
		t.Fatal(err)
	}
	core.Generation = 1
	if core.Validate() == nil {
		t.Fatal("core intent accepted a module generation")
	}
}

func TestDedupeSeparatesRecipientsRevisionsAndGenerations(t *testing.T) {
	i := fixtureIntent()
	key := i.DedupeKey()
	for name, mutate := range map[string]func(*Intent){
		"recipient":  func(i *Intent) { i.UserID = i.HouseholdID },
		"generation": func(i *Intent) { i.Generation++ },
		"revision":   func(i *Intent) { i.Revision++ },
		"occurrence": func(i *Intent) { i.Occurrence = "other" },
		"category":   func(i *Intent) { i.Category = "test" },
		"household":  func(i *Intent) { i.HouseholdID = i.UserID },
	} {
		t.Run(name, func(t *testing.T) {
			copy := i
			mutate(&copy)
			if copy.DedupeKey() == key {
				t.Fatal("business identities collided")
			}
		})
	}
	i.EventAt = i.EventAt.Add(time.Second)
	if i.DedupeKey() != key {
		t.Fatal("retry clock created a new logical event")
	}
	p := Categories()
	p[0].Freshness = 100 * time.Hour
	current, _ := Lookup("chores_due")
	if current.Freshness != time.Hour {
		t.Fatal("caller mutated the compiled registry")
	}
}

func TestQuietHoursAcrossMidnightAndDST(t *testing.T) {
	minute := func(v int) *int { return &v }
	for _, tc := range []struct {
		name, at, want string
		start, end     int
	}{
		{"ordinary", "2026-11-03T21:30:00Z", "2026-11-04T06:00:00Z", 22 * 60, 7 * 60},
		{"end exclusive", "2026-11-04T06:00:00Z", "2026-11-04T06:00:00Z", 22 * 60, 7 * 60},
		{"spring missing end", "2026-03-29T00:45:00Z", "2026-03-29T01:00:00Z", 90, 150},
		{"autumn repeated hour", "2026-10-25T01:15:00Z", "2026-10-25T02:00:00Z", 120, 180},
		{"day interval", "2026-11-03T12:01:17Z", "2026-11-03T13:00:00Z", 12 * 60, 14 * 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, tc.at)
			want, _ := time.Parse(time.RFC3339, tc.want)
			q := QuietHours{Timezone: "Europe/Vienna", Start: minute(tc.start), End: minute(tc.end)}
			got, err := q.NextAllowed(at)
			if err != nil || !got.Equal(want) {
				t.Fatalf("next=%v want=%v err=%v", got, want, err)
			}
		})
	}
	for _, q := range []QuietHours{
		{Timezone: "Local"}, {Timezone: "missing/zone"}, {Timezone: "UTC", Start: minute(10)},
		{Timezone: "UTC", Start: minute(10), End: minute(10)}, {Timezone: "UTC", Start: minute(-1), End: minute(100)},
	} {
		if q.Validate() == nil {
			t.Fatal("invalid quiet hours accepted")
		}
	}
}
