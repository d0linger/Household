//go:build integration

package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestShoppingMergeExactReplayAndPreview(t *testing.T) {
	f := newIntegrationFixture(t)
	p, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Flour", "pack_quantity": "500", "pack_unit": "g"})
	l, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Merge list"})
	a, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": l["id"], "product_id": p["id"], "name": "Flour kept", "quantity": "2", "unit_code": "piece", "note": "Same note"})
	b, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": l["id"], "product_id": p["id"], "name": "Flour removed", "quantity": "1", "unit_code": "kg", "note": "Same note"})
	url := "/api/v1/shopping/items/" + store.String(a["id"]) + "/merge-preview?other=" + store.String(b["id"])
	response, body, err := f.request(http.MethodGet, url, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"quantity":"4.000000"`) || !strings.Contains(string(body), `"compatible":true`) {
		t.Fatalf("exact preview: %s/%v", body, err)
	}
	response, _, err = f.request(http.MethodGet, url, "", "", "", nil, nil)
	if err != nil || response.StatusCode == 200 {
		t.Fatal("anonymous merge preview disclosed")
	}
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	mutation := auditMutation(t, f, "shopping", store.Record{"other_id": b["id"], "other_version": 1}, 1)
	key := value.UUID()
	type result struct {
		record store.Record
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			r, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(a["id"]), "merge", key, value.UUID(), mutation)
			results <- result{r, err}
		}()
	}
	close(start)
	for range 2 {
		result := <-results
		if result.err != nil || store.String(result.record["quantity"]) != "4.000000" || store.Int(result.record["version"]) != 2 {
			t.Fatalf("concurrent merge/replay: %v/%v", result.record, result.err)
		}
	}
	var amount string
	var count, projections, semantic, deleted int
	err = f.owner.QueryRow(f.ctx, `SELECT (SELECT quantity::text FROM shopping.items WHERE id=$1),(SELECT count(*) FROM shopping.items WHERE list_id=$2),
 (SELECT count(*) FROM core.search_documents WHERE module_id='shopping' AND resource_type='items' AND resource_id IN($1,$3)),
 (SELECT count(*) FROM core.audit_log WHERE action='merge' AND resource_id=$1),(SELECT count(*) FROM core.audit_log WHERE action='DELETE' AND resource_id=$3)`, a["id"], l["id"], b["id"]).Scan(&amount, &count, &projections, &semantic, &deleted)
	if err != nil || amount != "4.000000" || count != 1 || projections != 1 || semantic != 1 || deleted != 1 {
		t.Fatalf("merge audit/projection/atomicity: %s/%d/%d/%d/%d/%v", amount, count, projections, semantic, deleted, err)
	}
	if _, err = f.app.Store.Action(f.ctx, f.identity, m, e, store.String(a["id"]), "merge", value.UUID(), value.UUID(), mutation); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale merge accepted: %v", err)
	}
}

func TestShoppingMergeGuardsLeaveBothItems(t *testing.T) {
	f := newIntegrationFixture(t)
	p, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "One gram", "pack_quantity": "1", "pack_unit": "g"})
	otherProduct, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Different variant", "pack_quantity": "1", "pack_unit": "g"})
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	for _, kind := range []string{"other-version", "variant", "note", "assignment", "other-list", "template", "archived", "checked", "overflow", "rounding", "resource-link", "attachment", "locked-child", "locked-list"} {
		t.Run(kind, func(t *testing.T) {
			l, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Guard " + kind, "is_template": kind == "template"})
			a, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": l["id"], "product_id": p["id"], "name": "A", "quantity": "1", "unit_code": "g"})
			bData := store.Record{"list_id": l["id"], "product_id": p["id"], "name": "B", "quantity": "1", "unit_code": "g"}
			switch kind {
			case "variant":
				bData["product_id"] = otherProduct["id"]
			case "note":
				bData["note"] = "Must not lose"
			case "assignment":
				bData["assignee_id"] = f.identity.MembershipID
			case "other-list":
				other, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Other"})
				bData["list_id"] = other["id"]
			}
			b, _ := auditCreate(t, f, "shopping", "items", bData)
			switch kind {
			case "archived":
				_, err := f.owner.Exec(f.ctx, "UPDATE shopping.lists SET archived_at=now() WHERE id=$1", l["id"])
				if err != nil {
					t.Fatal(err)
				}
			case "checked":
				_, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET checked_at=now(),checked_by=$2 WHERE id=$1", b["id"], f.identity.UserID)
				if err != nil {
					t.Fatal(err)
				}
			case "overflow":
				_, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET quantity=999999999999.999999 WHERE id=$1", a["id"])
				if err != nil {
					t.Fatal(err)
				}
			case "rounding":
				_, err := f.owner.Exec(f.ctx, "UPDATE shopping.items SET unit_code='kg' WHERE id=$1", a["id"])
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.owner.Exec(f.ctx, "UPDATE shopping.items SET quantity=0.000001 WHERE id=$1", b["id"])
				if err != nil {
					t.Fatal(err)
				}
			case "resource-link":
				_, err := f.owner.Exec(f.ctx, "INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation) VALUES($1,'shopping','items',$2,'shopping','products',$3,'fixture')", f.identity.HouseholdID, b["id"], p["id"])
				if err != nil {
					t.Fatal(err)
				}
			case "attachment":
				_, err := f.owner.Exec(f.ctx, "INSERT INTO core.attachments(household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES($1,'shopping',1,'items',$2,$3,'fixture.pdf','application/pdf',1,decode(repeat('ab',32),'hex'),$4)", f.identity.HouseholdID, b["id"], value.UUID(), f.identity.UserID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "locked-child" || kind == "locked-list" {
				lock, err := f.owner.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Rollback(f.ctx) }()
				query, rid := "SELECT id FROM shopping.items WHERE id=$1 FOR UPDATE", b["id"]
				if kind == "locked-list" {
					query, rid = "SELECT id FROM shopping.lists WHERE id=$1 FOR UPDATE", l["id"]
				}
				if _, err = lock.Exec(f.ctx, query, rid); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "attachment" || kind == "resource-link" || kind == "template" || kind == "archived" {
				response, body, err := f.request(http.MethodGet, "/api/v1/shopping/items/"+store.String(a["id"])+"/merge-preview?other="+store.String(b["id"]), f.token, "", "", nil, nil)
				if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"compatible":false`) || !strings.Contains(string(body), `"reason":`) {
					t.Fatalf("preview concealed a known blocker: %s/%v", body, err)
				}
			}
			version := 1
			if kind == "other-version" {
				version = 0
			}
			_, err := f.app.Store.Action(f.ctx, f.identity, m, e, store.String(a["id"]), "merge", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"other_id": b["id"], "other_version": version}, 1))
			var invalid store.Invalid
			if !errors.Is(err, store.ErrConflict) && !errors.As(err, &invalid) {
				t.Fatalf("unsafe merge did not return a recoverable rejection: %v", err)
			}
			var unchanged bool
			if err = f.owner.QueryRow(f.ctx, "SELECT count(*)=2 AND bool_and(version=1) FROM shopping.items WHERE id IN($1,$2)", a["id"], b["id"]).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("guard left partial changes: %v/%v", unchanged, err)
			}
		})
	}
}
