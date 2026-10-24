//go:build integration

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pquerna/otp/totp"
)

func TestApplicationContract(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx, pool, owner, app := fixture.ctx, fixture.pool, fixture.owner, fixture.app
	cfg, token, id, server := app.Cfg, fixture.token, fixture.identity, fixture.server
	uid, hid, username := id.UserID, id.HouseholdID, id.Username
	var err error
	defer func() {
		_, _ = owner.Exec(ctx, "DELETE FROM core.capture_jobs WHERE source_attachment_id IN(SELECT id FROM core.attachments WHERE uploaded_by=$1)", uid)
		_, _ = owner.Exec(ctx, "DELETE FROM core.attachments WHERE uploaded_by=$1", uid)
	}()
	for _, m := range app.Store.Modules {
		st := app.Store.States()[m.ID]
		if st.State != "enabled" {
			if err = app.Store.SetModule(ctx, id, m.ID, true, st.Version, value.UUID()); err != nil {
				t.Fatal(err)
			}
		}
	}
	type result struct {
		status int
		record store.Record
		body   []byte
	}
	call := func(method, path string, body any, key string) result {
		t.Helper()
		var raw []byte
		var err error
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Errorf("encode request: %v", err)
				return result{}
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Errorf("create request: %v", err)
			return result{}
		}
		request.AddCookie(&http.Cookie{Name: app.cookieName(), Value: token})
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", app.Auth.CSRF(token, "session"))
		request.Header.Set("Idempotency-Key", key)
		response, err := fixture.client.Do(request)
		if err != nil {
			t.Errorf("send request: %v", err)
			return result{}
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("read response: %v", err)
			return result{}
		}
		record := store.Record{}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		_ = decoder.Decode(&record)
		return result{response.StatusCode, record, data}
	}
	mutation := func(mid string, data store.Record, version int64) store.Mutation {
		raw := map[string]json.RawMessage{}
		for k, v := range data {
			b, _ := json.Marshal(v)
			raw[k] = b
		}
		targets := map[string]int64{}
		for key, state := range app.Store.States() {
			targets[key] = state.Generation
		}
		return store.Mutation{Data: raw, Version: version, Generation: app.Store.States()[mid].Generation, TargetGenerations: targets}
	}
	create := func(mid, entity string, data store.Record) store.Record {
		t.Helper()
		response := call("POST", "/api/v1/m/"+mid+"/"+entity, mutation(mid, data, 0), value.UUID())
		if response.status != 200 {
			t.Fatalf("create %s/%s: %d %s", mid, entity, response.status, response.body)
		}
		return response.record
	}
	action := func(mid, entity string, record store.Record, name string, data store.Record) store.Record {
		t.Helper()
		response := call("POST", fmt.Sprintf("/api/v1/m/%s/%s/%v/actions/%s", mid, entity, record["id"], name), mutation(mid, data, store.Int(record["version"])), value.UUID())
		if response.status != 200 {
			t.Fatalf("action %s: %d %s", name, response.status, response.body)
		}
		return response.record
	}
	prefix := value.UUID()[:8]
	list := create("shopping", "lists", store.Record{"name": "Week " + prefix})
	item := create("shopping", "items", store.Record{"list_id": list["id"], "name": "Milk " + prefix, "quantity": "2"})
	checked := action("shopping", "items", item, "check", store.Record{"checked": true})
	if checked["checked_at"] == nil {
		t.Fatal("item not checked")
	}
	archived := action("shopping", "lists", list, "archive", store.Record{"archived": true})
	if call("POST", "/api/v1/m/shopping/items/"+store.String(checked["id"])+"/actions/check", mutation("shopping", store.Record{"checked": false}, store.Int(checked["version"])), value.UUID()).status != 422 {
		t.Fatal("archived item action accepted")
	}
	list = action("shopping", "lists", archived, "archive", store.Record{"archived": false})
	stale := call("PATCH", "/api/v1/m/shopping/items/"+store.String(item["id"]), mutation("shopping", store.Record{"name": "Stale"}, 1), value.UUID())
	if stale.status != 409 {
		t.Fatalf("stale write accepted: %s", stale.body)
	}
	input := mutation("shopping", store.Record{"list_id": list["id"], "name": "Once " + prefix}, 0)
	key := value.UUID()
	once := call("POST", "/api/v1/m/shopping/items", input, key)
	twice := call("POST", "/api/v1/m/shopping/items", input, key)
	if once.status != 200 || twice.status != 200 || once.record["id"] != twice.record["id"] {
		t.Fatal("idempotent replay duplicated or failed")
	}
	input.Data["name"] = json.RawMessage(`"Different"`)
	if call("POST", "/api/v1/m/shopping/items", input, key).status != 409 {
		t.Fatal("idempotency key allowed different payload")
	}
	vehicle := create("fuel", "vehicles", store.Record{"name": "Car " + prefix})
	series := create("fuel", "odometer_series", store.Record{"vehicle_id": vehicle["id"], "started_on": "2026-01-01", "initial_reading": "1000", "reason": "Original"})
	fill := create("fuel", "fillups", store.Record{"vehicle_id": vehicle["id"], "odometer_series_id": series["id"], "occurred_on": "2026-02-01", "odometer": "1500", "quantity": "30", "total_minor": "48,90", "full_tank": true})
	badFuel := call("POST", "/api/v1/m/fuel/fillups", mutation("fuel", store.Record{"vehicle_id": vehicle["id"], "odometer_series_id": series["id"], "occurred_on": "2026-03-01", "odometer": "1200", "quantity": "20", "total_minor": "30,00"}, 0), value.UUID())
	if badFuel.status != 422 {
		t.Fatalf("decreasing odometer accepted: %s", badFuel.body)
	}
	recipe := create("recipes", "recipes", store.Record{"title": "Pasta " + prefix, "base_servings": "2"})
	create("recipes", "ingredients", store.Record{"recipe_id": recipe["id"], "name": "Pasta", "quantity": "250", "unit_code": "g"})
	step := create("recipes", "steps", store.Record{"recipe_id": recipe["id"], "instruction": "Cook pasta"})
	action("recipes", "recipes", recipe, "shopping", store.Record{"list_id": list["id"], "servings": "4"})
	copyPath := "/api/v1/m/recipes/recipes/" + store.String(recipe["id"]) + "/actions/shopping"
	staleTarget := mutation("recipes", store.Record{"list_id": list["id"], "servings": "4"}, store.Int(recipe["version"]))
	staleTarget.TargetGenerations["shopping"]++
	if call("POST", copyPath, staleTarget, value.UUID()).status != 409 {
		t.Fatal("stale destination generation accepted")
	}
	if call("POST", copyPath, mutation("recipes", store.Record{"list_id": list["id"], "servings": "4", "ingredient_ids": []string{value.UUID()}}, store.Int(recipe["version"])), value.UUID()).status != 422 {
		t.Fatal("foreign ingredient accepted")
	}
	location := create("pantry", "locations", store.Record{"name": "Shelf " + prefix})
	product := create("pantry", "products", store.Record{"name": "Rice " + prefix, "integer_quantities": true, "low_stock_threshold": "2"})
	lot := create("pantry", "lots", store.Record{"product_id": product["id"], "location_id": location["id"], "received_on": "2026-01-01", "best_before_on": "2027-01-01", "notes": "Lot " + prefix})
	create("pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "3", "reason": "receive"})
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := call("POST", "/api/v1/m/pantry/stock_movements", mutation("pantry", store.Record{"lot_id": lot["id"], "quantity_delta": "-2", "reason": "consume"}, 0), value.UUID())
			statuses <- response.status
		}()
	}
	wait.Wait()
	close(statuses)
	accepted, rejected := 0, 0
	for status := range statuses {
		switch status {
		case 200:
			accepted++
		case 422:
			rejected++
		default:
			t.Fatalf("concurrent stock status %d", status)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent stock: accepted %d rejected %d", accepted, rejected)
	}
	category := create("expenses", "categories", store.Record{"name": "Food " + prefix})
	expense := create("expenses", "entries", store.Record{"occurred_on": "2026-01-01", "description": "Groceries", "amount_minor": "12,50"})
	posted := action("expenses", "entries", expense, "post", store.Record{"category_id": category["id"]})
	if posted["state"] != "posted" {
		t.Fatal("expense not posted")
	}
	locked := call("PATCH", "/api/v1/m/expenses/entries/"+store.String(posted["id"]), mutation("expenses", store.Record{"amount_minor": "99,99"}, store.Int(posted["version"])), value.UUID())
	if locked.status != 422 && locked.status != 409 {
		t.Fatalf("posted expense mutable: %s", locked.body)
	}
	reversal := action("expenses", "entries", posted, "reverse", store.Record{})
	if store.Int(reversal["amount_minor"]) != -1250 {
		t.Fatal("reversal not exact")
	}
	action("fuel", "fillups", fill, "expense", store.Record{"category_id": category["id"]})
	task := create("chores", "task_templates", store.Record{"title": "Clean " + prefix, "repeat_days": 7, "active": true})
	occurrence := create("chores", "task_occurrences", store.Record{"template_id": task["id"], "occurrence_key": prefix, "due_at": "2026-10-24T09:00:00+02:00"})
	action("chores", "task_occurrences", occurrence, "complete", store.Record{})
	var nextProjection bool
	if err = owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.task_occurrences o JOIN core.search_documents d ON d.resource_id=o.id AND d.module_id='chores' JOIN core.reminders r ON r.resource_id=o.id AND r.module_id='chores' WHERE o.template_id=$1 AND o.state='open')`, task["id"]).Scan(&nextProjection); err != nil || !nextProjection {
		t.Fatalf("recurring occurrence missing projection: %v", err)
	}
	meter := create("meters", "meters", store.Record{"name": "Electricity " + prefix})
	device := create("meters", "devices", store.Record{"meter_id": meter["id"], "serial_number": prefix, "installed_on": "2026-01-01", "initial_reading": "100"})
	create("meters", "readings", store.Record{"device_id": device["id"], "measured_at": "2026-02-01T12:00:00Z", "value": "150"})
	if report := call("GET", "/api/v1/m/meters/reports/summary", nil, ""); report.status != 200 || !bytes.Contains(report.body, []byte(`"device_label":"`+prefix+`"`)) || !bytes.Contains(report.body, []byte(`"from":`)) || !bytes.Contains(report.body, []byte(`"to":`)) {
		t.Fatal("meter report lacks device/period context")
	}
	if report := call("GET", "/api/v1/m/fuel/reports/summary", nil, ""); report.status != 200 || !bytes.Contains(report.body, []byte(`"vehicle_name":"Car `+prefix+`"`)) || !bytes.Contains(report.body, []byte(`"series_label":"Original`)) {
		t.Fatal("fuel report lacks vehicle/series context")
	}
	create("documents", "documents", store.Record{"title": "Warranty " + prefix, "warranty_until": "2027-01-01", "remind_days": 30})
	plan := create("mealplan", "plans", store.Record{"name": "Week " + prefix, "starts_on": "2026-10-24", "ends_on": "2026-10-30"})
	create("mealplan", "meals", store.Record{"plan_id": plan["id"], "planned_on": "2026-10-25", "title": "Pasta", "servings": "2"})
	for _, m := range app.Store.Modules {
		for _, e := range m.Entities {
			response := call("GET", "/m/"+m.ID+"/"+e.ID, nil, "")
			if response.status != 200 || !bytes.Contains(response.body, []byte("</html>")) {
				t.Fatalf("page %s/%s failed: %d %s", m.ID, e.ID, response.status, response.body)
			}
		}
	}
	for _, path := range []string{"/settings", "/settings/security", "/settings/users", "/search?q=" + prefix, "/notifications", "/manifest.webmanifest", "/sw.js", "/offline", "/api/v1/m/expenses/reports/summary", "/api/v1/m/shopping/items/export.csv"} {
		response := call("GET", path, nil, "")
		if response.status != 200 {
			t.Fatalf("route %s: %d %s", path, response.status, response.body)
		}
	}
	st := app.Store.States()["shopping"]
	if err = app.Store.SetModule(ctx, id, "shopping", false, st.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/v1/m/shopping/items", nil, "").status != 404 {
		t.Fatal("disabled module readable")
	}
	if call("POST", "/api/v1/m/shopping/items", mutation("shopping", store.Record{"list_id": list["id"], "name": "disabled"}, 0), value.UUID()).status != 404 {
		t.Fatal("disabled module writable")
	}
	st = app.Store.States()["shopping"]
	if err = app.Store.SetModule(ctx, id, "shopping", true, st.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var tokenBytes []byte
	if err = owner.QueryRow(ctx, "SELECT token_hash FROM core.sessions WHERE id=$1", id.SessionID).Scan(&tokenBytes); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(tokenBytes, []byte(token)) || len(tokenBytes) != 32 {
		t.Fatal("raw session token stored")
	}
	for _, permission := range []struct{ name, statement string }{
		{"create_schema", "CREATE SCHEMA forbidden_runtime_fixture"},
		{"create_core_table", "CREATE TABLE core.forbidden_runtime_fixture(id integer)"},
		{"create_public_table", "CREATE TABLE public.forbidden_runtime_fixture(id integer)"},
		{"truncate_users", "TRUNCATE core.users CASCADE"},
		{"update_audit", "UPDATE core.audit_log SET action='tamper'"},
		{"delete_audit", "DELETE FROM core.audit_log"},
		{"update_backup", "UPDATE core.backup_runs SET state='complete'"},
		{"delete_backup", "DELETE FROM core.backup_runs"},
	} {
		t.Run("runtime_privilege/"+permission.name, func(t *testing.T) {
			transaction, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer func() { _ = transaction.Rollback(ctx) }()
			_, denied := transaction.Exec(ctx, permission.statement)
			var postgresError *pgconn.PgError
			if !errors.As(denied, &postgresError) || postgresError.Code != "42501" {
				t.Fatalf("operation must be denied by PostgreSQL permissions, got %v", denied)
			}
		})
	}
	// Regression: a posted allocation cannot be moved to a draft parent.
	draft := create("expenses", "entries", store.Record{"occurred_on": "2026-01-02", "description": "Draft", "amount_minor": "12,50"})
	var allocation string
	if err = owner.QueryRow(ctx, "SELECT id FROM expenses.entry_lines WHERE entry_id=$1 LIMIT 1", posted["id"]).Scan(&allocation); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE expenses.entry_lines SET entry_id=$2 WHERE id=$1", allocation, draft["id"]); err == nil {
		t.Fatal("posted allocation was reparented")
	}
	// Regression: historical readings and lot identities preserve existing history.
	if call("POST", "/api/v1/m/meters/readings", mutation("meters", store.Record{"device_id": device["id"], "measured_at": "2026-01-15T12:00:00Z", "value": "200"}, 0), value.UUID()).status != 422 {
		t.Fatal("earlier meter reading exceeded later history")
	}
	otherLocation := create("pantry", "locations", store.Record{"name": "Other " + prefix})
	if call("PATCH", "/api/v1/m/pantry/lots/"+store.String(lot["id"]), mutation("pantry", store.Record{"location_id": otherLocation["id"]}, 1), value.UUID()).status != 422 {
		t.Fatal("stock silently moved through lot edit")
	}
	action("pantry", "lots", lot, "transfer", store.Record{"location_id": otherLocation["id"], "quantity": "1"})
	var lotProjection bool
	if err = owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pantry.lots l JOIN core.search_documents d ON d.resource_id=l.id AND d.module_id='pantry' JOIN core.reminders r ON r.resource_id=l.id AND r.module_id='pantry' WHERE l.product_id=$1 AND l.location_id=$2)`, product["id"], otherLocation["id"]).Scan(&lotProjection); err != nil || !lotProjection {
		t.Fatalf("transferred lot missing projection: %v", err)
	}
	var transferID string
	var transferVersion int64
	if err = owner.QueryRow(ctx, "SELECT id,version FROM pantry.stock_movements WHERE lot_id=$1 AND reason='transfer' LIMIT 1", lot["id"]).Scan(&transferID, &transferVersion); err != nil {
		t.Fatal(err)
	}
	if call("POST", "/api/v1/m/pantry/stock_movements/"+transferID+"/actions/reverse", mutation("pantry", store.Record{}, transferVersion), value.UUID()).status != 422 {
		t.Fatal("one transfer leg could be reversed")
	}
	// Real multipart upload, manual review confirmation, thumbnail and private download.
	document := create("documents", "documents", store.Record{"title": "Attachment test " + prefix, "valid_until": "2020-01-01"})
	var picture bytes.Buffer
	if err = png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var multipartBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&multipartBody)
	part, err := multipartWriter.CreateFormFile("file", "receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(picture.Bytes()); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"csrf": app.Auth.CSRF(token, "session"), "generation": fmt.Sprint(app.Store.States()["documents"].Generation), "capture": "true"} {
		if err = multipartWriter.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/m/documents/documents/"+store.String(document["id"])+"/attachments", &multipartBody)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: app.cookieName(), Value: token})
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	uploadResponse, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = uploadResponse.Body.Close()
	if uploadResponse.StatusCode != 303 {
		t.Fatalf("upload status %d", uploadResponse.StatusCode)
	}
	capturePath := uploadResponse.Header.Get("Location")
	capture := call("GET", capturePath, nil, "")
	if capture.status != 200 || !bytes.Contains(capture.body, []byte("Erfassung prüfen")) {
		t.Fatal("capture review unavailable")
	}
	var attachment string
	if err = owner.QueryRow(ctx, "SELECT id FROM core.attachments WHERE resource_id=$1", document["id"]).Scan(&attachment); err != nil {
		t.Fatal(err)
	}
	background := &worker.Worker{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Cfg: cfg}
	t.Cleanup(func() { _ = background.Close() })
	if err = background.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err = background.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	preview := call("GET", "/attachments/"+attachment+"/preview", nil, "")
	if preview.status != 200 || !bytes.HasPrefix(preview.body, []byte{137, 80, 78, 71}) {
		t.Fatalf("private thumbnail failed: %d %s", preview.status, preview.body)
	}
	confirmed := mutation("documents", store.Record{"title": "Captured " + prefix, "remind_days": 0}, 0)
	first := call("POST", capturePath+"/confirm", confirmed, value.UUID())
	second := call("POST", capturePath+"/confirm", confirmed, value.UUID())
	if first.status != 200 || second.status != 200 || first.record["id"] != second.record["id"] {
		t.Fatal("capture confirmation duplicated or failed")
	}
	confirmed.Data["title"] = json.RawMessage(`"Changed"`)
	if call("POST", capturePath+"/confirm", confirmed, value.UUID()).status != 409 {
		t.Fatal("confirmed capture accepted a different payload")
	}
	var notifications int
	if err = owner.QueryRow(ctx, "SELECT count(*) FROM core.notifications WHERE target_path=$1 AND user_id=$2", "/m/documents/documents/"+store.String(document["id"]), uid).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatalf("reminder delivery count %d", notifications)
	}
	// Approved values remain canonical even when storage maps zero timers to NULL.
	seedCapture := func(mid, entity, rid string) string {
		t.Helper()
		aid, key, cid := value.UUID(), value.UUID(), value.UUID()
		if writeErr := os.WriteFile(filepath.Join(cfg.UploadDir, key), picture.Bytes(), 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		sum := sha256.Sum256(picture.Bytes())
		if _, seedErr := owner.Exec(ctx, "INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,'fixture.png','image/png',$8,$9,$10)", aid, hid, mid, app.Store.States()[mid].Generation, entity, rid, key, picture.Len(), sum[:], uid); seedErr != nil {
			t.Fatal(seedErr)
		}
		if _, seedErr := owner.Exec(ctx, "INSERT INTO core.capture_jobs(id,household_id,module_id,module_generation,source_attachment_id,target_schema) VALUES($1,$2,$3,$4,$5,$6)", cid, hid, mid, app.Store.States()[mid].Generation, aid, entity); seedErr != nil {
			t.Fatal(seedErr)
		}
		return "/capture/" + cid
	}
	stepCapture := seedCapture("recipes", "steps", store.String(step["id"]))
	stepInput := mutation("recipes", store.Record{"recipe_id": recipe["id"], "instruction": "Captured step", "timer_seconds": 0}, 0)
	stepFirst := call("POST", stepCapture+"/confirm", stepInput, value.UUID())
	stepReplay := call("POST", stepCapture+"/confirm", stepInput, value.UUID())
	if stepFirst.status != 200 || stepReplay.status != 200 || stepFirst.record["id"] != stepReplay.record["id"] {
		t.Fatalf("canonical capture replay failed: %d %s / %d %s", stepFirst.status, stepFirst.body, stepReplay.status, stepReplay.body)
	}
	orphanCapture := seedCapture("documents", "documents", store.String(document["id"]))
	if call("DELETE", "/api/v1/m/documents/documents/"+store.String(document["id"]), mutation("documents", store.Record{}, store.Int(document["version"])), value.UUID()).status != 200 {
		t.Fatal("capture source deletion failed")
	}
	orphanResult := call("POST", orphanCapture+"/confirm", mutation("documents", store.Record{"title": "Must not resurrect"}, 0), value.UUID())
	if orphanResult.status != 409 && orphanResult.status != 404 {
		t.Fatal("deleted capture source was resurrected")
	}
	// Re-verifying an old snapshot must not make it eligible for a new module pause.
	docState := app.Store.States()["documents"]
	if err = app.Store.SetModule(ctx, id, "documents", false, docState.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backupID, purgeID := value.UUID(), value.UUID()
	if _, err = owner.Exec(ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now()-interval '8 days',now(),'test','test','stale-test-snapshot')", backupID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'documents',$3,$4,$5)", purgeID, hid, docState.Generation, uid, backupID); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(ctx, owner, purgeID); err == nil {
		t.Fatal("old snapshot qualified for purge after recent verification")
	}
	if _, err = owner.Exec(ctx, "DELETE FROM core.module_purge_requests WHERE id=$1", purgeID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, "DELETE FROM core.backup_runs WHERE id=$1", backupID); err != nil {
		t.Fatal(err)
	}
	if err = app.Store.SetModule(ctx, id, "documents", true, app.Store.States()["documents"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	// Regression: cascading deletion cleans child search projections in the same commit.
	removeList := create("shopping", "lists", store.Record{"name": "Delete " + prefix})
	removeItem := create("shopping", "items", store.Record{"list_id": removeList["id"], "name": "Deleted child " + prefix})
	if call("DELETE", "/api/v1/m/shopping/lists/"+store.String(removeList["id"]), mutation("shopping", store.Record{}, 1), value.UUID()).status != 200 {
		t.Fatal("list deletion failed")
	}
	var staleProjection bool
	if err = owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.search_documents WHERE resource_id=$1)", removeItem["id"]).Scan(&staleProjection); err != nil || staleProjection {
		t.Fatal("cascade left searchable child")
	}
	// Readers may inspect records but cannot write or administer users.
	if _, err = owner.Exec(ctx, "UPDATE core.memberships SET role='reader' WHERE household_id=$1 AND user_id=$2", hid, uid); err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/v1/m/shopping/lists", nil, "").status != 200 {
		t.Fatal("reader cannot inspect lists")
	}
	if call("POST", "/api/v1/m/shopping/lists", mutation("shopping", store.Record{"name": "Forbidden"}, 0), value.UUID()).status != 403 {
		t.Fatal("reader write accepted")
	}
	if call("GET", "/settings/users", nil, "").status != 403 {
		t.Fatal("reader admin access accepted")
	}
	if _, err = owner.Exec(ctx, "UPDATE core.memberships SET role='admin' WHERE household_id=$1 AND user_id=$2", hid, uid); err != nil {
		t.Fatal(err)
	}
	// A late relationship failure rolls back every imported row; confirmation retry stays once-only.
	batch := value.UUID()
	if _, err = owner.Exec(ctx, "INSERT INTO core.import_batches(id,household_id,module_id,module_generation,entity,row_count,valid_count,error_count) VALUES($1,$2,'shopping',$3,'items',2,2,0)", batch, hid, app.Store.States()["shopping"].Generation); err != nil {
		t.Fatal(err)
	}
	for n, listID := range []any{list["id"], value.UUID()} {
		raw, _ := json.Marshal(store.Record{"list_id": listID, "name": fmt.Sprintf("Import %s %d", prefix, n)})
		if _, err = owner.Exec(ctx, "INSERT INTO core.import_rows(batch_id,row_number,normalized_values,validation_errors) VALUES($1,$2,$3,'[]')", batch, n+1, raw); err != nil {
			t.Fatal(err)
		}
	}
	shoppingImport, _ := app.Store.Module("shopping")
	itemsImport, _ := shoppingImport.Entity("items")
	if err = app.Store.ConfirmImport(ctx, id, shoppingImport, itemsImport, batch, value.UUID(), app.Store.States()["shopping"].Generation); err == nil {
		t.Fatal("invalid relationship import accepted")
	}
	var imported int
	if err = owner.QueryRow(ctx, "SELECT count(*) FROM shopping.items WHERE household_id=$1 AND name LIKE $2", hid, "Import "+prefix+" %").Scan(&imported); err != nil || imported != 0 {
		t.Fatalf("partial import survived: %d %v", imported, err)
	}
	if _, err = owner.Exec(ctx, "UPDATE core.import_rows SET normalized_values=jsonb_set(normalized_values,'{list_id}',to_jsonb($2::text)) WHERE batch_id=$1 AND row_number=2", batch, list["id"]); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = app.Store.ConfirmImport(ctx, id, shoppingImport, itemsImport, batch, value.UUID(), app.Store.States()["shopping"].Generation); err != nil {
			t.Fatal(err)
		}
	}
	if err = owner.QueryRow(ctx, "SELECT count(*) FROM shopping.items WHERE household_id=$1 AND name LIKE $2", hid, "Import "+prefix+" %").Scan(&imported); err != nil || imported != 2 {
		t.Fatal("confirmed import retry duplicated rows")
	}
	create("shopping", "lists", store.Record{"name": "=1+1"})
	if exported := call("GET", "/api/v1/m/shopping/lists/export.csv", nil, ""); exported.status != 200 || !bytes.Contains(exported.body, []byte("'=1+1")) {
		t.Fatal("CSV formula prefix was not neutralized")
	}
	// TOTP secrets are encrypted, recovery codes work once, and password-only login is rejected.
	secret, _, err := app.Auth.SetupTOTP(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := app.Auth.EnableTOTP(ctx, id, code)
	if err != nil || len(recovery) != 8 {
		t.Fatalf("TOTP setup: %v", err)
	}
	var encrypted []byte
	if err = owner.QueryRow(ctx, "SELECT encrypted_secret FROM core.totp_credentials WHERE user_id=$1", uid).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("TOTP secret not encrypted")
	}
	if _, _, err = app.Auth.Login(ctx, username, integrationPassword, "", "127.0.0.1", "MFA test"); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("password-only MFA login accepted")
	}
	if _, _, err = app.Auth.Login(ctx, username, integrationPassword, recovery[0], "127.0.0.1", "MFA test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = app.Auth.Login(ctx, username, integrationPassword, recovery[0], "127.0.0.1", "MFA test"); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("recovery code replay accepted")
	}
	if err = app.Auth.RecoverPassword(ctx, username, "Runtime must not reset 1234"); err == nil {
		t.Fatal("runtime role could recover credentials")
	}
	// Regression: a queued write rechecks authorization after the household barrier.
	barrier, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Rollback(ctx) }()
	if _, err = barrier.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR UPDATE", hid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	shopping, _ := app.Store.Module("shopping")
	lists, _ := shopping.Entity("lists")
	go func() {
		_, writeErr := app.Store.Write(ctx, id, shopping, lists, "", value.UUID(), value.UUID(), mutation("shopping", store.Record{"name": "Revoked write"}, 0))
		done <- writeErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename='household_app' AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM core.households%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("write never reached household barrier")
	}
	if _, err = barrier.Exec(ctx, "UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1", uid); err != nil {
		t.Fatal(err)
	}
	if _, err = barrier.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1", uid); err != nil {
		t.Fatal(err)
	}
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("revoked queued mutation returned %v", err)
	}
	t.Log("Verified real PostgreSQL migrations, nine modules, immutable postings, idempotency, stock concurrency, CSRF-ready API and rendered pages")
}
