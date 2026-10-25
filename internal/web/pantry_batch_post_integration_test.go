//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
)

func savePantryBatch(t *testing.T, f *integrationFixture, input store.PantryBatchDraftInput) (string, store.PantryBatchPostInput) {
	t.Helper()
	draft := value.UUID()
	input.Generation = f.app.Store.States()["pantry"].Generation
	saved, err := f.app.Store.SavePantryBatchDraft(f.ctx, f.identity, draft, value.UUID(), value.UUID(), input)
	if err != nil {
		t.Fatal("save batch draft", err)
	}
	return draft, store.PantryBatchPostInput{Generation: input.Generation, Version: store.Int(saved["version"]), Fingerprint: store.String(saved["fingerprint"])}
}

func pantryCountLine(t *testing.T, f *integrationFixture, lid, quantity, reason string) store.PantryBatchLine {
	t.Helper()
	lot, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil {
		t.Fatal(err)
	}
	return store.PantryBatchLine{ID: value.UUID(), ProductID: store.String(lot["product_id"]), ProductVersion: store.Int(lot["product_version"]),
		LocationID: store.String(lot["location_id"]), LocationVersion: store.Int(lot["location_version"]), LotID: lid, LotVersion: store.Int(lot["version"]),
		MovementCount: store.Int(lot["movement_count"]), Balance: store.String(lot["stock"]), Quantity: quantity, Reason: reason}
}

func batchResultLines(t *testing.T, record store.Record) []map[string]any {
	t.Helper()
	// Normalize both freshly built []Record and JSON-decoded replay records.
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := store.DecodeRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	result := []map[string]any{}
	for _, item := range decoded["lines"].([]any) {
		result = append(result, item.(map[string]any))
	}
	return result
}

func TestPantryBatchMixedIntakeExactAndDurableReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "intake A")
	other, otherLot := pantryFixture(t, f, "intake B")
	pack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": product["id"], "label": "500 g bag", "quantity": "500", "unit_code": "g"})
	line := func(p, l store.Record, amount, label string) store.PantryBatchLine {
		return store.PantryBatchLine{ID: value.UUID(), ProductID: store.String(p["id"]), ProductVersion: store.Int(p["version"]),
			LocationID: store.String(l["location_id"]), LocationVersion: 1, Quantity: amount, Unit: "kg", ReceivedOn: "2026-10-25", Label: label}
	}
	first := line(product, lot, "0.5", "Half pack")
	first.Unit, first.PackID, first.PackVersion = "", store.String(pack["id"]), store.Int(pack["version"])
	days := int64(3)
	first.OpenedOn, first.AfterOpenDays, first.AfterOpenSource = "2026-10-25", &days, "manufacturer"
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "intake", Title: "Explicit mixed intake", Lines: []store.PantryBatchLine{first, line(other, otherLot, "0.000001", "Tiny exact lot")}})
	key := value.UUID()
	result, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, key, value.UUID(), in)
	if err != nil || store.Int(result["movement_count"]) != 2 {
		t.Fatal("mixed intake", result, err)
	}
	lines := batchResultLines(t, result)
	if lines[0]["counted"] != "0.250000" || lines[1]["delta"] != "0.000001" || lines[0]["pack"] == nil {
		t.Fatal("intake rounded or lost exact pack provenance", lines)
	}
	for _, item := range lines {
		state, stateErr := f.app.Store.PantryLotState(f.ctx, f.identity, store.String(item["lot_id"]))
		if stateErr != nil || state["stock"] != item["counted"] || store.Int(state["movement_count"]) != 1 {
			t.Fatal("intake projection", state, item, stateErr)
		}
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	for _, retryKey := range []string{key, value.UUID()} {
		prior, retryErr := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, retryKey, value.UUID(), in)
		if retryErr != nil || prior["id"] != result["id"] {
			t.Fatal("same-draft durable replay", prior, retryErr)
		}
	}
	in.Fingerprint = strings.Repeat("a", 64)
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed final request reused receipt", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_movements").Scan(&count); err != nil || count != 2 {
		t.Fatal("replay duplicated stock", count, err)
	}
}

func TestPantryBatchInventorySelectedZeroAndArchived(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "inventory")
	second, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": lot["location_id"], "received_on": "2026-01-01", "notes": "Positive count"})
	third, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": lot["location_id"], "received_on": "2026-01-01", "notes": "Unselected lot"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "3"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": third["id"], "reason": "receive", "quantity_delta": "7"})
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.products SET archived_at=clock_timestamp(),version=version+1 WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	zero := pantryCountLine(t, f, store.String(lot["id"]), "0", "Counted empty")
	positive := pantryCountLine(t, f, store.String(second["id"]), "1.250001", "Found physical stock")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: zero.LocationID, Lines: []store.PantryBatchLine{zero, positive}})
	result, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
	if err != nil || store.Int(result["movement_count"]) != 2 {
		t.Fatal("archived selected inventory", result, err)
	}
	lines := batchResultLines(t, result)
	if lines[0]["counted"] != "0.000000" || lines[0]["delta"] != "-3.000000" || lines[0]["product_archived"] != true || lines[1]["delta"] != "1.250001" {
		t.Fatal("absolute count effects", lines)
	}
	unselected, err := f.app.Store.PantryLotState(f.ctx, f.identity, store.String(third["id"]))
	if err != nil || unselected["stock"] != "7.000000" || unselected["product_archived"] != true {
		t.Fatal("unselected lot changed or archived product reactivated", unselected, err)
	}
	same := pantryCountLine(t, f, store.String(lot["id"]), "0", "")
	draft, in = savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: same.LocationID, Lines: []store.PantryBatchLine{same}})
	result, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
	if err != nil || store.Int(result["movement_count"]) != 0 || batchResultLines(t, result)[0]["movement_id"] != "" {
		t.Fatal("unchanged count created zero movement", result, err)
	}
}

func TestPantryBatchSameBalanceInterventionAndAtomicRollback(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "batch rollback")
	second, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": lot["location_id"], "received_on": "2026-01-01", "notes": "Second lot"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "3"})
	first := pantryCountLine(t, f, store.String(lot["id"]), "2", "Reviewed count")
	last := pantryCountLine(t, f, store.String(second["id"]), "1", "force second effect failure")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: first.LocationID, Lines: []store.PantryBatchLine{first, last}})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "adjust", "quantity_delta": "0.1"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "reason": "adjust", "quantity_delta": "-0.1"})
	_, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
	var conflict store.PantryStockConflict
	if !errors.As(err, &conflict) || store.String(conflict.Current["stock"]) != "3.000000" {
		t.Fatal("same-balance intervening movements were ignored", err, conflict.Current, first)
	}
	first = pantryCountLine(t, f, store.String(lot["id"]), "2", "Reviewed count")
	draft, in = savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: first.LocationID, Lines: []store.PantryBatchLine{first, last}})
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION pantry.fail_second_batch_test() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN
 IF NEW.note='force second effect failure' THEN RAISE EXCEPTION 'controlled second batch failure' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_second_batch_test BEFORE INSERT ON pantry.stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.fail_second_batch_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); err == nil || !strings.Contains(err.Error(), "controlled second batch failure") {
		t.Fatal("expected real second-effect failure", err)
	}
	var dirty bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pantry.stock_batch_receipts) OR EXISTS(SELECT 1 FROM pantry.stock_batch_lines)
 OR EXISTS(SELECT 1 FROM pantry.stock_batch_drafts WHERE state<>'active') OR (SELECT quantity FROM pantry.lot_balances WHERE lot_id=$1)<>3
 OR (SELECT coalesce(sum(quantity_delta),0) FROM pantry.stock_movements WHERE lot_id=$2)<>0`, lot["id"], second["id"]).Scan(&dirty); err != nil || dirty {
		t.Fatal("failed batch left partial effects", dirty, err)
	}
}

func TestPantryBatchParallelFinalizeAndVersionGuard(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "parallel count")
	line := pantryCountLine(t, f, store.String(lot["id"]), "0", "")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	changed := in
	changed.Version++
	if _, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("wrong reviewed draft version accepted", err)
	}
	type response struct {
		record store.Record
		err    error
	}
	done := make(chan response, 2)
	for range 2 {
		go func() {
			r, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
			done <- response{r, err}
		}()
	}
	accepted := 0
	for range 2 {
		r := <-done
		if r.err == nil {
			accepted++
		} else if !errors.Is(r.err, store.ErrConflict) {
			t.Fatal("unexpected concurrent finalization error", r.err)
		}
	}
	var receipts int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_batch_receipts").Scan(&receipts); err != nil || receipts != 1 || accepted < 1 {
		t.Fatal("parallel completion duplicated or lost receipt", receipts, accepted, err)
	}
}

func TestPantryBatchSharesLocksWithQuickConsumptionAndJournal(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "shared writer lock")
	lid := store.String(lot["id"])
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "quantity_delta": "3", "reason": "receive"})
	line := pantryCountLine(t, f, lid, "1", "Physical count")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	quick := pantryStockInput(t, f, lid)
	done, start := make(chan error, 2), make(chan struct{})
	go func() {
		<-start
		_, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
		done <- err
	}()
	go func() {
		<-start
		_, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), quick)
		done <- err
	}()
	close(start)
	accepted := 0
	for range 2 {
		if err := <-done; err == nil {
			accepted++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal("unexpected shared-writer failure", err)
		}
	}
	state, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil || accepted != 1 || store.Int(state["movement_count"]) != 2 || state["stock"] != "1.000000" && state["stock"] != "2.000000" {
		t.Fatal("concurrent count/quick consumption lost or duplicated effects", accepted, state, err)
	}
	line = pantryCountLine(t, f, lid, "0", "Explicit recount")
	draft, in = savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	tx, err := audit.Begin(f.ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,-0.000001,'adjust',$3)", f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("inventory bypassed the journal writer's lot lock", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("rolled-back journal writer left count blocked", err)
	}
}
