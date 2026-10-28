//go:build integration

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func repurchasePurchase(t *testing.T, f *integrationFixture, product string, day string) string {
	t.Helper()
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Historical reviewed purchase"})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Actual purchased variant", "product_id": product, "quantity": "1", "unit_code": "piece"})
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	if _, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(item["id"]), "check", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"checked": true}, store.Int(item["version"]))); err != nil {
		t.Fatal(err)
	}
	preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, store.String(list["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.CheckoutShopping(f.ctx, f.identity, store.String(list["id"]), value.UUID(), value.UUID(), store.CheckoutInput{Generation: f.app.Store.States()["shopping"].Generation, Fingerprint: store.String(preview["fingerprint"]), ReceivedOn: day, Lines: []store.CheckoutLine{{ItemID: store.String(item["id"]), Decision: "no_stock", Quantity: "1", Unit: "piece"}}}); err != nil {
		t.Fatal(err)
	}
	return store.String(list["id"])
}

func repurchaseFixture(t *testing.T, f *integrationFixture) (store.Record, string, []string) {
	t.Helper()
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Local typed oats", "brand": "Reviewed brand", "pack_quantity": "500", "pack_unit": "g"})
	var today string
	if err := f.pool.QueryRow(f.ctx, "SELECT (transaction_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&today); err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.DateOnly, today)
	if err != nil {
		t.Fatal(err)
	}
	lists := []string{}
	for _, days := range []int{-21, -14, -7} {
		lists = append(lists, repurchasePurchase(t, f, store.String(product["id"]), now.AddDate(0, 0, days).Format(time.DateOnly)))
	}
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Manually reviewed replenishment"})
	return product, store.String(list["id"]), lists
}

func repurchaseInput(t *testing.T, f *integrationFixture, list string) store.RepurchaseInput {
	t.Helper()
	page, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list)
	if err != nil || len(page.Candidates) != 1 {
		t.Fatal("real purchases did not yield one explained candidate", page, err)
	}
	c := page.Candidates[0]
	return store.RepurchaseInput{Generation: page.Generation, ListVersion: page.ListVersion, ProductID: c.ProductID, ProductVersion: c.ProductVersion, Fingerprint: c.Fingerprint, Quantity: "2"}
}

func TestShoppingRepurchaseActualEvidenceAndExplicitIdempotentAdd(t *testing.T) {
	f := newIntegrationFixture(t)
	product, list, _ := repurchaseFixture(t, f)
	page, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list)
	if err != nil || len(page.Candidates) != 1 || page.Candidates[0].IntervalDays != 7 || !page.Candidates[0].Due || page.Checkouts != 3 {
		t.Fatal("wrong actual purchase interval", page, err)
	}
	if page.Candidates[0].ProductID != product["id"] || len(page.Candidates[0].Dates) != 3 {
		t.Fatal("wrong typed evidence")
	}
	var before int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE list_id=$1", list).Scan(&before); err != nil || before != 0 {
		t.Fatal(err, before)
	}
	input := repurchaseInput(t, f, list)
	key, request := value.UUID(), value.UUID()
	item, err := f.app.Store.AddRepurchase(f.ctx, f.identity, list, key, request, input)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, repeatErr := f.app.Store.AddRepurchase(f.ctx, f.identity, list, key, value.UUID(), input); repeatErr != nil || repeated["id"] != item["id"] {
		t.Fatal("actual retry duplicated or lost item", repeatErr)
	}
	var exact bool
	err = f.pool.QueryRow(f.ctx, `SELECT i.quantity=2 AND i.unit_code='piece' AND i.product_id=$2 AND i.checked_at IS NULL
 AND (SELECT count(*) FROM shopping.items WHERE household_id=i.household_id AND list_id=i.list_id)=1
 AND EXISTS(SELECT 1 FROM core.search_documents d WHERE d.module_id='shopping' AND d.resource_type='items' AND d.resource_id=i.id)
 AND EXISTS(SELECT 1 FROM core.outbox_events o WHERE o.module_id='shopping' AND o.payload->>'id'=i.id::text)
 AND EXISTS(SELECT 1 FROM core.audit_log a WHERE a.request_id=$3 AND a.resource_id=i.id AND a.action='record_write' AND a.db_role='household_app')
 AND NOT EXISTS(SELECT 1 FROM shopping.checkouts c WHERE c.household_id=i.household_id AND c.list_id=i.list_id)
 FROM shopping.items i WHERE i.id=$1`, item["id"], product["id"], request).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("manual item/projections/audit differ or purchase was automatic", err)
	}
	input.Fingerprint = strings.Repeat("a", 64)
	if _, err = f.app.Store.AddRepurchase(f.ctx, f.identity, list, value.UUID(), value.UUID(), input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale/already listed proposal was applied", err)
	}
}

func TestShoppingRepurchaseCurrentAuthorityListAndProductChecks(t *testing.T) {
	for _, damage := range []string{"reader", "session", "generation", "version", "product", "template", "archived", "completed reopened"} {
		t.Run(damage, func(t *testing.T) {
			f := newIntegrationFixture(t)
			product, list, history := repurchaseFixture(t, f)
			input := repurchaseInput(t, f, list)
			id := f.identity
			var err error
			switch damage {
			case "reader":
				id.Role = "reader"
			case "session":
				id.AuthVersion++
			case "generation":
				input.Generation++
			case "version":
				input.ListVersion++
			case "product":
				input.ProductVersion++
			case "template":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET is_template=true WHERE id=$1", list)
			case "archived":
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=now() WHERE id=$1", list)
			case "completed reopened":
				list = history[0]
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=NULL WHERE id=$1", list)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.app.Store.AddRepurchase(f.ctx, id, list, value.UUID(), value.UUID(), input); err == nil {
				t.Fatal("invalid current scope accepted", damage, product)
			}
			var count int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE list_id=$1 AND checked_at IS NULL", list).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed review wrote an item", count, err)
			}
		})
	}
}

func TestShoppingRepurchasePackChangeInsufficientHistoryAndIndependentModules(t *testing.T) {
	f := newIntegrationFixture(t)
	product, list, _ := repurchaseFixture(t, f)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id IN('expenses','pantry')", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if page, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list); err != nil || len(page.Candidates) != 1 {
		t.Fatal("unrelated module state blocked local purchases", err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.products SET pack_quantity=750,version=version+1 WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	page, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list)
	if err != nil || len(page.Candidates) != 0 || page.Insufficient != 1 {
		t.Fatal("old pack history was reused for changed pack", page, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='shopping'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled shopping exposed history", err)
	}
}

func TestShoppingRepurchaseBudgetAndMalformedPairingProduceNoPartialHistory(t *testing.T) {
	for _, damage := range []string{"budget", "pairing"} {
		t.Run(damage, func(t *testing.T) {
			f := newIntegrationFixture(t)
			_, list, history := repurchaseFixture(t, f)
			if damage == "budget" {
				var product string
				if err := f.pool.QueryRow(f.ctx, "SELECT id FROM shopping.products LIMIT 1").Scan(&product); err != nil {
					t.Fatal(err)
				}
				for _, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
					repurchasePurchase(t, f, product, day)
				}
				if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.checkouts SET snapshot=snapshot||jsonb_build_object('unrelated_private_field',repeat('X',3000000)) WHERE household_id=$1", f.identity.HouseholdID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.checkouts SET snapshot=jsonb_set(snapshot,'{lines,0,item_id}',to_jsonb($2::text)) WHERE list_id=$1", history[0], value.UUID()); err != nil {
				t.Fatal(err)
			}
			if page, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list); err == nil || len(page.Candidates) != 0 {
				t.Fatal("partial or corrupted evidence produced recommendations", page, err)
			}
		})
	}
}

func TestShoppingRepurchasePopulatedUpgradePreservesHistoryAndExactReady(t *testing.T) {
	f := newIntegrationFixture(t)
	fuelModuleUpgradePriorCatalog(t, f)
	_, list, _ := repurchaseFixture(t, f)
	var before string
	if err := f.owner.QueryRow(f.ctx, "SELECT jsonb_agg(to_jsonb(c)-'history_bytes' ORDER BY c.id)::text FROM shopping.checkouts c").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Restore Shopping7 under the genuine Fuel2/Core26 audit predecessor;
	// otherwise rerunning Shopping8 would collide with its still-present table.
	barcodeRestoreShopping7(t, f)
	for _, query := range []string{"DROP FUNCTION shopping.repurchase_ready()", "DROP INDEX shopping.checkouts_history", "DROP INDEX shopping.items_repurchase_product", "ALTER TABLE shopping.checkouts DROP COLUMN history_bytes", "UPDATE shopping.schema_migrations SET version=6"} {
		if _, err := f.owner.Exec(f.ctx, query); err != nil {
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
	var after string
	if err := f.owner.QueryRow(f.ctx, "SELECT jsonb_agg(to_jsonb(c)-'history_bytes' ORDER BY c.id)::text FROM shopping.checkouts c").Scan(&after); err != nil || before != after {
		t.Fatal("forward migration rewrote accepted history", err)
	}
	if _, err := f.app.Store.ShoppingRepurchase(f.ctx, f.identity, list); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "DROP INDEX shopping.checkouts_history"); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("missing bounded-order index passed Ready")
	}
	if _, err := f.owner.Exec(f.ctx, "CREATE INDEX checkouts_history ON shopping.checkouts(household_id,created_at DESC,id DESC)"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "DROP INDEX shopping.items_repurchase_product"); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("missing bounded product/list lookup passed Ready")
	}
}

func repurchaseSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var result string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM shopping.items i),
 'receipts',(SELECT jsonb_agg(to_jsonb(i) ORDER BY operation,key) FROM core.idempotency_records i),
 'audit',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM core.audit_log i),
 'search',(SELECT jsonb_agg(to_jsonb(i) ORDER BY module_id,resource_id) FROM core.search_documents i),
 'outbox',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM core.outbox_events i))::text`).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestShoppingRepurchaseLateFailureRollsBackAndKeyRemainsUsable(t *testing.T) {
	f := newIntegrationFixture(t)
	_, list, _ := repurchaseFixture(t, f)
	input := repurchaseInput(t, f, list)
	key := value.UUID()
	before := repurchaseSnapshot(t, f)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.repurchase_test_abort() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'late repurchase fixture abort' USING ERRCODE='23514';END $$;
 CREATE CONSTRAINT TRIGGER repurchase_test_abort AFTER INSERT ON core.idempotency_records DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.repurchase_test_abort()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.AddRepurchase(f.ctx, f.identity, list, key, value.UUID(), input); err == nil {
		t.Fatal("late failure committed")
	}
	if repurchaseSnapshot(t, f) != before {
		t.Fatal("late abort left item, audit, projection or receipt")
	}
	if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER repurchase_test_abort ON core.idempotency_records; DROP FUNCTION core.repurchase_test_abort()"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.AddRepurchase(f.ctx, f.identity, list, key, value.UUID(), input); err != nil {
		t.Fatal("failed transaction consumed its key", err)
	}
}

func TestShoppingRepurchaseContentionConcurrencyAndOneConnectionRead(t *testing.T) {
	f := newIntegrationFixture(t)
	_, list, _ := repurchaseFixture(t, f)
	input := repurchaseInput(t, f, list)
	single, err := database.Open(f.ctx, os.Getenv("HOUSEHOLD_TEST_APP_URL"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(single.Close)
	reader := &store.Store{Pool: single}
	if page, err := reader.ShoppingRepurchase(f.ctx, f.identity, list); err != nil || len(page.Candidates) != 1 {
		t.Fatal("one-connection read leaked open rows", err)
	}
	held, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(f.ctx) }()
	if _, err = held.Exec(f.ctx, "SELECT id FROM shopping.lists WHERE id=$1 FOR UPDATE", list); err != nil {
		t.Fatal(err)
	}
	before := repurchaseSnapshot(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	_, err = f.app.Store.AddRepurchase(ctx, f.identity, list, value.UUID(), value.UUID(), input)
	if !errors.Is(err, store.ErrConflict) || ctx.Err() != nil || repurchaseSnapshot(t, f) != before {
		cancel()
		t.Fatal("held list waited or left effects", err)
	}
	cancel()
	if err = held.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(f.ctx, 15*time.Second)
	type result struct {
		item store.Record
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case <-start:
			case <-ctx.Done():
				return
			}
			item, err := f.app.Store.AddRepurchase(ctx, f.identity, list, value.UUID(), value.UUID(), input)
			results <- result{item, err}
		}()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("repurchase workers did not stop before teardown")
		}
	})
	close(start)
	success := 0
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				success++
			} else if !errors.Is(result.err, store.ErrConflict) {
				t.Fatal(result.err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent review stalled")
		}
	}
	if success == 0 {
		if _, err = f.app.Store.AddRepurchase(f.ctx, f.identity, list, value.UUID(), value.UUID(), input); err != nil {
			t.Fatal("safe retry failed", err)
		}
		success++
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE list_id=$1", list).Scan(&count); err != nil || count != 1 || success != 1 {
		t.Fatal("concurrent manual review duplicated an item", count, success, err)
	}
}

func TestShoppingRepurchaseHTTPUsesCurrentSessionAndCSRF(t *testing.T) {
	f := newIntegrationFixture(t)
	_, list, _ := repurchaseFixture(t, f)
	path := "/api/v1/shopping/lists/" + list + "/repurchase"
	response, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("actual suggestion API failed", response, err)
	}
	var page store.RepurchasePage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Candidates) != 1 {
		t.Fatal("HTTP evidence missing")
	}
	response, _, err = f.request(http.MethodPost, path, f.token, "", "application/json", strings.NewReader(`{}`), nil)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", response, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err := f.app.Store.ShoppingRepurchase(ctx, f.identity, list); err == nil {
		t.Fatal("canceled evidence read succeeded")
	}
}

func TestShoppingRepurchaseReadyRejectsGeneratedAndExpressionDrift(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.owner.Exec(f.ctx, `DROP INDEX shopping.items_repurchase_product;
 CREATE INDEX items_repurchase_product ON shopping.items(household_id,list_id,(coalesce(product_id,replacement_product_id)))`); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("wrong effective-product expression passed Ready")
	}
	if _, err := f.owner.Exec(f.ctx, `DROP INDEX shopping.items_repurchase_product;
 CREATE INDEX items_repurchase_product ON shopping.items(household_id,list_id,(coalesce(replacement_product_id,product_id)))`); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "ALTER TABLE shopping.checkouts ALTER COLUMN history_bytes DROP EXPRESSION"); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("untrusted writable byte count passed Ready")
	}
}
