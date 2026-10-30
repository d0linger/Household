//go:build integration

package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func planningExpansionFixture(t *testing.T) (*integrationFixture, string, string, store.Record, store.Record, store.Record, string) {
	t.Helper()
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["mealplan"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var day, end string
	if err := f.pool.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text,
 ((clock_timestamp() AT TIME ZONE timezone)::date+6)::text FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&day, &end); err != nil {
		t.Fatal(err)
	}
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Current planning extension", "starts_on": day, "ends_on": end})
	product, lot := pantryFixture(t, f, "planning extension")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "1", "reason": "receive"})
	catalog, _ := auditCreate(t, f, "recipes", "ingredient_catalog", store.Record{"name": "Explicit planning food"})
	ingredientBind(t, f, catalog, product)
	rid := suggestionRecipe(t, f, catalog, "Planning one kilogram", "1")
	return f, store.String(plan["id"]), day, product, lot, catalog, rid
}

func planningExpansionCommand(t *testing.T, f *integrationFixture, pid, week string) store.PlanningExtensionCommand {
	t.Helper()
	var version int64
	if err := f.pool.QueryRow(f.ctx, "SELECT version FROM mealplan.plans WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, pid).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return store.PlanningExtensionCommand{Generation: f.app.Store.States()["mealplan"].Generation, PlanVersion: version, Week: week}
}

func planningExpansionAutoplanInput(t *testing.T, f *integrationFixture, pid, day, rid string) store.PlanningAutoplanInput {
	t.Helper()
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	return store.PlanningAutoplanInput{PlanningExtensionCommand: planningExpansionCommand(t, f, pid, day),
		RecipesGeneration: f.app.Store.States()["recipes"].Generation,
		Candidates:        []store.PlanningAutoplanCandidateInput{{SourceVersionID: store.String(source["source_version_id"])}},
		Slots:             []store.PlanningAutoplanSlotInput{{PlannedOn: day, Slot: "breakfast", Servings: "1"}, {PlannedOn: day, Slot: "lunch", Servings: "1"}},
		Rules:             store.PlanningAutoplanRules{Stock: 100}}
}

func planningExpansionStockSnapshot(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var snapshot string
	if err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'movements',(SELECT count(*) FROM pantry.stock_movements),'quantity',(SELECT sum(quantity)::text FROM pantry.lot_balances),
 'reservations',(SELECT count(*) FROM core.meal_reservations),'allocations',(SELECT count(*) FROM core.meal_reservation_allocations),
 'purchases',(SELECT count(*) FROM shopping.items),'cooking',(SELECT count(*) FROM recipes.cooking_records))::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPlanningBudgetFrozenSnapshotManualReviewAndOptionalPrices(t *testing.T) {
	f, pid, day, _, _, catalog, rid := planningExpansionFixture(t)
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Reviewed price product", "pack_unit": "kg", "pack_quantity": "1"})
	shop, _ := auditCreate(t, f, "shopping", "stores", store.Record{"name": "Local price source"})
	auditCreate(t, f, "shopping", "price_observations", store.Record{"product_id": product["id"], "store_id": shop["id"],
		"observed_on": day, "quantity": "1", "unit_code": "kg", "price_minor": "2.00"})
	var currentCatalogVersion int64
	var bindingID string
	var bindingVersion int64
	if err := f.pool.QueryRow(f.ctx, "SELECT version FROM recipes.ingredient_catalog WHERE id=$1", catalog["id"]).Scan(&currentCatalogVersion); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT id,version FROM core.ingredient_product_bindings WHERE household_id=$1 AND catalog_id=$2", f.identity.HouseholdID, catalog["id"]).Scan(&bindingID, &bindingVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SaveRecipeShoppingBinding(f.ctx, f.identity, store.String(catalog["id"]), value.UUID(), value.UUID(), store.RecipePriceBindingInput{
		Generation: f.app.Store.States()["recipes"].Generation, ShoppingGeneration: f.app.Store.States()["shopping"].Generation,
		CatalogVersion: currentCatalogVersion, ProductID: store.String(product["id"]), ProductVersion: store.Int(product["version"]),
		BindingID: bindingID, Version: bindingVersion,
	}); err != nil {
		t.Fatal(err)
	}
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	base := planningExpansionCommand(t, f, pid, day)
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), store.PlanningInput{
		Generation: base.Generation, PlanVersion: base.PlanVersion, Action: "save",
		Meal:       store.PlanningMealInput{Title: "Frozen two portions", PlannedOn: day, Slot: "dinner", Kind: "recipe", Status: "planned", Servings: "2"},
		Components: []store.PlanningComponentInput{{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: f.app.Store.States()["recipes"].Generation, Servings: "2"}},
	}); err != nil {
		t.Fatal(err)
	}
	budget, err := f.app.Store.PlanningBudget(f.ctx, f.identity, pid, day)
	if err != nil || store.String(budget["known_minor"]) != "400" || budget["complete"] != true {
		t.Fatal("frozen production cost", budget, err)
	}
	// A live edit creates a different recipe version; the plan must retain1kg/base.
	var ingredientID string
	var ingredientVersion int64
	if err = f.pool.QueryRow(f.ctx, "SELECT id,version FROM recipes.ingredients WHERE recipe_id=$1 ORDER BY position LIMIT 1", rid).Scan(&ingredientID, &ingredientVersion); err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("recipes")
	e, _ := m.Entity("ingredients")
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, ingredientID, value.UUID(), value.UUID(), store.Mutation{
		Generation: f.app.Store.States()["recipes"].Generation, Version: ingredientVersion, Data: recipeData(t, store.Record{"quantity": "9"}),
	}); err != nil {
		t.Fatal(err)
	}
	budget, err = f.app.Store.PlanningBudget(f.ctx, f.identity, pid, day)
	if err != nil || store.String(budget["known_minor"]) != "400" {
		t.Fatal("live recipe edit rewrote plan cost quantities", budget, err)
	}
	free, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": pid, "planned_on": day,
		"meal_slot": "lunch", "kind": "out", "title": "Unknown eating-out estimate", "servings": "1"})
	target := "1000"
	settings := store.PlanningBudgetInput{PlanningExtensionCommand: planningExpansionCommand(t, f, pid, day), BudgetMinor: &target,
		ManualEstimates: map[string]store.PlanningManualEstimate{}, Rules: store.PlanningAutoplanRules{Stock: 50}}
	result, err := f.app.Store.MutatePlanningBudget(f.ctx, f.identity, pid, value.UUID(), value.UUID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	budget, err = f.app.Store.PlanningBudget(f.ctx, f.identity, pid, day)
	if err != nil || budget["budget_status"] != "unknown" || store.Int(budget["unknown_count"]) != 1 {
		t.Fatal("missing manual price was counted as free", budget, err)
	}
	settings.PlanningExtensionCommand = planningExpansionCommand(t, f, pid, day)
	settings.SettingsVersion = store.Int(result["settings_version"])
	settings.ManualEstimates[store.String(free["id"])] = store.PlanningManualEstimate{MealVersion: store.Int(free["version"]), Minor: "0"}
	if _, err = f.app.Store.MutatePlanningBudget(f.ctx, f.identity, pid, value.UUID(), value.UUID(), settings); err != nil {
		t.Fatal(err)
	}
	budget, err = f.app.Store.PlanningBudget(f.ctx, f.identity, pid, day)
	if err != nil || budget["budget_status"] != "within" || store.String(budget["known_minor"]) != "400" {
		t.Fatal("explicit reviewed zero estimate or target", budget, err)
	}
	shopping := f.app.Store.States()["shopping"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, shopping.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	budget, err = f.app.Store.PlanningBudget(f.ctx, f.identity, pid, day)
	if err != nil || budget["complete"] != false || budget["budget_status"] != "unknown" || store.String(budget["known_minor"]) != "0" {
		t.Fatal("disabled price module leaked a current price or hid owned plan", budget, err)
	}
}

func TestPlanningAutoplanJointPreviewAndAtomicOnceOnlyApply(t *testing.T) {
	f, pid, day, _, _, _, rid := planningExpansionFixture(t)
	in := planningExpansionAutoplanInput(t, f, pid, day, rid)
	stock := planningExpansionStockSnapshot(t, f)
	preview, err := f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in)
	if err != nil {
		t.Fatal(err)
	}
	slots := preview["slots"].([]store.Record)
	if len(slots) != 2 || slots[0]["coverage"] != "sufficient" || slots[1]["coverage"] != "shortage" || planningExpansionStockSnapshot(t, f) != stock {
		t.Fatal("preview reused one physical kilogram or wrote stock", preview)
	}
	in.ReviewFingerprint = store.String(preview["review_fingerprint"])
	in.ReviewExpiresAt = time.Now().Unix() + 300
	key := value.UUID()
	result, err := f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, key, value.UUID(), in)
	if err != nil || len(result["created_meal_ids"].([]string)) != 2 {
		t.Fatal("atomic apply", result, err)
	}
	replay, err := f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, key, value.UUID(), in)
	if err != nil || store.String(replay["review_fingerprint"]) != in.ReviewFingerprint {
		t.Fatal("committed replay reopened stale plan or source", replay, err)
	}
	var meals, components int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM mealplan.meals WHERE plan_id=$1),
 (SELECT count(*) FROM mealplan.components c JOIN mealplan.meals m ON m.id=c.meal_id WHERE m.plan_id=$1)`, pid).Scan(&meals, &components); err != nil || meals != 2 || components != 2 || planningExpansionStockSnapshot(t, f) != stock {
		t.Fatal("replay duplicated meals or apply mutated inventory", meals, components, err)
	}
}

func TestPlanningAutoplanStaleStockAndExpiredReviewAreAtomicDenials(t *testing.T) {
	f, pid, day, _, lot, _, rid := planningExpansionFixture(t)
	in := planningExpansionAutoplanInput(t, f, pid, day, rid)
	preview, err := f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ReviewFingerprint, in.ReviewExpiresAt = store.String(preview["review_fingerprint"]), time.Now().Unix()+300
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "1", "reason": "receive"})
	if _, err = f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed availability accepted stale preview", err)
	}
	preview, err = f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ReviewFingerprint, in.ReviewExpiresAt = store.String(preview["review_fingerprint"]), time.Now().Unix()-1
	if _, err = f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("expired uncommitted review accepted", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM mealplan.meals WHERE plan_id=$1", pid).Scan(&count); err != nil || count != 0 {
		t.Fatal("denial left partial meals", count, err)
	}
}

func TestPlanningAutoplanLaterSlotFailureRollsBackEarlierSlot(t *testing.T) {
	f, pid, day, _, _, _, rid := planningExpansionFixture(t)
	in := planningExpansionAutoplanInput(t, f, pid, day, rid)
	preview, err := f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ReviewFingerprint, in.ReviewExpiresAt = store.String(preview["review_fingerprint"]), time.Now().Unix()+300
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION mealplan.fixture_reject_lunch() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog AS $$ BEGIN IF NEW.meal_slot='lunch' THEN RAISE EXCEPTION 'controlled later-slot failure' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fixture_reject_lunch BEFORE INSERT ON mealplan.meals FOR EACH ROW EXECUTE FUNCTION mealplan.fixture_reject_lunch()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, cleanupErr := f.owner.Exec(cleanupCtx, "DROP TRIGGER IF EXISTS fixture_reject_lunch ON mealplan.meals; DROP FUNCTION IF EXISTS mealplan.fixture_reject_lunch()"); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	if _, err = f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("controlled second slot unexpectedly committed")
	}
	var meals, components int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM mealplan.meals WHERE plan_id=$1),
 (SELECT count(*) FROM mealplan.components c JOIN mealplan.meals m ON m.id=c.meal_id WHERE m.plan_id=$1)`, pid).Scan(&meals, &components); err != nil || meals != 0 || components != 0 {
		t.Fatal("first slot survived failed whole-week transaction", meals, components, err)
	}
}

func TestPlanningAutoplanSourceLocksAndExistingMealsArePreserved(t *testing.T) {
	f, pid, day, _, _, _, rid := planningExpansionFixture(t)
	existing, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": pid, "planned_on": day,
		"meal_slot": "breakfast", "kind": "free", "title": "Keep my existing meal", "servings": "1"})
	in := planningExpansionAutoplanInput(t, f, pid, day, rid)
	if _, err := f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("existing meal was an implicit replacement target", err)
	}
	in.Slots[0].Slot, in.Slots[1].Slot = "dinner", "snack"
	preview, err := f.app.Store.PlanningAutoplanPreview(f.ctx, f.identity, pid, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ReviewFingerprint, in.ReviewExpiresAt = store.String(preview["review_fingerprint"]), time.Now().Unix()+300
	blocked, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocked.Rollback(f.ctx) }()
	if _, err = blocked.Exec(f.ctx, "SELECT id FROM recipes.recipes WHERE id=$1 FOR UPDATE", rid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ApplyPlanningAutoplan(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("contended source was accepted or hung instead of named conflict", err)
	}
	if err = blocked.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var title string
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM mealplan.meals WHERE plan_id=$1", pid).Scan(&count); err != nil || count != 1 {
		t.Fatal("contended apply left partial new meals", count, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT title FROM mealplan.meals WHERE id=$1", existing["id"]).Scan(&title); err != nil || title != "Keep my existing meal" {
		t.Fatal("existing meal changed", title, err)
	}
	reader := f.identity
	reader.Role = "reader"
	if _, err = f.app.Store.PlanningAutoplanPreview(f.ctx, reader, pid, in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader received a writer proposal", err)
	}
}
