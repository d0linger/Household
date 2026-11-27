//go:build testtools

package config

// BackupTargetForTests builds a target with an archive password without
// reading the environment, so tests can use temporary paths of any platform.
// It does not exist in ordinary production builds.
func BackupTargetForTests(kind, repository, password string) BackupTarget {
	return BackupTarget{Kind: kind, Repository: repository, password: password}
}
