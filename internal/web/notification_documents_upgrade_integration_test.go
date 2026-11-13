//go:build integration

package web

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

// Reconstruct only the frozen Documents2 predecessor in the exclusively leased
// test database. This refuses populated sidecars and is never a runtime downgrade.
func notificationDocumentsRestore2(t *testing.T, f *integrationFixture) {
	t.Helper()
	notificationMealplanRestore12(t, f)
	var valid bool
	var version int
	err := f.owner.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner',
 (SELECT version FROM documents.schema_migrations WHERE NOT dirty)`).Scan(&valid, &version)
	if err != nil || !valid {
		t.Fatal("Documents predecessor requires the isolated owner fixture", valid, err)
	}
	if version == 2 {
		return
	}
	if version != 3 {
		t.Fatal("unexpected Documents predecessor source version", version)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	_, err = tx.Exec(f.ctx, `DO $restore$
 DECLARE snapshot jsonb;item record;
 BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM meters.schema_migrations)
 OR EXISTS(SELECT 1 FROM documents.notification_settings)
 OR EXISTS(SELECT 1 FROM documents.notification_processing_events) OR EXISTS(SELECT 1 FROM documents.notification_import_events) THEN
 RAISE EXCEPTION 'Document predecessor requires clean Core39/Expenses6/Meters3 and unused sidecars';END IF;
 snapshot=documents.notification_predecessor_catalog();
 IF snapshot->'functions' IS NULL OR snapshot->'documents_catalog' IS NULL OR snapshot->'pantry_catalog' IS NULL THEN
 RAISE EXCEPTION 'missing reviewed Documents2 predecessor evidence';END IF;
 DROP TRIGGER notification_deadline_clock ON documents.deadlines;
 DROP TRIGGER notification_document_clock ON documents.documents;
 DROP TRIGGER notification_processing_transition ON documents.processing;
 DROP TRIGGER notification_import_transition ON documents.integration_sources;
 DROP TABLE documents.notification_settings,documents.notification_processing_events,documents.notification_import_events;
 ALTER TABLE documents.deadlines DROP COLUMN notification_updated_at;
 ALTER TABLE documents.documents DROP COLUMN notification_active_since;
 DROP FUNCTION documents.notification_settings_guard(),documents.notification_settings_audit(),documents.notification_deadline_clock(),
 documents.notification_document_clock(),documents.notification_processing_transition(),documents.notification_import_transition(),
 documents.notification_ready(),documents.notification_catalog();
 FOR item IN SELECT key,value FROM jsonb_each(snapshot->'functions') ORDER BY key LOOP
 EXECUTE item.value->>'definition';END LOOP;
 DROP FUNCTION documents.notification_predecessor_catalog();
 UPDATE documents.schema_migrations SET version=2 WHERE version=3 AND NOT dirty;
 IF documents.expansion_catalog() IS DISTINCT FROM snapshot->'documents_catalog'
 OR pantry.notification_observations_catalog() IS DISTINCT FROM snapshot->'pantry_catalog'
 OR NOT documents.expansion_ready() OR NOT meters.notification_ready() OR NOT core.audit_ready() OR NOT pantry.prepared_capture_ready()
 OR NOT pantry.notification_observations_ready() OR NOT core.finance_ready() OR NOT expenses.notification_ready()
 OR NOT core.notification_financial_ready() OR NOT fuel.notification_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
 RAISE EXCEPTION 'Documents2 reconstruction changed reviewed contracts';END IF;
 END $restore$;`)
	if err != nil {
		t.Fatal("restore exact Documents2 predecessor", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationDocumentsLegacyFacts(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'documents',(SELECT jsonb_agg(to_jsonb(x)-'notification_active_since' ORDER BY id) FROM documents.documents x),
 'profiles',(SELECT jsonb_agg(to_jsonb(x) ORDER BY document_id) FROM documents.profiles x),
 'deadlines',(SELECT jsonb_agg(to_jsonb(x)-'notification_updated_at' ORDER BY id) FROM documents.deadlines x),
 'versions',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM documents.file_versions x),
 'processing',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM documents.processing x),
 'sources',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM documents.integration_sources x),
 'imports',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM documents.import_receipts x),
 'receipts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY household_id,user_id,command_key) FROM documents.command_receipts x),
 'reminders',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.reminders x WHERE module_id='documents'),
 'jobs',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.jobs x WHERE module_id='documents'),
 'audit',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM core.audit_log x))::text`).Scan(&facts)
	if err != nil {
		t.Fatal("snapshot existing Documents facts", err)
	}
	return facts
}

func TestNotificationDocumentsPopulated2UpgradePreservesFacts(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationDocumentsRestore2(t, f)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["documents"]
	}
	write := func(action, id string, version int64, data store.Record) store.Record {
		t.Helper()
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		result, err := f.app.Store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), store.DocumentCommand{
			Generation: state.Generation, Action: action, ID: id, Version: version, Data: raw,
		})
		if err != nil {
			t.Fatal("populate actual Documents2 operation", action, err)
		}
		return result
	}
	doc := write("save", "", 0, store.Record{"title": "Synthetic notification predecessor", "document_type": "contract", "fields": store.Record{}, "reviewed": true})
	id := store.String(doc["id"])
	write("deadline", id, 1, store.Record{"kind": "cancellation", "due_on": time.Now().UTC().AddDate(0, 0, 7).Format(time.DateOnly),
		"label": "Synthetic confirmed deadline", "source": "Reviewed synthetic contract", "remind_days": 7, "confirmed": true})
	source := write("integration", "", 0, store.Record{"adapter": "watch", "enabled": true})
	// Valid failed predecessor rows represent operational history. They are not
	// generated by a real OCR tool or external import in this upgrade test.
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	job, processing := value.UUID(), value.UUID()
	if _, err := audit.Exec(ctx, f.owner, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key,state,finished_at)
 VALUES($1,$2,'documents',$3,'document_processing',jsonb_build_object('processing_id',$4::text),'document/'||$4::text,'failed',clock_timestamp())`,
		job, f.identity.HouseholdID, state.Generation, processing); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Exec(ctx, f.owner, `INSERT INTO documents.processing(id,household_id,document_id,operation,state,status_code,job_id,module_generation,requested_by)
 VALUES($1,$2,$3,'extract','failed','worker_failed',$4,$5,$6)`, processing, f.identity.HouseholdID, id, job, state.Generation, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Exec(ctx, f.owner, `UPDATE documents.integration_sources SET status_code='input_failed',failures=3 WHERE id=$1`, source["id"]); err != nil {
		t.Fatal(err)
	}
	facts := notificationDocumentsLegacyFacts(t, f)
	var before time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal("upgrade populated Documents2 predecessor", err)
		}
		if err := database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("complete readiness after Documents upgrade", err)
		}
		notificationConstraintTriggerCensus(t, f, "documents.notification_catalog()", []string{"documents.notification_settings", "documents.notification_processing_events", "documents.notification_import_events"})
		if notificationDocumentsLegacyFacts(t, f) != facts {
			t.Fatal("upgrade or repeat migration changed existing document, deadline or audit history")
		}
		var untouched bool
		err := f.owner.QueryRow(f.ctx, `SELECT NOT EXISTS(SELECT 1 FROM documents.notification_settings)
 AND NOT EXISTS(SELECT 1 FROM documents.notification_processing_events) AND NOT EXISTS(SELECT 1 FROM documents.notification_import_events)
 AND NOT EXISTS(SELECT 1 FROM core.notification_intents WHERE category='documents')
 AND NOT EXISTS(SELECT 1 FROM core.notification_preferences WHERE category='documents' AND enabled)
 AND NOT EXISTS(SELECT 1 FROM documents.documents WHERE notification_active_since<$1)
 AND NOT EXISTS(SELECT 1 FROM documents.deadlines WHERE notification_updated_at<$1)
 AND (SELECT count(*) FROM core.audit_policies)=165
 AND (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='documents' AND c.relkind IN('r','p') AND c.relname<>'schema_migrations')=15`, before).Scan(&untouched)
		if err != nil || !untouched {
			t.Fatal("upgrade invented responsibility, consent, old failure events or invalid clock baselines", untouched, err)
		}
	}
}

func TestNotificationDocumentsReadinessRejectsDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_settings")
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_processing_events")
	notificationConstraintTriggerRecreation(t, f, "documents.notification_catalog()", "documents.notification_ready()", "documents.notification_import_events")
	for _, tc := range []struct{ name, sql string }{
		{"public settings read", `GRANT SELECT ON documents.notification_settings TO PUBLIC`},
		{"worker can forge processing event", `GRANT INSERT ON documents.notification_processing_events TO household_app`},
		{"worker can rewrite import event", `GRANT UPDATE ON documents.notification_import_events TO household_app`},
		{"deadline clock disabled", `ALTER TABLE documents.deadlines DISABLE TRIGGER notification_deadline_clock`},
		{"activation clock disabled", `ALTER TABLE documents.documents DISABLE TRIGGER notification_document_clock`},
		{"nullable deadline clock", `ALTER TABLE documents.deadlines ALTER COLUMN notification_updated_at DROP NOT NULL`},
		{"nullable activation clock", `ALTER TABLE documents.documents ALTER COLUMN notification_active_since DROP NOT NULL`},
		{"failure transition disabled", `ALTER TABLE documents.processing DISABLE TRIGGER notification_processing_transition`},
		{"import transition disabled", `ALTER TABLE documents.integration_sources DISABLE TRIGGER notification_import_transition`},
		{"settings guard disabled", `ALTER TABLE documents.notification_settings DISABLE TRIGGER notification_settings_guard`},
		{"audit disabled", `ALTER TABLE documents.notification_settings DISABLE TRIGGER notification_settings_audit`},
		{"public trigger helper", `GRANT EXECUTE ON FUNCTION documents.notification_import_transition() TO PUBLIC`},
		{"public predecessor snapshot", `GRANT EXECUTE ON FUNCTION documents.notification_predecessor_catalog() TO PUBLIC`},
		{"row security changed", `ALTER TABLE documents.notification_settings ENABLE ROW LEVEL SECURITY`},
		{"extra content column", `ALTER TABLE documents.notification_processing_events ADD COLUMN unreviewed_document_title text`},
		{"nullable failure owner", `ALTER TABLE documents.notification_processing_events ALTER COLUMN recipient_user_id DROP NOT NULL`},
		{"unbounded failure epoch", `ALTER TABLE documents.notification_import_events DROP CONSTRAINT notification_import_events_epoch_check`},
		{"missing responsibility index", `DROP INDEX documents.notification_settings_responsible`},
		{"missing active processing index", `DROP INDEX documents.notification_processing_active`},
		{"missing active import index", `DROP INDEX documents.notification_import_active`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := audit.Begin(audit.WithSystem(f.ctx, "operator"), f.owner)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = tx.Rollback(ctx)
			}()
			if _, err = tx.Exec(f.ctx, tc.sql); err != nil {
				t.Fatal("install rollback-only document drift", err)
			}
			var ready bool
			if err = tx.QueryRow(f.ctx, "SELECT documents.notification_ready() AND documents.expansion_ready() AND core.audit_ready()").Scan(&ready); err != nil || ready {
				t.Fatal("Document readiness accepted metadata drift", ready, err)
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("readiness after rollback-only probes", err)
	}
}

func TestNotificationDocumentsUpgradeRejectsReplacedPredecessor(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationDocumentsRestore2(t, f)
	facts := notificationDocumentsLegacyFacts(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE OR REPLACE FUNCTION documents.expansion_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT true'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err == nil {
		t.Fatal("upgrade trusted a substituted Documents2 ready function")
	}
	var absent bool
	if err := f.owner.QueryRow(f.ctx, `SELECT to_regclass('documents.notification_settings') IS NULL
 AND to_regclass('documents.notification_processing_events') IS NULL AND to_regclass('documents.notification_import_events') IS NULL
 AND to_regprocedure('documents.notification_ready()') IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='documents.deadlines'::regclass AND attname='notification_updated_at' AND NOT attisdropped)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='documents.documents'::regclass AND attname='notification_active_since' AND NOT attisdropped)`).Scan(&absent); err != nil || !absent {
		t.Fatal("rejected predecessor left partial document notification objects", absent, err)
	}
	if notificationDocumentsLegacyFacts(t, f) != facts {
		t.Fatal("rejected predecessor changed existing facts")
	}
}
