// Package buildinfo reports the release identity compiled into this binary.
package buildinfo

import "regexp"

// Version and Commit are set at build time with
//
//	-ldflags "-X household/internal/buildinfo.Version=… -X household/internal/buildinfo.Commit=…"
//
// Local builds keep the defaults; they never claim a release they are not.
var (
	Version = "0.1.0"
	Commit  = "unknown"
)

var (
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// Release returns the validated version, falling back to the default when a
// build injected an unexpected value. The value is stored in backup metadata
// and shown to administrators, so it must stay a short printable token.
func Release() string {
	if versionPattern.MatchString(Version) {
		return Version
	}
	return "0.1.0"
}

// Revision returns the validated source revision or "unknown".
func Revision() string {
	if commitPattern.MatchString(Commit) {
		return Commit
	}
	return "unknown"
}

// ShortRevision returns the first twelve characters of a known revision.
func ShortRevision() string {
	revision := Revision()
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// Label identifies the build in logs, backup tags and the status page.
func Label() string {
	if revision := ShortRevision(); revision != "unknown" {
		return Release() + "+" + revision
	}
	return Release()
}
