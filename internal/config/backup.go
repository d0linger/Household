package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
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

// ArchivePassword is used by the isolated backup worker for portable archives.
func (t BackupTarget) ArchivePassword() string { return t.password }

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
	"HOUSEHOLD_BACKUP_FORMAT",
	"HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY", "HOUSEHOLD_BACKUP_OFFSITE_PASSWORD",
	"HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID", "HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY", "HOUSEHOLD_BACKUP_S3_REGION",
	"HOUSEHOLD_BACKUP_REST_USERNAME", "HOUSEHOLD_BACKUP_REST_PASSWORD",
	"HOUSEHOLD_S3_ENDPOINT", "HOUSEHOLD_S3_BUCKET", "HOUSEHOLD_S3_PREFIX", "HOUSEHOLD_S3_REGION",
	"HOUSEHOLD_S3_ACCESS_KEY", "HOUSEHOLD_S3_SECRET_KEY", "HOUSEHOLD_S3_USE_SSL",
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

// OffsiteUsesResticREST reports whether the configured offsite repository is
// a restic REST server. It reads only the repository setting, so incomplete
// or invalid offsite credentials do not change the local backup format: the
// copy reports the credential error, and the local snapshot stays copyable
// once the credentials are fixed. A setting that cannot be read returns its
// error; callers then write a restic snapshot, which every destination stores.
func OffsiteUsesResticREST() (bool, error) {
	repository, err := secret("HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY")
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.TrimSpace(repository), "rest:"), nil
}

// OffsiteArchivePassword reads HOUSEHOLD_BACKUP_OFFSITE_PASSWORD on its own,
// without the transport settings, so a downloaded offsite file can be
// imported where no offsite repository or credentials are configured. It
// returns "" when no password is set.
func OffsiteArchivePassword() (string, error) {
	return resticPassword("HOUSEHOLD_BACKUP_OFFSITE_PASSWORD")
}

// OffsiteBackupTarget reads the optional offsite repository. It returns
// (nil, nil) when neither a repository URL nor S3 settings are configured.
func OffsiteBackupTarget(local BackupTarget) (*BackupTarget, error) {
	repository, err := secret("HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY")
	if err != nil {
		return nil, err
	}
	namedS3, err := namedS3Repository()
	if err != nil {
		return nil, err
	}
	if namedS3 != "" {
		if repository != "" {
			return nil, errors.New("use either HOUSEHOLD_S3_ENDPOINT or HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY")
		}
		repository = namedS3
	}
	if repository == "" {
		return nil, nil
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
		accessVariable, credentialVariable, regionVariable := s3EnvironmentNames(namedS3 != "")
		access, err := secret(accessVariable)
		if err != nil {
			return nil, err
		}
		key, err := secret(credentialVariable)
		if err != nil {
			return nil, err
		}
		if access == "" || key == "" || strings.ContainsAny(access+key, "\x00\r\n") {
			return nil, fmt.Errorf("an s3 offsite repository needs %s and %s", accessVariable, credentialVariable)
		}
		target.extra = append(target.extra, "AWS_ACCESS_KEY_ID="+access, "AWS_SECRET_ACCESS_KEY="+key)
		if region := strings.TrimSpace(os.Getenv(regionVariable)); region != "" {
			if !regionPattern(region) {
				return nil, fmt.Errorf("%s is not a valid region name", regionVariable)
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

func s3EnvironmentNames(named bool) (string, string, string) {
	if named {
		return "HOUSEHOLD_S3_ACCESS_KEY", "HOUSEHOLD_S3_SECRET_KEY", "HOUSEHOLD_S3_REGION"
	}
	return "HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID", "HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY", "HOUSEHOLD_BACKUP_S3_REGION"
}

// namedS3Repository accepts the endpoint/bucket/prefix layout used by the
// sibling applications and derives the equivalent restic location.
func namedS3Repository() (string, error) {
	endpoint := strings.TrimSpace(os.Getenv("HOUSEHOLD_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("HOUSEHOLD_S3_BUCKET"))
	if endpoint == "" && bucket == "" {
		return "", nil
	}
	if endpoint == "" || bucket == "" || strings.ContainsAny(endpoint, "/?#@") || strings.ContainsAny(bucket, "/?#@") {
		return "", errors.New("HOUSEHOLD_S3_ENDPOINT needs a host and HOUSEHOLD_S3_BUCKET needs a bucket name")
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(env("HOUSEHOLD_S3_PREFIX", "household")), "/")
	if prefix == "" || prefix == "." || strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix || strings.ContainsAny(prefix, `\%`) {
		return "", errors.New("HOUSEHOLD_S3_PREFIX must be a clean, relative path")
	}
	scheme := "https"
	if raw := os.Getenv("HOUSEHOLD_S3_USE_SSL"); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return "", errors.New("HOUSEHOLD_S3_USE_SSL must be true or false")
		}
		if !secure {
			scheme = "http"
		}
	}
	return "s3:" + scheme + "://" + endpoint + "/" + bucket + "/" + prefix, nil
}

// S3Connection exposes only the fields required for a signed bucket probe.
type S3Connection struct {
	Endpoint, Bucket, AccessKey, SecretKey, Region string
	Secure                                         bool
}

func (t BackupTarget) S3Connection() (S3Connection, bool) {
	raw, found := strings.CutPrefix(t.Repository, "s3:")
	if !found {
		return S3Connection{}, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return S3Connection{}, false
	}
	bucket, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	connection := S3Connection{Endpoint: u.Host, Bucket: bucket, Secure: u.Scheme == "https"}
	for _, variable := range t.extra {
		name, value, _ := strings.Cut(variable, "=")
		switch name {
		case "AWS_ACCESS_KEY_ID":
			connection.AccessKey = value
		case "AWS_SECRET_ACCESS_KEY":
			connection.SecretKey = value
		case "AWS_DEFAULT_REGION":
			connection.Region = value
		}
	}
	return connection, true
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
