package store

import (
	"context"
	"math/big"
	"sort"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func readPlanningAutoplanData(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, in PlanningAutoplanInput, lock bool) (planningAutoplanData, error) {
	out := planningAutoplanData{OwnClaims: map[string]string{}}
	var err error
	out.Scope, err = readPlanningBudgetScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if !out.Scope.RecipesEnabled || out.Scope.RecipesGeneration != in.RecipesGeneration {
		return out, PlanningConflict{"Aktuelle freigegebene Rezeptgeneration erneut auswählen"}
	}
	pid := String(plan["id"])
	out.Settings, err = readPlanningWeekSettings(ctx, tx, id, pid, in.Week)
	if err != nil {
		return out, err
	}
	out.Week, err = readPlanningWeek(ctx, tx, id, pid, in.Week)
	if err != nil {
		return out, err
	}
	out.Candidates, err = readPlanningAutoplanCandidates(ctx, tx, id, in)
	if err != nil {
		return out, err
	}
	bundles := map[string]RecipeBundle{}
	for _, candidate := range out.Candidates {
		bundles[candidate.SourceVersionID] = candidate.Bundle
	}
	catalogIDs := planningCatalogIDs(bundles)
	out.Catalogs, err = readPlanningBudgetCatalogs(ctx, tx, id, out.Scope, catalogIDs)
	if err != nil {
		return out, err
	}
	out.Mappings, err = comparisonMappings(ctx, tx, id, catalogIDs, out.Scope.RecipesGeneration, out.Scope.PantryGeneration, out.Scope.PantryEnabled)
	if err != nil {
		return out, err
	}
	selected := []string{}
	for _, meal := range out.Week.Meals {
		if meal.Kind == "recipe" && (meal.Status == "open" || meal.Status == "planned") {
			selected = append(selected, meal.ID)
		}
	}
	if len(selected) > 0 {
		out.Existing, err = readPlanningDemand(ctx, tx, id, pid, selected, nil)
		if err != nil {
			return out, err
		}
		for _, line := range out.Existing.Lines {
			if line.Reason != "" || line.Demand == nil {
				out.UnknownLines++
			}
		}
		out.UnknownLines += len(out.Existing.Issues)
		for _, product := range out.Existing.Products {
			if planningAutoplanRat(product.Undercovered).Sign() > 0 {
				out.UnknownLines++
			}
		}
	}
	products := map[string]string{}
	for _, mapping := range out.Mappings {
		if comparisonMappingReason("present", mapping, out.Scope.PantryEnabled) == "" {
			products[String(mapping["product_id"])] = String(mapping["unit"])
		}
	}
	for _, product := range out.Existing.Products {
		if product.Unit != "" && value.ValidUUID(product.ID) {
			products[product.ID] = product.Unit
		}
	}
	if out.Scope.PantryEnabled {
		out.Lots, err = readSuggestionLots(ctx, tx, id, products, in.Generation, out.Scope.PantryGeneration, out.Scope.ReadAt)
		if err != nil {
			return out, err
		}
		out.OwnClaims, err = readPlanningAutoplanOwnClaims(ctx, tx, id, selected, in.Generation, out.Scope)
		if err != nil {
			return out, err
		}
	}
	if lock {
		if err = lockPlanningAutoplanDependencies(ctx, tx, id, out, products, catalogIDs); err != nil {
			return out, err
		}
		// Re-read after all NOWAIT parent locks. This is required by the shared
		// READ COMMITTED domain guards and prevents pre-lock dependency carry.
		return readPlanningAutoplanData(ctx, tx, id, plan, in, false)
	}
	return out, nil
}

func readPlanningAutoplanCandidates(ctx context.Context, tx pgx.Tx, id auth.Identity, in PlanningAutoplanInput) ([]planningAutoplanCandidate, error) {
	ids := make([]string, 0, len(in.Candidates))
	for _, candidate := range in.Candidates {
		ids = append(ids, candidate.SourceVersionID)
	}
	rows, err := tx.Query(ctx, `SELECT v.id,v.recipe_id,octet_length(v.snapshot::text),jsonb_array_length(v.snapshot->'ingredients'),
 encode(sha256(convert_to(v.snapshot::text,'UTF8')),'hex'),
 CASE WHEN octet_length(v.snapshot::text)<=1048576 THEN v.snapshot END
 FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id
 WHERE v.household_id=$1 AND v.id=ANY($2::uuid[]) AND r.archived_at IS NULL ORDER BY v.id LIMIT 21`, id.HouseholdID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []planningAutoplanCandidate{}
	var bytes int64
	ingredients := 0
	for rows.Next() {
		var candidate planningAutoplanCandidate
		var count int
		var raw []byte
		if err = rows.Scan(&candidate.SourceVersionID, &candidate.RecipeID, &candidate.SnapshotBytes, &count, &candidate.SnapshotHash, &raw); err != nil {
			return nil, err
		}
		bytes += candidate.SnapshotBytes
		ingredients += count
		if candidate.SnapshotBytes > planningComponentBytes || bytes > planningCopyBytes || ingredients > planningIngredientLimit {
			return nil, Invalid{"Rezeptauswahl auf 1 MiB je Stand, 16 MiB insgesamt und 500 Zutaten begrenzen"}
		}
		candidate.Bundle, err = recipeBundle(raw)
		if err != nil {
			return nil, err
		}
		candidate.Title = String(candidate.Bundle.Recipe["title"])
		if candidate.Title == "" {
			return nil, Invalid{"Rezeptstand ohne Titel kann nicht geplant werden"}
		}
		candidate.PrepMinutes = planningAutoplanSourceMinutes(candidate.Bundle.Recipe["prep_minutes"])
		candidate.CookMinutes = planningAutoplanSourceMinutes(candidate.Bundle.Recipe["cook_minutes"])
		out = append(out, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(ids) {
		return nil, PlanningConflict{"Ein ausgewählter Rezeptstand ist nicht mehr verfügbar"}
	}
	return out, nil
}

func planningAutoplanSourceMinutes(raw any) *int64 {
	minutes := Int(raw)
	if minutes <= 0 || minutes > 525600 {
		return nil
	}
	return &minutes
}

func readPlanningAutoplanOwnClaims(ctx context.Context, tx pgx.Tx, id auth.Identity, selected []string, generation int64, scope planningBudgetScope) (map[string]string, error) {
	out := map[string]string{}
	if len(selected) == 0 {
		return out, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT 1 FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND r.meal_id=ANY($2::uuid[]) AND r.state='active' AND r.expires_at>$3
 AND r.mealplan_generation=$4 AND r.pantry_generation=$5 LIMIT 5001) bounded`,
		id.HouseholdID, selected, scope.ReadAt, generation, scope.PantryGeneration).Scan(&count); err != nil {
		return nil, err
	}
	if count > suggestionLotLimit {
		return nil, Invalid{"Mehr als 5000 eigene Reservierungspositionen; Auswahl verkleinern"}
	}
	rows, err := tx.Query(ctx, `WITH selected_lots AS (
 SELECT DISTINCT a.lot_id FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND r.meal_id=ANY($2::uuid[]) AND r.state='active' AND r.expires_at>$3
 AND r.mealplan_generation=$4 AND r.pantry_generation=$5), claims AS (
 SELECT a.lot_id,sum(a.quantity) quantity FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND a.lot_id IN(SELECT lot_id FROM selected_lots) AND r.state='active' AND r.expires_at>$3
 AND r.mealplan_generation=$4 AND r.pantry_generation=$5 GROUP BY a.lot_id)
 SELECT r.meal_id,a.product_id,sum(a.quantity)::text FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 JOIN mealplan.meals m ON m.household_id=r.household_id AND m.id=r.meal_id
 JOIN pantry.lots l ON l.household_id=a.household_id AND l.id=a.lot_id
 JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 JOIN claims ON claims.lot_id=l.id AND b.quantity>=claims.quantity
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id AND p.archived_at IS NULL
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id AND loc.archived_at IS NULL
 WHERE a.household_id=$1 AND r.meal_id=ANY($2::uuid[]) AND r.state='active' AND r.expires_at>$3
 AND (l.use_by_on IS NULL OR l.use_by_on>=m.planned_on)
 AND r.mealplan_generation=$4 AND r.pantry_generation=$5 GROUP BY r.meal_id,a.product_id ORDER BY r.meal_id,a.product_id`,
		id.HouseholdID, selected, scope.ReadAt, generation, scope.PantryGeneration)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, pid, quantity string
		if err = rows.Scan(&mid, &pid, &quantity); err != nil {
			return nil, err
		}
		out[mid+"/"+pid] = quantity
	}
	return out, rows.Err()
}

func lockPlanningAutoplanDependencies(ctx context.Context, tx pgx.Tx, id auth.Identity, data planningAutoplanData, products map[string]string, catalogs []string) error {
	if _, err := tx.Exec(ctx, `SELECT module_id FROM core.module_states WHERE household_id=$1
 AND module_id=ANY($2::text[]) ORDER BY module_id FOR SHARE NOWAIT`, id.HouseholdID, []string{"pantry", "recipes", "shopping"}); err != nil {
		return shoppingContention(err)
	}
	recipes := []string{}
	for _, candidate := range data.Candidates {
		recipes = append(recipes, candidate.RecipeID)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM recipes.recipes WHERE household_id=$1 AND id=ANY($2::uuid[])
 ORDER BY id FOR SHARE NOWAIT`, id.HouseholdID, recipes); err != nil {
		return shoppingContention(err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=ANY($2::uuid[])
 ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, catalogs); err != nil {
		return shoppingContention(err)
	}
	productIDs := make([]string, 0, len(products))
	for pid := range products {
		productIDs = append(productIDs, pid)
	}
	sort.Strings(productIDs)
	if _, err := tx.Exec(ctx, `SELECT id FROM pantry.products WHERE household_id=$1 AND id=ANY($2::uuid[])
 ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, productIDs); err != nil {
		return shoppingContention(err)
	}
	lotIDs := make([]string, 0, len(data.Lots))
	for _, lot := range data.Lots {
		lotIDs = append(lotIDs, lot.ID)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM pantry.locations WHERE household_id=$1 AND id IN
 (SELECT location_id FROM pantry.lots WHERE household_id=$1 AND id=ANY($2::uuid[])) ORDER BY id FOR SHARE NOWAIT`, id.HouseholdID, lotIDs); err != nil {
		return shoppingContention(err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM pantry.lots WHERE household_id=$1 AND id=ANY($2::uuid[])
 ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, lotIDs); err != nil {
		return shoppingContention(err)
	}
	shopping := map[string]bool{}
	for _, catalog := range data.Catalogs {
		if pid := String(catalog["product_id"]); value.ValidUUID(pid) {
			shopping[pid] = true
		}
	}
	shoppingIDs := make([]string, 0, len(shopping))
	for pid := range shopping {
		shoppingIDs = append(shoppingIDs, pid)
	}
	sort.Strings(shoppingIDs)
	_, err := tx.Exec(ctx, `SELECT id FROM shopping.products WHERE household_id=$1 AND id=ANY($2::uuid[])
 ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, shoppingIDs)
	return shoppingContention(err)
}

func planningAutoplanQuantities(candidate planningAutoplanCandidate, servings string, data planningAutoplanData) ([]planningDemandQuantity, error) {
	selected, selectedOK := recipePositiveDecimal(servings)
	base, baseOK := recipePositiveDecimal(String(candidate.Bundle.Recipe["base_servings"]))
	if !selectedOK || !baseOK {
		return nil, Invalid{"Rezept- und Produktionsportionen prüfen"}
	}
	out := []planningDemandQuantity{}
	for _, ingredient := range candidate.Bundle.Ingredients {
		catalog := String(ingredient["catalog_id"])
		mapping := data.Mappings[catalog]
		line := planningDemandQuantity{}
		if comparisonMappingReason(catalog, mapping, data.Scope.PantryEnabled) == "" {
			line.ProductID, line.Unit = String(mapping["product_id"]), String(mapping["unit"])
			line.Quantity, _ = suggestionIngredientQuantity(ingredient, mapping, base, selected)
		}
		out = append(out, line)
	}
	return out, nil
}

func planningAutoplanFloor(quantity *big.Rat) string {
	if quantity == nil || quantity.Sign() <= 0 {
		return "0"
	}
	scaled := new(big.Rat).Mul(quantity, big.NewRat(1000000, 1))
	integer := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	return new(big.Rat).SetFrac(integer, big.NewInt(1000000)).FloatString(6)
}
