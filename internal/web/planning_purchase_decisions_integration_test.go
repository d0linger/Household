//go:build integration

package web

import (
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
)

func purchaseDecisions(t *testing.T, view store.PlanningPurchasesPreview) []store.PlanningPurchaseDecision {
	t.Helper()
	choices := []store.PlanningPurchaseDecision{}
	seenMeals := map[string]bool{}
	for _, line := range view.Lines {
		if line.RequiredDecision != "" {
			choice := store.PlanningPurchaseDecision{RequirementID: line.RequirementID, Action: line.RequiredDecision}
			if choice.Action == "reactivate" {
				if seenMeals[line.MealID] {
					continue
				}
				seenMeals[line.MealID] = true
				choice.RequirementID, choice.MealID = "", line.MealID
			}
			choices = append(choices, choice)
		}
	}
	return choices
}

func reviewedPurchaseDecisionInput(t *testing.T, f *integrationFixture, selected []store.PlanningPurchaseSelection, choices []store.PlanningPurchaseDecision, listID string) (store.PlanningPurchaseInput, store.PlanningPurchasesPreview) {
	t.Helper()
	in, _ := reviewedPurchaseInput(t, f, selected, listID)
	view, err := f.app.Store.ReviewPlanningPurchases(f.ctx, f.identity, selected, choices)
	if err != nil {
		t.Fatal(err)
	}
	in.Fingerprint, in.Decisions = view.Fingerprint, choices
	return in, view
}

func TestPlanningPurchaseDecisionsSourceAdoptionAndWholeMealReactivation(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Explicit lifecycle review"})
	listID := store.String(list["id"])
	selected := []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}
	in, _ := reviewedPurchaseInput(t, f, selected, listID)
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE mealplan.components SET servings=2 WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	_, blocked := reviewedPurchaseInput(t, f, selected, listID)
	choices := purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 2 || choices[0].Action != "accept" {
		t.Fatal("source change lacked explicit full review", blocked)
	}
	in, reviewed := reviewedPurchaseDecisionInput(t, f, selected, choices, listID)
	if !reviewed.Ready || purchaseDesiredTotal(t, reviewed) != "7/5" {
		t.Fatal("accepted source did not recalculate exact target", reviewed)
	}
	result, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("source decision commit", err)
	}
	assertPurchaseCounts(t, result, 2, 1, 1, 0)
	var itemID string
	if err = f.pool.QueryRow(f.ctx, "SELECT item_id FROM core.planning_purchase_requirements WHERE pending_quantity=1.2").Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if _, err = audit.Exec(ctx, f.pool, "UPDATE shopping.items SET name='User edited name retained',note='User note retained',version=version+1 WHERE id=$1", itemID); err != nil {
		t.Fatal(err)
	}
	_, blocked = reviewedPurchaseInput(t, f, selected, listID)
	choices = purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 1 || choices[0].Action != "accept" {
		t.Fatal("manual item edit was not explicitly reviewed", blocked)
	}
	var reviewedNote bool
	for _, line := range blocked.Lines {
		if store.String(line.CurrentItem["id"]) == itemID {
			reviewedNote = store.String(line.CurrentItem["note"]) == "User note retained"
		}
	}
	if !reviewedNote {
		t.Fatal("adoption review omitted the actual item note", blocked.Lines)
	}
	in, reviewed = reviewedPurchaseDecisionInput(t, f, selected, choices, listID)
	if !reviewed.Ready {
		t.Fatal(reviewed)
	}
	result, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("adopt actual open item without quantity write", err)
	}
	assertPurchaseCounts(t, result, 1, 0, 0, 0)
	var version int64
	var retained bool
	if err = f.pool.QueryRow(f.ctx, "SELECT version,name='User edited name retained' AND note='User note retained' FROM shopping.items WHERE id=$1", itemID).Scan(&version, &retained); err != nil || version != 3 || !retained {
		t.Fatal("adoption changed user's item", version, retained, err)
	}
	ending := reviewedPurchaseControl(t, f, "end", []string{mid}, nil)
	if _, err = f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), ending); err != nil {
		t.Fatal(err)
	}
	_, blocked = reviewedPurchaseInput(t, f, selected, listID)
	choices = purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 1 || choices[0].Action != "reactivate" || choices[0].MealID != mid {
		t.Fatal("ended requirement was silently reactivated", blocked)
	}
	partial, err := f.app.Store.ReviewPlanningPurchases(f.ctx, f.identity, selected, nil)
	if err != nil || partial.Ready {
		t.Fatal("partial whole-meal reactivation became executable", partial, err)
	}
	in, reviewed = reviewedPurchaseDecisionInput(t, f, selected, choices, listID)
	if !reviewed.Ready {
		t.Fatal(reviewed)
	}
	result, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("explicit whole meal reactivation", err)
	}
	assertPurchaseCounts(t, result, 2, 2, 0, 0)
	var active, items int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),(SELECT count(*) FROM shopping.items)").Scan(&active, &items); err != nil || active != 2 || items != 4 {
		t.Fatal("reactivation erased independently retained items", active, items, err)
	}
}

func TestPlanningPurchaseDecisionsNoStockRenewalRejectsReopenedCompletion(t *testing.T) {
	f, _, _, _, _, oldList, checkout := planningCheckoutFixture(t, "no_stock", "0.2", false)
	if _, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, oldList, value.UUID(), value.UUID(), checkout); err != nil {
		t.Fatal(err)
	}
	_, blocked := reviewedPurchaseInput(t, f, nil, oldList)
	choices := purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 1 || choices[0].Action != "renew" {
		t.Fatal("bought without stock became automatic reorder", blocked)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE shopping.lists SET archived_at=NULL,version=version+1 WHERE id=$1", oldList); err != nil {
		t.Fatal(err)
	}
	in, reviewed := reviewedPurchaseDecisionInput(t, f, nil, choices, oldList)
	if !reviewed.Ready {
		t.Fatal("explicit renewal preview", reviewed)
	}
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("completed list accepted a new purchase after reopening")
	}
	newList, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "New explicitly renewed purchase"})
	in, _ = reviewedPurchaseDecisionInput(t, f, nil, choices, store.String(newList["id"]))
	result, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("explicit no-stock renewal to a genuinely open list", err)
	}
	assertPurchaseCounts(t, result, 1, 1, 0, 0)
	var checkouts, items int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM shopping.checkouts),(SELECT count(*) FROM shopping.items)").Scan(&checkouts, &items); err != nil || checkouts != 1 || items != 2 {
		t.Fatal("renewal changed purchased history", checkouts, items, err)
	}
}

func TestPlanningPurchaseDecisionsAcceptedSourceReductionRemovesToActiveZero(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	_, itemID := purchaseSchemaManage(t, f, ids)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE mealplan.components SET servings=0.5 WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	_, blocked := reviewedPurchaseInput(t, f, nil, "")
	in, view := reviewedPurchaseDecisionInput(t, f, nil, purchaseDecisions(t, blocked), "")
	if !view.Ready || purchaseDesiredTotal(t, view) != "0" {
		t.Fatal("reduced source did not review exact zero", view)
	}
	result, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("explicit accept/remove must clear managed disposition before deletion", err)
	}
	assertPurchaseCounts(t, result, 2, 0, 0, 1)
	var active, zero, items int
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE active AND pending_quantity=0 AND procurement_disposition='none'),
 (SELECT count(*) FROM shopping.items WHERE id=$1)`, itemID).Scan(&active, &zero, &items)
	if err != nil || active != 2 || zero != 2 || items != 0 {
		t.Fatal("accepted removal lost active zero demand", active, zero, items, err)
	}
}

func TestPlanningPurchaseDecisionsFullComponentReplacementRequiresMealReactivation(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Replacement reactivation destination"})
	selected := []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}
	in, _ := reviewedPurchaseInput(t, f, selected, store.String(list["id"]))
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	ending := reviewedPurchaseControl(t, f, "end", []string{mid}, nil)
	if _, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), ending); err != nil {
		t.Fatal(err)
	}
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	old := meal.Components[0]
	replace := planningInput(t, f, pid)
	replace.Action, replace.MealID, replace.Version = "save", mid, meal.Version
	replace.Meal = store.PlanningMealInput{Title: meal.Title, PlannedOn: meal.PlannedOn, Slot: meal.Slot, Kind: meal.Kind, Status: meal.Status, Servings: meal.Servings}
	replace.Components = []store.PlanningComponentInput{{SourceVersionID: old.SourceVersionID, SourceGeneration: old.SourceGeneration, Servings: old.Servings}}
	planningMutation(t, f, pid, replace)
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil || len(meal.Components) != 1 || meal.Components[0].ID == cid {
		t.Fatal("actual complete component replacement was not exercised", meal, err)
	}
	bindPlanningIngredients(t, f, pid, mid, meal.Components[0].ID, product)
	in, blocked := reviewedPurchaseInput(t, f, selected, store.String(list["id"]))
	if blocked.Ready || len(blocked.Requirements) != 0 || len(blocked.EndedMeals) != 1 || len(blocked.Lines) != 2 {
		t.Fatal("ended meal marker disappeared with replaced components", blocked)
	}
	if _, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("complete replacement silently reactivated the ended meal")
	}
	choices := purchaseDecisions(t, blocked)
	if len(choices) != 1 || choices[0].Action != "reactivate" || choices[0].MealID != mid {
		t.Fatal("whole-meal reactivation decision missing", choices)
	}
	in, view := reviewedPurchaseDecisionInput(t, f, selected, choices, store.String(list["id"]))
	if !view.Ready {
		t.Fatal(view)
	}
	if _, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("reviewed new source identities under explicitly reactivated meal", err)
	}
	var active, ended, items int
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE NOT active),(SELECT count(*) FROM shopping.items)`).Scan(&active, &ended, &items)
	if err != nil || active != 2 || ended != 2 || items != 2 {
		t.Fatal("reactivation erased ended identity or independently retained purchase", active, ended, items, err)
	}
}

func TestPlanningPurchaseDecisionsExplicitRebindPreservesPhysicalStock(t *testing.T) {
	f, pid, mid, cid, _, oldProduct := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, oldProduct)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Explicit product rebind destination"})
	in, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	newProduct, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Different explicitly confirmed stock identity", "unit_code": "kg"})
	bindPlanningIngredients(t, f, pid, mid, cid, newProduct)
	_, blocked := reviewedPurchaseInput(t, f, nil, store.String(list["id"]))
	choices := purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 2 || choices[0].Action != "accept" || choices[1].Action != "accept" {
		t.Fatal("changed product mapping was silently adopted", blocked)
	}
	in, view := reviewedPurchaseDecisionInput(t, f, nil, choices, store.String(list["id"]))
	if !view.Ready || purchaseDesiredTotal(t, view) != "6/5" {
		t.Fatal("old product stock was counted against the new product", view)
	}
	result, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("explicit same-unit product rebind", err)
	}
	assertPurchaseCounts(t, result, 2, 1, 1, 0)
	var rebound, newMovements int
	var oldStock, purchases string
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE product_id=$1 AND active),
 (SELECT count(*) FROM pantry.stock_movements m JOIN pantry.lots l ON l.id=m.lot_id WHERE l.product_id=$1),
 (SELECT sum(b.quantity)::text FROM pantry.lot_balances b JOIN pantry.lots l ON l.id=b.lot_id WHERE l.product_id=$2),
 (SELECT sum(quantity)::text FROM shopping.items)`, newProduct["id"], oldProduct["id"]).Scan(&rebound, &newMovements, &oldStock, &purchases)
	if err != nil || rebound != 2 || newMovements != 0 || oldStock != "1.000000" || purchases != "1.200000" {
		t.Fatal("explicit rebind altered stock or kept the old identity", rebound, newMovements, oldStock, purchases, err)
	}
}
