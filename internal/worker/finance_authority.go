package worker

import (
	"context"
	"errors"

	"household/internal/audit"
	"household/internal/store"
)

// retireObsoleteSourceJob finishes a thumbnail or capture job whose preflight
// reported not found because its source attachment is no longer ready (deleted
// record, orphaned or purged). Such a job can never succeed, so retrying it only
// spends worker claims; every other preflight error is returned unchanged.
func (w *Worker) retireObsoleteSourceJob(ctx context.Context, job, lease string, preflight error) error {
	if !errors.Is(preflight, store.ErrNotFound) {
		return preflight
	}
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE core.jobs j SET state='done',finished_at=now(),last_error='obsolete_source',lease_until=NULL,lease_token=NULL
 WHERE j.id=$1 AND j.lease_token=$2 AND j.state='running' AND j.lease_until>now() AND j.kind IN('thumbnail','capture_ocr')
 AND NOT EXISTS(SELECT 1 FROM core.attachments a WHERE a.household_id=j.household_id AND a.state='ready'
  AND a.id::text=coalesce(j.payload->>'attachment_id',(SELECT c.source_attachment_id::text FROM core.capture_jobs c WHERE c.id::text=j.payload->>'capture_id')))`, job, lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return preflight
	}
	return tx.Commit(ctx)
}

func (w *Worker) financeArtifactPreflight(ctx context.Context, job, lease string) error {
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return store.CheckFinanceArtifactJob(ctx, tx, job, lease)
}
