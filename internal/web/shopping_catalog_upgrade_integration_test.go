//go:build integration

package web

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"os"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"
)

func TestShoppingCatalogPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		if schema == "core" {
			migratePriorAuditSchema(t, f, schema, 5)
		} else {
			migratePriorAuditSchema(t, f, schema)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal(err)
	}
	hid, uid, list, item := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior catalog release')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-catalog','Prior catalog','fixture-no-login')", []any{uid}},
		{"INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Preserved list',$3)", []any{list, hid, uid}},
		{"INSERT INTO shopping.items(id,household_id,list_id,name,quantity,unit_code,created_by) VALUES($1,$2,$3,'Precise existing item',9007199254.740993,'g',$4)", []any{item, hid, list, uid}},
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
	var quantity, availability string
	var unchanged bool
	err := f.pool.QueryRow(f.ctx, "SELECT quantity::text,availability,product_id IS NULL AND replacement_product_id IS NULL AND assignee_id IS NULL AND version=1 FROM shopping.items WHERE id=$1", item).Scan(&quantity, &availability, &unchanged)
	if err != nil || quantity != "9007199254.740993" || availability != "available" || !unchanged {
		t.Fatalf("upgrade changed existing shopping values: %s/%s/%t/%v", quantity, availability, unchanged, err)
	}
	var policies, events int
	if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.audit_policies),(SELECT count(*) FROM core.audit_log WHERE resource_id=$1 AND action='CREATE')", item).Scan(&policies, &events); err != nil || policies != 54 || events != 1 {
		t.Fatalf("upgrade lost audit contract/history: %d/%d/%v", policies, events, err)
	}
}

func TestShoppingProductPrivatePreview(t *testing.T) {
	f := newIntegrationFixture(t)
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Picture product"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Picture list"})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Picture item", "product_id": product["id"]})
	var picture, body bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "product.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(picture.Bytes()); err != nil {
		t.Fatal(err)
	}
	for key, text := range map[string]string{"csrf": f.app.Auth.CSRF(f.token, "session"), "generation": "1", "capture": "false"} {
		if err = writer.WriteField(key, text); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/api/v1/m/shopping/products/"+store.String(product["id"])+"/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: f.app.cookieName(), Value: f.token})
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("product upload: %d", response.StatusCode)
	}
	background := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	t.Cleanup(func() { _ = background.Close() })
	for range 2 {
		if err = background.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	row := shoppingTestBoard(t, f, store.String(list["id"]))["records"].([]any)[0].(map[string]any)
	attachment := store.String(row["image_id"])
	if !value.ValidUUID(attachment) {
		t.Fatal("ready product preview not included in board")
	}
	preview := "/attachments/" + attachment + "/preview"
	get, raw, err := f.request(http.MethodGet, preview, f.token, "", "", nil, nil)
	if err != nil || get.StatusCode != 200 || !bytes.HasPrefix(raw, []byte{137, 80, 78, 71}) {
		t.Fatal("private product preview failed")
	}
	get, _, err = f.request(http.MethodGet, preview, "", "", "", nil, nil)
	if err != nil || get.StatusCode == 200 {
		t.Fatal("anonymous product preview disclosed")
	}
	state := f.app.Store.States()["shopping"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get, _, err = f.request(http.MethodGet, preview, f.token, "", "", nil, nil)
	if err != nil || get.StatusCode != 404 {
		t.Fatal("disabled product preview disclosed")
	}
	state = f.app.Store.States()["shopping"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "shopping", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(item["id"]), 1, 1, value.UUID()); err != nil {
		t.Fatal(err)
	}
	e, _ = m.Entity("products")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(product["id"]), 1, 1, value.UUID()); err != nil {
		t.Fatal(err)
	}
	get, _, err = f.request(http.MethodGet, preview, f.token, "", "", nil, nil)
	if err != nil || get.StatusCode != 404 {
		t.Fatal("deleted product preview disclosed")
	}
}
