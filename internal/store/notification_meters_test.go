package store

import (
	"testing"
	"time"
)

func TestMeterDueStagesCivilCalendarAndFreshness(t *testing.T) {
	for _, tt := range []struct {
		name, anchor, zone, now, want string
		days, count                   int
	}{
		{"spring transition", "2026-03-28", "Europe/Vienna", "2026-03-29T08:30:00Z", "2026-03-29T07:00:00Z", 1, 1},
		{"fall transition", "2026-10-24", "Europe/Vienna", "2026-10-25T08:30:00Z", "2026-10-25T08:00:00Z", 1, 1},
		{"old missed periods cannot block", "2000-01-01", "UTC", "2026-11-09T10:00:00Z", "2026-11-09T09:00:00Z", 1, 1},
		{"not yet anchored", "2026-12-01", "UTC", "2026-11-09T10:00:00Z", "", 1, 0},
		{"before morning", "2026-11-09", "UTC", "2026-11-09T08:59:59Z", "", 1, 0},
		{"expired previous stage", "2026-11-08", "UTC", "2026-11-09T09:00:00Z", "", 2, 0},
		{"prior day still fresh", "2026-11-08", "UTC", "2026-11-09T08:59:59Z", "2026-11-08T09:00:00Z", 2, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			stages, err := meterDueStages(tt.anchor, tt.zone, tt.days, now)
			if err != nil || len(stages) != tt.count {
				t.Fatalf("stages=%v error=%v", stages, err)
			}
			if tt.count > 0 && stages[len(stages)-1].UTC().Format(time.RFC3339) != tt.want {
				t.Fatal(stages)
			}
		})
	}
}

func TestMeterObservationEpisodesRecoveryAndCooldown(t *testing.T) {
	start := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name            string
		old             meterObservation
		signal          meterSignal
		reset, accepted bool
		offset          time.Duration
		state           string
		episode         int64
	}{
		{"initial anomaly is silent", meterObservation{}, meterSignal{known: true, alert: true}, true, false, 0, "baseline", 0},
		{"healthy baseline", meterObservation{}, meterSignal{known: true}, true, false, 0, "healthy", 0},
		{"first actual crossing", meterObservation{state: "healthy", healthy: &start}, meterSignal{known: true, alert: true}, false, false, time.Minute, "alert", 1},
		{"unknown cannot heal", meterObservation{state: "alert", episode: 1, healthy: &start, detected: &start, cutoff: &start, lastAlert: &start}, meterSignal{}, false, false, time.Minute, "alert", 1},
		{"unreceived alarm has no all clear", meterObservation{state: "alert", episode: 1, healthy: &start, detected: &start, cutoff: &start, lastAlert: &start}, meterSignal{known: true}, false, false, time.Minute, "healthy", 1},
		{"accepted alarm recovers", meterObservation{state: "alert", episode: 1, healthy: &start, detected: &start, cutoff: &start, lastAlert: &start}, meterSignal{known: true}, false, true, time.Minute, "recovered", 1},
		{"cooldown suppresses second episode", meterObservation{state: "recovered", episode: 1, healthy: &start, detected: &start, cutoff: &start, lastAlert: &start}, meterSignal{known: true, alert: true}, false, true, time.Hour, "baseline", 1},
		{"new healthy crossing after cooldown", meterObservation{state: "healthy", episode: 1, healthy: &start, lastAlert: &start}, meterSignal{known: true, alert: true}, false, true, 24 * time.Hour, "alert", 2},
		{"config change silences current episode", meterObservation{state: "alert", episode: 1, healthy: &start, detected: &start, cutoff: &start, lastAlert: &start}, meterSignal{known: true, alert: true}, true, true, time.Hour, "baseline", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next, err := advanceMeterObservation(tt.old, tt.signal, tt.reset, tt.accepted, start.Add(tt.offset))
			if err != nil || next.state != tt.state || next.episode != tt.episode {
				t.Fatalf("%+v %v", next, err)
			}
			if tt.state == "alert" && tt.old.state != "alert" && (next.cutoff == nil || !next.cutoff.Equal(start) || next.detected == nil || !next.detected.Equal(start.Add(tt.offset))) {
				t.Fatal("episode lost immutable consent/event boundary")
			}
		})
	}
}
