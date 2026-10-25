//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func replenishmentFixture(t *testing.T, f *integrationFixture) (store.Record, store.Record, store.Record, store.Record) {
	t.Helper()
	p, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Replenishment rice", "unit_code": "kg", "low_stock_threshold": "1", "target_stock": "3"})
	loc, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Replenishment shelf"})
	lot, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": p["id"], "location_id": loc["id"], "received_on": "2026-10-25", "notes": "Replenishment lot"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "0.25", "reason": "receive"})
	pack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": p["id"], "label": "500 g bag", "quantity": "500", "unit_code": "g"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Reviewed replenishment"})
	return p, lot, pack, list
}

func replenishmentInput(t *testing.T, f *integrationFixture, pid, pack, list string) store.Mutation {
	t.Helper()
	view, err := f.app.Store.PantryReplenishment(f.ctx, f.identity, pid, pack)
	if err != nil {
		t.Fatal(err)
	}
	in := auditMutation(t, f, "pantry", store.Record{"reviewed": true, "list_id": list, "balance": view.Balance,
		"movement_count": view.MovementCount, "pack_id": pack, "pack_version": view.PackVersion, "quantity": view.Suggestion.Quantity}, store.Int(view.Product["version"]))
	in.TargetGenerations = map[string]int64{"shopping": f.app.Store.States()["shopping"].Generation}
	return in
}

func TestPantryReplenishmentConfigurationAndHTTP(t *testing.T) {
	f := newIntegrationFixture(t)
	p, _, pack, list := replenishmentFixture(t, f)
	pid := store.String(p["id"])
	var targetAudited bool
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'
 AND (changes->'target_stock'->>'after')::numeric=3)`, pid).Scan(&targetAudited); err != nil || !targetAudited {
		t.Fatal("configured target was not present in its safe audit diff", targetAudited, err)
	}
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("products")
	for _, fields := range []store.Record{{"target_stock": "0.5"}, {"low_stock_threshold": nil}, {"target_stock": "-1"}, {"integer_quantities": true, "target_stock": "3.5"}} {
		if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, pid, value.UUID(), value.UUID(), auditMutation(t, f, "pantry", fields, 1)); err == nil {
			t.Fatal("invalid target accepted", fields)
		}
	}
	for _, sql := range []string{
		"UPDATE pantry.products SET target_stock=0.5 WHERE id=$1",
		"UPDATE pantry.products SET low_stock_threshold=NULL WHERE id=$1",
		"UPDATE pantry.products SET target_stock='NaN' WHERE id=$1",
		"UPDATE pantry.products SET integer_quantities=true,target_stock=3.5 WHERE id=$1",
	} {
		if _, err := f.pool.Exec(f.ctx, sql, pid); err == nil {
			t.Fatal("runtime SQL bypassed target constraint", sql)
		}
	}
	path := "/api/v1/pantry/products/" + pid + "/replenish?pack=" + store.String(pack["id"])
	response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
	var view store.PantryReplenishmentView
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &view) != nil || view.Suggestion.Quantity != "3.000000" || view.Suggestion.PackCount != "6" {
		t.Fatal("real exact preview", response, string(raw), err)
	}
	response, _, err = f.request(http.MethodGet, path, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("anonymous preview accepted", response, err)
	}
	reader := shoppingTestMember(t, f, "replenishment-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, "replenishment-reader", integrationPassword, "", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	response, raw, err = f.request(http.MethodGet, "/m/pantry/replenish/"+pid, readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || strings.Contains(string(raw), `id="replenish-shopping"`) {
		t.Fatal("reader surface missing or offers mutation", response, err)
	}
	in := replenishmentInput(t, f, pid, store.String(pack["id"]), store.String(list["id"]))
	if _, err = f.app.Store.Action(f.ctx, reader, m, e, pid, "shopping", value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader purchase accepted", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PantryReplenishment(f.ctx, foreign, pid, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign product disclosed", err)
	}
	payload, _ := json.Marshal(in)
	response, _, err = f.request(http.MethodPost, "/api/v1/m/pantry/products/"+pid+"/actions/shopping", f.token, "invalid", "application/json", bytes.NewReader(payload), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("purchase bypassed CSRF", response, err)
	}
	response, raw, err = f.request(http.MethodPost, "/api/v1/m/pantry/products/"+pid+"/actions/shopping", f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(payload), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), `"quantity":"3.000000"`) {
		t.Fatal("authenticated purchase failed", response, string(raw), err)
	}
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE"} {
		// These names are a compiled test allowlist, never request input.
		if _, err = f.owner.Exec(f.ctx, "REVOKE "+privilege+" ON pantry.products FROM household_app"); err != nil {
			t.Fatal(err)
		}
		if err = database.Ready(f.ctx, f.pool); err == nil {
			t.Fatal("readiness accepted a missing product privilege", privilege)
		}
		if _, err = f.owner.Exec(f.ctx, "GRANT "+privilege+" ON pantry.products TO household_app"); err != nil {
			t.Fatal(err)
		}
		if err = database.Ready(f.ctx, f.pool); err != nil {
			t.Fatal("restored product privilege is not ready", privilege, err)
		}
	}
	legacy, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Whole pieces with legacy fractional minimum", "unit_code": "piece", "integer_quantities": true, "low_stock_threshold": "0.5"})
	manual := auditMutation(t, f, "pantry", store.Record{"list_id": list["id"], "quantity": "1"}, 1)
	manual.TargetGenerations = in.TargetGenerations
	if result, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(legacy["id"]), "shopping", value.UUID(), value.UUID(), manual); err != nil || result["quantity"] != "1.000000" {
		t.Fatal("valid manual legacy quantity depends on an unusable automatic suggestion", result, err)
	}
}

func TestPantryReplenishmentReplayAndStaleBasis(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot, pack, list := replenishmentFixture(t, f)
	pid, packID, listID := store.String(p["id"]), store.String(pack["id"]), store.String(list["id"])
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("products")
	in := replenishmentInput(t, f, pid, packID, listID)
	post := func(key string, input store.Mutation) (store.Record, error) {
		return f.app.Store.Action(f.ctx, f.identity, m, e, pid, "shopping", key, value.UUID(), input)
	}
	for n, target := range []string{"4", "3"} {
		if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, pid, value.UUID(), value.UUID(), auditMutation(t, f, "pantry", store.Record{"target_stock": target}, int64(n+1))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := post(value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed-then-restored target version accepted", err)
	}
	in = replenishmentInput(t, f, pid, packID, listID)
	for _, amount := range []string{"0.1", "-0.1"} {
		auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": amount, "reason": "adjust"})
	}
	if _, err := post(value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same-balance intervening journal accepted", err)
	}
	in = replenishmentInput(t, f, pid, packID, listID)
	pe, _ := m.Entity("packs")
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, pe, packID, value.UUID(), value.UUID(), auditMutation(t, f, "pantry", store.Record{"quantity": "750"}, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := post(value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale pack accepted", err)
	}
	in = replenishmentInput(t, f, pid, packID, listID)
	stale := in
	stale.TargetGenerations = map[string]int64{"shopping": in.TargetGenerations["shopping"] + 1}
	if _, err := post(value.UUID(), stale); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale destination generation accepted", err)
	}
	key := value.UUID()
	result, err := post(key, in)
	if err != nil || result["quantity"] != "3.000000" {
		t.Fatal(result, err)
	}
	// A committed response can be recovered without recalculating a later stock.
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "1", "reason": "receive"})
	prior, err := post(key, in)
	if err != nil || prior["item_id"] != result["item_id"] {
		t.Fatal("committed response replay failed", prior, err)
	}
	changed := in
	changed.Data = map[string]json.RawMessage{}
	for k, v := range in.Data {
		changed.Data[k] = v
	}
	changed.Data["quantity"] = json.RawMessage(`"9"`)
	if _, err = post(key, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed idempotent payload accepted", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE list_id=$1", listID).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated shopping", count, err)
	}
}

func TestPantryReplenishmentFullPhysicalAggregateAndModuleBoundary(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot, pack, _ := replenishmentFixture(t, f)
	pid := store.String(p["id"])
	if _, err := f.owner.Exec(f.ctx, `WITH lots AS (
 INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,notes,created_by)
 SELECT $1,$2,$3,'2026-10-25','Aggregate lot '||n,$4 FROM generate_series(1,54) n RETURNING id)
 INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by)
 SELECT $1,id,0.000001,'receive',$4 FROM lots`, f.identity.HouseholdID, pid, lot["location_id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE pantry.locations SET archived_at=now() WHERE id=$1", lot["location_id"]); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["shopping"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PantryReplenishment(f.ctx, f.identity, pid, store.String(pack["id"]))
	if err != nil || view.Balance != "0.250054" || view.MovementCount != "55" || view.Suggestion.Shortage != "2.749946" || view.Suggestion.PackCount != "6" {
		t.Fatal("preview truncated, omitted archived physical stock or requires shopping", view, err)
	}
	other, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Other product", "unit_code": "kg"})
	foreignPack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": other["id"], "label": "Other pack", "quantity": "1", "unit_code": "kg"})
	if _, err = f.app.Store.PantryReplenishment(f.ctx, f.identity, pid, store.String(foreignPack["id"])); err == nil {
		t.Fatal("foreign product pack selected")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.products SET archived_at=now() WHERE id=$1", pid); err != nil {
		t.Fatal(err)
	}
	view, err = f.app.Store.PantryReplenishment(f.ctx, f.identity, pid, "")
	if err != nil || view.Suggestion.Suggested || view.Suggestion.Reason != "product_archived" {
		t.Fatal("archived product suggested", view, err)
	}
}

func TestPantryReplenishmentLocksAndLateRollback(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot, pack, list := replenishmentFixture(t, f)
	pid := store.String(p["id"])
	in := replenishmentInput(t, f, pid, store.String(pack["id"]), store.String(list["id"]))
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("products")
	key, requestID := value.UUID(), value.UUID()
	post := func() (store.Record, error) {
		return f.app.Store.Action(f.ctx, f.identity, m, e, pid, "shopping", key, requestID, in)
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,0.1,'receive',$3)", f.identity.HouseholdID, lot["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = post(); !errors.Is(err, store.ErrConflict) {
		t.Fatal("uncommitted writer did not reject the review promptly", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.outbox_events ADD CONSTRAINT replenish_late_failure CHECK(module_id<>'shopping') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err = post(); err == nil {
		t.Fatal("controlled late failure accepted")
	}
	var dirty int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM shopping.items WHERE list_id=$1)
 +(SELECT count(*) FROM core.audit_log WHERE request_id=$2)+(SELECT count(*) FROM core.idempotency_records WHERE key=$3)`, list["id"], requestID, key).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatal("partial late-write effects escaped rollback", dirty, err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.outbox_events DROP CONSTRAINT replenish_late_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err = post(); err != nil {
		t.Fatal("clean retry after rollback", err)
	}
}

func TestPantryReplenishmentPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		if schema == "core" {
			version = 17
		}
		if schema == "pantry" {
			version = 5
		}
		if schema == "mealplan" {
			version = 2 // This fixture predates the Core reservation contract.
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, product, location, lot := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior target fixture')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-target','Prior','fixture-no-login')", []any{user}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,low_stock_threshold,created_by) VALUES($1,$2,'Legacy target','kg',0.25,$3)", []any{product, hh, user}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Prior place',$3)", []any{location, hh, user}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by) VALUES($1,$2,$3,$4,'2026-10-25','Prior lot',$5)", []any{lot, hh, product, location, user}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,0.125001,'receive',$3)", []any{hh, lot, user}},
	} {
		if _, err := f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var unchanged bool
	if err := f.pool.QueryRow(f.ctx, `SELECT p.target_stock IS NULL AND p.low_stock_threshold=0.25 AND b.quantity=0.125001 AND b.movement_count=1
 FROM pantry.products p JOIN pantry.lots l ON l.product_id=p.id JOIN pantry.lot_balances b ON b.lot_id=l.id WHERE p.id=$1`, product).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("upgrade invented a target or changed stock", unchanged, err)
	}
	if _, err := f.owner.Exec(f.ctx, "ALTER TABLE pantry.products DROP CONSTRAINT pantry_target_stock_valid"); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("readiness accepted missing target constraint")
	}
}

func TestPantryReplenishmentDestinationLifecycle(t *testing.T) {
	f := newIntegrationFixture(t)
	p, lot, pack, list := replenishmentFixture(t, f)
	pid, packID := store.String(p["id"]), store.String(pack["id"])
	in := replenishmentInput(t, f, pid, packID, store.String(list["id"]))
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("products")
	key := value.UUID()
	post := func(operation string, input store.Mutation) (store.Record, error) {
		return f.app.Store.Action(f.ctx, f.identity, m, e, pid, "shopping", operation, value.UUID(), input)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.packs SET disabled=true WHERE id=$1", packID); err != nil {
		t.Fatal(err)
	}
	if _, err := post(key, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("disabled reviewed pack accepted", err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.packs SET disabled=false WHERE id=$1", packID); err != nil {
		t.Fatal(err)
	}
	in = replenishmentInput(t, f, pid, packID, store.String(list["id"]))
	if _, err := post(key, in); err != nil {
		t.Fatal(err)
	}
	if _, err := post(value.UUID(), in); err == nil {
		t.Fatal("another key duplicated an already open product")
	}
	state := f.app.Store.States()["shopping"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err := post(key, in); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("replay ignored a disabled destination", err)
	}
	if _, err := f.app.Store.PantryReplenishment(f.ctx, f.identity, pid, packID); err != nil {
		t.Fatal("read-only preview requires Shopping", err)
	}
	// Owner-only synthetic backup metadata authorizes a real disposable purge;
	// encrypted file/data restore is exercised separately by test-recovery.ps1.
	backup, purge := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'shopping',$3,$4,$5)", purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "shopping", true, f.app.Store.States()["shopping"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err := post(key, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old destination replay survived an actual Shopping purge", err)
	}
	newList, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Recreated destination"})
	fresh := replenishmentInput(t, f, pid, packID, store.String(newList["id"]))
	if _, err := post(value.UUID(), fresh); err != nil {
		t.Fatal("fresh generation could not accept explicit new review", err)
	}
	view, err := f.app.Store.PantryLotState(f.ctx, f.identity, store.String(lot["id"]))
	if err != nil || view["stock"] != "0.250000" || store.Int(view["movement_count"]) != 1 {
		t.Fatal("shopping lifecycle implicitly changed physical stock", view, err)
	}
}
