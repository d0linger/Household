package store

import (
	"testing"
	"time"

	"household/internal/notification"
	"household/internal/value"
)

func TestDocumentNotificationStageRetainsCivilLeadTime(t *testing.T) {
	for _, tc := range []struct {
		name, due, zone, want string
		offset                int
	}{
		{"spring transition", "2026-03-29", "Europe/Vienna", "2026-03-28T08:00:00Z", 1},
		{"fall transition", "2026-10-25", "Europe/Vienna", "2026-10-25T08:00:00Z", 0},
		{"leap year lead", "2025-03-01", "UTC", "2024-02-29T09:00:00Z", 366},
		{"zero lead", "2026-12-01", "UTC", "2026-12-01T09:00:00Z", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := documentNotificationStage(tc.due, tc.zone, tc.offset)
			if err != nil || got.UTC().Format(time.RFC3339) != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, tc := range []struct {
		due, zone string
		offset    int
	}{{"2026-12-01", "UTC", 367}, {"2026-12-01", "UTC", -1}, {"2026-02-30", "UTC", 0},
		{"2026-12-01", "Unknown/Clock", 0}, {"1999-01-01", "UTC", 0}} {
		if _, err := documentNotificationStage(tc.due, tc.zone, tc.offset); err == nil {
			t.Fatal("invalid deadline accepted", tc)
		}
	}
}

func TestDocumentNotificationTargetsContainNoSourceContent(t *testing.T) {
	at := time.Now().UTC()
	i := notification.Intent{Schema: notification.Schema, HouseholdID: value.UUID(), UserID: value.UUID(), Module: "documents",
		Generation: 1, ResourceID: value.UUID(), Revision: 1, Category: "documents", Occurrence: "failed/1",
		EventAt: at, NotBefore: at, ExpiresAt: at.Add(24 * time.Hour)}
	for kind, destination := range map[string]string{
		"document_deadline":   "/m/documents/workspace/deadlines",
		"document_processing": "/m/documents/workspace/processing",
		"document_import":     "/m/documents/workspace/integrations",
		"unreviewed":          "/notifications",
	} {
		i.ResourceType = kind
		if got := NotificationTarget(i); got != destination {
			t.Fatal(kind, got)
		}
	}
}
