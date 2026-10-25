//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
)

func reviewedPurchaseInput(t *testing.T, f *integrationFixture, selected []store.PlanningPurchaseSelection, listID string) (store.PlanningPurchaseInput, store.PlanningPurchasesPreview) {
	t.Helper()
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, selected)
	if err != nil {
		t.Fatal(err)
	}
	in := store.PlanningPurchaseInput{Generation: view.Generation, PantryGeneration: view.PantryGeneration, ShoppingGeneration: view.ShoppingGeneration,
		Fingerprint: view.Fingerprint, Selected: selected, ListID: listID}
	if listID != "" {
		if err = f.pool.QueryRow(f.ctx, "SELECT version FROM shopping.lists WHERE id=$1", listID).Scan(&in.ListVersion); err != nil {
			t.Fatal(err)
		}
	}
	return in, view
}

func assertPurchaseCounts(t *testing.T, result store.Record, changes, creates, updates, removes int64) {
	t.Helper()
	counts, ok := result["counts"].(map[string]any)
	if !ok || store.Int(counts["changes"]) != changes || store.Int(counts["creates"]) != creates || store.Int(counts["updates"]) != updates || store.Int(counts["removes"]) != removes {
		t.Fatal("unexpected durable effect counts", result)
	}
}

func TestPlanningPurchaseSyncCreateNoopUpdateRemoveAndDurableReplay(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Reviewed exact planning destination"})
	listID := store.String(list["id"])
	selected := []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}
	in, view := reviewedPurchaseInput(t, f, selected, listID)
	if !view.Ready || purchaseDesiredTotal(t, view) != "1/5" {
		t.Fatal("initial1.2kg demand against1kg physical", view)
	}
	key := value.UUID()
	first, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal("actual initial sync", err)
	}
	assertPurchaseCounts(t, first, 2, 1, 0, 0)
	replay, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	beforeJSON, _ := json.Marshal(first)
	afterJSON, _ := json.Marshal(replay)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatal("same request did not return original immutable evidence", replay, err)
	}
	var itemID, requirementID, lotID string
	var outboxBefore, outboxAfter int
	if err = f.pool.QueryRow(f.ctx, `SELECT item_id,id,(SELECT id FROM pantry.lots WHERE product_id=$1),
 (SELECT count(*) FROM core.outbox_events) FROM core.planning_purchase_requirements WHERE item_id IS NOT NULL`, product["id"]).
		Scan(&itemID, &requirementID, &lotID, &outboxBefore); err != nil {
		t.Fatal(err)
	}
	noopInput, noopView := reviewedPurchaseInput(t, f, selected, listID)
	for _, line := range noopView.Lines {
		if line.Delta.Rational != "0" || line.Additional.Rational != "0" {
			t.Fatal("post-sync preview must attribute the existing purchase before comparing additional demand", line)
		}
	}
	noop, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), noopInput)
	if err != nil {
		t.Fatal("new-key no-op", err)
	}
	assertPurchaseCounts(t, noop, 0, 0, 0, 0)
	var itemVersion, requirementVersion int64
	if err = f.pool.QueryRow(f.ctx, `SELECT i.version,r.version,(SELECT count(*) FROM core.outbox_events) FROM shopping.items i
 JOIN core.planning_purchase_requirements r ON r.household_id=i.household_id AND r.item_id=i.id WHERE i.id=$1`, itemID).
		Scan(&itemVersion, &requirementVersion, &outboxAfter); err != nil || itemVersion != 1 || requirementVersion != 1 || outboxBefore != outboxAfter {
		t.Fatal("no-op changed item/version/projections", itemVersion, requirementVersion, outboxBefore, outboxAfter, err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "-0.1", "reason": "consume"})
	updateInput, _ := reviewedPurchaseInput(t, f, selected, listID)
	updated, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), updateInput)
	if err != nil {
		t.Fatal("stable current contribution update", err)
	}
	assertPurchaseCounts(t, updated, 1, 0, 1, 0)
	var quantity string
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text,version FROM shopping.items WHERE id=$1", itemID).Scan(&quantity, &itemVersion); err != nil || quantity != "0.300000" || itemVersion != 2 {
		t.Fatal("update created another item or wrong target", quantity, itemVersion, err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "0.3", "reason": "receive"})
	removeInput, _ := reviewedPurchaseInput(t, f, selected, listID)
	removed, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), removeInput)
	if err != nil {
		t.Fatal("reviewed target reduction to zero", err)
	}
	assertPurchaseCounts(t, removed, 1, 0, 0, 1)
	var items int
	var active, empty bool
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM shopping.items),version,active,item_id IS NULL AND pending_quantity=0
 FROM core.planning_purchase_requirements WHERE id=$1`, requirementID).Scan(&items, &requirementVersion, &active, &empty); err != nil || items != 0 || requirementVersion != 3 || !active || !empty {
		t.Fatal("removal lost source or caused delete-hook double version", items, requirementVersion, active, empty, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM shopping.planning_purchase_receipts WHERE operation_id=$1", first["id"]); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	replay, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil || replay["id"] != first["id"] || replay["details_available"] != false || replay["snapshot"] != nil {
		t.Fatal("old key after successor/source deletion/receipt pruning recreated effects", replay, err)
	}
	assertPurchaseCounts(t, replay, 2, 1, 0, 0)
	altered := in
	altered.ListVersion++
	if _, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), altered); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old key accepted an altered canonical request", err)
	}
}

func TestPlanningPurchaseSyncCoveredSourceAndConcurrentDisjointFirstScopes(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	var lotID string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lotID); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "1", "reason": "receive"})
	pid2, mid2, _ := purchaseCopyMeal(t, f, pid, mid, cid, store.String(product["id"]))
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Shared stock scope destination"})
	inputs := make([]store.PlanningPurchaseInput, 0, 2)
	for _, selected := range [][]store.PlanningPurchaseSelection{
		{{PlanID: pid, MealIDs: []string{mid}}}, {{PlanID: pid2, MealIDs: []string{mid2}}},
	} {
		in, view := reviewedPurchaseInput(t, f, selected, store.String(list["id"]))
		if !view.Ready || purchaseDesiredTotal(t, view) != "0" {
			t.Fatal("each independently reviewed source must initially be covered", view)
		}
		inputs = append(inputs, in)
	}
	type outcome struct {
		index int
		err   error
	}
	start, done := make(chan struct{}), make(chan outcome, 2)
	for index, in := range inputs {
		go func() {
			<-start
			_, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
			done <- outcome{index, err}
		}()
	}
	close(start)
	winners, loser := 0, -1
	for range 2 {
		result := <-done
		if result.err == nil {
			winners++
		} else {
			var conflict store.PlanningConflict
			if !errors.As(result.err, &conflict) && !errors.Is(result.err, store.ErrConflict) {
				t.Fatal("unexpected concurrent failure", result.err)
			}
			loser = result.index
		}
	}
	if winners != 1 || loser < 0 {
		t.Fatal("two first registrations reused the same physical stock", winners, loser)
	}
	refreshed, combined := reviewedPurchaseInput(t, f, inputs[loser].Selected, store.String(list["id"]))
	if !combined.Ready || purchaseDesiredTotal(t, combined) != "2/5" {
		t.Fatal("covered winner disappeared from the complete scope", combined)
	}
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), refreshed); err != nil {
		t.Fatal("freshly reviewed disjoint registration", err)
	}
	var requirements, items int
	var quantity string
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),
 (SELECT count(*) FROM shopping.items),(SELECT sum(quantity)::text FROM shopping.items)`).Scan(&requirements, &items, &quantity); err != nil || requirements != 4 || items != 1 || quantity != "0.400000" {
		t.Fatal("global allocation lost zero sources or duplicated purchases", requirements, items, quantity, err)
	}
}
