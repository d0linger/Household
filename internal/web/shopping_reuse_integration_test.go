//go:build integration

package web

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func TestShoppingTemplatePopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		switch schema {
		case "core":
			migratePriorAuditSchema(t, f, schema, 6)
		case "shopping":
			migratePriorAuditSchema(t, f, schema, 2)
		default:
			migratePriorAuditSchema(t, f, schema)
		}
	}
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal(err)
	}
	hid, uid, list, product := value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior reuse release')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-reuse','Prior reuse','fixture-no-login')", []any{uid}},
		{"INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES($1,$2,'Working list',$3)", []any{list, hid, uid}},
		{"INSERT INTO shopping.products(id,household_id,name,pack_quantity,pack_unit,created_by) VALUES($1,$2,'Exact pack',9007199254.740993,'g',$3)", []any{product, hid, uid}},
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
	var unchanged bool
	if err := f.pool.QueryRow(f.ctx, `SELECT NOT l.is_template AND l.version=1 AND NOT p.favorite AND p.version=1 AND p.pack_quantity=9007199254.740993
 FROM shopping.lists l JOIN shopping.products p ON p.household_id=l.household_id WHERE l.id=$1 AND p.id=$2`, list, product).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("reuse migration changed existing records: %v/%v", unchanged, err)
	}
	var policies, events int
	if err := f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.audit_policies),(SELECT count(*) FROM core.audit_log WHERE resource_id=$1 AND action='CREATE')", product).Scan(&policies, &events); err != nil || policies != 73 || events != 1 {
		t.Fatalf("reuse migration lost audit history: %d/%d/%v", policies, events, err)
	}
}

func TestShoppingTemplateCopyAndFavorites(t *testing.T) {
	f := newIntegrationFixture(t)
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Tea", "brand": "Green", "favorite": true})
	other, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Tea", "brand": "Other"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Weekly", "is_template": true, "description": "Template note"})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Tea", "product_id": product["id"], "replacement_product_id": other["id"], "availability": "substituted", "assignee_id": f.identity.MembershipID, "quantity": "9007199254.740993", "unit_code": "piece", "note": "Remember", "group_name": "Pantry"})
	if _, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET checked_at=now(),checked_by=$2 WHERE id=$1", item["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("lists")
	mutation := auditMutation(t, f, "shopping", store.Record{"name": "This week"}, 1)
	key := value.UUID()
	copy, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", key, value.UUID(), mutation)
	if err != nil || copy["id"] == list["id"] || copy["is_template"] != false || copy["url"] != "/m/shopping/lists/"+store.String(copy["id"]) {
		t.Fatalf("independent list copy: %v/%v", copy, err)
	}
	replayed, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", key, value.UUID(), mutation)
	if err != nil || replayed["id"] != copy["id"] {
		t.Fatal("copy replay duplicated destination")
	}
	for _, rejected := range []store.Mutation{auditMutation(t, f, "shopping", store.Record{"name": "Changed payload"}, 1), auditMutation(t, f, "shopping", store.Record{"name": "Stale version"}, 0)} {
		requestKey := key
		if rejected.Version == 0 {
			requestKey = value.UUID()
		}
		if _, err = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", requestKey, value.UUID(), rejected); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("copy replay/stale version was accepted: %v", err)
		}
	}
	var lists, children, semanticEvents int
	if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM shopping.lists),(SELECT count(*) FROM shopping.items),(SELECT count(*) FROM core.audit_log WHERE action='clone')").Scan(&lists, &children, &semanticEvents); err != nil || lists != 2 || children != 2 || semanticEvents != 1 {
		t.Fatal("replayed or rejected copy created extra rows or audit events")
	}
	if _, err = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"name": strings.Repeat("Ä", 120)}, 1)); err != nil {
		t.Fatalf("valid 120-character copy name rejected: %v", err)
	}
	_, err = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"name": strings.Repeat("Ä", 121)}, 1))
	var invalidName store.Invalid
	if !errors.As(err, &invalidName) {
		t.Fatalf("overlong copy name did not produce a field-level validation result: %v", err)
	}
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM shopping.lists").Scan(&lists); err != nil || lists != 3 {
		t.Fatal("invalid name left a partial list")
	}
	var child, quantity, availability, note string
	var reset, searchable bool
	err = f.owner.QueryRow(f.ctx, `SELECT id,quantity::text,availability,note,
 checked_at IS NULL AND checked_by IS NULL AND assignee_id IS NULL AND replacement_product_id IS NULL,
 EXISTS(SELECT 1 FROM core.search_documents d WHERE d.resource_id=t.id AND d.module_id='shopping' AND d.resource_type='items')
 FROM shopping.items t WHERE list_id=$1`, copy["id"]).Scan(&child, &quantity, &availability, &note, &reset, &searchable)
	if err != nil || child == item["id"] || quantity != "9007199254.740993" || availability != "available" || note != "Remember" || !reset || !searchable {
		t.Fatalf("copy changed precision, retained history or missed projection: %s/%s/%s/%v/%v/%v", child, quantity, availability, reset, searchable, err)
	}
	items, _ := m.Entity("items")
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, items, store.String(item["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"name": "Template changed"}, 1)); err != nil {
		t.Fatal(err)
	}
	var unchanged bool
	if err = f.owner.QueryRow(f.ctx, "SELECT name='Tea' FROM shopping.items WHERE id=$1", child).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("template edit mutated an earlier copy")
	}
	for _, path := range []string{"/api/v1/core/reference-options/shopping/lists", "/api/v1/m/shopping/lists"} {
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || r.StatusCode != 200 || strings.Contains(string(raw), store.String(list["id"])) || !strings.Contains(string(raw), store.String(copy["id"])) {
			t.Fatalf("working list choices mixed templates: %s/%v", raw, err)
		}
	}
	r, raw, err := f.request(http.MethodGet, "/api/v1/core/reference-options/shopping/products?favorites=1", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), store.String(product["id"])) || strings.Contains(string(raw), store.String(other["id"])) {
		t.Fatal("favorite choices include an unselected product")
	}
	for _, action := range []string{"check", "activity"} {
		entity, resource, data := items, store.String(item["id"]), store.Record{"checked": true}
		version := int64(2)
		if action == "activity" {
			entity, resource, data, version = e, store.String(list["id"]), store.Record{"active": true}, 1
		}
		_, err = f.app.Store.Action(f.ctx, f.identity, m, entity, resource, action, value.UUID(), value.UUID(), auditMutation(t, f, "shopping", data, version))
		var invalid store.Invalid
		if !errors.As(err, &invalid) {
			t.Fatalf("template accepts shopping activity %s: %v", action, err)
		}
	}
}

func TestShoppingTemplateCopyLimitsAndContention(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Large template", "is_template": true})
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO shopping.items(household_id,list_id,name,created_by) SELECT $1,$2,'Item '||n,$3 FROM generate_series(1,501) n", f.identity.HouseholdID, list["id"], f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("lists")
	mutation := auditMutation(t, f, "shopping", store.Record{"name": "Rejected copy"}, 1)
	_, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", value.UUID(), value.UUID(), mutation)
	var invalid store.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("oversized copy accepted: %v", err)
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM shopping.lists WHERE household_id=$1", f.identity.HouseholdID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed bounded copy left a partial destination")
	}
	lock, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(f.ctx) }()
	if _, err = lock.Exec(f.ctx, "SELECT id FROM shopping.items WHERE list_id=$1 ORDER BY id LIMIT 1 FOR UPDATE", list["id"]); err != nil {
		t.Fatal(err)
	}
	_, err = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", value.UUID(), value.UUID(), mutation)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("copy waited on a competing child lock: %v", err)
	}
	if err = lock.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM shopping.items WHERE id=(SELECT id FROM shopping.items WHERE list_id=$1 ORDER BY id LIMIT 1)", list["id"]); err != nil {
		t.Fatal(err)
	}
	copy, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(list["id"]), "clone", value.UUID(), value.UUID(), mutation)
	if err != nil {
		t.Fatalf("exactly 500 source items rejected: %v", err)
	}
	var projections, audited int
	err = f.owner.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER(WHERE EXISTS(SELECT 1 FROM core.search_documents d WHERE d.resource_id=i.id AND d.module_id='shopping' AND d.resource_type='items')),
 count(*) FILTER(WHERE EXISTS(SELECT 1 FROM core.audit_log a WHERE a.resource_id=i.id AND a.action='CREATE')) FROM shopping.items i WHERE i.list_id=$1`, copy["id"]).Scan(&count, &projections, &audited)
	if err != nil || count != 500 || projections != 500 || audited != 500 {
		t.Fatalf("bounded copy missed item projections or audit: %d/%d/%d/%v", count, projections, audited, err)
	}
}
