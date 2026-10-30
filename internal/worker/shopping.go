package worker

import (
	"context"
	"time"

	"household/internal/audit"
	"household/internal/modules"
	"household/internal/store"
)

func (w *Worker) shoppingNotifications(ctx context.Context) error {
	if _, err := store.New(w.Pool, modules.Registry()).StageShoppingNotifications(ctx, 20); err != nil {
		return err
	}
	if w.Push != nil {
		return w.Push.Housekeeping(ctx, 100)
	}
	return nil
}

func (w *Worker) shoppingPush(ctx context.Context, id, lease string) error {
	if w.Push != nil {
		return w.Push.SendJob(ctx, id, lease)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := audit.Exec(ctx, w.Pool, `UPDATE core.jobs SET state='paused',last_error='push_service_unavailable',
 finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND kind=$2 AND state='running' AND lease_token=$3 AND lease_until>clock_timestamp()`, id, store.ShoppingPushJobKind, lease)
	return err
}
