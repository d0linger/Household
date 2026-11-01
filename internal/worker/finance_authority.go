package worker

import (
	"context"

	"household/internal/audit"
	"household/internal/store"
)

func (w *Worker) financeArtifactPreflight(ctx context.Context, job, lease string) error {
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return store.CheckFinanceArtifactJob(ctx, tx, job, lease)
}
