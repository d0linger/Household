package auth

import (
	"testing"
	"time"
)

func TestIdentityRecent(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, tc := range []struct {
		name                   string
		created, authenticated time.Time
		want                   bool
	}{
		{"new login", now.Add(-time.Minute), now.Add(-time.Minute), true},
		{"old login", now.Add(-time.Hour), now.Add(-time.Hour), false},
		{"confirmed old session", now.Add(-time.Hour), now.Add(-time.Minute), true},
		{"future timestamp", now, now.Add(time.Hour), false},
		{"zero identity", time.Time{}, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := Identity{CreatedAt: tc.created, AuthenticatedAt: tc.authenticated}
			if got := id.Recent(); got != tc.want {
				t.Fatalf("Recent()=%v want %v", got, tc.want)
			}
		})
	}
}
