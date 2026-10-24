//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

func TestShoppingCatalogPricingAndIntegrity(t *testing.T) {
	f := newIntegrationFixture(t)
	create := func(entity string, data store.Record) store.Record {
		r, _ := auditCreate(t, f, "shopping", entity, data)
		return r
	}
	shop := create("stores", store.Record{"name": "Nearby shop"})
	create("aisles", store.Record{"name": "Z", "store_id": shop["id"], "sort_order": 10})
	create("aisles", store.Record{"name": "A", "store_id": shop["id"], "sort_order": 20})
	list := create("lists", store.Record{"name": "Catalog list", "store_id": shop["id"]})
	product := create("products", store.Record{"name": "Pasta", "brand": "A", "pack_quantity": "500", "pack_unit": "g", "note": "private note"})
	replacement := create("products", store.Record{"name": "Pasta", "brand": "B", "pack_quantity": "1", "pack_unit": "kg"})
	for _, p := range []struct{ product, date, amount any }{{product["id"], "2020-01-01", "1.00"}, {product["id"], "2199-01-01", "9.00"}, {replacement["id"], "2020-02-01", "3.00"}} {
		create("price_observations", store.Record{"product_id": p.product, "store_id": shop["id"], "observed_on": p.date, "quantity": "1", "unit_code": "piece", "price_minor": p.amount})
	}
	for _, fields := range []store.Record{
		{"name": "Mass", "quantity": "1.5", "unit_code": "kg", "group_name": "Z"},
		{"name": "Packs", "quantity": "2", "unit_code": "piece", "group_name": "A"},
		{"name": "Small amount", "quantity": "100", "unit_code": "g", "group_name": "A"},
		{"name": "Substitute", "quantity": "500", "unit_code": "g", "availability": "substituted", "replacement_product_id": replacement["id"], "group_name": "Z"},
		{"name": "No quantity"},
		{"name": "Free item", "quantity": "1", "product_id": nil},
		{"name": "Unavailable", "quantity": "1000", "availability": "unavailable"},
	} {
		fields["list_id"] = list["id"]
		if _, exists := fields["product_id"]; !exists {
			fields["product_id"] = product["id"]
		}
		create("items", fields)
	}
	board := shoppingTestBoard(t, f, store.String(list["id"]))
	context := board["context"].(map[string]any)
	if store.String(context["estimate_minor"]) != "670" || store.Int(context["unknown"]) != 2 || store.Int(context["unavailable"]) != 1 || store.Int(board["total"]) != 7 {
		t.Fatalf("incorrect exact estimate: %v", context)
	}
	if context["oldest_price"] != "2020-01-01" || context["newest_price"] != "2020-02-01" {
		t.Fatalf("future or incorrect price provenance: %v", context)
	}
	rows := board["records"].([]any)
	if rows[0].(map[string]any)["group_name"] != "Z" || rows[1].(map[string]any)["group_name"] != "Z" {
		t.Fatal("store aisle order ignored")
	}
	selected := rows[0].(map[string]any)
	selectedPath := "/api/v1/shopping/items/" + store.String(selected["id"])
	response, raw, err := f.request(http.MethodGet, selectedPath, f.token, "", "", nil, nil)
	var current store.Record
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &current) != nil || current["product"] == nil || current["quantity"] != selected["quantity"] {
		t.Fatalf("conflict item lost exact quantity or named product: %s/%v", raw, err)
	}
	response, _, err = f.request(http.MethodGet, selectedPath, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("anonymous conflict item access")
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/shopping/items/"+value.UUID(), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("unknown conflict item access")
	}
	response, raw, err = f.request(http.MethodGet, "/api/v1/core/reference-options/shopping/products?q=Pasta", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "Pasta · A · 500 g") || !strings.Contains(string(raw), "Pasta · B · 1 kg") || strings.Contains(string(raw), "private note") {
		t.Fatalf("ambiguous or excessive catalog labels: %s/%v", raw, err)
	}
	m, _ := f.app.Store.Module("shopping")
	items, _ := m.Entity("items")
	for _, data := range []store.Record{
		{"name": "Wrong dimension", "product_id": product["id"], "unit_code": "ml"},
		{"name": "Missing substitute", "availability": "substituted"},
		{"name": "Same substitute", "product_id": product["id"], "availability": "substituted", "replacement_product_id": product["id"]},
		{"name": "Foreign assignment", "assignee_id": value.UUID()},
	} {
		data["list_id"] = list["id"]
		_, err = f.app.Store.Write(f.ctx, f.identity, m, items, "", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", data, 0))
		var invalid store.Invalid
		if !errors.As(err, &invalid) {
			t.Fatalf("invalid catalog input accepted: %v/%v", data, err)
		}
	}
	products, _ := m.Entity("products")
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, products, store.String(product["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"pack_quantity": "600"}, 1)); err == nil {
		t.Fatal("used pack identity changed")
	}
	member := shoppingTestMember(t, f, "inactive-catalog", "editor")
	assigned := create("items", store.Record{"list_id": list["id"], "name": "Assigned", "assignee_id": member.MembershipID})
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=now() WHERE id=$1", member.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, items, store.String(assigned["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"name": "Still assigned", "assignee_id": member.MembershipID}, 1)); err != nil {
		t.Fatalf("unchanged historic assignment blocked ordinary edit: %v", err)
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, items, "", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"list_id": list["id"], "name": "New inactive assignment", "assignee_id": member.MembershipID}, 0)); err == nil {
		t.Fatal("inactive member newly assigned")
	}
	response, raw, err = f.request(http.MethodGet, "/m/shopping/items/"+store.String(assigned["id"]), f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "nicht mehr aktiv") {
		t.Fatal("historic assignment not retained in editor")
	}
	stores, _ := m.Entity("stores")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, stores, store.String(shop["id"]), 1, 1, value.UUID()); err == nil {
		t.Fatal("referenced store deleted")
	}
}

func TestShoppingActivityConcurrencyAndExpiry(t *testing.T) {
	f := newIntegrationFixture(t)
	member := shoppingTestMember(t, f, "second-shopper", "editor")
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Activity"})
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("lists")
	claim := auditMutation(t, f, "shopping", store.Record{"active": true, "shopper_id": member.MembershipID, "shopper_until": "2199-01-01"}, 1)
	key := value.UUID()
	first, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "activity", key, value.UUID(), claim)
	if err != nil || first["shopper_id"] != f.identity.MembershipID {
		t.Fatalf("caller-controlled claimant accepted: %v/%v", first, err)
	}
	replay, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "activity", key, value.UUID(), claim)
	if err != nil || replay["shopper_until"] != first["shopper_until"] || store.Int(replay["version"]) != 2 {
		t.Fatal("activity replay extended or duplicated claim")
	}
	var bounded bool
	if err = f.owner.QueryRow(f.ctx, "SELECT shopper_until BETWEEN now()+interval '29 minutes' AND now()+interval '30 minutes' FROM shopping.lists WHERE id=$1", list["id"]).Scan(&bounded); err != nil || !bounded {
		t.Fatal("unbounded activity lease")
	}
	for _, active := range []bool{true, false} {
		_, err = f.app.Store.Action(f.ctx, member, m, e, store.String(list["id"]), "activity", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"active": active}, 2))
		if err == nil {
			t.Fatal("another editor replaced or released live claim")
		}
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE shopping.lists SET shopper_until=now()-interval '1 second' WHERE id=$1", list["id"]); err != nil {
		t.Fatal(err)
	}
	if shoppingTestBoard(t, f, store.String(list["id"]))["context"].(map[string]any)["shopper_name"] != "" {
		t.Fatal("expired shopper still displayed")
	}
	var group sync.WaitGroup
	errorsChannel := make(chan error, 2)
	for _, actor := range []auth.Identity{f.identity, member} {
		group.Go(func() {
			_, actionErr := f.app.Store.Action(f.ctx, actor, m, e, store.String(list["id"]), "activity", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"active": true}, 2))
			errorsChannel <- actionErr
		})
	}
	group.Wait()
	close(errorsChannel)
	successes, conflicts := 0, 0
	for actionErr := range errorsChannel {
		if actionErr == nil {
			successes++
		} else if errors.Is(actionErr, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(actionErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("competing shoppers were not serialized: success=%d conflict=%d", successes, conflicts)
	}
}

func shoppingTestMember(t *testing.T, f *integrationFixture, username, role string) auth.Identity {
	t.Helper()
	uid := value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) SELECT $1,$2,$2,password_hash,false FROM core.users WHERE id=$3", uid, username, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,$3)", f.identity.HouseholdID, uid, role); err != nil {
		t.Fatal(err)
	}
	_, member, err := f.app.Auth.Login(f.ctx, username, integrationPassword, "", "127.0.0.1", "catalog-test")
	if err != nil {
		t.Fatal(err)
	}
	return member
}

func shoppingTestBoard(t *testing.T, f *integrationFixture, id string) store.Record {
	t.Helper()
	response, raw, err := f.request(http.MethodGet, "/api/v1/shopping/lists/"+id+"/items", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("board request: %d %s/%v", response.StatusCode, raw, err)
	}
	result, err := store.DecodeRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
