// Package worker runs durable, lease-fenced local jobs and reminder delivery.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"household/internal/audit"
	"household/internal/config"
	"household/internal/metrics"
	"household/internal/modules"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
	Cfg  config.Config
	Push *webpush.Service
	// Admit takes part in the live restore drain: a tick runs only when
	// admitted and releases its admission when it ends, so the web process
	// confirms a pause only after the running tick stopped writing.
	Admit func() (func(), bool)
	scan  directoryScan
}

func (w *Worker) Run(ctx context.Context) {
	defer func() {
		if err := w.Close(); err != nil && w.Log != nil {
			w.Log.Error("close background file scan", "error", err)
		}
	}()
	feedContext, stopFeeds := context.WithCancel(ctx)
	var feeds sync.WaitGroup
	if w.Push != nil && w.Push.Configuration().State == "ready" {
		for _, schedule := range []bool{true, false, false} {
			feeds.Add(1)
			go func() {
				defer feeds.Done()
				w.timerNotificationLoop(feedContext, schedule)
			}()
		}
	}
	if w.Cfg.MeterFeeds.HAURL != "" || w.Cfg.MeterFeeds.MQTTURL != "" {
		for range 2 {
			feeds.Add(1)
			go func() {
				defer feeds.Done()
				w.energyAdapterLoop(feedContext)
			}()
		}
	}
	defer func() {
		stopFeeds()
		feeds.Wait()
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
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

// Close releases the current orphan-directory cursor. Run calls it on shutdown;
// callers using Tick directly must call Close after their final cycle.
func (w *Worker) Close() error {
	if w.Push != nil {
		w.Push.Close()
	}
	return w.scan.close()
}

func (w *Worker) Tick(ctx context.Context) error {
	if w.Admit != nil {
		release, ok := w.Admit()
		if !ok {
			return nil
		}
		defer release()
	}
	ctx = audit.WithRequest(audit.WithSystem(ctx, "worker"), value.UUID())
	// An unreachable database would fail every step; report it once instead.
	if err := w.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	records := store.New(w.Pool, modules.Registry())
	return runSteps(ctx, []step{
		{"housekeeping schedule", w.scheduleHousekeeping},
		{"reminders", w.deliver},
		{"backup notices", w.BackupNotices},
		{"low stock", w.lowStock},
		{"outbox", w.outbox},
		{"shopping notifications", w.shoppingNotifications},
		{"pantry notifications", func(ctx context.Context) error {
			_, err := records.StagePantryNotifications(ctx, 50)
			return err
		}},
		{"vehicle notifications", func(ctx context.Context) error {
			_, err := records.StageFuelNotifications(ctx, 50)
			return err
		}},
		{"finance notifications", func(ctx context.Context) error {
			if w.Push == nil {
				return nil
			}
			_, err := w.Push.StageFinanceNotifications(ctx, 10)
			return err
		}},
		{"meter notifications", func(ctx context.Context) error {
			_, err := records.StageMeterNotifications(ctx, 10)
			return err
		}},
		{"document notifications", func(ctx context.Context) error {
			_, err := records.StageDocumentNotifications(ctx, 50)
			return err
		}},
		{"meal preparation notifications", func(ctx context.Context) error {
			_, err := records.StagePlanningNotifications(ctx, 10)
			return err
		}},
		{"personal security notifications", func(ctx context.Context) error {
			_, err := records.StageSecurityNotifications(ctx, 50)
			return err
		}},
		{"device notification schedule", w.notificationSchedule},
		{"device notification delivery", w.notificationJobs},
		{"files", w.files},
		{"document inputs", w.scheduleDocumentInputs},
		{"fuel projections", func(ctx context.Context) error { return records.FuelModuleHousekeeping(ctx, 1) }},
		{"fuel capture", func(ctx context.Context) error {
			return records.FuelModuleCaptureHousekeeping(ctx, w.Cfg.OCREnabled, w.fuelOCR)
		}},
		{"finance recurrence scheduling", records.ScheduleFinanceRecurrences},
		{"chores workspace scheduling", records.ScheduleChoreWorkspace},
		{"energy workspace scheduling", records.ScheduleEnergyWorkspace},
		{"energy adapter scheduling", func(ctx context.Context) error {
			return records.ScheduleEnergyAdapters(ctx, w.Cfg.MeterFeeds.HAURL != "", w.Cfg.MeterFeeds.MQTTURL != "")
		}},
		{"jobs", w.jobs},
	})
}

type step struct {
	name string
	run  func(context.Context) error
}

// runSteps runs every step even after another fails, so one broken subsystem
// cannot starve reminders, jobs or document inputs. Failures keep their names.
func runSteps(ctx context.Context, steps []step) error {
	var failures []error
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := guarded(ctx, st.run); err != nil {
			metrics.Default.Inc("household_worker_step_failures_total")
			failures = append(failures, fmt.Errorf("%s: %w", st.name, err))
		}
	}
	metrics.Default.Inc("household_worker_ticks_total")
	return errors.Join(failures...)
}

// panicked describes a recovered panic by type and call path only: the value
// and argument words can carry household data, so they never reach logs.
type panicked struct {
	kind, path string
}

func recovered(value any) panicked {
	return panicked{kind: fmt.Sprintf("%T", value), path: callPath()}
}

func (p panicked) Error() string { return "panic (" + p.kind + ") at\n" + p.path }

// callPath lists the panicking frames as function and file:line. Call it
// directly from the recovering deferred function.
func callPath() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(4, pcs)])
	var path strings.Builder
	origin := false
	for {
		frame, more := frames.Next()
		// Skip the runtime's own panic frames above the panicking function.
		if origin = origin || !strings.HasPrefix(frame.Function, "runtime."); origin {
			fmt.Fprintf(&path, "%s %s:%d\n", frame.Function, frame.File, frame.Line)
		}
		if !more {
			return path.String()
		}
	}
}

// guarded turns a panic into an error: background work shares its process with
// the HTTP server, and an unrecovered goroutine panic would stop both.
func guarded(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = recovered(value)
		}
	}()
	return run(ctx)
}

// Housekeeping is a real durable job; only its current lease holder may finish it.
func (w *Worker) scheduleHousekeeping(ctx context.Context) error {
	_, err := audit.Exec(ctx, w.Pool, `INSERT INTO core.jobs(household_id,kind,dedupe_key) SELECT id,'housekeeping','housekeeping/'||to_char(now(),'YYYY-MM-DD-HH24-MI') FROM core.households ON CONFLICT(dedupe_key) DO NOTHING`)
	return err
}

// jobs claims up to ten due jobs. A failure ends this tick's claims; failJob
// has already scheduled its retry.
// jobsPerKindPerTick caps one job kind's share of the ten claims per tick.
const jobsPerKindPerTick = 5

// jobsTickBudget bounds the wall-clock time one tick spends claiming jobs.
const jobsTickBudget = 25 * time.Second

// claimError marks a failure to claim a job, as opposed to a claimed job that
// failed. Only the former stops the tick's claim loop.
type claimError struct{ err error }

func (e claimError) Error() string { return "claim job: " + e.err.Error() }
func (e claimError) Unwrap() error { return e.err }

// jobs claims up to ten due jobs per tick. A failed job has already been moved to
// backoff by failJob, so it no longer stops the loop: otherwise one deterministic
// failure per tick would hold every other job kind behind it.
func (w *Worker) jobs(ctx context.Context) error {
	var failed []error
	claimed := map[string]int{}
	deadline := time.Now().Add(jobsTickBudget)
	for range 10 {
		if time.Now().After(deadline) {
			break
		}
		// No single job kind may take more than half a tick, so a burst from one
		// producer (shopping digests and pushes, document processing, uploads,
		// recurrence backlog) cannot hold every other kind behind it.
		exhausted := []string{}
		for kind, count := range claimed {
			if count >= jobsPerKindPerTick {
				exhausted = append(exhausted, kind)
			}
		}
		kind, err := w.job(ctx, exhausted)
		if kind == "" && err == nil {
			break
		}
		if kind != "" {
			claimed[kind]++
		}
		if err == nil {
			continue
		}
		failed = append(failed, err)
		var claim claimError
		if errors.As(err, &claim) || ctx.Err() != nil {
			break
		}
	}
	return errors.Join(failed...)
}
func (w *Worker) deliver(ctx context.Context) error {
	ctx = audit.WithSystem(ctx, "worker")
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT r.id,r.household_id,r.module_id,r.resource_type,r.resource_id,r.kind,r.recipient_user_id,r.dedupe_key FROM core.reminders r JOIN core.module_states m ON m.household_id=r.household_id AND m.module_id=r.module_id AND m.state='enabled' AND m.data_generation=r.module_generation WHERE r.state='pending' AND r.module_id NOT IN('expenses','chores') AND r.scheduled_at<=now() ORDER BY r.scheduled_at LIMIT 50 FOR UPDATE OF r SKIP LOCKED`)
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
	ctx = audit.WithSystem(ctx, "worker")
	_, err := audit.Exec(ctx, w.Pool, `INSERT INTO core.event_deliveries(event_id,consumer_id,state,delivered_at) SELECT e.id,'local-projections-v1','done',now() FROM core.outbox_events e JOIN core.module_states m ON m.household_id=e.household_id AND m.module_id=e.module_id AND m.state='enabled' AND m.data_generation=e.module_generation WHERE NOT EXISTS(SELECT 1 FROM core.event_deliveries d WHERE d.event_id=e.id AND d.consumer_id='local-projections-v1') ORDER BY e.created_at LIMIT 100 ON CONFLICT DO NOTHING`)
	return err
}
func (w *Worker) job(ctx context.Context, exhausted []string) (kind string, jobErr error) {
	ctx = audit.WithSystem(ctx, "worker")
	lease := value.UUID()
	var id string
	err := w.Pool.QueryRow(ctx, `WITH candidate AS(SELECT j.id FROM core.jobs j WHERE NOT (kind=ANY($2::text[])) AND kind NOT IN('energy_adapter','notification_push_v1','pantry_notification_scan_v1','pantry_low_stock_scan_v1','fuel_notification_scan_v1','finance_notification_scan_v1','meter_notification_scan_v1','document_notification_scan_v1','mealplan_notification_scan_v1','security_notification_source_v1') AND ((state='pending' AND available_at<=now()) OR (state='running' AND lease_until<now())) AND attempts<max_attempts AND (module_id IS NULL OR EXISTS(SELECT 1 FROM core.module_states m WHERE m.household_id=j.household_id AND m.module_id=j.module_id AND m.state='enabled' AND m.data_generation=j.module_generation)) ORDER BY available_at LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE core.jobs j SET state='running',lease_token=$1,lease_until=now()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.kind`, lease, exhausted).Scan(&id, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", claimError{err}
	}
	return kind, w.run(ctx, id, kind, lease)
}

// run executes one claimed job and records a failure (including a panic) so the
// lease is released and the job enters backoff.
func (w *Worker) run(ctx context.Context, id, kind, lease string) (jobErr error) {
	defer func() {
		// A panicking handler must still release its lease and enter backoff.
		if value := recover(); value != nil {
			jobErr = recovered(value)
		}
		if jobErr != nil {
			retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if err := w.failJob(retryCtx, id, lease); err != nil {
				w.Log.Error("record job failure", "job_id", id, "error", err)
			}
			jobErr = fmt.Errorf("%s job %s: %w", kind, id, jobErr)
		}
	}()
	if kind == "capture_ocr" {
		return w.capture(ctx, id, lease)
	}
	if kind == "thumbnail" {
		return w.thumbnail(ctx, id, lease)
	}
	if kind == "document_processing" {
		return w.documentJob(ctx, id, lease)
	}
	if kind == "document_input" {
		return w.documentInput(ctx, id, lease)
	}
	if kind == store.ShoppingDigestJobKind {
		return store.New(w.Pool, modules.Registry()).CompleteShoppingNotificationJob(ctx, id, lease)
	}
	if kind == store.PantryNotificationDigestJobKind {
		return store.New(w.Pool, modules.Registry()).CompletePantryNotificationDigest(ctx, id, lease)
	}
	if kind == store.ShoppingPushJobKind {
		return w.shoppingPush(ctx, id, lease)
	}
	if kind == store.FinanceRecurrenceJobKind {
		return store.New(w.Pool, modules.Registry()).CompleteFinanceRecurrenceJob(ctx, id, lease)
	}
	if kind == store.ChoreWorkspaceJobKind {
		return store.New(w.Pool, modules.Registry()).CompleteChoreWorkspaceJob(ctx, id, lease)
	}
	if kind == store.EnergyWorkspaceJobKind {
		return store.New(w.Pool, modules.Registry()).CompleteEnergyWorkspaceJob(ctx, id, lease)
	}
	tx, err := audit.Begin(ctx, w.Pool)
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
		_, err = tx.Exec(ctx, "UPDATE core.jobs SET state='failed',last_error='unsupported local job kind',finished_at=now(),lease_until=NULL,lease_token=NULL WHERE id=$1 AND lease_token=$2", id, lease)
		if err != nil {
			return err
		}
		if err = recordJobFailure(ctx, tx, id, "worker_job_unsupported"); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, "SELECT core.finance_prune_artifacts($1,$2)", id, lease); err != nil {
		return fmt.Errorf("finance artifact retention: %w", err)
	}
	for _, query := range []string{"DELETE FROM core.rate_limit_buckets WHERE window_end<now()-interval '1 day'", "DELETE FROM core.sessions WHERE absolute_expires_at<now()-interval '7 days' OR revoked_at<now()-interval '7 days'", "DELETE FROM core.auth_challenges WHERE expires_at<now()-interval '1 day'", "DELETE FROM core.idempotency_records WHERE expires_at<now()"} {
		if _, err = tx.Exec(ctx, query); err != nil {
			return fmt.Errorf("housekeeping: %w", err)
		}
	}
	if err = prunePantryDrafts(ctx, tx); err != nil {
		return err
	}
	if err = expireMealReservations(ctx, tx); err != nil {
		return err
	}
	if err = expireJobs(ctx, tx); err != nil {
		return err
	}
	if err = reviewCaptures(ctx, tx, nil, "failed", "Texterkennung fehlgeschlagen. Werte bitte manuell prüfen."); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.jobs SET state='done',finished_at=now(),lease_until=NULL,lease_token=NULL WHERE id=$1 AND lease_token=$2", id, lease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func prunePantryDrafts(ctx context.Context, tx pgx.Tx) error {
	// Housekeeping already holds the household maintenance barrier. Older
	// schema versions and disabled optional modules do not require this table.
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('pantry.stock_batch_drafts') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM pantry.stock_batch_drafts WHERE id IN(
 SELECT id FROM pantry.stock_batch_drafts WHERE state<>'posted' AND(state='discarded' OR expires_at<=clock_timestamp())
 ORDER BY expires_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)`)
	if err != nil {
		return fmt.Errorf("prune private pantry drafts: %w", err)
	}
	return nil
}

func expireMealReservations(ctx context.Context, tx pgx.Tx) error {
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('core.meal_reservations') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	// Effective reads already exclude expired leases. This bounded operation
	// records terminal history without taking source or lot locks after Core.
	_, err := tx.Exec(ctx, `WITH selected AS (
 SELECT id,version,expires_at FROM core.meal_reservations WHERE state='active' AND expires_at<=clock_timestamp()
 ORDER BY expires_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)
 UPDATE core.meal_reservations r SET state='expired',release_reason='expired' FROM selected s
 WHERE r.id=s.id AND r.version=s.version AND r.expires_at=s.expires_at AND r.state='active' AND r.expires_at<=clock_timestamp()`)
	if err != nil {
		return fmt.Errorf("expire meal reservations: %w", err)
	}
	return nil
}

func (w *Worker) failJob(ctx context.Context, id, lease string) error {
	ctx = audit.WithSystem(ctx, "worker")
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET
		state=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'pending' END,
		available_at=now()+make_interval(secs=>least(3600,30*power(2,least(attempts,7))::integer)),
		finished_at=CASE WHEN attempts>=max_attempts THEN now() ELSE NULL END,
		lease_until=NULL,lease_token=NULL,last_error='local worker failed; inspect operator logs'
		WHERE id=$1 AND lease_token=$2 AND state='running'`, id, lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	var documentFailed bool
	if err = tx.QueryRow(ctx, `SELECT kind='document_processing' AND state='failed' FROM core.jobs WHERE id=$1`, id).Scan(&documentFailed); err != nil {
		return err
	}
	if documentFailed {
		if _, err = tx.Exec(ctx, `UPDATE documents.processing SET state='failed',status_code='worker_failed',updated_at=clock_timestamp() WHERE job_id=$1 AND state IN('pending','running')`, id); err != nil {
			return err
		}
	}
	if err = recordJobFailure(ctx, tx, id, "worker_job_failed"); err != nil {
		return err
	}
	if err = reviewCaptures(ctx, tx, &id, "failed", "Texterkennung fehlgeschlagen. Werte bitte manuell prüfen."); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Lease churn is operational state, but terminal job failures are retained as
// security-safe semantic events in the transaction that marks them failed.
func recordJobFailure(ctx context.Context, tx pgx.Tx, id, action string) error {
	var household, module string
	err := tx.QueryRow(ctx, "SELECT household_id,coalesce(module_id,'core') FROM core.jobs WHERE id=$1 AND state='failed'", id).Scan(&household, &module)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx = audit.WithHousehold(ctx, household)
	if err = audit.Bind(ctx, tx); err != nil {
		return err
	}
	return audit.Event(ctx, tx, module, "jobs", id, action, nil)
}

func expireJobs(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `UPDATE core.jobs SET state='failed',finished_at=now(),lease_until=NULL,lease_token=NULL,last_error='lease retries exhausted'
		WHERE id IN (SELECT id FROM core.jobs WHERE state='running' AND lease_until<now() AND attempts>=max_attempts ORDER BY lease_until LIMIT 100 FOR UPDATE SKIP LOCKED)
		RETURNING id`)
	if err != nil {
		return err
	}
	ids := make([]string, 0, 100)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE documents.processing p SET state='failed',status_code='processing_interrupted',updated_at=clock_timestamp()
 FROM core.jobs j WHERE j.id=$1 AND j.kind='document_processing' AND j.state='failed'
 AND p.job_id=j.id AND p.household_id=j.household_id AND p.module_generation=j.module_generation
 AND p.state IN('pending','running')`, id); err != nil {
			return err
		}
		if err = recordJobFailure(ctx, tx, id, "worker_lease_exhausted"); err != nil {
			return err
		}
	}
	return nil
}

// The caller holds the household barrier, so pause/purge cannot invalidate the
// generation or attachment between checking the job and exposing manual review.
func reviewCaptures(ctx context.Context, tx pgx.Tx, jobID *string, state, message string) error {
	_, err := tx.Exec(ctx, `UPDATE core.capture_jobs c SET state='review',source_text=$3
		FROM core.jobs j,core.module_states m,core.attachments a
		WHERE ($1::uuid IS NULL OR j.id=$1) AND j.kind='capture_ocr' AND j.state=$2
		AND c.id::text=j.payload->>'capture_id' AND c.state='extracting'
		AND c.household_id=j.household_id AND c.module_id=j.module_id AND c.module_generation=j.module_generation
		AND m.household_id=c.household_id AND m.module_id=c.module_id AND m.state='enabled' AND m.data_generation=c.module_generation
		AND a.id=c.source_attachment_id AND a.household_id=c.household_id AND a.module_id=c.module_id
		AND a.module_generation=c.module_generation AND a.state='ready'
		AND NOT EXISTS(SELECT 1 FROM core.finance_origin_resources f WHERE f.household_id=c.household_id
		AND f.resource_type='capture' AND f.resource_id=c.id)`, jobID, state, message)
	return err
}
