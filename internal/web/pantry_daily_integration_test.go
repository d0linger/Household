//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func pantryStockInput(t *testing.T, f *integrationFixture, lid string) store.PantryStockInput {
	t.Helper()
	lot, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil {
		t.Fatal(err)
	}
	return store.PantryStockInput{Generation: f.app.Store.States()["pantry"].Generation, Action: "consume", Mode: "amount", Quantity: "1",
		LotVersion: store.Int(lot["version"]), ProductVersion: store.Int(lot["product_version"]), LocationVersion: store.Int(lot["location_version"]),
		MovementCount: store.Int(lot["movement_count"]), Balance: store.String(lot["stock"])}
}

func TestPantryDailyHTTPAndRollbackGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "HTTP guards")
	lid := store.String(lot["id"])
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "3"})
	alias, _ := auditCreate(t, f, "pantry", "barcodes", store.Record{"product_id": product["id"], "code": "0012345678905"})
	in := pantryStockInput(t, f, lid)
	url := "/api/v1/pantry/lots/" + lid + "/consume"
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, tc := range []struct {
		name, method, path, token, csrf, body string
		want                                  int
	}{
		{"anonymous", http.MethodGet, url, "", "", "", 401},
		{"csrf", http.MethodPost, url, f.token, "", "{}", 403},
		{"unknown field", http.MethodPost, url, f.token, csrf, `{"administrator":true}`, 422},
		{"wrong scalar", http.MethodPost, url, f.token, csrf, `{"movement_count":"1"}`, 422},
		{"review", http.MethodGet, url, f.token, "", "", 200},
		{"barcode", http.MethodGet, "/api/v1/pantry/lookup?code=" + store.String(alias["code"]), f.token, "", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, raw, err := f.request(tc.method, tc.path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || r.StatusCode != tc.want {
				t.Fatal(r, string(raw), err)
			}
		})
	}
	in.Quantity = "4"
	if _, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("overconsumption accepted")
	}
	var dirty bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pantry.stock_operations) OR (SELECT quantity FROM pantry.lot_balances WHERE lot_id=$1)<>3", lid).Scan(&dirty); err != nil || dirty {
		t.Fatal("rejected consumption wrote effects", dirty, err)
	}
	in.Quantity = "1"
	encoded, _ := json.Marshal(in)
	shoppingTestMember(t, f, "pantry-http-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, "pantry-http-reader", integrationPassword, "", "127.0.0.1", "stock-reader-http")
	if err != nil {
		t.Fatal(err)
	}
	readerResponse, readerRaw, err := f.request(http.MethodPost, url, readerToken, f.app.Auth.CSRF(readerToken, "session"), "application/json", strings.NewReader(string(encoded)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || readerResponse.StatusCode != 403 {
		t.Fatal("HTTP reader wrote stock", readerResponse, string(readerRaw), err)
	}
	response, raw, err := f.request(http.MethodPost, url, f.token, csrf, "application/json", strings.NewReader(string(encoded)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("actual HTTP consume", response, string(raw), err)
	}
	result, err := store.DecodeRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	response, raw, err = f.request(http.MethodGet, "/api/v1/pantry/operations/"+store.String(result["id"]), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatal("operation read", response, string(raw), err)
	}
	response, raw, err = f.request(http.MethodPost, url, f.token, csrf, "application/json", strings.NewReader(string(encoded)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 409 || !strings.Contains(string(raw), `"stock":"2.000000"`) {
		t.Fatal("HTTP conflict omitted current stock", response, string(raw), err)
	}
	if _, err = f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,reverses_movement_id,created_by) VALUES($1,$2,0.5,'reversal',$3,$4)", f.identity.HouseholdID, lid, result["movement_id"], f.identity.UserID); err == nil {
		t.Fatal("direct reversal amount mismatch accepted")
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	response, raw, err = f.request(http.MethodGet, url, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("disabled pantry disclosed review", response, string(raw), err)
	}
}

func TestPantryDailyPriorityPaginationAndDateKinds(t *testing.T) {
	f := newIntegrationFixture(t)
	product, first := pantryFixture(t, f, "priority")
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.lots SET best_before_on='2026-10-23' WHERE id=$1", first["id"]); err != nil {
		t.Fatal(err)
	}
	for _, fields := range []store.Record{
		{"notes": "Use-by lot", "use_by_on": "2026-10-24"},
		{"notes": "Opening hint lot", "opened_on": "2026-10-25", "after_open_days": "0", "after_open_source": "user"},
	} {
		fields["product_id"], fields["location_id"], fields["received_on"] = product["id"], first["location_id"], "2026-10-01"
		auditCreate(t, f, "pantry", "lots", fields)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.lots(household_id,product_id,location_id,received_on,notes,created_by)
 SELECT $1,$2,$3,'2026-10-01','Undated candidate '||g,$4 FROM generate_series(1,51) g`, f.identity.HouseholdID, product["id"], first["location_id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) SELECT household_id,id,1,'receive',$3 FROM pantry.lots WHERE household_id=$1 AND product_id=$2", f.identity.HouseholdID, product["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Records []store.Record `json:"records"`
		Next    string         `json:"next_cursor"`
	}
	load := func(path string) page {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 {
			t.Fatal(response, string(raw), err)
		}
		var result page
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	base := "/api/v1/pantry/priority?product=" + store.String(product["id"])
	firstPage := load(base)
	if len(firstPage.Records) != 50 || firstPage.Next == "" {
		t.Fatal("priority sentinel", len(firstPage.Records), firstPage.Next)
	}
	for i, kind := range []string{"best_before", "use_by", "opening_hint", "unknown"} {
		if firstPage.Records[i]["priority_kind"] != kind {
			t.Fatal("date kind or ordering lost", i, firstPage.Records[i])
		}
	}
	secondPage := load(base + "&cursor=" + url.QueryEscape(firstPage.Next))
	if len(secondPage.Records) != 4 || secondPage.Next != "" {
		t.Fatal("unknown-date cursor skipped/repeated rows", len(secondPage.Records), secondPage.Next)
	}
	ids := map[string]bool{}
	for _, item := range append(firstPage.Records, secondPage.Records...) {
		id := store.String(item["id"])
		if ids[id] {
			t.Fatal("candidate repeated", id)
		}
		ids[id] = true
	}
	response, raw, err := f.request(http.MethodGet, base+"&q=changed&cursor="+url.QueryEscape(firstPage.Next), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 {
		t.Fatal("priority cursor not filter-bound", response, string(raw), err)
	}
}

func TestPantryDailyPopulatedBarcodeUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 13, "pantry": 2, "recipes": 3, "shopping": 4, "mealplan": 2}
	for _, schema := range database.Schemas {
		if version, ok := prior[schema]; ok {
			migratePriorAuditSchema(t, f, schema, version)
		} else {
			migratePriorAuditSchema(t, f, schema)
		}
	}
	hid, uid := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.households(id,name) VALUES($1,'Barcode upgrade')", hid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'barcode-upgrade','Prior','fixture-no-login')", uid); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"001234", " 001234 ", "00000073", "two codes", ""} {
		if _, err := f.owner.Exec(f.ctx, "INSERT INTO pantry.products(household_id,name,unit_code,barcode,created_by) VALUES($1,'Prior product','piece',$2,$3)", hid, code, uid); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	var aliases, retained, unknownDurations, operations int
	var exact string
	err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pantry.barcodes),(SELECT code FROM pantry.barcodes),
 (SELECT count(*) FROM pantry.products WHERE barcode IN('001234',' 001234 ','00000073','two codes','')),
 (SELECT count(*) FROM pantry.lots WHERE after_open_days IS NOT NULL),(SELECT count(*) FROM pantry.stock_operations)`).Scan(&aliases, &exact, &retained, &unknownDurations, &operations)
	if err != nil || aliases != 1 || exact != "00000073" || retained != 5 || unknownDurations != 0 || operations != 0 {
		t.Fatal("barcode upgrade lost legacy values or fabricated facts", aliases, exact, retained, unknownDurations, operations, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestPantryDailyDurableConsumptionAndUndo(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "reviewed consumption")
	lid := store.String(lot["id"])
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "3"})
	pack, _ := auditCreate(t, f, "pantry", "packs", store.Record{"product_id": product["id"], "label": "500 g bag", "quantity": "500", "unit_code": "g"})
	auditCreate(t, f, "pantry", "barcodes", store.Record{"product_id": product["id"], "pack_id": pack["id"], "code": "0012345678905"})
	if found, err := f.app.Store.PantryBarcode(f.ctx, f.identity, "0012345678905"); err != nil || found["pack_id"] != pack["id"] || found["product_id"] != product["id"] {
		t.Fatal("local barcode identity", found, err)
	}
	in := pantryStockInput(t, f, lid)
	in.Mode, in.Quantity, in.PackID, in.PackVersion = "pack", "2", store.String(pack["id"]), store.Int(pack["version"])
	key := value.UUID()
	first, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, key, value.UUID(), in)
	if err != nil || first["quantity"] != "1.000000" || first["stock_after"] != "2.000000" {
		t.Fatal("exact pack consumption", first, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.packs SET quantity=750 WHERE id=$1", pack["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, key, value.UUID(), in)
	if err != nil || replay["id"] != first["id"] || replay["quantity"] != first["quantity"] {
		t.Fatal("durable retry reinterpreted changed pack", replay, err)
	}
	changed := in
	changed.Quantity = "1"
	if _, err = f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, key, value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("durable key accepted another amount", err)
	}
	if _, err = f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale movement count accepted", err)
	}
	in = pantryStockInput(t, f, lid)
	in.Mode, in.Quantity, in.PackID, in.PackVersion = "pack", "1", store.String(pack["id"]), 2
	if second, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in); err != nil || second["quantity"] != "0.750000" || second["stock_after"] != "1.250000" {
		t.Fatal("new pack version not reviewed independently", second, err)
	}
	undo := pantryStockInput(t, f, lid)
	undo.Action, undo.MovementID = "undo", store.String(first["movement_id"])
	undoKey := value.UUID()
	undone, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, undoKey, value.UUID(), undo)
	if err != nil || undone["quantity"] != "1.000000" || undone["stock_after"] != "2.250000" {
		t.Fatal("undo used changed pack rather than original movement", undone, err)
	}
	if repeated, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, undoKey, value.UUID(), undo); err != nil || repeated["id"] != undone["id"] {
		t.Fatal("undo replay", repeated, err)
	}
	undo = pantryStockInput(t, f, lid)
	undo.Action, undo.MovementID = "undo", store.String(first["movement_id"])
	if _, err = f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), undo); !errors.Is(err, store.ErrConflict) {
		t.Fatal("second undo accepted", err)
	}
	view, err := f.app.Store.PantryConsumptionView(f.ctx, f.identity, lid)
	if err != nil || len(view["packs"].([]any)) != 1 || len(view["barcodes"].([]any)) != 1 || len(view["operations"].([]any)) != 3 {
		t.Fatal("bounded review bundle", view, err)
	}
	for _, statement := range []string{"UPDATE pantry.stock_operations SET kind='undo'", "DELETE FROM pantry.stock_operations", "TRUNCATE pantry.stock_operations"} {
		if _, err = f.pool.Exec(f.ctx, statement); err == nil {
			t.Fatal("runtime changed durable history", statement)
		}
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("new guard/readiness contract", err)
	}
}

func TestPantryDailyConcurrentReviewedConsumption(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "reviewed concurrent")
	lid := store.String(lot["id"])
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "3"})
	in := pantryStockInput(t, f, lid)
	in.Mode = "half"
	start, results := make(chan struct{}), make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			_, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in)
			results <- err
		}()
	}
	close(start)
	success, conflicts := 0, 0
	for range 8 {
		if err := <-results; err == nil {
			success++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	current, err := f.app.Store.PantryLotState(f.ctx, f.identity, lid)
	if err != nil || success != 1 || conflicts != 7 || current["stock"] != "1.500000" || store.Int(current["movement_count"]) != 2 {
		t.Fatal("concurrent review changed amount or duplicated", current, success, conflicts, err)
	}
	in = pantryStockInput(t, f, lid)
	reader := shoppingTestMember(t, f, "stock-review-reader", "reader")
	if _, err = f.app.Store.MutatePantryStock(f.ctx, reader, lid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader wrote stock", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PantryConsumptionView(f.ctx, foreign, lid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign stock review disclosed", err)
	}
}

func TestPantryDailyTransferPreservesOpeningSource(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "opening source")
	lid := store.String(lot["id"])
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.lots SET opened_on='2026-10-25',after_open_days=2,after_open_source='manufacturer',after_open_note='Label on original pack' WHERE id=$1", lid); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "3"})
	destination, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Opened pack destination"})
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("lots")
	current, err := f.app.Store.Get(f.ctx, f.identity, m, e, lid)
	if err != nil {
		t.Fatal(err)
	}
	input := auditMutation(t, f, "pantry", store.Record{"quantity": "1", "location_id": destination["id"]}, store.Int(current["version"]))
	if _, err = f.app.Store.Action(f.ctx, f.identity, m, e, lid, "transfer", value.UUID(), value.UUID(), input); err != nil {
		t.Fatal(err)
	}
	var copied string
	if err = f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE household_id=$1 AND product_id=$2 AND location_id=$3", f.identity.HouseholdID, product["id"], destination["id"]).Scan(&copied); err != nil {
		t.Fatal(err)
	}
	for _, testID := range []string{lid, copied} {
		state, readErr := f.app.Store.PantryLotState(f.ctx, f.identity, testID)
		if readErr != nil || state["opened_on"] != "2026-10-25" || store.Int(state["after_open_days"]) != 2 || state["after_open_source"] != "manufacturer" || state["after_open_note"] != "Label on original pack" || state["opening_hint_on"] != "2026-10-27" {
			t.Fatal("transfer lost opening provenance", state, readErr)
		}
	}
}
