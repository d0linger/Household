package main

import (
	"slices"
	"testing"
)

func TestServeEnvironmentDropsSetupCredentials(t *testing.T) {
	got := serveEnvironment([]string{
		"PATH=/usr/bin", "POSTGRES_PASSWORD=super", "POSTGRES_PASSWORD_FILE=/x", "HOUSEHOLD_OWNER_PASSWORD=owner",
		"HOUSEHOLD_APP_PASSWORD=app", "HOUSEHOLD_DATABASE_URL=postgres://x", "HOUSEHOLD_DATABASE_USER=household_owner",
		"HOUSEHOLD_CSRF_SECRET=csrf", "HOUSEHOLD_OWNER_PASSWORD_FILE=/run/secrets/owner_password",
	}, "app")
	want := []string{"PATH=/usr/bin", "HOUSEHOLD_CSRF_SECRET=csrf", "HOUSEHOLD_DATABASE_USER=household_app", "HOUSEHOLD_DATABASE_PASSWORD=app"}
	if !slices.Equal(got, want) {
		t.Fatalf("serve environment = %q", got)
	}
}
