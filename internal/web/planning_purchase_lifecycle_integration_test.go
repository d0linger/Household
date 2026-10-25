//go:build integration

package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func TestPlanningPurchaseManagedMergeBothDirectionsAndDeletedItem(t *testing.T) {
	f, ids, _ := purchaseSchemaRegister(t)
	listID, itemID := purchaseSchemaManage(t, f, ids)
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Explicit merge variant", "pack_quantity": "1", "pack_unit": "kg"})
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE shopping.items SET product_id=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1", itemID, product["id"]); err != nil {
		t.Fatal(err)
	}
	other, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": listID, "product_id": product["id"], "name": "Independent compatible position", "quantity": "0.1", "unit_code": "kg"})
	otherID := store.String(other["id"])
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	for _, tc := range []struct {
		name, keep, remove string
		version, other     int64
	}{{"managed survivor", itemID, otherID, 2, 1}, {"managed removed participant", otherID, itemID, 1, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/v1/shopping/items/" + tc.keep + "/merge-preview?other=" + tc.remove
			response, body, err := f.request(http.MethodGet, url, f.token, "", "", nil, nil)
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"compatible":false`) || !strings.Contains(string(body), "Mahlzeitenplanung") {
				t.Fatal("typed relationship was omitted from merge preview", string(body), err)
			}
			_, err = f.app.Store.Action(f.ctx, f.identity, m, e, tc.keep, "merge", value.UUID(), value.UUID(),
				auditMutation(t, f, "shopping", store.Record{"other_id": tc.remove, "other_version": tc.other}, tc.version))
			var invalid store.Invalid
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "Mahlzeitenplanung") {
				t.Fatal("managed merge bypassed its typed contract", err)
			}
		})
	}
	var count int
	var pending string
	if err := f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM shopping.items WHERE list_id=$1),pending_quantity::text FROM core.planning_purchase_requirements WHERE id=$2", listID, ids[0]).Scan(&count, &pending); err != nil || count != 2 || pending != "0.200000" {
		t.Fatal("rejected merge changed either participant or contribution", count, pending, err)
	}
	if err := f.app.Store.Delete(f.ctx, f.identity, m, e, itemID, 2, f.app.Store.States()["shopping"].Generation, value.UUID()); err != nil {
		t.Fatal("actual user deletion must invalidate only its destination", err)
	}
	var active, missing bool
	var proofs int
	if err := f.pool.QueryRow(f.ctx, `SELECT active,procurement_disposition='missing' AND review_reason='item_missing' AND pending_quantity=0 AND item_id IS NULL,
 (SELECT count(*) FROM core.planning_purchase_operations) FROM core.planning_purchase_requirements WHERE id=$1`, ids[0]).Scan(&active, &missing, &proofs); err != nil || !active || !missing || proofs != 2 {
		t.Fatal("ordinary deletion erased the active source or durable proof", active, missing, proofs, err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || view.Ready || len(view.Requirements) != 2 || len(view.Issues) == 0 {
		t.Fatal("missing contribution became permission to recreate a purchase", view, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE id=$1", otherID).Scan(&count); err != nil || count != 1 {
		t.Fatal("unrelated independent position was removed", count, err)
	}
}

func TestPlanningPurchaseActualModulePurgesPreserveMinimalProof(t *testing.T) {
	for _, moduleID := range []string{"mealplan", "pantry", "shopping"} {
		t.Run(moduleID, func(t *testing.T) {
			f, ids, _, _, _, listID, in := planningCheckoutFixture(t, "store", "0.2", false)
			if _, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, listID, value.UUID(), value.UUID(), in); err != nil {
				t.Fatal(err)
			}
			// Keep one stored historical checkout and one current managed purchase.
			purchaseSchemaManage(t, f, []string{ids[1], ids[0]})
			state := f.app.Store.States()[moduleID]
			if err := f.app.Store.SetModule(f.ctx, f.identity, moduleID, false, state.Version, value.UUID()); err != nil {
				t.Fatal(err)
			}
			backup, request := value.UUID(), value.UUID()
			// Synthetic backup metadata authorizes the real isolated maintenance path.
			// This fixture does not claim to execute an encrypted backup or restore.
			if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'fixture','isolated-test',$2)`, backup, "snapshot-"+backup); err != nil {
				t.Fatal(err)
			}
			if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,$3,$4,$5,$6)`, request, f.identity.HouseholdID, moduleID, state.Generation, f.identity.UserID, backup); err != nil {
				t.Fatal(err)
			}
			if err := maintenance.Purge(f.ctx, f.owner, request); err != nil {
				t.Fatal("actual module purge", err)
			}
			var proofs, receipts, requirements, items, lots, active, pending, targets, history int
			err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_operations),
 (SELECT count(*) FROM shopping.planning_purchase_receipts),(SELECT count(*) FROM core.planning_purchase_requirements),
 (SELECT count(*) FROM shopping.items),(SELECT count(*) FROM pantry.lots),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE pending_quantity>0),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE product_id IS NOT NULL),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE last_checkout_id IS NOT NULL)`).
				Scan(&proofs, &receipts, &requirements, &items, &lots, &active, &pending, &targets, &history)
			if err != nil || proofs != 4 {
				t.Fatal("module purge erased durable request proof", proofs, err)
			}
			switch moduleID {
			case "mealplan":
				if receipts != 4 || requirements != 0 || items != 2 || lots != 2 {
					t.Fatal("source purge crossed independent module boundaries", receipts, requirements, items, lots)
				}
			case "pantry":
				if receipts != 4 || requirements != 2 || active != 2 || items != 2 || lots != 0 || pending != 1 || targets != 0 || history != 1 {
					t.Fatal("target purge lost activity/purchases or retained Pantry payload", receipts, requirements, active, items, lots, pending, targets, history)
				}
			case "shopping":
				if receipts != 0 || requirements != 2 || active != 2 || items != 0 || lots != 2 || pending != 0 || targets != 2 || history != 0 {
					t.Fatal("destination purge lost source activity or retained purchase payload", receipts, requirements, active, items, lots, pending, targets, history)
				}
			}
			if err = database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("purge damaged schema readiness", err)
			}
		})
	}
}
