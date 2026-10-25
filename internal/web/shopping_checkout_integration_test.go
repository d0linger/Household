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

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func checkoutFixture(t *testing.T, f *integrationFixture) (string, store.CheckoutInput) {
	t.Helper()
	p, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Planned oats", "pack_quantity": "500", "pack_unit": "g"})
	replacement, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Actual oats", "pack_quantity": "750", "pack_unit": "g"})
	shop, _ := auditCreate(t, f, "shopping", "stores", store.Record{"name": "Local store"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Checkout", "store_id": shop["id"]})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Oats", "quantity": "2", "unit_code": "piece", "product_id": p["id"], "replacement_product_id": replacement["id"], "availability": "substituted"})
	auditCreate(t, f, "shopping", "price_observations", store.Record{"product_id": replacement["id"], "store_id": shop["id"], "observed_on": "2026-01-01", "quantity": "1", "unit_code": "piece", "price_minor": "3.00"})
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	if _, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(item["id"]), "check", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"checked": true}, 1)); err != nil {
		t.Fatal(err)
	}
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Pantry oats", "unit_code": "g"})
	location, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Shelf " + value.UUID()})
	preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, store.String(list["id"]))
	if err != nil {
		t.Fatal(err)
	}
	return store.String(list["id"]), store.CheckoutInput{Generation: f.app.Store.States()["shopping"].Generation,
		PantryGeneration: f.app.Store.States()["pantry"].Generation, Fingerprint: store.String(preview["fingerprint"]), ReceivedOn: "2026-01-02", ReceiptTotal: "6.20",
		Lines: []store.CheckoutLine{{ItemID: store.String(item["id"]), Decision: "store", Quantity: "1.5", Unit: "kg", Price: "6.00", ProductID: store.String(product["id"]), ProductVersion: 1, LocationID: store.String(location["id"]), LocationVersion: 1, BestBefore: "2027-01-02"}}}
}

func TestShoppingCheckoutAtomicReplayAndProjections(t *testing.T) {
	f := newIntegrationFixture(t)
	list, input := checkoutFixture(t, f)
	attachment := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by)
 VALUES($1,$2,'shopping',1,'lists',$3,($1::uuid)::text,'Receipt.pdf','application/pdf',42,decode(repeat('ab',32),'hex'),$4)`, attachment, f.identity.HouseholdID, list, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	input.ReceiptID = attachment
	type result struct {
		record store.Record
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			r, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, list, value.UUID(), value.UUID(), input)
			results <- result{r, err}
		}()
	}
	close(start)
	var first store.Record
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if first != nil && r.record["id"] != first["id"] {
			t.Fatal("concurrent checkout created two completions")
		}
		first = r.record
	}
	var stock string
	var lots, movements, links, search, reminders, complete, auditRows int
	var archived bool
	err := f.pool.QueryRow(f.ctx, `SELECT (SELECT sum(quantity)::text FROM pantry.lot_balances),
 (SELECT count(*) FROM pantry.lots),(SELECT count(*) FROM pantry.stock_movements),(SELECT count(*) FROM core.resource_links WHERE relation='shopping_stock'),
 (SELECT count(*) FROM core.search_documents WHERE module_id='pantry' AND resource_type IN('lots','stock_movements')),
 (SELECT count(*) FROM core.reminders WHERE module_id='pantry'),(SELECT count(*) FROM core.audit_log WHERE action='complete'),
 (SELECT count(*) FROM core.audit_log WHERE resource_type='checkouts' AND action='CREATE'),
 (SELECT archived_at IS NOT NULL FROM shopping.lists WHERE id=$1)`, list).Scan(&stock, &lots, &movements, &links, &search, &reminders, &complete, &auditRows, &archived)
	if err != nil || stock != "1500.000000" || lots != 1 || movements != 1 || links != 1 || search != 2 || reminders != 1 || complete != 1 || auditRows != 1 || !archived {
		t.Fatalf("atomic effects: %s/%d/%d/%d/%d/%d/%d/%d/%v/%v", stock, lots, movements, links, search, reminders, complete, auditRows, archived, err)
	}
	snapshot := first["snapshot"].(map[string]any)
	if snapshot["planned_known_minor"] != "600" || snapshot["actual_known_minor"] != "600" || snapshot["unallocated_minor"] != "20" || snapshot["receipt"].(map[string]any)["id"] != attachment {
		t.Fatalf("receipt comparison lost exact values: %v", snapshot)
	}
	for _, query := range []string{"UPDATE shopping.checkouts SET item_count=1", "DELETE FROM shopping.checkouts"} {
		if _, err = f.pool.Exec(f.ctx, query); err == nil {
			t.Fatal("runtime mutated immutable completion")
		}
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, purge := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'pantry',$3,$4,$5)", purge, f.identity.HouseholdID, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, list, value.UUID(), value.UUID(), input)
	if err != nil || replay["id"] != first["id"] {
		t.Fatal("durable replay depended on short-lived receipts or destination generation")
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM pantry.lots").Scan(&lots); err != nil || lots != 0 {
		t.Fatal("retry recreated stock after a real pantry purge")
	}
	input.ReceiptTotal = "6.21"
	if _, err = f.app.Store.CheckoutShopping(f.ctx, f.identity, list, value.UUID(), value.UUID(), input); err == nil {
		t.Fatal("altered completion payload was accepted")
	}
	preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, list)
	if err != nil || preview["completed"] == nil {
		t.Fatal("completed purchase history disappeared")
	}
}

func TestShoppingCheckoutPopulatedUpgradeAndBounds(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		switch schema {
		case "core":
			version = 9
		case "shopping":
			version = 3
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hid, uid, list := value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior checkout release')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-checkout','Prior checkout','fixture-no-login')", []any{uid}},
		{"INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Preserved open list',$3)", []any{list, hid, uid}},
		{"INSERT INTO shopping.items(household_id,list_id,name,quantity,unit_code,created_by) VALUES($1,$2,'Exact amount',9007199254.740993,'g',$3)", []any{hid, list, uid}},
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
	var exact bool
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT quantity=9007199254.740993 FROM shopping.items WHERE list_id=$1)
 AND (SELECT archived_at IS NULL AND version=1 FROM shopping.lists WHERE id=$1)
 AND NOT EXISTS(SELECT 1 FROM shopping.checkouts)`, list).Scan(&exact); err != nil || !exact {
		t.Fatal("upgrade changed an existing purchase or invented a completion")
	}
}

func TestShoppingCheckoutCompleteSelectionBound(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, count := range []int{500, 501} {
		list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Bounded review"})
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO shopping.items(household_id,list_id,name,created_by)
 SELECT $1,$2,'Unpurchased '||n,$3 FROM generate_series(1,$4::integer) n`, f.identity.HouseholdID, list["id"], f.identity.UserID, count); err != nil {
			t.Fatal(err)
		}
		preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, store.String(list["id"]))
		if count == 501 {
			var invalid store.Invalid
			if !errors.As(err, &invalid) {
				t.Fatal("oversized selection silently truncated")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		input := store.CheckoutInput{Generation: f.app.Store.States()["shopping"].Generation, Fingerprint: store.String(preview["fingerprint"]), ReceivedOn: "2026-01-01"}
		for _, item := range preview["items"].([]store.Record) {
			input.Lines = append(input.Lines, store.CheckoutLine{ItemID: store.String(item["id"]), Decision: "skip"})
		}
		if _, err = f.app.Store.CheckoutShopping(f.ctx, f.identity, store.String(list["id"]), value.UUID(), value.UUID(), input); err != nil {
			t.Fatalf("complete maximum-size explicit selection rejected: %v", err)
		}
	}
}

func TestShoppingCheckoutGuardsAndRollback(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, kind := range []string{"stale-item", "stale-product", "stale-location", "stale-generation", "missing-receipt", "foreign-receipt", "missing-line", "duplicate-line", "integer", "rounding", "overflow", "future", "locked-child", "reader", "unchecked", "late-invalid"} {
		t.Run(kind, func(t *testing.T) {
			list, input := checkoutFixture(t, f)
			id := f.identity
			switch kind {
			case "stale-item":
				_, _ = f.owner.Exec(f.ctx, "UPDATE shopping.items SET quantity=3,version=version+1 WHERE id=$1", input.Lines[0].ItemID)
			case "stale-product":
				input.Lines[0].ProductVersion++
			case "stale-location":
				input.Lines[0].LocationVersion++
			case "stale-generation":
				input.PantryGeneration++
			case "missing-receipt":
				input.ReceiptID = value.UUID()
			case "foreign-receipt":
				input.ReceiptID = value.UUID()
				_, err := f.owner.Exec(f.ctx, `INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by)
 VALUES($1,$2,'shopping',1,'items',$3,($1::uuid)::text,'Wrong.pdf','application/pdf',42,decode(repeat('ab',32),'hex'),$4)`, input.ReceiptID, f.identity.HouseholdID, input.Lines[0].ItemID, f.identity.UserID)
				if err != nil {
					t.Fatal(err)
				}
			case "missing-line":
				input.Lines = nil
			case "duplicate-line":
				input.Lines = append(input.Lines, input.Lines[0])
			case "integer":
				_, _ = f.owner.Exec(f.ctx, "UPDATE pantry.products SET integer_quantities=true WHERE id=$1", input.Lines[0].ProductID)
				input.Lines[0].Quantity, input.Lines[0].Unit = "0.5", "g"
			case "rounding":
				_, _ = f.owner.Exec(f.ctx, "UPDATE pantry.products SET unit_code='kg' WHERE id=$1", input.Lines[0].ProductID)
				input.Lines[0].Quantity, input.Lines[0].Unit = "0.000001", "g"
			case "overflow":
				input.Lines[0].Quantity = "999999999999"
			case "future":
				input.ReceivedOn = "2200-01-01"
			case "reader":
				id.Role = "reader"
			case "unchecked":
				_, _ = f.owner.Exec(f.ctx, "UPDATE shopping.items SET checked_at=NULL,checked_by=NULL WHERE id=$1", input.Lines[0].ItemID)
			case "late-invalid":
				second, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list, "name": "Later unchecked"})
				if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET checked_at=now(),checked_by=$2 WHERE id=$1", second["id"], f.identity.UserID); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unchecked" || kind == "late-invalid" {
				preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, list)
				if err != nil {
					t.Fatal(err)
				}
				input.Fingerprint = store.String(preview["fingerprint"])
				if kind == "late-invalid" {
					items := preview["items"].([]store.Record)
					valid := input.Lines[0]
					valid.ItemID = store.String(items[0]["id"])
					invalid := valid
					invalid.ItemID, invalid.Quantity = store.String(items[1]["id"]), "0"
					input.Lines = []store.CheckoutLine{valid, invalid}
				}
			}
			if kind == "locked-child" {
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(f.ctx) }()
				if _, err = tx.Exec(f.ctx, "SELECT id FROM shopping.items WHERE id=$1 FOR UPDATE", input.Lines[0].ItemID); err != nil {
					t.Fatal(err)
				}
			}
			requestID := value.UUID()
			if _, err := f.app.Store.CheckoutShopping(f.ctx, id, list, value.UUID(), requestID, input); err == nil {
				t.Fatal("invalid checkout accepted")
			}
			var dirty bool
			if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM shopping.checkouts WHERE list_id=$1)
 OR EXISTS(SELECT 1 FROM pantry.lots) OR EXISTS(SELECT 1 FROM core.resource_links WHERE relation='shopping_stock')
 OR EXISTS(SELECT 1 FROM core.audit_log WHERE request_id=$2)
 OR EXISTS(SELECT 1 FROM shopping.lists WHERE id=$1 AND archived_at IS NOT NULL)`, list, requestID).Scan(&dirty); err != nil || dirty {
				t.Fatalf("rejected operation left effects: %v/%v", dirty, err)
			}
		})
	}
}

func TestShoppingCheckoutHTTPAndWithoutPantry(t *testing.T) {
	f := newIntegrationFixture(t)
	list, input := checkoutFixture(t, f)
	input.Lines[0] = store.CheckoutLine{ItemID: input.Lines[0].ItemID, Decision: "no_stock", Quantity: "2", Unit: "piece"}
	input.PantryGeneration = 0
	if err := f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	url := "/api/v1/shopping/lists/" + list + "/checkout"
	for _, path := range []string{url, "/m/shopping/lists/" + list + "/checkout"} {
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("checkout read: %s/%v", raw, err)
		}
	}
	r, _, err := f.request(http.MethodGet, url, "", "", "", nil, nil)
	if err != nil || r.StatusCode == 200 {
		t.Fatal("anonymous checkout read accepted")
	}
	raw, _ := json.Marshal(input)
	r, _, err = f.request(http.MethodPost, url, f.token, "", "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || r.StatusCode != 403 {
		t.Fatal("checkout without CSRF accepted")
	}
	r, body, err := f.request(http.MethodPost, url, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(body), `"actual_unknown":1`) || !strings.Contains(string(body), `"receipt":null`) {
		t.Fatalf("independent shopping checkout lost unknown values: %s/%v", body, err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "UPDATE shopping.checkouts SET item_count=1"); err == nil {
		t.Fatal("runtime changed completion")
	}
	input.Generation++
	if _, err = f.app.Store.CheckoutShopping(f.ctx, f.identity, list, value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale shopping generation replay accepted")
	}
}
