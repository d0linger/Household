//go:build integration

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pantryFixture(t *testing.T, f *integrationFixture, suffix string) (store.Record, store.Record) {
	t.Helper()
	p, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Rice " + suffix, "unit_code": "kg"})
	l, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Shelf " + suffix})
	lot, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": p["id"], "location_id": l["id"], "received_on": "2026-01-01", "notes": "Lot " + suffix})
	return p, lot
}

func pantryJSON(t *testing.T, f *integrationFixture, path string, status int) map[string]json.RawMessage {
	t.Helper()
	response, body, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != status {
		t.Fatalf("pantry response %s: %v/%s/%v", path, response, body, err)
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPantryProjectionAtomicityAndPermissions(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "atomic")
	move := func(quantity string) store.Record {
		r, _ := auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": quantity, "reason": "adjust"})
		return r
	}
	first := move("999999999999.999999")
	move("999999999999.999999")
	move("-1") // Aggregate range is wider than one input movement.
	var amount string
	if err := f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot["id"]).Scan(&amount); err != nil || amount != "1999999999998.999998" {
		t.Fatalf("large exact balance: %s/%v", amount, err)
	}
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("stock_movements")
	if _, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(first["id"]), "reverse", value.UUID(), value.UUID(), auditMutation(t, f, "pantry", store.Record{}, 1)); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"UPDATE pantry.lot_balances SET quantity=0", "DELETE FROM pantry.lot_balances", "TRUNCATE pantry.lot_balances",
		"SELECT pantry.project_stock_movement()",
	} {
		if _, err := f.pool.Exec(f.ctx, sql); err == nil {
			t.Fatalf("runtime projection mutation allowed: %s", sql)
		}
	}
	if _, err := maintenance.PantryProjection(f.ctx, f.pool, true); err == nil {
		t.Fatal("runtime projection rebuild allowed")
	}
	before, err := maintenance.PantryProjection(f.ctx, f.pool, false)
	if err != nil || before != 0 {
		t.Fatalf("initial reconciliation: %d/%v", before, err)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,2,'receive',$3)", f.identity.HouseholdID, lot["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if drift, err := maintenance.PantryProjection(f.ctx, f.pool, false); err != nil || drift != 0 {
		t.Fatalf("rollback drift: %d/%v", drift, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.lot_balances SET quantity=0 WHERE lot_id=$1", lot["id"]); err != nil {
		t.Fatal(err)
	}
	if drift, err := maintenance.PantryProjection(f.ctx, f.pool, false); err != nil || drift != 1 {
		t.Fatalf("undetected owner corruption: %d/%v", drift, err)
	}
	if drift, err := maintenance.PantryProjection(f.ctx, f.owner, true); err != nil || drift != 0 {
		t.Fatalf("operator rebuild: %d/%v", drift, err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE pantry.stock_movements DISABLE TRIGGER pantry_project_stock"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("disabled projection trigger accepted by readiness")
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE pantry.stock_movements ENABLE TRIGGER pantry_project_stock"); err != nil {
		t.Fatal(err)
	}
}

func TestPantryConcurrentConsumption(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "concurrency")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "3", "reason": "receive"})
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("stock_movements")
	mutation := auditMutation(t, f, "pantry", store.Record{"lot_id": lot["id"], "quantity_delta": "-2", "reason": "consume"}, 0)
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := f.app.Store.Write(f.ctx, f.identity, m, e, "", value.UUID(), value.UUID(), mutation)
			results <- err
		}()
	}
	close(start)
	success := 0
	for range 2 {
		if <-results == nil {
			success++
		}
	}
	var amount string
	if err := f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot["id"]).Scan(&amount); err != nil || amount != "1.000000" || success != 1 {
		t.Fatalf("concurrent consumption: %s/%d/%v", amount, success, err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,-2,'consume',$3)", f.identity.HouseholdID, lot["id"], f.identity.UserID); err == nil {
		t.Fatal("direct SQL bypassed non-negative stock projection")
	}
	if drift, err := maintenance.PantryProjection(f.ctx, f.pool, false); err != nil || drift != 0 {
		t.Fatalf("concurrent drift: %d/%v", drift, err)
	}
}

func TestPantryRebuildWaitsForWriterBeforeJournalLock(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "rebuild-order")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "3", "reason": "receive"})
	config, err := pgxpool.ParseConfig(os.Getenv("HOUSEHOLD_TEST_OWNER_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	repairPool, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer repairPool.Close()
	var inherited string
	var repairPID int
	if err = repairPool.QueryRow(f.ctx, "SELECT current_setting('default_transaction_isolation'),pg_backend_pid()").Scan(&inherited, &repairPID); err != nil || inherited != "repeatable read" {
		t.Fatal("repair fixture did not install the alternative default", inherited, err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	writer, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(f.ctx) }()
	if _, err = writer.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(ctx, "SELECT id FROM pantry.lots WHERE id=$1 FOR UPDATE", lot["id"]); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := maintenance.PantryProjection(ctx, repairPool, true); done <- err }()
	blocked := false
	for range 100 {
		if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, repairPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("rebuild did not wait for active writer")
	}
	if _, err = writer.Exec(ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,-1,'consume',$3)", f.identity.HouseholdID, lot["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if drift, err := maintenance.PantryProjection(ctx, f.pool, false); err != nil || drift != 0 {
		t.Fatalf("post-writer rebuild: %d/%v", drift, err)
	}
	var quantity string
	var movements int64
	if err = f.pool.QueryRow(ctx, "SELECT quantity::text,movement_count FROM pantry.lot_balances WHERE lot_id=$1", lot["id"]).Scan(&quantity, &movements); err != nil || quantity != "2.000000" || movements != 2 {
		t.Fatal("repair lost the writer committed after its barrier read", quantity, movements, err)
	}
}

func TestPantryPagedTotalsAndHistory(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot := pantryFixture(t, f, "historical")
	for _, table := range []string{"products", "lots"} {
		if _, err := f.owner.Exec(f.ctx, "UPDATE pantry."+table+" SET created_at='2026-01-01T00:00:00Z' WHERE household_id=$1", f.identity.HouseholdID); err != nil {
			t.Fatal(err)
		}
	}
	for _, move := range []struct{ quantity, date, reason string }{
		{"3.000001", "2026-03-29T21:59:59Z", "receive"},
		{"-1.000001", "2026-03-29T22:00:00Z", "adjust"},
	} {
		if _, err := f.owner.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6::timestamptz)", f.identity.HouseholdID, lot["id"], move.quantity, move.reason, f.identity.UserID, move.date); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ query, stock, corrections string }{{"?at=2026-03-29", "3.000001", "1"}, {"?at=2026-03-30", "2.000000", "0"}, {"", "2.000000", "0"}} {
		data := pantryJSON(t, f, "/api/v1/pantry/stock"+tc.query, 200)
		var products []store.Record
		if err := json.Unmarshal(data["products"], &products); err != nil || len(products) != 1 || products[0]["stock"] != tc.stock || store.String(products[0]["later_corrections"]) != tc.corrections {
			t.Fatalf("historical boundary %s: %s/%v", tc.query, data["products"], err)
		}
		if tc.corrections == "1" {
			at, err := time.Parse(time.RFC3339, store.String(products[0]["latest_correction_at"]))
			if err != nil || at.UTC().Format(time.RFC3339) != "2026-03-29T22:00:00Z" {
				t.Fatalf("correction booking time missing: %v/%v", products[0], err)
			}
		}
		pantryJSON(t, f, "/api/v1/pantry/products/"+store.String(p["id"])+"/lots"+tc.query, 200)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO pantry.products(household_id,name,unit_code,created_by)
		SELECT $1,'Paged rice '||n,'kg',$2 FROM generate_series(1,106) n`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	first := pantryJSON(t, f, "/api/v1/pantry/stock", 200)
	if string(first["total"]) != "107" {
		t.Fatalf("total clipped: %s", first["total"])
	}
	seen := map[string]bool{}
	page := first
	for {
		var products []store.Record
		if err := json.Unmarshal(page["products"], &products); err != nil || len(products) > 50 {
			t.Fatalf("bounded products: %s/%v", page["products"], err)
		}
		for _, product := range products {
			id := store.String(product["id"])
			if seen[id] {
				t.Fatal("duplicate cursor product")
			}
			seen[id] = true
		}
		var next string
		if err := json.Unmarshal(page["next_cursor"], &next); err != nil {
			t.Fatal(err)
		}
		if next == "" {
			break
		}
		pantryJSON(t, f, "/api/v1/pantry/stock?q=other&cursor="+url.QueryEscape(next), 422)
		pantryJSON(t, f, "/api/v1/pantry/stock?at=2026-03-29&cursor="+url.QueryEscape(next), 422)
		page = pantryJSON(t, f, "/api/v1/pantry/stock?cursor="+url.QueryEscape(next), 200)
	}
	if len(seen) != 107 {
		t.Fatalf("lost products beyond 100: %d", len(seen))
	}
	// The old reports URL must expose the same explicit paged contract.
	report := pantryJSON(t, f, "/api/v1/m/pantry/reports/summary", 200)
	if string(report["total"]) != "107" {
		t.Fatal("report kept the previous 100-product ceiling")
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,notes,created_by)
	 SELECT $1,$2,$3,'2026-01-01','Empty lot '||n,$4 FROM generate_series(1,103) n`, f.identity.HouseholdID, p["id"], lot["location_id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	lotCount := 0
	lotCursor := ""
	for {
		lots := pantryJSON(t, f, "/api/v1/pantry/products/"+store.String(p["id"])+"/lots?cursor="+url.QueryEscape(lotCursor), 200)
		var records []store.Record
		if err := json.Unmarshal(lots["records"], &records); err != nil || len(records) > 50 {
			t.Fatal("unbounded lot page")
		}
		lotCount += len(records)
		if err := json.Unmarshal(lots["next_cursor"], &lotCursor); err != nil {
			t.Fatal(err)
		}
		if lotCursor == "" {
			break
		}
	}
	if lotCount != 104 {
		t.Fatalf("empty or later lots lost: %d", lotCount)
	}
	var totals []store.Record
	if err := json.Unmarshal(first["totals"], &totals); err != nil || len(totals) != 1 || totals[0]["quantity"] != "2.000000" {
		t.Fatalf("full-selection totals: %s/%v", first["totals"], err)
	}
	pantryJSON(t, f, "/api/v1/pantry/stock?at=2099-01-01", 422)
	pantryJSON(t, f, "/api/v1/pantry/products/"+value.UUID()+"/lots", 404)
	if err := f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	pantryJSON(t, f, "/api/v1/pantry/stock", 404)
	pantryJSON(t, f, "/api/v1/pantry/products/"+store.String(p["id"])+"/lots", 404)
}

func TestPantryPopulatedProjectionUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "core" {
			version = 8
		}
		if schema == "shopping" {
			version = 4 // MP17 Shopping receipts require Core21.
		}
		if schema == "pantry" {
			version = 1
		}
		if schema == "recipes" {
			version = 1 // Match the actual pre-projection core8 release.
		}
		if schema == "mealplan" {
			version = 2 // This fixture predates the Core reservation contract.
		}
		if schema == "expenses" {
			version = 3 // Expenses4 requires the later Core25 contract.
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hid, uid, pid, lid, lot := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Old stock')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'old-stock','Old stock','fixture')", []any{uid}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Rice','kg',$3)", []any{pid, hid, uid}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Shelf',$3)", []any{lid, hid, uid}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,created_by) VALUES($1,$2,$3,$4,'2026-01-01',$5)", []any{lot, hid, pid, lid, uid}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,999999999999.999999,'receive',$3),($1,$2,999999999999.999999,'receive',$3)", []any{hid, lot, uid}},
	} {
		if _, err := f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if drift, err := maintenance.PantryProjection(f.ctx, f.pool, false); err != nil || drift != 0 {
		t.Fatalf("upgraded projection: %d/%v", drift, err)
	}
	var amount string
	if err := f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot).Scan(&amount); err != nil || amount != "1999999999999.999998" {
		t.Fatalf("backfill lost precision: %s/%v", amount, err)
	}
}
