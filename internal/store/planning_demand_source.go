package store

import (
	"context"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func readDemandMeals(ctx context.Context, tx pgx.Tx, id auth.Identity, pid string, selected []string, out *PlanningDemand) error {
	prior := len(out.Meals)
	rows, err := tx.Query(ctx, `SELECT id,title,planned_on::text,meal_slot,version,kind,status FROM mealplan.meals
 WHERE household_id=$1 AND plan_id=$2 AND id=ANY($3::uuid[]) ORDER BY planned_on,meal_slot,id LIMIT 43`, id.HouseholdID, pid, selected)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var meal PlanningDemandMeal
		var kind, status string
		if err = rows.Scan(&meal.ID, &meal.Title, &meal.Date, &meal.Slot, &meal.Version, &kind, &status); err != nil {
			return err
		}
		if kind != "recipe" || status != "open" && status != "planned" {
			return PlanningConflict{"Nur ganze offene oder geplante Rezeptmahlzeiten berücksichtigen. Aktuelle Auswahl erneut prüfen."}
		}
		meal.PlanID = pid
		out.Meals = append(out.Meals, meal)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(out.Meals)-prior != len(selected) {
		return ErrNotFound
	}
	return nil
}

func readDemandComponents(ctx context.Context, tx pgx.Tx, id auth.Identity, selected []string, out *PlanningDemand) ([]planningDemandComponent, error) {
	rows, err := tx.Query(ctx, `SELECT id,meal_id,octet_length(snapshot::text),jsonb_array_length(snapshot->'ingredients')
 FROM mealplan.components WHERE household_id=$1 AND meal_id=ANY($2::uuid[]) ORDER BY meal_id,position,id LIMIT 421`, id.HouseholdID, selected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	mealCount, mealBytes := make(map[string]int), make(map[string]int64)
	var totalBytes, ingredients int64
	for rows.Next() {
		var cid, mid string
		var size, count int64
		if err = rows.Scan(&cid, &mid, &size, &count); err != nil {
			return nil, err
		}
		ids = append(ids, cid)
		mealCount[mid]++
		mealBytes[mid] += size
		totalBytes += size
		ingredients += count
		if len(ids) > planningMealLimit*planningComponentLimit || mealCount[mid] > planningComponentLimit ||
			size > planningComponentBytes || mealBytes[mid] > planningMealBytes || totalBytes > planningCopyBytes || ingredients > planningIngredientLimit {
			return nil, Invalid{"Vorschau auf 42 Mahlzeiten, zehn Komponenten je Mahlzeit, insgesamt 500 Zutaten und 16 MiB Rezeptstände begrenzen"}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, meal := range out.Meals {
		if mealCount[meal.ID] == 0 {
			out.Issues = append(out.Issues, Record{"meal_id": meal.ID, "reason": "components_missing"})
		}
	}
	components := make([]planningDemandComponent, 0, len(ids))
	if len(ids) == 0 {
		return components, nil
	}
	rows, err = tx.Query(ctx, `SELECT id,meal_id,position,servings::text,version,source_generation,encode(sha256(convert_to(snapshot::text,'UTF8')),'hex'),snapshot
 FROM mealplan.components WHERE household_id=$1 AND id=ANY($2::uuid[]) ORDER BY meal_id,position,id LIMIT 421`, id.HouseholdID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var component planningDemandComponent
		if err = rows.Scan(&component.id, &component.meal, &component.position, &component.servings, &component.version, &component.sourceGeneration, &component.hash, &component.snapshot); err != nil {
			return nil, err
		}
		components = append(components, component)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(components) != len(ids) {
		return nil, ErrConflict
	}
	return components, nil
}

func readDemandBindings(ctx context.Context, tx pgx.Tx, id auth.Identity, components []planningDemandComponent, generation, pantryGeneration int64, enabled bool) (map[string]Record, error) {
	result := make(map[string]Record)
	if len(components) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(components))
	for _, component := range components {
		ids = append(ids, component.id)
	}
	rows, err := tx.Query(ctx, `SELECT b.component_id,b.ingredient_id,jsonb_build_object('id',b.id,'version',b.version,'assigned',b.product_id IS NOT NULL,
 'target_current',$5 AND b.pantry_generation=$4,'product_id',p.id,'product_name',p.name,'product_version',p.version,'unit',p.unit_code,
 'confirmed_unit',CASE WHEN $5 AND b.pantry_generation=$4 THEN b.unit_code END,'archived',p.archived_at IS NOT NULL,
 'integer_quantities',p.integer_quantities,'source_hash',encode(b.source_hash,'hex')) FROM core.planning_ingredient_bindings b
 LEFT JOIN pantry.products p ON $5 AND b.pantry_generation=$4 AND p.household_id=b.household_id AND p.id=b.product_id
 WHERE b.household_id=$1 AND b.mealplan_generation=$2 AND b.component_id=ANY($3::uuid[]) ORDER BY b.component_id,b.ingredient_id LIMIT 501`,
		id.HouseholdID, generation, ids, pantryGeneration, enabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, ingredient string
		var raw []byte
		if err = rows.Scan(&cid, &ingredient, &raw); err != nil {
			return nil, err
		}
		binding, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result[cid+"/"+ingredient] = binding
		if len(result) > planningIngredientLimit {
			return nil, Invalid{"Mehr als 500 bestätigte Zutaten in dieser Auswahl"}
		}
	}
	return result, rows.Err()
}

func demandBindingView(binding Record, enabled bool, generation int64) Record {
	if binding == nil {
		return publicPlanningBinding(nil, enabled, generation)
	}
	current := enabled && binding["target_current"] == true && binding["product_id"] != nil
	result := Record{"id": binding["id"], "version": binding["version"], "assigned": binding["assigned"],
		"target_current": current, "product_id": nil, "product_name": nil, "product_version": nil, "unit": nil}
	if current {
		for _, field := range []string{"product_id", "product_name", "product_version", "unit"} {
			result[field] = binding[field]
		}
	}
	return result
}

func planningMappingReason(binding Record, sourceHash string, enabled bool) string {
	switch {
	case !enabled:
		return "pantry_disabled"
	case binding == nil || binding["assigned"] != true:
		return "product_unassigned"
	case String(binding["source_hash"]) != sourceHash:
		return "binding_source_changed"
	case binding["target_current"] != true:
		return "binding_stale"
	case binding["product_id"] == nil:
		return "product_missing"
	case binding["archived"] == true:
		return "product_archived"
	case binding["confirmed_unit"] != binding["unit"]:
		return "product_unit_changed"
	default:
		return ""
	}
}
