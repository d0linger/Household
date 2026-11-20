package buildinfo

import "testing"

func TestReleaseAndRevisionValidateInjectedValues(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	for _, tc := range []struct {
		name, version, commit, release, revision, label string
	}{
		{"defaults", "0.1.0", "unknown", "0.1.0", "unknown", "0.1.0"},
		{"release", "1.4.2", "0123456789abcdef0123456789abcdef01234567", "1.4.2", "0123456789abcdef0123456789abcdef01234567", "1.4.2+0123456789ab"},
		{"short commit", "1.4.2", "abcdef1", "1.4.2", "abcdef1", "1.4.2+abcdef1"},
		{"unsafe version", "1.0 <script>", "zz", "0.1.0", "unknown", "0.1.0"},
		{"empty", "", "", "0.1.0", "unknown", "0.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Version, Commit = tc.version, tc.commit
			if got := Release(); got != tc.release {
				t.Fatalf("Release() = %q, want %q", got, tc.release)
			}
			if got := Revision(); got != tc.revision {
				t.Fatalf("Revision() = %q, want %q", got, tc.revision)
			}
			if got := Label(); got != tc.label {
				t.Fatalf("Label() = %q, want %q", got, tc.label)
			}
		})
	}
}
