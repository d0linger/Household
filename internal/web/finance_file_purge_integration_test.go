//go:build integration

package web

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"household/internal/audit"
	"household/internal/maintenance"
	"household/internal/value"
	"household/internal/worker"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFinancePurgeWorkerDeletesExactOriginalAndDerivatives(t *testing.T) {
	f, _, attachment := financeReceiptHTTPFixture(t)
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal("create actual protected thumbnail", err)
	}
	rows, err := f.owner.Query(f.ctx, `SELECT storage_key FROM core.attachments WHERE id=$1
 UNION ALL SELECT storage_key FROM core.attachment_derivatives WHERE attachment_id=$1`, attachment)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil || !value.ValidUUID(key) || len(keys) >= 24 {
			rows.Close()
			t.Fatal("bounded physical file identity", err)
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(keys) < 2 {
		t.Fatal("actual original and derivative are required", len(keys), err)
	}
	for _, key := range keys {
		if info, statErr := os.Stat(filepath.Join(f.app.Cfg.UploadDir, key)); statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatal("actual source file missing", statErr)
		}
	}
	// A worker audit tag alone cannot delete a ready protected attachment.
	tx, err := audit.Begin(audit.WithRequest(audit.WithSystem(f.ctx, "worker"), value.UUID()), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	_, denial := tx.Exec(f.ctx, "DELETE FROM core.attachments WHERE id=$1", attachment)
	_ = tx.Rollback(f.ctx)
	var denied *pgconn.PgError
	if !errors.As(denial, &denied) || denied.Code != "42501" {
		t.Fatal("unqualified worker deletion bypassed financial authority", denial)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.file_deletions(storage_key,household_id,attachment_id)
 SELECT storage_key,household_id,id FROM core.attachments WHERE id=$1`, attachment); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(f.ctx); err == nil {
		t.Fatal("worker accepted a deletion queue entry for an active source")
	}
	for _, key := range keys {
		if _, statErr := os.Stat(filepath.Join(f.app.Cfg.UploadDir, key)); statErr != nil {
			t.Fatal("worker removed active bytes before checking retirement", statErr)
		}
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.file_deletions WHERE attachment_id=$1", attachment); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["expenses"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "expenses", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	// Synthetic qualification metadata tests the real maintenance/worker boundary;
	// the separate operator rehearsal proves actual encrypted backup and restore.
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'fixture','isolated-finance-files',$2)`, backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'expenses',$3,$4,$5)`, purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	purgeConfig, err := pgxpool.ParseConfig(os.Getenv("HOUSEHOLD_TEST_OWNER_URL"))
	if err != nil {
		t.Fatal(err)
	}
	purgeConfig.MaxConns = 1
	purgeConfig.ConnConfig.RuntimeParams["timezone"] = "Pacific/Auckland"
	purgePool, err := pgxpool.NewWithConfig(f.ctx, purgeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer purgePool.Close()
	if err = maintenance.Purge(f.ctx, purgePool, purge); err != nil {
		t.Fatal(err)
	}
	var sessionZone string
	if err = purgePool.QueryRow(f.ctx, "SELECT current_setting('TimeZone')").Scan(&sessionZone); err != nil || sessionZone != "Pacific/Auckland" {
		t.Fatal("canonical purge hashing leaked its transaction timezone", sessionZone, err)
	}
	var exact bool
	if err = f.owner.QueryRow(f.ctx, `SELECT o.retired_at IS NOT NULL AND o.row_hash=sha256(convert_to(to_jsonb(a)::text,'UTF8'))
 FROM core.finance_origin_resources o JOIN core.attachments a ON a.id=o.resource_id AND a.household_id=o.household_id
 WHERE o.resource_type='attachment' AND o.resource_id=$1 AND o.module_generation=$2`, attachment, state.Generation).Scan(&exact); err != nil || !exact {
		t.Fatal("maintenance did not retain the exact retired purged image", exact, err)
	}
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal("ordinary worker physical retirement failed", err)
	}
	for _, key := range keys {
		if _, statErr := os.Stat(filepath.Join(f.app.Cfg.UploadDir, key)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("queued original or derivative still exists", statErr)
		}
	}
	var remaining int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.attachments WHERE id=$1)
 +(SELECT count(*) FROM core.attachment_derivatives WHERE attachment_id=$1)
 +(SELECT count(*) FROM core.file_deletions WHERE attachment_id=$1)`, attachment).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("physical retirement left metadata or queued work", remaining, err)
	}
	var retired int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.finance_origin_resources WHERE household_id=$1
 AND resource_type IN('attachment','attachment_derivative') AND retired_at IS NOT NULL`, f.identity.HouseholdID).Scan(&retired); err != nil || retired < len(keys) {
		t.Fatal("physical deletion discarded financial deny evidence", retired, err)
	}
}
