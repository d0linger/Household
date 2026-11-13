package store

import (
	"testing"
	"time"

	"household/internal/notification"
)

func TestNotificationSecurityCompiledReference(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	base := notification.Intent{Schema: notification.Schema, HouseholdID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222",
		ResourceType: "security_event", ResourceID: "33333333-3333-4333-8333-333333333333", Revision: 1, Occurrence: "password_changed", Category: "security", EventAt: at, NotBefore: at, ExpiresAt: at.Add(time.Hour)}
	if !NotificationCategoryAvailable("security") || !securityNotificationReference(base) || NotificationTarget(base) != "/settings/security" {
		t.Fatal("personal security adapter unavailable")
	}
	for _, tc := range []struct {
		name   string
		change func(*notification.Intent)
	}{
		{"unknown event", func(i *notification.Intent) { i.Occurrence = "login_failed" }},
		{"arbitrary destination", func(i *notification.Intent) { i.Occurrence = "https://example.invalid/private" }},
		{"other source", func(i *notification.Intent) { i.ResourceType = "users" }},
		{"revision", func(i *notification.Intent) { i.Revision = 2 }},
		{"module", func(i *notification.Intent) { i.Module = "shopping"; i.Generation = 1 }},
		{"generation", func(i *notification.Intent) { i.Generation = 1 }},
		{"shifted clock", func(i *notification.Intent) { i.NotBefore = at.Add(time.Minute) }},
		{"changed expiry", func(i *notification.Intent) { i.ExpiresAt = at.Add(30 * time.Minute) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := base
			tc.change(&altered)
			if securityNotificationReference(altered) || NotificationTarget(altered) == "/settings/security" {
				t.Fatal("uncompiled security reference accepted")
			}
		})
	}
}
