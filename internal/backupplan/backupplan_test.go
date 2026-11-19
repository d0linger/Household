package backupplan

import (
	"testing"
	"time"
)

func vienna(t *testing.T) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Skip("time zone database unavailable")
	}
	return zone
}

func at(t *testing.T, zone *time.Location, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, zone)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestLastSlot(t *testing.T) {
	zone := vienna(t)
	for _, tc := range []struct {
		now, local string
		interval   int
		want       string
	}{
		{"2026-11-19 10:00", "03:00", 24, "2026-11-19 03:00"},
		{"2026-11-19 02:59", "03:00", 24, "2026-11-18 03:00"},
		{"2026-11-19 03:00", "03:00", 24, "2026-11-19 03:00"},
		{"2026-11-19 10:00", "03:00", 6, "2026-11-19 09:00"},
		{"2026-11-19 02:00", "03:00", 6, "2026-11-18 21:00"},
		{"2026-11-19 23:30", "00:15", 1, "2026-11-19 23:15"},
		{"2026-11-19 10:00", "03:00", 8, "2026-11-19 03:00"},
		{"2026-11-19 11:00", "03:00", 8, "2026-11-19 11:00"},
		// Daylight saving ends on 2026-10-25 in Vienna: slots stay on the wall clock.
		{"2026-10-25 12:00", "03:00", 24, "2026-10-25 03:00"},
	} {
		got, err := LastSlot(at(t, zone, tc.now), zone, tc.local, tc.interval)
		if err != nil {
			t.Fatal(err)
		}
		if want := at(t, zone, tc.want); !got.Equal(want) {
			t.Fatalf("LastSlot(%s,%s,%d) = %s, want %s", tc.now, tc.local, tc.interval, got.In(zone), want)
		}
	}
}

func TestLastSlotMultiDayIntervalsAreStable(t *testing.T) {
	zone := vienna(t)
	now := at(t, zone, "2026-11-19 10:00")
	weekly, err := LastSlot(now, zone, "03:00", 168)
	if err != nil {
		t.Fatal(err)
	}
	if weekly.After(now) || now.Sub(weekly) >= 7*24*time.Hour || weekly.Hour() != 3 {
		t.Fatalf("weekly slot = %s", weekly)
	}
	for hours := 1; hours < 24*7; hours++ {
		later, _ := LastSlot(now.Add(time.Duration(hours)*time.Hour), zone, "03:00", 168)
		if later.Before(weekly) || later.After(weekly) && later.Sub(weekly) != 7*24*time.Hour {
			t.Fatalf("weekly slot moved irregularly: %s → %s", weekly, later)
		}
	}
	if _, err = LastSlot(now, zone, "25:00", 24); err == nil {
		t.Fatal("invalid clock accepted")
	}
}

func TestDueAndEvaluate(t *testing.T) {
	zone := vienna(t)
	now := at(t, zone, "2026-11-19 10:00")
	before := at(t, zone, "2026-11-19 02:00")
	after := at(t, zone, "2026-11-19 03:05")
	if due, _ := Due(now, zone, "03:00", 24, nil); !due {
		t.Fatal("never attempted is due")
	}
	if due, _ := Due(now, zone, "03:00", 24, &before); !due {
		t.Fatal("attempt before slot is due")
	}
	if due, _ := Due(now, zone, "03:00", 24, &after); due {
		t.Fatal("attempt after slot is not due")
	}
	if h := Evaluate(now, zone, "03:00", 24, true, &after, false); h.State != StateOK || h.NextDue == nil {
		t.Fatalf("fresh backup health = %+v", h)
	}
	old := at(t, zone, "2026-11-17 03:05")
	if h := Evaluate(now, zone, "03:00", 24, true, &old, false); h.State != StateOverdue {
		t.Fatalf("old backup health = %+v", h)
	}
	// Within the grace period after the expected run it stays ok.
	if h := Evaluate(at(t, zone, "2026-11-20 03:30"), zone, "03:00", 24, true, &after, false); h.State != StateOK {
		t.Fatalf("grace period health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, true, &after, true); h.State != StateFailed {
		t.Fatalf("failed attempt health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, true, nil, false); h.State != StateNever {
		t.Fatalf("never health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, false, nil, true); h.State != StateDisabled {
		t.Fatalf("disabled health = %+v", h)
	}
}

func TestProbeStale(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	recent := now.Add(-3 * 24 * time.Hour)
	old := now.Add(-9 * 24 * time.Hour)
	if ProbeStale(now, 7, &recent) || !ProbeStale(now, 7, &old) || !ProbeStale(now, 7, nil) {
		t.Fatal("weekly probe staleness")
	}
	if ProbeStale(now, 0, &old) {
		t.Fatal("disabled probe is only stale after 30 days")
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	for field, change := range map[string]func(*Settings){
		"local_time":             func(s *Settings) { s.LocalTime = "3:00" },
		"local_interval_hours":   func(s *Settings) { s.LocalIntervalHours = 5 },
		"keep_daily":             func(s *Settings) { s.KeepDaily = 0 },
		"keep_weekly":            func(s *Settings) { s.KeepWeekly = -1 },
		"keep_monthly":           func(s *Settings) { s.KeepMonthly = 121 },
		"offsite_interval_hours": func(s *Settings) { s.OffsiteIntervalHours = 36 },
		"offsite_keep_daily":     func(s *Settings) { s.OffsiteKeepDaily = 91 },
		"probe_interval_days":    func(s *Settings) { s.ProbeIntervalDays = 3 },
		"probe_target":           func(s *Settings) { s.ProbeTarget = "s3" },
	} {
		s := Defaults()
		change(&s)
		if err := s.Validate(); err == nil || err.Error() != field {
			t.Fatalf("%s: err = %v", field, err)
		}
	}
}

func TestRestoreConfirmationIsBoundToTheSnapshot(t *testing.T) {
	snapshot := "0123abcd" + "ef" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789"[:54]
	if !ValidSnapshot(snapshot) || ValidSnapshot("0123abcd") || ValidSnapshot("ZZ"+snapshot[2:]) {
		t.Fatal("snapshot validation")
	}
	if got := RestoreConfirmation(snapshot); got != "WIEDERHERSTELLEN 0123ABCD" {
		t.Fatalf("phrase = %q", got)
	}
	if !ConfirmationMatches(snapshot, "  wiederherstellen   0123abcd ") {
		t.Fatal("normalized phrase rejected")
	}
	other := "ffffffff" + snapshot[8:]
	if ConfirmationMatches(other, "WIEDERHERSTELLEN 0123ABCD") || ConfirmationMatches(snapshot, "WIEDERHERSTELLEN") || ConfirmationMatches("", "") {
		t.Fatal("phrase for another snapshot accepted")
	}
}

func TestSchemaCompatible(t *testing.T) {
	current := map[string]int{"core": 42, "shopping": 9}
	if !SchemaCompatible(map[string]int{"core": 42, "shopping": 9}, current) {
		t.Fatal("equal manifest rejected")
	}
	for _, manifest := range []map[string]int{{"core": 41, "shopping": 9}, {"core": 42}, {}, nil, {"core": 42, "shopping": 9, "x": 1}} {
		if SchemaCompatible(manifest, current) {
			t.Fatalf("manifest %v accepted", manifest)
		}
	}
}
