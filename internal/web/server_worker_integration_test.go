//go:build integration

package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"household/internal/maintenance"
	"household/internal/value"
	"household/internal/worker"

	"github.com/jackc/pgx/v5"
)

func TestBackupLockCancellationContract(t *testing.T) {
	f := newIntegrationFixture(t)
	// Cancellation must finish before any external backup tool or repository access.
	t.Setenv("RESTIC_REPOSITORY", "")
	t.Setenv("RESTIC_PASSWORD_FILE", "")
	before := f.owner.Stat().AcquiredConns()
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := maintenance.Backup(canceled, f.owner, f.app.Cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled backup returned %v", err)
	}
	waitForReleasedConnections(t, f, before)
	blocker, err := f.owner.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err = blocker.Exec(f.ctx, "SELECT pg_advisory_lock(721607137)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := blocker.Exec(f.ctx, "SELECT pg_advisory_unlock(721607137)"); err != nil {
			t.Errorf("unlock backup fixture: %v", err)
		}
	}()
	before = f.owner.Stat().AcquiredConns()
	deadline, stop := context.WithTimeout(f.ctx, 150*time.Millisecond)
	defer stop()
	if _, err = maintenance.Backup(deadline, f.owner, f.app.Cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended backup cancellation returned %v", err)
	}
	waitForReleasedConnections(t, f, before)
	var backups int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.backup_runs").Scan(&backups); err != nil || backups != 0 {
		t.Fatalf("canceled backup wrote a run: count=%d error=%v", backups, err)
	}
}

func waitForReleasedConnections(t *testing.T, f *integrationFixture, want int32) {
	t.Helper()
	// pgx destroys a connection interrupted during a query asynchronously.
	deadline := time.Now().Add(3 * time.Second)
	for f.owner.Stat().AcquiredConns() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.owner.Stat().AcquiredConns(); got != want {
		t.Fatalf("canceled backup retained connections: before=%d after=%d", want, got)
	}
}

func TestWorkerCaptureRecoveryContract(t *testing.T) {
	for _, tc := range []struct {
		name, state, wantJob, wantCapture string
		attempts                          int
		ocr, expired, failed, finished    bool
	}{
		{"exhausted_lease", "running", "failed", "review", 5, true, true, false, true},
		{"ocr_disabled", "pending", "paused", "review", 0, false, false, false, false},
		{"retryable_failure", "pending", "pending", "extracting", 0, true, false, true, false},
		{"terminal_failure", "pending", "failed", "review", 4, true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			generation := f.app.Store.States()["recipes"].Generation
			job, capture := seedCaptureWork(t, f, generation, "extracting", tc.state, tc.attempts, tc.expired)
			cfg := f.app.Cfg
			cfg.OCREnabled = tc.ocr
			w := &worker.Worker{Pool: f.pool, Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			t.Cleanup(func() { _ = w.Close() })
			err := w.Tick(f.ctx)
			if (err != nil) != tc.failed {
				t.Fatalf("worker failure = %v, want error %t", err, tc.failed)
			}
			var state, captureState, text string
			var finished, leaseCleared, retryScheduled bool
			if err = f.owner.QueryRow(f.ctx, "SELECT state,finished_at IS NOT NULL,lease_token IS NULL AND lease_until IS NULL,available_at>now() FROM core.jobs WHERE id=$1", job).Scan(&state, &finished, &leaseCleared, &retryScheduled); err != nil {
				t.Fatal(err)
			}
			if state != tc.wantJob || finished != tc.finished || !leaseCleared {
				t.Fatalf("job state=%q finished=%t lease cleared=%t, want %q/%t/true", state, finished, leaseCleared, tc.wantJob, tc.finished)
			}
			if tc.wantJob == "pending" && !retryScheduled {
				t.Fatal("failed work did not receive a future retry deadline")
			}
			if err = f.owner.QueryRow(f.ctx, "SELECT state,coalesce(source_text,'') FROM core.capture_jobs WHERE id=$1", capture).Scan(&captureState, &text); err != nil {
				t.Fatal(err)
			}
			if captureState != tc.wantCapture || (captureState == "review" && !strings.Contains(text, "manuell")) {
				t.Fatalf("capture state=%q text=%q, want %q with a manual recovery explanation", captureState, text, tc.wantCapture)
			}
		})
	}
}

func TestWorkerRecoveryGenerationFence(t *testing.T) {
	f := newIntegrationFixture(t)
	generation := f.app.Store.States()["recipes"].Generation
	_, current := seedCaptureWork(t, f, generation, "extracting", "running", 5, true)
	_, stale := seedCaptureWork(t, f, generation+1, "extracting", "running", 5, true)
	_, confirmed := seedCaptureWork(t, f, generation, "confirmed", "running", 5, true)
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, id, want string }{
		{"current_capture", current, "review"},
		{"different_generation", stale, "extracting"},
		{"confirmed_capture", confirmed, "confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state string
			if err := f.owner.QueryRow(f.ctx, "SELECT state FROM core.capture_jobs WHERE id=$1", tc.id).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != tc.want {
				t.Fatalf("capture state = %q, want %q", state, tc.want)
			}
		})
	}
}

func seedCaptureWork(t *testing.T, f *integrationFixture, generation int64, captureState, jobState string, attempts int, expired bool) (string, string) {
	t.Helper()
	resource, attachment, capture, job := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,'Recovery fixture',2,$3)", resource, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("synthetic source metadata"))
	var source pgx.Tx
	var err error
	if generation == f.app.Store.States()["recipes"].Generation {
		// Current nonfinancial fixtures use the real App actor and classification
		// triggers. The deliberately stale generation remains Owner-seeded and
		// unclassified: the worker must never reach that source.
		source, err = f.app.Store.BeginMutation(f.ctx, f.identity, "recipes", generation)
	} else {
		source, err = f.owner.Begin(f.ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Rollback(f.ctx) }()
	if _, err = source.Exec(f.ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by)
		VALUES($1,$2,'recipes',$3,'recipes',$4,$5,'fixture.png','image/png',1,$6,$7)`, attachment, f.identity.HouseholdID, generation, resource, value.UUID(), digest[:], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(f.ctx, `INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema,state)
		VALUES($1,$2,'recipes',$3,$4,'recipes',$5)`, capture, f.identity.HouseholdID, generation, attachment, captureState); err != nil {
		t.Fatal(err)
	}
	if err = source.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// The malformed storage key makes retry failures deterministic without relying
	// on an OCR executable, filesystem permissions or an external service.
	payload, err := json.Marshal(map[string]string{"capture_id": capture, "storage_key": "invalid-fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key,state,attempts,available_at,lease_token,lease_until)
		VALUES($1::uuid,$2,'recipes',$3,'capture_ocr',$4,$1::uuid::text,$5,$6,now()-interval '1 minute',CASE WHEN $7 THEN gen_random_uuid() END,CASE WHEN $7 THEN now()-interval '1 minute' END)`, job, f.identity.HouseholdID, generation, payload, jobState, attempts, expired); err != nil {
		t.Fatal(err)
	}
	return job, capture
}
