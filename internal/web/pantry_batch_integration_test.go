//go:build integration

package web

import (
	"encoding/json"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestPantryBatchSchemaCompletionAndZeroCount(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "count foundation")
	draft, line := value.UUID(), value.UUID()
	payload, _ := json.Marshal(store.Record{"lines": []store.Record{{"id": line, "lot_id": lot["id"], "quantity": "0"}}})
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_batch_drafts(id,household_id,owner_id,generation,kind,location_id,payload)
 VALUES($1,$2,$3,$4,'inventory',$5,$6)`, draft, f.identity.HouseholdID, f.identity.UserID, f.app.Store.States()["pantry"].Generation, lot["location_id"], payload); err != nil {
		t.Fatal(err)
	}
	root := `INSERT INTO pantry.stock_batch_receipts(id,household_id,draft_id,generation,draft_version,kind,created_by,request_key,request_hash,line_count,movement_count,snapshot)
 VALUES($1,$2,$3,$4,1,'inventory',$5,$6,decode(repeat('00',32),'hex'),1,0,'{"lines":[{}]}')`
	tx, err := audit.Begin(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, root, value.UUID(), f.identity.HouseholdID, draft, f.app.Store.States()["pantry"].Generation, f.identity.UserID, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE pantry.stock_batch_drafts SET state='posted' WHERE id=$1", draft); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil || !strings.Contains(err.Error(), "incomplete stock batch") {
		t.Fatal("receipt without count line committed", err)
	}
	var version int64
	var state string
	if err = f.pool.QueryRow(f.ctx, "SELECT version,state FROM pantry.stock_batch_drafts WHERE id=$1", draft).Scan(&version, &state); err != nil || version != 1 || state != "active" {
		t.Fatal("incomplete bundle changed draft", version, state, err)
	}
	tx, err = audit.Begin(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	posting := value.UUID()
	if _, err = tx.Exec(f.ctx, root, posting, f.identity.HouseholdID, draft, f.app.Store.States()["pantry"].Generation, f.identity.UserID, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO pantry.stock_batch_lines(household_id,receipt_id,line_id,lot_id,before_quantity,counted_quantity,quantity_delta,unit_code)
 VALUES($1,$2,$3,$4,0,0,0,'kg')`, f.identity.HouseholdID, posting, line, lot["id"]); err != nil {
		t.Fatal("zero count line rejected", err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE pantry.stock_batch_drafts SET state='posted' WHERE id=$1", draft); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("complete unchanged count rejected", err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.stock_batch_drafts SET title='Changed after posting' WHERE id=$1", draft); err == nil {
		t.Fatal("posted draft editable")
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM pantry.stock_batch_drafts WHERE id=$1", draft); err == nil {
		t.Fatal("posted source draft prunable")
	}
	for _, table := range []string{"pantry.stock_batch_receipts", "pantry.stock_batch_lines"} {
		if _, err = f.pool.Exec(f.ctx, "DELETE FROM "+table); err == nil {
			t.Fatal("immutable batch table writable", table)
		}
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var indexes int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pg_indexes WHERE schemaname='pantry' AND indexname IN('pantry_inventory_page','pantry_batch_lot_history','pantry_batch_owner')").Scan(&indexes); err != nil || indexes != 3 {
		t.Fatal("batch selection/history indexes missing", indexes, err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE pantry.stock_batch_receipts DISABLE TRIGGER pantry_batch_receipt_complete"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("readiness accepted missing batch completeness")
	}
}

func TestPantryBatchPrivateDraftQuotaAndLifecycle(t *testing.T) {
	f := newIntegrationFixture(t)
	makeDraft := func() error {
		_, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_batch_drafts(household_id,owner_id,generation,kind,payload)
 VALUES($1,$2,$3,'intake','{"lines":[]}')`, f.identity.HouseholdID, f.identity.UserID, f.app.Store.States()["pantry"].Generation)
		return err
	}
	done := make(chan error, 12)
	for range 12 {
		go func() { done <- makeDraft() }()
	}
	success := 0
	for range 12 {
		if err := <-done; err == nil {
			success++
		} else if !strings.Contains(err.Error(), "active draft limit") {
			t.Fatal("unexpected quota race error", err)
		}
	}
	if success != 10 {
		t.Fatal("concurrent draft quota", success)
	}
	var draft string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.stock_batch_drafts WHERE owner_id=$1 ORDER BY id LIMIT 1", f.identity.UserID).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM pantry.stock_batch_drafts WHERE id=$1", draft); err == nil {
		t.Fatal("active nonexpired draft deleted")
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.stock_batch_drafts SET state='discarded' WHERE id=$1", draft); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM pantry.stock_batch_drafts WHERE id=$1", draft); err != nil {
		t.Fatal("discarded draft cannot be pruned", err)
	}
	if err := makeDraft(); err != nil {
		t.Fatal("discard did not release quota", err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
