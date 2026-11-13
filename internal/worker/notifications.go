package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (w *Worker) notificationSchedule(ctx context.Context) error {
	if w.Push == nil {
		return nil
	}
	return w.Push.ScheduleNotifications(ctx)
}

// One urgent and one ordinary slot per pass prevent priority-only starvation.
// Transport concurrency and shared database budgets remain the final limits.
func (w *Worker) notificationJobs(ctx context.Context) error {
	var failures []error
	for _, urgent := range []bool{true, false} {
		if err := w.notificationJob(ctx, urgent); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) notificationJob(ctx context.Context, urgent bool) (result error) {
	_, err := w.notificationJobScoped(ctx, urgent, false)
	return err
}

func (w *Worker) notificationJobScoped(ctx context.Context, urgent, timersOnly bool) (worked bool, result error) {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 20*time.Second)
	defer cancel()
	lease := value.UUID()
	var job string
	err := w.Pool.QueryRow(ctx, `WITH candidate AS(SELECT j.id FROM core.jobs j
 LEFT JOIN core.notification_deliveries d ON d.job_id=j.id LEFT JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE j.kind=$2 AND((j.state='pending' AND j.available_at<=clock_timestamp()) OR(j.state='running' AND j.lease_until<clock_timestamp()))
 AND j.attempts<j.max_attempts
 AND(NOT $4 OR(i.category='cooking_timer' AND d.state='pending' AND i.state='pending' AND i.expires_at>clock_timestamp()))
 ORDER BY CASE WHEN coalesce(i.category IN('cooking_timer','security','test'),false)=$3 THEN 0 ELSE 1 END,j.available_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED)
 UPDATE core.jobs j SET state='running',lease_token=$1,lease_until=clock_timestamp()+interval '30 seconds',attempts=attempts+1
 FROM candidate c WHERE j.id=c.id RETURNING j.id::text`, lease, notification.JobKind, urgent, timersOnly).Scan(&job)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	worked = true
	defer func() {
		if v := recover(); v != nil {
			result = recovered(v)
		}
		if result != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer stop()
			if e := w.failJob(cleanup, job, lease); e != nil {
				result = errors.Join(result, fmt.Errorf("record notification job failure: %w", e))
			}
		}
	}()
	if w.Push != nil {
		return true, w.Push.SendNotificationJob(ctx, job, lease)
	}
	_, err = audit.Exec(ctx, w.Pool, `UPDATE core.jobs SET state='paused',last_error='push_service_unavailable',finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, job, lease)
	return true, err
}
