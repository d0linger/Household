//go:build integration

package web

import (
	"errors"
	"math/big"
	"testing"

	"household/internal/audit"
	"household/internal/store"
	"household/internal/value"
)

func purchaseCopyMeal(t *testing.T, f *integrationFixture, pid, mid, cid, productID string, samePlan ...bool) (string, string, string) {
	t.Helper()
	newPlan, newMeal, newComponent := value.UUID(), value.UUID(), value.UUID()
	withinPlan := len(samePlan) == 1 && samePlan[0]
	dayOffset := 1
	if withinPlan {
		newPlan, dayOffset = pid, 0
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := audit.Begin(ctx, f.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if !withinPlan {
		if _, err = tx.Exec(f.ctx, `INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by)
 SELECT $1,household_id,'Second independently selected plan',starts_on+1,ends_on+1,created_by FROM mealplan.plans WHERE id=$2`, newPlan, pid); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,meal_slot,title,servings,kind,status,created_by)
 SELECT $1,household_id,$2,planned_on+$4::integer,CASE WHEN $5 THEN 'snack' ELSE meal_slot END,title,servings,kind,status,created_by
 FROM mealplan.meals WHERE id=$3`, []any{newMeal, newPlan, mid, dayOffset, withinPlan}},
		{`INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT $1,household_id,$2,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by
 FROM mealplan.components WHERE id=$3`, []any{newComponent, newMeal, cid}},
	} {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	bindPlanningIngredients(t, f, newPlan, newMeal, newComponent, store.Record{"id": productID})
	return newPlan, newMeal, newComponent
}

func purchaseDesiredTotal(t *testing.T, view store.PlanningPurchasesPreview) string {
	t.Helper()
	total := new(big.Rat)
	for _, line := range view.Lines {
		amount, ok := new(big.Rat).SetString(line.Desired.Rational)
		if !ok {
			t.Fatal("missing exact per-source target", line)
		}
		total.Add(total, amount)
	}
	return total.RatString()
}

func TestPlanningPurchaseReadOnlyGlobalScopeAndCoveredSources(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	var pid, mid, productID string
	if err := f.pool.QueryRow(f.ctx, "SELECT plan_id,meal_id,product_id FROM core.planning_purchase_requirements WHERE id=$1", ids[0]).Scan(&pid, &mid, &productID); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || !view.Ready || len(view.Lines) != 2 || purchaseDesiredTotal(t, view) != "0" {
		t.Fatal("covered active source was lost", view, err)
	}
	pid2, mid2, _ := purchaseCopyMeal(t, f, pid, mid, cid, productID)
	selected := []store.PlanningPurchaseSelection{{PlanID: pid2, MealIDs: []string{mid2}}}
	combined, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, selected)
	if err != nil || !combined.Ready || len(combined.Plans) != 2 || len(combined.Requirements) != 2 || len(combined.Lines) != 4 || purchaseDesiredTotal(t, combined) != "2/5" {
		t.Fatal("two plans reused the same physical stock or omitted zero-purchase sources", combined, err)
	}
	for _, line := range combined.Lines {
		if line.ComponentID == cid && line.Desired.Rational != "0" {
			t.Fatal("later meal displaced the earlier canonical source", line)
		}
	}
	repeat, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, selected)
	if err != nil || repeat.Fingerprint != combined.Fingerprint {
		t.Fatal("unchanged preview was unstable", repeat.Fingerprint, combined.Fingerprint, err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.planning_purchase_requirements").Scan(&count); err != nil || count != 2 {
		t.Fatal("preview registered a new scope", count, err)
	}
	currentOnly, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || len(currentOnly.Lines) != 2 {
		t.Fatal("preview changed the next active-only calculation", currentOnly, err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningPurchases(f.ctx, foreign, selected); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign household preview was not denied", err)
	}
}

func TestPlanningPurchaseDeletedActiveSourceRemainsBlocked(t *testing.T) {
	f, _, cid := purchaseSchemaRegister(t)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, "DELETE FROM mealplan.components WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || view.Ready || len(view.Requirements) != 2 || len(view.Lines) != 0 || len(view.Issues) < 2 {
		t.Fatal("deleted active source silently released forecast coverage", view, err)
	}
	for _, requirement := range view.Requirements {
		if !requirement.Active || requirement.ReviewReason != "source_missing" {
			t.Fatal("ordinary deletion removed the active identity", requirement)
		}
	}
}

func TestPlanningPurchaseChangedSourceRequiresReview(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	_, itemID := purchaseSchemaManage(t, f, ids)
	before, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || !before.Ready {
		t.Fatal("unchanged reviewed source", before, err)
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	if _, err = audit.Exec(ctx, f.pool, "UPDATE mealplan.components SET servings=2 WHERE id=$1", cid); err != nil {
		t.Fatal(err)
	}
	after, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || after.Ready || len(after.Lines) != 2 || after.Fingerprint == before.Fingerprint {
		t.Fatal("changed portions were automatically accepted", after, err)
	}
	for _, line := range after.Lines {
		if line.Reason != "source_changed" || line.Action != "blocked" {
			t.Fatal("changed source did not require a named review", line)
		}
	}
	var quantity string
	var version int64
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text,version FROM shopping.items WHERE id=$1", itemID).Scan(&quantity, &version); err != nil || quantity != "0.200000" || version != 1 {
		t.Fatal("review-only source change overwrote the managed purchase", quantity, version, err)
	}
}

func TestPlanningPurchaseAdditionalMealDoesNotInvalidatePriorSource(t *testing.T) {
	f, ids, cid := purchaseSchemaRegister(t)
	var pid, mid, productID string
	if err := f.pool.QueryRow(f.ctx, "SELECT plan_id,meal_id,product_id FROM core.planning_purchase_requirements WHERE id=$1", ids[0]).Scan(&pid, &mid, &productID); err != nil {
		t.Fatal(err)
	}
	_, mid2, _ := purchaseCopyMeal(t, f, pid, mid, cid, productID, true)
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid2}}})
	if err != nil || !view.Ready || len(view.Plans) != 1 || len(view.Lines) != 4 || purchaseDesiredTotal(t, view) != "2/5" {
		t.Fatal("unrelated plan revision falsely changed the accepted source basis", view, err)
	}
}
