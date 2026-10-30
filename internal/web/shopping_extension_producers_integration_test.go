//go:build integration

package web

import (
	"errors"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestShoppingExtensionsPlanningDirectRemovalAndReplay(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Planning producer"})
	listID := store.String(list["id"])
	selected := []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}
	initial, _ := reviewedPurchaseInput(t, f, selected, listID)
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), initial); err != nil {
		t.Fatal(err)
	}
	var item, lot string
	if err := f.pool.QueryRow(f.ctx, "SELECT item_id,(SELECT id FROM pantry.lots WHERE product_id=$1) FROM core.planning_purchase_requirements WHERE item_id IS NOT NULL", product["id"]).Scan(&item, &lot); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot, "quantity_delta": "0.2", "reason": "receive"})
	remove, _ := reviewedPurchaseInput(t, f, selected, listID)
	key := value.UUID()
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), remove); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), remove); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, `SELECT count(*) FROM core.outbox_events WHERE event_version=2 AND payload->>'id'=$1
 AND payload->'shopping'->>'kind'='item_deleted' AND payload->'shopping'->'list_ids'=jsonb_build_array($2::text)`, item, listID) != 1 {
		t.Fatal("direct planning removal not emitted exactlyonce")
	}
}

func TestShoppingExtensionsCheckoutAndFailedMutationProducer(t *testing.T) {
	f := newIntegrationFixture(t)
	list, in := checkoutFixture(t, f)
	before := shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events WHERE module_id='shopping' AND payload->>'id'=$1 AND event_version=2", list)
	key := value.UUID()
	if _, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, list, key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, list, key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events WHERE module_id='shopping' AND payload->>'id'=$1 AND event_version=2", list) != before+1 {
		t.Fatal("checkout/replay list producer wrong")
	}
	active, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Rollback target"})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": active["id"], "name": "Keep unchanged"})
	m, _ := f.app.Store.Module("shopping")
	items, _ := m.Entity("items")
	before = shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events")
	_, err := f.app.Store.Write(f.ctx, f.identity, m, items, store.String(item["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"list_id": value.UUID()}, 1))
	if err == nil {
		t.Fatal("foreign destination accepted")
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events") != before {
		t.Fatal("failed move published event")
	}
	if err = f.app.Store.Delete(f.ctx, f.identity, m, items, store.String(item["id"]), 99, f.app.Store.States()["shopping"].Generation, value.UUID()); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale delete accepted", err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events") != before {
		t.Fatal("stale delete published event")
	}
}

func TestShoppingExtensionsDeletedListFinalNoticeRetainedUntilDismissal(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Deleted watched list"})
	reader := shoppingTestMember(t, f, "deleted-watch-reader", "reader")
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	m, _ := f.app.Store.Module("shopping")
	lists, _ := m.Entity("lists")
	if err := f.app.Store.Delete(f.ctx, f.identity, m, lists, store.String(list["id"]), 1, f.app.Store.States()["shopping"].Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	shoppingExtensionStage(t, f)
	job, lease := shoppingExtensionDigest(t, f)
	if err := f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var notice string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1'", reader.UserID).Scan(&notice); err != nil {
		t.Fatal(err)
	}
	shoppingExtensionStage(t, f) // pruning must not discard the final notice's watch.
	view, err := f.app.Store.ShoppingNotificationProjection(f.ctx, reader, notice)
	if err != nil || view.TargetPath != "/notifications" {
		t.Fatal("deleted final notice lost safe fallback", view, err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.notifications SET dismissed_at=clock_timestamp() WHERE id=$1", notice); err != nil {
		t.Fatal(err)
	}
	shoppingExtensionStage(t, f)
	if _, err = f.app.Store.ShoppingNotificationProjection(f.ctx, reader, notice); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("dismissed final notice visible", err)
	}
}
