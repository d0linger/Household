//go:build integration

package web

import (
	"net/http"
	"net/url"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPantryBatchCountRejectsContradictoryUnit(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "explicit base unit")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "3", "reason": "receive"})
	line := pantryCountLine(t, f, store.String(lot["id"]), "1", "Physical count")
	line.Unit = "g" // The selected product's immutable base is kg.
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	if _, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("contradictory count unit booked as base units")
	}
	var dirty bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pantry.stock_batch_receipts) OR (SELECT quantity FROM pantry.lot_balances WHERE lot_id=$1)<>3 OR EXISTS(SELECT 1 FROM pantry.stock_batch_drafts WHERE state<>'active')", lot["id"]).Scan(&dirty); err != nil || dirty {
		t.Fatal("invalid unit left booking effects", dirty, err)
	}
}

func TestPantryBatchInventoryPaginationIncludesZeroAndBindsScope(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "inventory paging")
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,notes,created_by)
 SELECT $1,$2,$3,'2026-01-01','Zero lot '||n,$4 FROM generate_series(1,51) n`, f.identity.HouseholdID, product["id"], lot["location_id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/pantry/batches/inventory-lots?location=" + store.String(lot["location_id"])
	get := func(path string, want int) store.Record {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != want {
			t.Fatal(response, string(raw), err)
		}
		record, err := store.DecodeRecord(raw)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	first := get(path, 200)
	if len(first["records"].([]any)) != 50 || first["partial"] != true || store.String(first["next_cursor"]) == "" {
		t.Fatal("zero-inclusive first page", first)
	}
	rows := first["records"].([]any)
	anchor := rows[len(rows)-1].(map[string]any)
	if store.String(anchor["stock"]) != "0" {
		t.Fatal("missing projection did not represent zero", anchor)
	}
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM pantry.lots WHERE id=$1", anchor["id"]); err != nil {
		t.Fatal(err)
	}
	cursor := url.QueryEscape(store.String(first["next_cursor"]))
	second := get(path+"&cursor="+cursor, 200)
	if len(second["records"].([]any)) != 2 {
		t.Fatal("deleted anchor invalidated remaining page", second)
	}
	get(path+"&cursor="+cursor+"&q=changed", 422)
	get(path+"&cursor="+cursor+"&archived=1", 422)
	get("/api/v1/pantry/batches/inventory-lots?location="+value.UUID()+"&cursor="+cursor, 422)
	other := shoppingTestMember(t, f, "inventory-cursor-editor", "editor")
	otherToken, _, err := f.app.Auth.Login(f.ctx, other.Username, integrationPassword, "", "127.0.0.1", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	response, raw, err := f.request(http.MethodGet, path+"&cursor="+cursor, otherToken, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 {
		t.Fatal("actor-reused signed cursor", response, string(raw), err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET archived_at=clock_timestamp() WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	if active := get(path, 200); len(active["records"].([]any)) != 0 {
		t.Fatal("archived inventory was silently included", active)
	}
	if archived := get(path+"&archived=1", 200); len(archived["records"].([]any)) != 50 {
		t.Fatal("explicit archived stock was hidden", archived)
	}
}
