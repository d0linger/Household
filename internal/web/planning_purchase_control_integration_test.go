//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func reviewedPurchaseControl(t *testing.T, f *integrationFixture, action string, meals, requirements []string) store.PlanningPurchaseControlInput {
	t.Helper()
	view, err := f.app.Store.PlanningPurchaseScope(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	return store.PlanningPurchaseControlInput{Generation: view.Generation, PantryGeneration: view.PantryGeneration, ShoppingGeneration: view.ShoppingGeneration,
		Fingerprint: view.Fingerprint, Action: action, MealIDs: meals, RequirementIDs: requirements}
}

func TestPlanningPurchaseControlDisabledMissingSourceAndHistoricalEvidence(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Private retained recovery destination"})
	in, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	initial, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	var rid, itemID string
	var before, after []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT r.id,i.id,to_jsonb(i) FROM core.planning_purchase_requirements r JOIN shopping.items i ON i.id=r.item_id WHERE r.item_id IS NOT NULL`).Scan(&rid, &itemID, &before); err != nil {
		t.Fatal(err)
	}
	visible, err := f.app.Store.PlanningPurchaseScope(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	var identified bool
	for _, r := range visible.Requirements {
		if store.String(r["id"]) == rid {
			identified = store.String(r["item_name"]) != "" && store.String(r["list_name"]) == "Private retained recovery destination" &&
				store.String(r["item_quantity"]) == "0.200000" && store.String(r["item_unit"]) == "kg"
		}
	}
	if !identified {
		t.Fatal("detach review omitted actual item/list/quantity context", visible)
	}
	for _, module := range []string{"mealplan", "pantry", "shopping"} {
		state := f.app.Store.States()[module]
		if err = f.app.Store.SetModule(f.ctx, f.identity, module, false, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	scope, err := f.app.Store.PlanningPurchaseScope(f.ctx, f.identity)
	public, _ := json.Marshal(scope)
	if err != nil || len(scope.Requirements) != 2 || scope.MealplanEnabled || scope.PantryEnabled || scope.ShoppingEnabled ||
		strings.Contains(string(public), "Private retained") || strings.Contains(string(public), `"meal_name":"`) || strings.Contains(string(public), `"item_name":"`) {
		t.Fatal("disabled/missing-source recovery omitted identity or exposed private labels", string(public), err)
	}
	for _, r := range scope.Requirements {
		for _, key := range []string{"item_name", "item_quantity", "item_unit", "item_list_id", "list_name"} {
			if r[key] != nil {
				t.Fatal("disabled Shopping exposed item/list detail", key, r[key])
			}
		}
	}
	detach := reviewedPurchaseControl(t, f, "detach", nil, []string{rid})
	key := value.UUID()
	detached, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, key, value.UUID(), detach)
	if err != nil {
		t.Fatal("detach with all modules disabled and missing source", err)
	}
	assertPurchaseCounts(t, detached, 1, 0, 0, 0)
	if detached["details_available"] != false || detached["snapshot"] != nil {
		t.Fatal("disabled Shopping exposed receipt details", detached)
	}
	var active, cleared, sourceMissing bool
	if err = f.pool.QueryRow(f.ctx, `SELECT active,item_id IS NULL AND pending_quantity=0 AND procurement_disposition='detached',review_reason='source_missing'
 FROM core.planning_purchase_requirements WHERE id=$1`, rid).Scan(&active, &cleared, &sourceMissing); err != nil || !active || !cleared || !sourceMissing {
		t.Fatal("detach changed demand or source review decision", active, cleared, sourceMissing, err)
	}
	ending := reviewedPurchaseControl(t, f, "end", []string{mid}, nil)
	ended, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), ending)
	if err != nil {
		t.Fatal("whole missing meal end", err)
	}
	assertPurchaseCounts(t, ended, 2, 0, 0, 0)
	if err = f.pool.QueryRow(f.ctx, "SELECT to_jsonb(i) FROM shopping.items i WHERE id=$1", itemID).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("control changed the independent actual item", err)
	}
	scope, err = f.app.Store.PlanningPurchaseScope(f.ctx, f.identity)
	if err != nil || len(scope.Requirements) != 0 {
		t.Fatal("end did not cover the whole meal", scope, err)
	}
	replay, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, key, value.UUID(), detach)
	if err != nil || replay["id"] != detached["id"] {
		t.Fatal("durable detach replay reevaluated ended source", replay, err)
	}
	history, err := f.app.Store.PlanningPurchaseOperation(f.ctx, f.identity, store.String(initial["id"]))
	if err != nil || history["details_available"] != false {
		t.Fatal("history lost minimal evidence", history, err)
	}
	if _, err = f.app.Store.PlanningPurchaseOperation(f.ctx, f.identity, value.UUID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unknown history must be not found", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningPurchaseOperation(f.ctx, foreign, store.String(initial["id"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign household history was visible", err)
	}
}

func TestPlanningPurchaseControlStaleReviewAndGenuineLateRollback(t *testing.T) {
	f, ids, _ := purchaseSchemaRegister(t)
	_, itemID := purchaseSchemaManage(t, f, ids)
	var mealID string
	if err := f.pool.QueryRow(f.ctx, "SELECT meal_id FROM core.planning_purchase_requirements WHERE id=$1", ids[0]).Scan(&mealID); err != nil {
		t.Fatal(err)
	}
	in := reviewedPurchaseControl(t, f, "end", []string{mealID}, nil)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE shopping.items SET name='Explicitly changed retained item',version=version+1 WHERE id=$1", itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("stale item review was accepted")
	}
	in = reviewedPurchaseControl(t, f, "end", []string{mealID}, nil)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION core.reject_planning_control_fixture() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
 BEGIN IF NOT NEW.active THEN RAISE EXCEPTION 'forced planning control late failure' USING ERRCODE='23514';END IF;RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER reject_planning_control_fixture AFTER UPDATE ON core.planning_purchase_requirements
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.reject_planning_control_fixture()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER reject_planning_control_fixture ON core.planning_purchase_requirements;DROP FUNCTION core.reject_planning_control_fixture()"); err != nil {
			t.Error(err)
		}
	}()
	key := value.UUID()
	_, err := f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.Message != "forced planning control late failure" {
		t.Fatal("genuine deferred control failure not exercised", err)
	}
	var active, managed, proofs, receipts, keyProof int
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM core.planning_purchase_requirements WHERE active),
 (SELECT count(*) FROM core.planning_purchase_requirements WHERE item_id=$1),
 (SELECT count(*) FROM core.planning_purchase_operations),(SELECT count(*) FROM shopping.planning_purchase_receipts),
 (SELECT count(*) FROM core.planning_purchase_operations WHERE request_key=$2)`, itemID, key).Scan(&active, &managed, &proofs, &receipts, &keyProof)
	if err != nil || active != 2 || managed != 1 || proofs != 2 || receipts != 2 || keyProof != 0 {
		t.Fatal("late control failure left a partial end/proof/receipt", active, managed, proofs, receipts, keyProof, err)
	}
}

func TestPlanningPurchaseControlMovedItemUsesActualDestination(t *testing.T) {
	f, ids, _ := purchaseSchemaRegister(t)
	_, itemID := purchaseSchemaManage(t, f, ids)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Actual manually moved destination"})
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "UPDATE shopping.items SET list_id=$1,version=version+1 WHERE id=$2", list["id"], itemID); err != nil {
		t.Fatal(err)
	}
	scope, err := f.app.Store.PlanningPurchaseScope(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	var rid string
	for _, r := range scope.Requirements {
		if store.String(r["item_id"]) != itemID {
			continue
		}
		rid = store.String(r["id"])
		if store.String(r["item_list_id"]) != store.String(list["id"]) || store.String(r["list_name"]) != "Actual manually moved destination" || store.String(r["list_id"]) == store.String(list["id"]) {
			t.Fatal("recovery mixed original association and actual Shopping destination", r)
		}
	}
	if rid == "" {
		t.Fatal("missing actual moved item")
	}
	var before, after []byte
	if err = f.pool.QueryRow(f.ctx, "SELECT to_jsonb(i) FROM shopping.items i WHERE id=$1", itemID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	in := reviewedPurchaseControl(t, f, "detach", nil, []string{rid})
	if _, err = f.app.Store.ControlPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("detach actually moved item", err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT to_jsonb(i) FROM shopping.items i WHERE id=$1", itemID).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("detach changed the independently moved item", err)
	}
}
