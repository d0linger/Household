//go:build testtools

package config

// BackupTargetForTests builds a target with an archive password without
// reading the environment, so tests can use temporary paths of any platform.
// It does not exist in ordinary production builds.
func BackupTargetForTests(kind, repository, password string) BackupTarget {
	return BackupTarget{Kind: kind, Repository: repository, password: password}
}

// S3BackupTargetForTests builds an offsite S3 target with synthetic
// credentials and a fixed region for an in-process test server.
func S3BackupTargetForTests(repository, password string) BackupTarget {
	return BackupTarget{Kind: "offsite", Repository: repository, password: password, extra: []string{
		"AWS_ACCESS_KEY_ID=synthetic-access-key", "AWS_SECRET_ACCESS_KEY=synthetic-secret-key", "AWS_DEFAULT_REGION=us-east-1",
	}}
}
