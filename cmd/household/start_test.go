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

func TestServeEnvironmentDropsBackupRepositoryCredentials(t *testing.T) {
	got := serveEnvironment([]string{
		"PATH=/usr/bin", "RESTIC_REPOSITORY=/data/backups", "RESTIC_PASSWORD=local", "RESTIC_PASSWORD_FILE=/run/secrets/restic",
		"HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY=s3:https://s3.example.test/bucket/house", "HOUSEHOLD_BACKUP_OFFSITE_PASSWORD=offsite",
		"HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID=id", "HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY_FILE=/run/secrets/s3", "HOUSEHOLD_BACKUP_S3_REGION=eu-central-1",
		"HOUSEHOLD_BACKUP_REST_USERNAME=u", "HOUSEHOLD_BACKUP_REST_PASSWORD=p", "AWS_ACCESS_KEY_ID=aws", "AWS_SECRET_ACCESS_KEY=aws",
	}, "app")
	// The nonsecret local repository path stays: it is the image default.
	want := []string{"PATH=/usr/bin", "RESTIC_REPOSITORY=/data/backups", "HOUSEHOLD_DATABASE_USER=household_app", "HOUSEHOLD_DATABASE_PASSWORD=app"}
	if !slices.Equal(got, want) {
		t.Fatalf("serve environment = %q", got)
	}
}
