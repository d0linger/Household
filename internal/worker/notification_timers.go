package worker

import (
	"context"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/value"
)

// Timer work has its own bounded cadence so document processing or a slow
// ordinary notification cannot hold the deadline scheduler for a full Tick.
// Two senders still share the service's transport slots and persistent budgets.
func (w *Worker) timerNotificationLoop(ctx context.Context, schedule bool) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastFailure time.Time
	for ctx.Err() == nil {
		err := guarded(ctx, func(ctx context.Context) error {
			ctx = audit.WithRequest(audit.WithSystem(ctx, "worker"), value.UUID())
			if schedule {
				return w.Push.ScheduleCookingTimers(ctx)
			}
			for range 8 {
				worked, err := w.notificationJobScoped(ctx, true, true)
				if err != nil || !worked {
					return err
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && time.Since(lastFailure) >= 30*time.Second {
			lastFailure = time.Now()
			if w.Log != nil {
				w.Log.Error("timer notification work failed", "scheduler", schedule, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
