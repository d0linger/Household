//go:build integration

package web

import (
	"errors"
	"os"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"
)

func TestPantryBatchHousekeepingIsBoundedAndPreservesPostedDraft(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "housekeeping")
	line := pantryCountLine(t, f, store.String(lot["id"]), "0", "")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	if _, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	active, _ := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "intake"})
	// Controlled owner-only expiry fixture; runtime cannot forge timestamps or
	// exceed the active quota through this setup path.
	tx, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "ALTER TABLE pantry.stock_batch_drafts DISABLE TRIGGER pantry_batch_draft_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO pantry.stock_batch_drafts(household_id,owner_id,generation,kind,payload,expires_at)
 SELECT $1,$2,$3,'intake','{"lines":[]}',clock_timestamp()-interval '1 day' FROM generate_series(1,101)`, f.identity.HouseholdID, f.identity.UserID, in.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "ALTER TABLE pantry.stock_batch_drafts ENABLE TRIGGER pantry_batch_draft_guard"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var remaining, protected int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FILTER(WHERE state='active' AND expires_at<clock_timestamp()),count(*) FILTER(WHERE id IN($1::uuid,$2::uuid)) FROM pantry.stock_batch_drafts", draft, active).Scan(&remaining, &protected); err != nil || remaining != 1 || protected != 2 {
		t.Fatal("housekeeping removed unbounded or protected drafts", remaining, protected, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestPantryBatchPopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		v := uint(database.ExpectedVersion(schema))
		if schema == "documents" {
			v = 1 // Documents2 requires the later complete Core33 predecessor.
		}
		if schema == "meters" {
			v = 1 // Meters2 requires the later complete Core32 predecessor.
		}
		if schema == "chores" {
			v = 1 // Chores2 requires the later complete Core31 predecessor.
		}
		if schema == "recipes" {
			v = 4 // Recipes5 belongs to the later Core27 expansion.
		}
		if schema == "fuel" {
			v = 2 // Keep the actual predecessor compatible with Core16.
		}
		if schema == "core" {
			v = 16
		}
		if schema == "shopping" {
			v = 4 // MP17 Shopping receipts require Core21.
		}
		if schema == "pantry" {
			v = 4
		}
		if schema == "mealplan" {
			v = 2 // This fixture predates the Core reservation contract.
		}
		if schema == "expenses" {
			v = 3 // Expenses4 requires the later Core25 contract.
		}
		migratePriorAuditSchema(t, f, schema, v)
	}
	hh, user, product, location, lot := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	tx, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior stock-batch release')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-stock-batch','Prior user','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hh, user}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES($1,$2,'Prior exact pantry','kg',$3)", []any{product, hh, user}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Prior shelf',$3)", []any{location, hh, user}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by) VALUES($1,$2,$3,$4,'2026-10-25','Prior lot',$5)", []any{lot, hh, product, location, user}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,9007199254.740993,'receive',$3)", []any{hh, lot, user}},
	} {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var stock string
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot).Scan(&stock); err != nil || stock != "9007199254.740993" {
		t.Fatal("batch upgrade changed prior exact stock", stock, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM pantry.stock_batch_drafts)+(SELECT count(*) FROM pantry.stock_batch_receipts)+(SELECT count(*) FROM pantry.stock_batch_lines)").Scan(&count); err != nil || count != 0 {
		t.Fatal("upgrade synthesized draft or stock completion", count, err)
	}
}

func TestPantryBatchRealPurgeRejectsOldDraftAndReceipt(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "purge")
	line := pantryCountLine(t, f, store.String(lot["id"]), "0", "")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	posted, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'pantry',$3,$4,$5)", purge, f.identity.HouseholdID, in.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old generation batch replay accepted after real purge", err)
	}
	if _, err = f.app.Store.PantryBatchReceipt(f.ctx, f.identity, store.String(posted["id"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("purged receipt survived local history purge", err)
	}
	in.Generation = f.app.Store.States()["pantry"].Generation
	if _, err = f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("new generation revived removed draft", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.stock_movements").Scan(&count); err != nil || count != 0 {
		t.Fatal("purge retry recreated stock", count, err)
	}
}
