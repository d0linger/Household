//go:build integration

package web

import (
	"errors"
	"testing"

	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func planningCheckoutFixture(t *testing.T, decision, quantity string, otherProduct bool) (*integrationFixture, []string, string, string, string, string, store.CheckoutInput) {
	t.Helper()
	f, ids, cid := purchaseSchemaRegister(t)
	var pid, mid, productID, lotID, locationID, shortageID string
	if err := f.pool.QueryRow(f.ctx, `SELECT r.plan_id,r.meal_id,r.product_id,l.id,l.location_id,r.id FROM core.planning_purchase_requirements r
 JOIN mealplan.components c ON c.household_id=r.household_id AND c.id=r.component_id
 JOIN pantry.lots l ON l.household_id=r.household_id AND l.product_id=r.product_id
 WHERE r.component_id=$1 AND r.ingredient_id::text=c.snapshot->'ingredients'->1->>'id'`, cid).
		Scan(&pid, &mid, &productID, &lotID, &locationID, &shortageID); err != nil {
		t.Fatal(err)
	}
	if ids[0] != shortageID {
		ids[0], ids[1] = ids[1], ids[0]
	}
	// Demand1.2kg, physical1kg, one actual managed shortage0.2kg.
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "-1", "reason": "consume"})
	listID, itemID := purchaseSchemaManage(t, f, ids)
	if decision != "skip" {
		m, _ := f.app.Store.Module("shopping")
		e, _ := m.Entity("items")
		if _, err := f.app.Store.Action(f.ctx, f.identity, m, e, itemID, "check", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"checked": true}, 1)); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := f.app.Store.ShoppingCheckoutPreview(f.ctx, f.identity, listID)
	if err != nil {
		t.Fatal(err)
	}
	line := store.CheckoutLine{ItemID: itemID, Decision: decision}
	if decision != "skip" {
		line.Quantity, line.Unit = quantity, "kg"
	}
	if decision == "store" {
		line.ProductID, line.LocationID, line.ProductVersion, line.LocationVersion = productID, locationID, 1, 1
		if otherProduct {
			other, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Deliberately different intake product", "unit_code": "kg"})
			line.ProductID = store.String(other["id"])
		}
	}
	in := store.CheckoutInput{Generation: f.app.Store.States()["shopping"].Generation, PantryGeneration: f.app.Store.States()["pantry"].Generation,
		Fingerprint: store.String(preview["fingerprint"]), ReceivedOn: "2026-01-02", Lines: []store.CheckoutLine{line}}
	return f, ids, pid, mid, cid, listID, in
}

func TestPlanningPurchaseActualCheckoutDispositionsAndStoredScope(t *testing.T) {
	for _, tc := range []struct {
		name, decision, quantity, disposition string
		other, ready                          bool
	}{
		{"matching intake retains active need", "store", "0.2", "stored", false, true},
		{"partial intake requires renewed residual decision", "store", "0.1", "stored", false, false},
		{"other target is unresolved", "store", "0.2", "unresolved", true, false},
		{"bought without stock", "no_stock", "0.2", "no_stock", false, false},
		{"explicitly skipped", "skip", "", "skipped", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ids, pid, mid, cid, listID, in := planningCheckoutFixture(t, tc.decision, tc.quantity, tc.other)
			result, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, listID, value.UUID(), value.UUID(), in)
			if err != nil {
				t.Fatal("actual checkout did not record its typed contribution", err)
			}
			var active, unlinked bool
			var disposition, pending, checkoutID, productID string
			if err = f.pool.QueryRow(f.ctx, `SELECT active,procurement_disposition,pending_quantity::text,last_checkout_id,product_id,
 item_id IS NULL AND list_id IS NULL AND shopping_generation IS NULL FROM core.planning_purchase_requirements WHERE id=$1`, ids[0]).
				Scan(&active, &disposition, &pending, &checkoutID, &productID, &unlinked); err != nil || !active || !unlinked || pending != "0.000000" ||
				disposition != tc.disposition || checkoutID != store.String(result["id"]) {
				t.Fatal("checkout lost activity or misattributed intake", active, unlinked, pending, disposition, checkoutID, err)
			}
			view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
			if err != nil || view.Ready != tc.ready || len(view.Requirements) != 2 {
				t.Fatal("checkout procurement state corrupted complete active scope", view, err)
			}
			if tc.ready {
				if purchaseDesiredTotal(t, view) != "0" {
					t.Fatal("matching actual stock did not cover its still-planned source", view)
				}
				pid2, mid2, _ := purchaseCopyMeal(t, f, pid, mid, cid, productID)
				combined, readErr := f.app.Store.PlanningPurchases(f.ctx, f.identity, []store.PlanningPurchaseSelection{{PlanID: pid2, MealIDs: []string{mid2}}})
				if readErr != nil || !combined.Ready || purchaseDesiredTotal(t, combined) != "6/5" {
					t.Fatal("stored but still planned source released the same physical stock twice", combined, readErr)
				}
			}
			replay, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, listID, value.UUID(), value.UUID(), in)
			if err != nil || replay["id"] != result["id"] {
				t.Fatal("actual checkout replay changed its identity", replay, err)
			}
			var operations int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.planning_purchase_operations WHERE kind='checkout'").Scan(&operations); err != nil || operations != 1 {
				t.Fatal("replay repeated procurement transition", operations, err)
			}
		})
	}
}

func TestPlanningPurchaseCheckoutLateFailureRollsBackActualIntake(t *testing.T) {
	f, ids, _, _, _, listID, in := planningCheckoutFixture(t, "store", "0.2", false)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.reject_planning_checkout_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.last_checkout_id IS NOT NULL THEN RAISE EXCEPTION 'forced planning checkout late failure' USING ERRCODE='23514';END IF;RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER reject_planning_checkout_fixture AFTER UPDATE ON core.planning_purchase_requirements
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.reject_planning_checkout_fixture()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER reject_planning_checkout_fixture ON core.planning_purchase_requirements;DROP FUNCTION core.reject_planning_checkout_fixture()"); err != nil {
			t.Error(err)
		}
	}()
	_, err := f.app.Store.CheckoutShopping(f.ctx, f.identity, listID, value.UUID(), value.UUID(), in)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.Message != "forced planning checkout late failure" {
		t.Fatal("late database failure was not exercised", err)
	}
	var lots, checkouts, proofs int
	var pending, disposition, physical string
	var open bool
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pantry.lots),(SELECT count(*) FROM shopping.checkouts),
 (SELECT count(*) FROM core.planning_purchase_operations WHERE kind='checkout'),pending_quantity::text,procurement_disposition,
 (SELECT sum(quantity)::text FROM pantry.lot_balances),(SELECT archived_at IS NULL FROM shopping.lists WHERE id=$2)
 FROM core.planning_purchase_requirements WHERE id=$1`, ids[0], listID).Scan(&lots, &checkouts, &proofs, &pending, &disposition, &physical, &open)
	if err != nil || lots != 1 || checkouts != 0 || proofs != 0 || pending != "0.200000" || disposition != "managed" || physical != "1.000000" || !open {
		t.Fatal("late checkout failure left partial actual effects", lots, checkouts, proofs, pending, disposition, physical, open, err)
	}
}
