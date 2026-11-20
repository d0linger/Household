package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
)

// BackupTarget is one restic repository the backup service writes to. Its
// credentials stay in the backup service: `start` removes every variable read
// here before `serve` runs (see BackupServiceVariables), and Environment is
// handed to restic only for the one command that needs it.
type BackupTarget struct {
	// Kind is "local" or "offsite".
	Kind string
	// Repository is the restic repository location; it never contains credentials.
	Repository string
	password   string
	extra      []string
}

// Label is the nonsecret repository reference stored and shown to administrators.
func (t BackupTarget) Label() string { return t.Repository }

// Environment returns the restic variables for this repository only.
func (t BackupTarget) Environment() []string {
	env := []string{"RESTIC_REPOSITORY=" + t.Repository, "RESTIC_PASSWORD=" + t.password}
	return append(env, t.extra...)
}

// SourceEnvironment addresses this repository as the source of `restic copy`
// or `restic init --from-repo`.
func (t BackupTarget) SourceEnvironment() []string {
	env := []string{"RESTIC_FROM_REPOSITORY=" + t.Repository, "RESTIC_FROM_PASSWORD=" + t.password}
	// A local source needs no transport credentials. Remote sources would need
	// separate *_FROM variables; copying is only ever local → offsite.
	return env
}

// BackupServiceVariables are read only by the owner backup service. `start`
// strips them (and their _FILE forms) so the web process never inherits them.
var BackupServiceVariables = []string{
	"RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND",
	"HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY", "HOUSEHOLD_BACKUP_OFFSITE_PASSWORD",
	"HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID", "HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY", "HOUSEHOLD_BACKUP_S3_REGION",
	"HOUSEHOLD_BACKUP_REST_USERNAME", "HOUSEHOLD_BACKUP_REST_PASSWORD",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
}

// LocalBackupTarget reads the local repository (RESTIC_REPOSITORY, set to
// /data/backups by the image) and its password (RESTIC_PASSWORD, its _FILE
// form or the secrets folder).
func LocalBackupTarget() (BackupTarget, error) {
	repository := strings.TrimSpace(os.Getenv("RESTIC_REPOSITORY"))
	if repository == "" {
		return BackupTarget{}, errors.New("RESTIC_REPOSITORY is required for backups")
	}
	if !path.IsAbs(repository) || strings.ContainsAny(repository, "\x00\r\n") || path.Clean(repository) != repository {
		return BackupTarget{}, errors.New("RESTIC_REPOSITORY must be a clean absolute local path")
	}
	password, err := resticPassword("RESTIC_PASSWORD")
	if err != nil {
		return BackupTarget{}, err
	}
	if password == "" {
		return BackupTarget{}, errors.New("encrypted restic repository and password required")
	}
	return BackupTarget{Kind: "local", Repository: repository, password: password}, nil
}

// resticPassword reads a repository password from name, name_FILE or the
// secrets folder. Leading and trailing whitespace of files is ignored, as for
// every other Household secret.
func resticPassword(name string) (string, error) {
	value, err := secret(name)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s must be one line", name)
	}
	return value, nil
}

// OffsiteBackupTarget reads the optional offsite repository. It returns
// (nil, nil) when HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY is unset.
func OffsiteBackupTarget(local BackupTarget) (*BackupTarget, error) {
	repository, err := secret("HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY")
	if err != nil || repository == "" {
		return nil, err
	}
	repository, scheme, err := ValidateOffsiteRepository(repository)
	if err != nil {
		return nil, err
	}
	if repository == local.Repository {
		return nil, errors.New("the offsite repository must differ from the local repository")
	}
	password, err := resticPassword("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD")
	if err != nil {
		return nil, err
	}
	if len(password) < 16 {
		return nil, errors.New("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD must contain at least 16 characters")
	}
	target := &BackupTarget{Kind: "offsite", Repository: repository, password: password}
	switch scheme {
	case "s3":
		access, err := secret("HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID")
		if err != nil {
			return nil, err
		}
		key, err := secret("HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY")
		if err != nil {
			return nil, err
		}
		if access == "" || key == "" || strings.ContainsAny(access+key, "\x00\r\n") {
			return nil, errors.New("an s3 offsite repository needs HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID and HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY")
		}
		target.extra = append(target.extra, "AWS_ACCESS_KEY_ID="+access, "AWS_SECRET_ACCESS_KEY="+key)
		if region := strings.TrimSpace(os.Getenv("HOUSEHOLD_BACKUP_S3_REGION")); region != "" {
			if !regionPattern(region) {
				return nil, errors.New("HOUSEHOLD_BACKUP_S3_REGION is not a valid region name")
			}
			target.extra = append(target.extra, "AWS_DEFAULT_REGION="+region)
		}
	case "rest":
		user, err := secret("HOUSEHOLD_BACKUP_REST_USERNAME")
		if err != nil {
			return nil, err
		}
		pass, err := secret("HOUSEHOLD_BACKUP_REST_PASSWORD")
		if err != nil {
			return nil, err
		}
		if (user == "") != (pass == "") || strings.ContainsAny(user+pass, "\x00\r\n") {
			return nil, errors.New("set both or neither of HOUSEHOLD_BACKUP_REST_USERNAME and HOUSEHOLD_BACKUP_REST_PASSWORD")
		}
		if user != "" {
			target.extra = append(target.extra, "RESTIC_REST_USERNAME="+user, "RESTIC_REST_PASSWORD="+pass)
		}
	}
	return target, nil
}

func regionPattern(region string) bool {
	if len(region) > 64 {
		return false
	}
	for _, r := range region {
		lower, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
		if !lower && !digit && r != '-' {
			return false
		}
	}
	return true
}

// ValidateOffsiteRepository accepts s3:http(s)://host/bucket[/prefix],
// rest:http(s)://host[/path] and absolute local paths (a mounted NAS or USB
// disk). Credentials, queries and fragments are refused, so the returned
// repository is also a safe, nonsecret label.
func ValidateOffsiteRepository(raw string) (repository, scheme string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, "\x00\r\n\t ") {
		return "", "", errors.New("invalid offsite repository")
	}
	if strings.HasPrefix(raw, "/") {
		if path.Clean(raw) != raw || raw == "/" {
			return "", "", errors.New("an offsite path must be a clean absolute path")
		}
		return raw, "local", nil
	}
	scheme, rest, found := strings.Cut(raw, ":")
	if !found || scheme != "s3" && scheme != "rest" {
		return "", "", errors.New("the offsite repository must start with s3:, rest: or /")
	}
	u, err := url.Parse(rest)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.Opaque != "" {
		return "", "", errors.New("the offsite repository needs an http(s) endpoint")
	}
	if u.User != nil {
		return "", "", errors.New("put offsite credentials into their own settings, never into the repository URL")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(rest, "#") {
		return "", "", errors.New("the offsite repository must not contain a query or fragment")
	}
	if strings.Contains(u.Path, "..") || strings.Contains(u.Path, "//") {
		return "", "", errors.New("invalid offsite repository path")
	}
	if scheme == "s3" && strings.Trim(u.Path, "/") == "" {
		return "", "", errors.New("an s3 offsite repository needs a bucket: s3:https://host/bucket/prefix")
	}
	return raw, scheme, nil
}
