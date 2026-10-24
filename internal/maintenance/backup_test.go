package maintenance

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"household/internal/config"
)

func TestLimitedBuffer(t *testing.T) {
	t.Parallel()
	const limit = 4 << 20
	t.Run("exact limit and rejected excess", func(t *testing.T) {
		t.Parallel()
		var output limitedBuffer
		if n, err := output.Write([]byte(strings.Repeat("x", limit))); err != nil || n != limit {
			t.Fatalf("exact limit: n=%d err=%v", n, err)
		}
		if n, err := output.Write([]byte("x")); err == nil || n != 0 || len(output.Bytes()) != limit {
			t.Fatalf("excess accepted: n=%d err=%v size=%d", n, err, len(output.Bytes()))
		}
	})
	t.Run("streaming cannot bypass cap", func(t *testing.T) {
		t.Parallel()
		var output limitedBuffer
		source := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", limit+1))}
		n, err := io.Copy(&output, source)
		if err == nil || n > limit || len(output.Bytes()) > limit {
			t.Fatalf("unbounded streaming output: n=%d err=%v size=%d", n, err, len(output.Bytes()))
		}
	})
}

func TestMaintenanceURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"explicit local URI", "postgres://owner:private-value@db:5432/household?sslmode=disable", true},
		{"IPv6 with TLS", "postgresql://owner:private-value@[::1]:5432/household?sslmode=verify-full&sslrootcert=%2Frun%2Fca.pem", true},
		{"empty", "", false},
		{"keyword DSN", "host=db user=owner dbname=household", false},
		{"wrong protocol", "https://owner:private-value@db:5432/household", false},
		{"missing user", "postgres://db/household", false},
		{"missing host", "postgres://owner@/household", false},
		{"missing database", "postgres://owner@db:5432/", false},
		{"missing explicit port", "postgres://owner@db/household", false},
		{"extra path", "postgres://owner@db:5432/household/another", false},
		{"multiple hosts with implicit port", "postgres://owner@first,db:5432/household", false},
		{"unescaped credential delimiter", "postgres://owner:private-value@other@db:5432/household", false},
		{"raw database whitespace", "postgres://owner@db:5432/ household ", false},
		{"zero port", "postgres://owner@db:0/household", false},
		{"large port", "postgres://owner@db:65536/household", false},
		{"fragment", "postgres://owner@db:5432/household#other", false},
		{"database override", "postgres://owner@db:5432/household_restore?dbname=household", false},
		{"database alias override", "postgres://owner@db:5432/household_restore?database=household", false},
		{"host override", "postgres://owner@db:5432/household?host=another", false},
		{"service override", "postgres://owner@db:5432/household?service=production", false},
		{"duplicate parameter", "postgres://owner@db:5432/household?sslmode=require&sslmode=disable", false},
		{"invalid parameter encoding", "postgres://owner@db:5432/household?sslmode=%xx", false},
		{"form-style query encoding", "postgres://owner@db:5432/household?sslrootcert=/run/my+ca.pem", false},
		{"unescaped query delimiter", "postgres://owner@db:5432/household?sslrootcert=/run/my=ca.pem", false},
		{"invalid TLS mode", "postgres://owner@db:5432/household?sslmode=unsafe", false},
		{"invalid timeout", "postgres://owner@db:5432/household?connect_timeout=-1", false},
		{"NUL database", "postgres://owner@db:5432/household%00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := maintenanceURL(tc.raw)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-value") {
				t.Fatal("maintenance error disclosed credentials")
			}
		})
	}
}

func TestDatabaseEnvironment(t *testing.T) {
	t.Parallel()
	existing := []string{"PATH=keep", "PGHOSTADDR=192.0.2.1", "PGSERVICE=production", "PGDATABASE=wrong", "PGSSLMODE=disable", "pgservice=production", "RESTIC_REPOSITORY=keep"}
	got, err := databaseEnvironment(existing, "postgres://owner:p%40ss@db:5432/household?sslrootcert=%2Frun%2Fca.pem&connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, item := range got {
		key, val, ok := strings.Cut(item, "=")
		if !ok {
			t.Fatal("invalid environment entry")
		}
		values[key] = val
	}
	for key, want := range map[string]string{"PATH": "keep", "RESTIC_REPOSITORY": "keep", "PGHOST": "db", "PGPORT": "5432", "PGUSER": "owner", "PGPASSWORD": "p@ss", "PGDATABASE": "household", "PGSSLMODE": "require", "PGSSLROOTCERT": "/run/ca.pem", "PGCONNECT_TIMEOUT": "5"} {
		if values[key] != want {
			t.Errorf("unexpected value for %s", key)
		}
	}
	for _, key := range []string{"PGHOSTADDR", "PGSERVICE", "pgservice"} {
		if _, present := values[key]; present {
			t.Errorf("inherited %s can redirect the connection", key)
		}
	}
	if existing[1] != "PGHOSTADDR=192.0.2.1" {
		t.Fatal("caller environment was mutated")
	}
}

func TestCommandRejectsUnapprovedInputs(t *testing.T) {
	t.Parallel()
	if _, err := command(t.Context(), "sh", []string{"-c", "exit 0"}, ""); err == nil {
		t.Fatal("unapproved executable accepted")
	}
	if _, err := command(t.Context(), "pg_dump", nil, "postgres://owner@db:5432/household?dbname=other"); err == nil {
		t.Fatal("ambiguous database URI accepted")
	}
}

func TestNewManifestUsesAbsoluteUploadPath(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 24, 15, 30, 0, 0, time.FixedZone("test", 2*60*60))
	manifest, err := newManifest(filepath.Join("fixtures", "uploads"), created)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.UploadPath != filepath.Join(cwd, "fixtures", "uploads") || !filepath.IsAbs(manifest.UploadPath) {
		t.Fatal("relative upload configuration was not normalized to the restic source path")
	}
	if !manifest.Created.Equal(created) || manifest.Created.Location() != time.UTC {
		t.Fatal("manifest timestamp lost its instant or UTC normalization")
	}
	if _, err := newManifest("", created); err == nil {
		t.Fatal("empty upload directory can back up the working directory")
	}
}

func TestBackupRejectsInvalidConnectionBeforeLock(t *testing.T) {
	t.Parallel()
	if _, err := Backup(t.Context(), nil, config.Config{DatabaseURL: "invalid"}); err == nil {
		t.Fatal("invalid backup connection accepted")
	}
}

func TestVerifyRestoreRejectsInvalidTargetBeforeLock(t *testing.T) {
	t.Parallel()
	cfg := config.Config{DatabaseURL: "postgres://owner@db:5432/household"}
	if err := VerifyRestore(t.Context(), nil, cfg, "unused", "postgres://owner@db:5432/household"); err == nil {
		t.Fatal("non-disposable target accepted")
	}
}

func TestValidateRestoreTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, target string
		valid                bool
	}{
		{"isolated database", "postgres://owner@db:5432/household", "postgres://owner@restore-db:5432/household_restore", true},
		{"distinct database on same server", "postgres://owner@db:5432/household", "postgres://owner@db:5432/household_restore", true},
		{"wrong target database", "postgres://owner@db:5432/household", "postgres://owner@db:5432/household", false},
		{"target override", "postgres://owner@db:5432/household", "postgres://owner@db:5432/household_restore?dbname=household", false},
		{"same source", "postgres://owner@db:5432/household_restore", "postgres://owner@db:5432/household_restore", false},
		{"source host alias", "postgres://owner@localhost:5432/household_restore", "postgres://owner@127.0.0.1:5432/household_restore", false},
		{"source override", "postgres://owner@db:5432/household?database=household_restore", "postgres://owner@restore-db:5432/household_restore", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateRestoreTarget(tc.source, tc.target); (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}

func TestRestoreUploadPath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	for _, tc := range []struct {
		name, path, relative string
	}{
		{"absolute POSIX upload", "/data/uploads", "data/uploads"},
		{"relative upload", ".local/uploads", ".local/uploads"},
		{"harmless dots in name", "/uploads..old", "uploads..old"},
		{"empty", "", ""},
		{"root", "/", ""},
		{"parent traversal", "../outside", ""},
		{"absolute traversal", "/uploads/../../outside", ""},
		{"internal traversal", "/uploads/../other", ""},
		{"Windows path", `C:\uploads`, ""},
		{"Windows traversal", `uploads\..\outside`, ""},
		{"NUL", "uploads\x00bad", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := restoreUploadPath(base, tc.path)
			if tc.relative == "" {
				if err == nil {
					t.Fatal("unsafe restore upload path accepted")
				}
				return
			}
			if err != nil || got != filepath.Join(base, filepath.FromSlash(tc.relative)) {
				t.Fatalf("path=%q err=%v", got, err)
			}
		})
	}
}
