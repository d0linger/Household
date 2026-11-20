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

// The ambiguous and missing wall-clock hours around both daylight-saving
// changes are given as exact instants, because local parsing cannot name them.
func TestLastSlotAcrossDaylightSavingChanges(t *testing.T) {
	zone := vienna(t)
	utc := func(value string) time.Time {
		parsed, err := time.Parse("2006-01-02 15:04", value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, tc := range []struct {
		name, now, local string
		interval         int
		want             time.Time
	}{
		// 02:30 CET on 2026-10-25 lies after the fall-back; the 03:00 slot of
		// that day is still ahead, so the previous day's 03:00 CEST applies.
		{"autumn before anchor", "2026-10-25 01:30", "03:00", 24, utc("2026-10-24 01:00")},
		{"autumn after anchor", "2026-10-25 02:30", "03:00", 24, utc("2026-10-25 02:00")},
		{"autumn intraday", "2026-10-25 09:30", "03:00", 6, at(t, zone, "2026-10-25 09:00")},
		{"spring intraday", "2026-03-29 06:30", "03:00", 6, at(t, zone, "2026-03-29 03:00")},
		{"spring after slot", "2026-03-29 07:30", "03:00", 6, at(t, zone, "2026-03-29 09:00")},
		{"spring daily", "2026-03-29 10:00", "03:00", 24, at(t, zone, "2026-03-29 03:00")},
	} {
		got, err := LastSlot(utc(tc.now), zone, tc.local, tc.interval)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(tc.want) {
			t.Fatalf("%s: LastSlot = %s, want %s", tc.name, got.In(zone), tc.want.In(zone))
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
	if h := Evaluate(now, zone, "03:00", 24, true, nil, &after, false); h.State != StateOK || h.NextDue == nil {
		t.Fatalf("fresh backup health = %+v", h)
	}
	old := at(t, zone, "2026-11-17 03:05")
	if h := Evaluate(now, zone, "03:00", 24, true, nil, &old, false); h.State != StateOverdue {
		t.Fatalf("old backup health = %+v", h)
	}
	// Within the grace period after the expected run it stays ok.
	if h := Evaluate(at(t, zone, "2026-11-20 03:30"), zone, "03:00", 24, true, nil, &after, false); h.State != StateOK {
		t.Fatalf("grace period health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, true, nil, &after, true); h.State != StateFailed {
		t.Fatalf("failed attempt health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, true, nil, nil, false); h.State != StateNever {
		t.Fatalf("never health = %+v", h)
	}
	if h := Evaluate(now, zone, "03:00", 24, false, nil, nil, true); h.State != StateDisabled {
		t.Fatalf("disabled health = %+v", h)
	}
}

func TestEvaluateTargetThatNeverSucceeded(t *testing.T) {
	zone := vienna(t)
	activated := at(t, zone, "2026-11-18 10:00")
	// Before the first slot after activation (2026-11-19 03:00) plus grace.
	if h := Evaluate(at(t, zone, "2026-11-19 03:30"), zone, "03:00", 24, true, &activated, nil, false); h.State != StateNever {
		t.Fatalf("initial grace health = %+v", h)
	}
	// The first slot passed with grace and no backup ever succeeded.
	if h := Evaluate(at(t, zone, "2026-11-19 12:00"), zone, "03:00", 24, true, &activated, nil, false); h.State != StateOverdue || h.NextDue == nil {
		t.Fatalf("missed first backup health = %+v", h)
	}
	if h := Evaluate(at(t, zone, "2026-11-19 12:00"), zone, "03:00", 24, true, &activated, nil, true); h.State != StateFailed {
		t.Fatalf("failed first backup health = %+v", h)
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
