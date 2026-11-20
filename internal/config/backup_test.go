package config

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateOffsiteRepository(t *testing.T) {
	for _, tc := range []struct {
		raw, scheme string
		ok          bool
	}{
		{"s3:https://s3.eu-central-1.amazonaws.com/household-backup/main", "s3", true},
		{"s3:http://minio:9000/household", "s3", true},
		{"rest:https://backup.example.test:8000/household/", "rest", true},
		{"/mnt/nas/household", "local", true},
		{"s3:https://s3.example.test/", "", false},
		{"s3:https://key:secret@s3.example.test/bucket", "", false},
		{"rest:https://user:pw@backup.example.test/", "", false},
		{"s3:https://s3.example.test/bucket?region=x", "", false},
		{"s3:https://s3.example.test/bucket#x", "", false},
		{"s3:https://s3.example.test/a/../b", "", false},
		{"b2:bucket:path", "", false},
		{"sftp:user@host:/srv", "", false},
		{"relative/path", "", false},
		{"/mnt/../etc", "", false},
		{"/", "", false},
		{"s3:https://s3.example.test/bucket with space", "", false},
		{"", "", false},
	} {
		repository, scheme, err := ValidateOffsiteRepository(tc.raw)
		if (err == nil) != tc.ok {
			t.Fatalf("%q: err=%v, want ok=%v", tc.raw, err, tc.ok)
		}
		if tc.ok && (repository != tc.raw || scheme != tc.scheme) {
			t.Fatalf("%q: got %q/%q", tc.raw, repository, scheme)
		}
	}
}

func TestBackupTargetsReadCredentialsIntoTheirOwnEnvironment(t *testing.T) {
	t.Setenv(SecretsDirVariable, "")
	t.Setenv("RESTIC_REPOSITORY", "/data/backups")
	t.Setenv("RESTIC_PASSWORD", "local-password")
	local, err := LocalBackupTarget()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(local.Environment(), []string{"RESTIC_REPOSITORY=/data/backups", "RESTIC_PASSWORD=local-password"}) {
		t.Fatalf("local environment = %q", local.Environment())
	}
	if offsite, err := OffsiteBackupTarget(local); err != nil || offsite != nil {
		t.Fatalf("unset offsite = %v, %v", offsite, err)
	}
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY", "s3:https://s3.example.test/bucket/household")
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD", "offsite-password-123")
	if _, err = OffsiteBackupTarget(local); err == nil || !strings.Contains(err.Error(), "ACCESS_KEY") {
		t.Fatalf("missing s3 keys error = %v", err)
	}
	t.Setenv("HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("HOUSEHOLD_BACKUP_S3_REGION", "eu-central-1")
	offsite, err := OffsiteBackupTarget(local)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"RESTIC_REPOSITORY=s3:https://s3.example.test/bucket/household", "RESTIC_PASSWORD=offsite-password-123",
		"AWS_ACCESS_KEY_ID=AKIDEXAMPLE", "AWS_SECRET_ACCESS_KEY=secret", "AWS_DEFAULT_REGION=eu-central-1"}
	if !slices.Equal(offsite.Environment(), want) {
		t.Fatalf("offsite environment = %q", offsite.Environment())
	}
	if offsite.Label() != "s3:https://s3.example.test/bucket/household" || strings.Contains(offsite.Label(), "secret") {
		t.Fatalf("label = %q", offsite.Label())
	}
	if !slices.Equal(local.SourceEnvironment(), []string{"RESTIC_FROM_REPOSITORY=/data/backups", "RESTIC_FROM_PASSWORD=local-password"}) {
		t.Fatalf("source environment = %q", local.SourceEnvironment())
	}
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD", "short")
	if _, err = OffsiteBackupTarget(local); err == nil {
		t.Fatal("short offsite password accepted")
	}
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD", "offsite-password-123")
	t.Setenv("HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY", "/data/backups")
	if _, err = OffsiteBackupTarget(local); err == nil {
		t.Fatal("offsite equal to local accepted")
	}
}

func TestLocalBackupTargetRequiresPasswordAndAbsolutePath(t *testing.T) {
	t.Setenv(SecretsDirVariable, "")
	t.Setenv("RESTIC_REPOSITORY", "/data/backups")
	t.Setenv("RESTIC_PASSWORD", "")
	if _, err := LocalBackupTarget(); err == nil {
		t.Fatal("missing password accepted")
	}
	t.Setenv("RESTIC_PASSWORD", "x")
	t.Setenv("RESTIC_REPOSITORY", "data/backups")
	if _, err := LocalBackupTarget(); err == nil {
		t.Fatal("relative repository accepted")
	}
}
