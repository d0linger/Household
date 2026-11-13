//go:build integration

package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"
)

func TestNotificationDocumentsWorkerExhaustedLeaseAndFailureFences(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	generation := f.app.Store.States()["documents"].Generation
	doc, err := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{
		Generation: generation, Action: "save",
		Data: json.RawMessage(`{"title":"Synthetic interrupted processing","category":"","issuer":"","notes":"","document_type":"other","fields":{},"reviewed":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A disappeared worker cannot be reproduced by completing a handler. Seed
	// only its durable lease/processing state; the actual worker must expire it.
	queue := func(processingState, jobState, actor string, attempts int, expired bool) string {
		t.Helper()
		pid, jid := value.UUID(), value.UUID()
		ctx := audit.WithActor(f.ctx, actor, f.identity.HouseholdID)
		tx, e := audit.Begin(ctx, f.pool)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, e = tx.Exec(ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key,state,
 attempts,max_attempts,lease_token,lease_until) VALUES($1,$2,'documents',$3,'document_processing',jsonb_build_object('processing_id',$4::text),
 'document/'||$4::text,$5,$6,5,CASE WHEN $5='running' THEN $7::uuid END,
 CASE WHEN $5='running' THEN clock_timestamp()+CASE WHEN $8 THEN interval '-1 minute' ELSE interval '1 hour' END END)`,
			jid, f.identity.HouseholdID, generation, pid, jobState, attempts, value.UUID(), expired)
		if e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO documents.processing(id,household_id,document_id,operation,input,job_id,module_generation,requested_by,state)
 VALUES($1,$2,$3,'scan','{}',$4,$5,$6,$7)`, pid, f.identity.HouseholdID, doc["id"], jid, generation, actor, processingState)
		}
		if e == nil {
			e = tx.Commit(ctx)
		}
		if e != nil {
			t.Fatal("durable interrupted fixture", e)
		}
		return pid
	}
	interruptedPending := queue("pending", "running", f.identity.UserID, 5, true)
	interruptedRunning := queue("running", "running", f.identity.UserID, 5, true)
	currentLease := queue("running", "running", f.identity.UserID, 5, false)
	formerWriter := shoppingTestMember(t, f, "document-former-writer", "editor")
	unauthorized := queue("pending", "pending", formerWriter.UserID, 0, false)
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.memberships SET role='reader' WHERE household_id=$1 AND user_id=$2`, f.identity.HouseholdID, formerWriter.UserID); err != nil {
		t.Fatal(err)
	}
	// Run real housekeeping through its normal lease. No expiry function or
	// notification event is called directly.
	if _, err = audit.Exec(audit.WithSystem(f.ctx, "worker"), f.pool, `INSERT INTO core.jobs(household_id,kind,dedupe_key,available_at)
 VALUES($1,'housekeeping',$2,clock_timestamp()-interval '1 day')`, f.identity.HouseholdID, "document-worker-test/"+value.UUID()); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	defer func() { _ = w.Close() }()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal("actual expiry and authorization retirement", err)
	}
	check := func(pid, wantJob, wantProcessing, wantCode string, wantEpoch int64) time.Time {
		t.Helper()
		var job, processing, code, recipient string
		var epoch, eventGeneration int64
		var active bool
		var occurred *time.Time
		e := f.pool.QueryRow(f.ctx, `SELECT j.state,p.state,p.status_code,coalesce(e.epoch,0),coalesce(e.active,false),e.occurred_at,
 coalesce(e.recipient_user_id::text,''),coalesce(e.source_generation,0)
 FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id
 LEFT JOIN documents.notification_processing_events e ON e.household_id=p.household_id AND e.id=p.id WHERE p.id=$1`, pid).
			Scan(&job, &processing, &code, &epoch, &active, &occurred, &recipient, &eventGeneration)
		if e != nil || job != wantJob || processing != wantProcessing || code != wantCode || epoch != wantEpoch || active != (wantEpoch > 0) {
			t.Fatalf("terminal state: job=%s processing=%s code=%s epoch=%d active=%t err=%v", job, processing, code, epoch, active, e)
		}
		if wantEpoch > 0 {
			if occurred == nil || recipient != f.identity.UserID || eventGeneration != generation {
				t.Fatal("terminal event lost source/recipient/time binding")
			}
			return *occurred
		}
		return time.Time{}
	}
	first := check(interruptedPending, "failed", "failed", "processing_interrupted", 1)
	check(interruptedRunning, "failed", "failed", "processing_interrupted", 1)
	check(currentLease, "running", "running", "queued", 0)
	check(unauthorized, "failed", "failed", "authorization_changed", 0)
	var events int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE household_id=$1 AND action='worker_lease_exhausted'`, f.identity.HouseholdID).Scan(&events); err != nil || events != 2 {
		t.Fatal("terminal lease audit", events, err)
	}
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal("unchanged worker repeat", err)
	}
	if repeated := check(interruptedPending, "failed", "failed", "processing_interrupted", 1); !repeated.Equal(first) {
		t.Fatal("repeat renewed the original failure event")
	}
	// The synthetic interrupted scan has no valid set_id. Actual handler
	// validation therefore enters failJob, exercising retry and exhaustion.
	transient := queue("pending", "pending", f.identity.UserID, 0, false)
	if err = w.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), "document_processing") {
		t.Fatal("actual transient processing failure was not exercised", err)
	}
	check(transient, "pending", "running", "processing", 0)
	terminal := queue("pending", "pending", f.identity.UserID, 4, false)
	if err = w.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), "document_processing") {
		t.Fatal("actual final processing attempt was not exercised", err)
	}
	check(terminal, "failed", "failed", "worker_failed", 1)
}
