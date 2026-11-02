//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/document"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgxpool"
)

func documentNativeCommand(t *testing.T, f *recipeExpansionFixture, action, rid string, version int64, data Record) DocumentCommand {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return DocumentCommand{Generation: f.store.States()["documents"].Generation, Action: action, ID: rid, Version: version, Data: raw}
}
func documentNativeWrite(t *testing.T, f *recipeExpansionFixture, action, rid string, version int64, data Record) Record {
	t.Helper()
	out, err := f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), documentNativeCommand(t, f, action, rid, version, data))
	if err != nil {
		t.Fatal(action, err)
	}
	return out
}
func documentNativeMetadata(title string) Record {
	return Record{"title": title, "category": "", "issuer": "", "notes": "", "document_type": "invoice", "fields": Record{"total_minor": 1299, "currency": "EUR"}, "reviewed": false}
}

func TestDocumentsNativeReviewAtomicBatchAndRoleFences(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	a := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("First invoice"))
	b := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("Second invoice"))
	ids := []Record{{"id": a["id"], "version": a["version"]}, {"id": b["id"], "version": 999}}
	_, err := f.store.DocumentWrite(f.ctx, f.identity, value.UUID(), value.UUID(), documentNativeCommand(t, f, "batch_apply", "", 0, Record{"items": ids, "category": "Changed"}))
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale batch", err)
	}
	actual, err := f.store.DocumentDetail(f.ctx, f.identity, String(a["id"]))
	if err != nil || actual["category"] != "" {
		t.Fatal("partial batch committed", actual, err)
	}
	ids[1]["version"] = b["version"]
	preview := documentNativeWrite(t, f, "batch_preview", "", 0, Record{"items": ids, "category": "Changed"})
	if preview["applied"] != false {
		t.Fatal(preview)
	}
	documentNativeWrite(t, f, "batch_apply", "", 0, Record{"items": ids, "category": "Changed"})
	actual, err = f.store.DocumentDetail(f.ctx, f.identity, String(a["id"]))
	if err != nil || actual["category"] != "Changed" {
		t.Fatal(actual, err)
	}
	reader := f.identity
	reader.Role = "reader"
	_, err = f.store.DocumentWrite(f.ctx, reader, value.UUID(), value.UUID(), documentNativeCommand(t, f, "save", "", 0, documentNativeMetadata("Forbidden")))
	if !errors.Is(err, ErrForbidden) {
		t.Fatal("reader wrote", err)
	}
	stale := f.identity
	stale.AuthVersion++
	if _, err = f.store.DocumentContext(f.ctx, stale); err == nil {
		t.Fatal("stale read identity accepted")
	}
}

func TestDocumentsNativeDurableCommandConcurrencyAndGeneration(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	command := documentNativeCommand(t, f, "save", "", 0, documentNativeMetadata("Once only"))
	key := value.UUID()
	results := make(chan Record, 2)
	problems := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			out, err := f.store.DocumentWrite(f.ctx, f.identity, key, value.UUID(), command)
			results <- out
			problems <- err
		})
	}
	wg.Wait()
	close(results)
	close(problems)
	for err := range problems {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for out := range results {
		if first == "" {
			first = String(out["id"])
		} else if first != String(out["id"]) {
			t.Fatal("duplicate command created another document")
		}
	}
	command.Generation++
	if _, err := f.store.DocumentWrite(f.ctx, f.identity, key, value.UUID(), command); !errors.Is(err, ErrConflict) {
		t.Fatal("old receipt crossed generation", err)
	}
}

func documentMigrateEnergyPredecessor(t *testing.T, ctx context.Context, url string, owner *pgxpool.Pool) {
	t.Helper()
	energyMigrateChorePredecessor(t, ctx, url, owner)
	for _, step := range []struct {
		schema  string
		version uint
	}{{"meters", 2}, {"core", 33}, {"fuel", 10}, {"pantry", 16}, {"mealplan", 11}} {
		recipeExpansionMigrateTo(t, url, step.schema, step.version)
	}
}

func TestDocumentsNativePopulatedCore33UpgradeAndExactReadiness(t *testing.T) {
	f := newRecipeExpansionFixtureForMigration(t, documentMigrateEnergyPredecessor)
	rid, aid := value.UUID(), value.UUID()
	raw := []byte("%PDF-1.4\nlegacy exact original\n")
	sum := sha256.Sum256(raw)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO documents.documents(id,household_id,title,created_by) VALUES($1,$2,'Preserved original',$3)`, rid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',1,'documents',$3,$4,'Legacy.pdf','application/pdf',$5,$6,$7)`, aid, f.identity.HouseholdID, rid, value.UUID(), len(raw), sum[:], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, f.ownerURL); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.app); err != nil {
		t.Fatal(err)
	}
	var exact bool
	var policies, tables int
	if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM documents.profiles p JOIN documents.file_versions v ON v.id=p.current_version_id WHERE p.document_id=$1 AND v.original_sha256=$2 AND v.source='legacy'),(SELECT count(*) FROM core.audit_policies),(SELECT count(*) FROM pg_tables WHERE schemaname='documents' AND tablename<>'schema_migrations')`, rid, sum[:]).Scan(&exact, &policies, &tables); err != nil || !exact || policies != 163 || tables != 12 {
		t.Fatal(exact, policies, tables, err)
	}
	if _, err := f.owner.Exec(f.ctx, `ALTER TABLE documents.profiles ADD COLUMN unreviewed_drift text`); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.app); err == nil {
		t.Fatal("catalog drift passed readiness")
	}
}

func TestDocumentsNativeDeadlinesViewsRulesAndOriginalBindings(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	d := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("Manual invoice"))
	rid := String(d["id"])
	deadline := documentNativeWrite(t, f, "deadline", rid, Int(d["version"]), Record{"deadline_id": "", "deadline_version": 0, "kind": "return", "due_on": "2026-12-01", "label": "Return item", "remind_days": 7, "source": "Printed receipt", "confirmed": true, "cancelled": false})
	if deadline["deadline_id"] == nil {
		t.Fatal(deadline)
	}
	rule := documentNativeWrite(t, f, "rule_save", "", 0, Record{"name": "Invoice rule", "contains_text": "Manual", "document_type": "warranty", "issuer": "Unwanted overwrite", "category": "Automatic", "enabled": true})
	current, err := f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "rule_apply", "", 0, Record{"rule_id": rule["id"], "rule_version": 1, "items": []Record{{"id": rid, "version": current["version"]}}})
	current, err = f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil || current["document_type"] != "invoice" || current["issuer"] != "" {
		t.Fatal("manual fields overwritten", current, err)
	}
	documentNativeWrite(t, f, "view", "", 0, Record{"name": "Returns", "filters": Record{"document_type": "invoice", "due_before": "2026-12-31"}})
	context, err := f.store.DocumentContext(f.ctx, f.identity)
	if err != nil || len(context["views"].([]Record)) != 1 {
		t.Fatal(context, err)
	}
	if _, err = f.app.Exec(f.ctx, `DELETE FROM documents.documents WHERE id=$1`, rid); err == nil {
		t.Fatal("runtime deleted original record")
	}
}

func TestDocumentsNativeOriginalVersionScanJobCancelRetryAndSearch(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	d := documentNativeWrite(t, f, "save", "", 0, documentNativeMetadata("Invoice source"))
	rid := String(d["id"])
	sum := sha256.Sum256([]byte("%PDF-1.4 original"))
	aid := value.UUID()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.store.BeginMutation(ctx, f.identity, "documents", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,'documents',1,'documents',$3,$4,'Source.pdf','application/pdf',17,$5,$6)`, aid, f.identity.HouseholdID, rid, value.UUID(), sum[:], f.identity.UserID); err != nil {
		rollback(tx)
		t.Fatal(err)
	}
	original, err := RegisterDocumentOriginal(ctx, tx, f.identity, rid, aid, "upload", 1, true)
	if err != nil {
		rollback(tx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	vid := String(original["version_id"])
	if _, err = f.app.Exec(f.ctx, `UPDATE documents.file_versions SET ordinal=99 WHERE id=$1`, vid); err == nil {
		t.Fatal("original identity changed")
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE documents.file_versions SET extracted_text='Hausversicherung unverwechselbarer Suchbegriff',text_provider='pdf_text',page_count=3 WHERE id=$1`, vid); err != nil {
		t.Fatal(err)
	}
	// Explicit current selection refreshes the actual shared full-text projection.
	current, err := f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "current", rid, Int(current["version"]), Record{"version_id": vid})
	found, err := f.store.DocumentLibrary(f.ctx, f.identity, document.Filters{Search: "unverwechselbarer"})
	if err != nil || len(found["items"].([]Record)) != 1 {
		t.Fatal("authorized actual full text", found, err)
	}
	current, err = f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	pages := []Record{{"version_id": vid, "page_number": 3}, {"version_id": vid, "page_number": 1}}
	preview := documentNativeWrite(t, f, "scan_preview", rid, Int(current["version"]), Record{"label": "Reviewed ordered split", "pages": pages})
	if preview["originals_preserved"] != true {
		t.Fatal(preview)
	}
	queued := documentNativeWrite(t, f, "scan_process", rid, Int(current["version"]), Record{"label": "Reviewed ordered split", "pages": pages})
	pid := String(queued["processing_id"])
	current, err = f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "cancel", rid, Int(current["version"]), Record{"processing_id": pid})
	var state, jobState string
	if err = f.owner.QueryRow(f.ctx, `SELECT p.state,j.state FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id WHERE p.id=$1`, pid).Scan(&state, &jobState); err != nil || state != "cancelled" || jobState != "paused" {
		t.Fatal("cancel fences actual job", state, jobState, err)
	}
	current, err = f.store.DocumentDetail(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	documentNativeWrite(t, f, "retry", rid, Int(current["version"]), Record{"processing_id": pid})
	if err = f.owner.QueryRow(f.ctx, `SELECT p.state,j.state FROM documents.processing p JOIN core.jobs j ON j.id=p.job_id WHERE p.id=$1`, pid).Scan(&state, &jobState); err != nil || state != "pending" || jobState != "pending" {
		t.Fatal("explicit retry", state, jobState, err)
	}
}
