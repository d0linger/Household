// Package worker runs durable, lease-fenced local jobs and reminder delivery.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"household/internal/config"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	Pool       *pgxpool.Pool
	Log        *slog.Logger
	Cfg        config.Config
	scanOffset int
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.Log.Error("background work failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *Worker) Tick(ctx context.Context) error {
	// Housekeeping is a real durable job; only its current lease holder may finish it.
	if _, err := w.Pool.Exec(ctx, `INSERT INTO core.jobs(household_id,kind,dedupe_key) SELECT id,'housekeeping','housekeeping/'||to_char(now(),'YYYY-MM-DD-HH24-MI') FROM core.households ON CONFLICT(dedupe_key) DO NOTHING`); err != nil {
		return err
	}
	if err := w.deliver(ctx); err != nil {
		return err
	}
	if err := w.lowStock(ctx); err != nil {
		return err
	}
	if err := w.outbox(ctx); err != nil {
		return err
	}
	if err := w.files(ctx); err != nil {
		return err
	}
	for range 10 {
		if err := w.job(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (w *Worker) deliver(ctx context.Context) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT r.id,r.household_id,r.module_id,r.resource_type,r.resource_id,r.kind,r.recipient_user_id,r.dedupe_key FROM core.reminders r JOIN core.module_states m ON m.household_id=r.household_id AND m.module_id=r.module_id AND m.state='enabled' AND m.data_generation=r.module_generation WHERE r.state='pending' AND r.scheduled_at<=now() ORDER BY r.scheduled_at LIMIT 50 FOR UPDATE OF r SKIP LOCKED`)
	if err != nil {
		return err
	}
	type reminder struct {
		id, household, module, entity, resource, kind, key string
		recipient                                          *string
	}
	items := []reminder{}
	for rows.Next() {
		var x reminder
		if err = rows.Scan(&x.id, &x.household, &x.module, &x.entity, &x.resource, &x.kind, &x.recipient, &x.key); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range items {
		recipient := ""
		if x.recipient != nil {
			recipient = *x.recipient
		}
		title := map[string]string{"document_expiry": "Dokument oder Garantie läuft bald ab", "stock_expiry": "Ein Vorrat erreicht sein Haltbarkeitsdatum", "task_due": "Eine Haushaltsaufgabe ist fällig"}[x.kind]
		if title == "" {
			title = "Eine Erinnerung ist fällig"
		}
		_, err = tx.Exec(ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key) SELECT m.household_id,m.user_id,$3,$4,$5,$6,$7||'/'||m.user_id::text FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND ($2='' OR m.user_id=nullif($2,'')::uuid) ON CONFLICT(dedupe_key) DO NOTHING`, x.household, recipient, x.module, x.kind, title, "/m/"+x.module+"/"+x.entity+"/"+x.resource, x.key)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE core.reminders SET state='delivered',delivered_at=now() WHERE id=$1", x.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (w *Worker) outbox(ctx context.Context) error {
	_, err := w.Pool.Exec(ctx, `INSERT INTO core.event_deliveries(event_id,consumer_id,state,delivered_at) SELECT e.id,'local-projections-v1','done',now() FROM core.outbox_events e JOIN core.module_states m ON m.household_id=e.household_id AND m.module_id=e.module_id AND m.state='enabled' AND m.data_generation=e.module_generation WHERE NOT EXISTS(SELECT 1 FROM core.event_deliveries d WHERE d.event_id=e.id AND d.consumer_id='local-projections-v1') ORDER BY e.created_at LIMIT 100 ON CONFLICT DO NOTHING`)
	return err
}
func (w *Worker) job(ctx context.Context) (jobErr error) {
	lease := value.UUID()
	var id, kind string
	err := w.Pool.QueryRow(ctx, `WITH candidate AS(SELECT j.id FROM core.jobs j WHERE ((state='pending' AND available_at<=now()) OR (state='running' AND lease_until<now())) AND attempts<max_attempts AND (module_id IS NULL OR EXISTS(SELECT 1 FROM core.module_states m WHERE m.household_id=j.household_id AND m.module_id=j.module_id AND m.state='enabled' AND m.data_generation=j.module_generation)) ORDER BY available_at LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE core.jobs j SET state='running',lease_token=$1,lease_until=now()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.kind`, lease).Scan(&id, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		if jobErr != nil {
			retryCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = w.Pool.Exec(retryCtx, "UPDATE core.jobs SET state=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'pending' END,available_at=now()+make_interval(secs=>least(3600,30*power(2,attempts)::integer)),lease_until=NULL,last_error='local worker failed; inspect operator logs' WHERE id=$1 AND lease_token=$2 AND state='running'", id, lease)
			_, _ = w.Pool.Exec(retryCtx, "UPDATE core.capture_jobs SET state='review',source_text='Texterkennung fehlgeschlagen. Werte bitte manuell prüfen.' WHERE id=(SELECT nullif(payload->>'capture_id','')::uuid FROM core.jobs WHERE id=$1 AND state='failed') AND state='extracting'", id)
		}
	}()
	if kind == "capture_ocr" {
		return w.capture(ctx, id, lease)
	}
	if kind == "thumbnail" {
		return w.thumbnail(ctx, id, lease)
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, "SELECT true FROM core.jobs WHERE id=$1 AND lease_token=$2 AND lease_until>now() AND state='running' FOR UPDATE", id, lease).Scan(&valid); err != nil {
		return err
	}
	if kind != "housekeeping" {
		_, err = tx.Exec(ctx, "UPDATE core.jobs SET state='failed',last_error='unsupported local job kind',finished_at=now() WHERE id=$1 AND lease_token=$2", id, lease)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for _, query := range []string{"DELETE FROM core.rate_limit_buckets WHERE window_end<now()-interval '1 day'", "DELETE FROM core.sessions WHERE absolute_expires_at<now()-interval '7 days' OR revoked_at<now()-interval '7 days'", "DELETE FROM core.auth_challenges WHERE expires_at<now()-interval '1 day'", "DELETE FROM core.idempotency_records WHERE expires_at<now()", "DELETE FROM core.capture_jobs WHERE expires_at<now() AND state<>'confirmed'", "DELETE FROM core.import_batches WHERE created_at<now()-interval '30 days' AND state='preview'", "UPDATE core.jobs SET state='failed',finished_at=now(),last_error='lease retries exhausted' WHERE state='running' AND lease_until<now() AND attempts>=max_attempts"} {
		if _, err = tx.Exec(ctx, query); err != nil {
			return fmt.Errorf("housekeeping: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE core.jobs SET state='done',finished_at=now(),lease_until=NULL WHERE id=$1 AND lease_token=$2", id, lease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
