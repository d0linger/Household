package store

import (
	"testing"
	"time"
)

func TestChoreCalendarCandidatesRespectVersionWindows(t *testing.T) {
	for _, test := range []struct {
		name     string
		versions []Record
		from     string
		through  string
		want     []string
	}{
		{
			name: "one-time history before revised effective instant",
			versions: []Record{
				{"id": "new", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2026-11-02", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-02T12:00:00Z"},
				{"id": "old", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2026-11-02", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-02T00:00:00Z", "until": "2026-11-02T12:00:00Z"},
			},
			from: "2026-11-02T00:00:00Z", through: "2026-11-04T00:00:00Z", want: []string{"old"},
		},
		{
			name: "exact effective instant belongs to new revision",
			versions: []Record{
				{"id": "new", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2026-11-02", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-02T09:00:00Z"},
				{"id": "old", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2026-11-02", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-02T00:00:00Z", "until": "2026-11-02T09:00:00Z"},
			},
			from: "2026-11-02T00:00:00Z", through: "2026-11-04T00:00:00Z", want: []string{"new"},
		},
		{
			name: "completion history never supplies calendar cadence",
			versions: []Record{
				{"id": "new", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2026-11-04", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-04T00:00:00Z"},
				{"id": "old", "rule_kind": "days", "interval_value": 1, "anchor_kind": "completion", "starts_on": "2026-11-02", "local_time": "09:00", "timezone": "UTC", "effective_at": "2026-11-02T00:00:00Z", "until": "2026-11-04T00:00:00Z"},
			},
			from: "2026-11-02T00:00:00Z", through: "2026-11-05T00:00:00Z", want: []string{"new"},
		},
		{
			name: "explicit old one-time date stays bounded",
			versions: []Record{
				{"id": "old", "rule_kind": "once", "anchor_kind": "scheduled", "starts_on": "2024-01-01", "local_time": "09:00", "timezone": "UTC", "effective_at": "2024-01-01T00:00:00Z"},
			},
			from: "2024-01-01T00:00:00Z", through: "2026-11-05T00:00:00Z", want: []string{"old"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			from, err := time.Parse(time.RFC3339, test.from)
			if err != nil {
				t.Fatal(err)
			}
			through, err := time.Parse(time.RFC3339, test.through)
			if err != nil {
				t.Fatal(err)
			}
			slots, more, err := choreCandidateSlots(test.versions, from, through, 12)
			if err != nil || more || len(slots) != len(test.want) {
				t.Fatal("effective calendar window", slots, more, err)
			}
			for index, slot := range slots {
				if slot.versionID != test.want[index] {
					t.Errorf("slot %d captured %s, want %s", index, slot.versionID, test.want[index])
				}
			}
		})
	}
}
