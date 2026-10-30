package store

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func readPlanningBudget(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, start string, generation int64) (Record, error) {
	if _, err := planningExtensionWindow(plan, start); err != nil {
		return nil, err
	}
	pid := String(plan["id"])
	week, err := readPlanningWeek(ctx, tx, id, pid, start)
	if err != nil {
		return nil, err
	}
	settings, err := readPlanningWeekSettings(ctx, tx, id, pid, start)
	if err != nil {
		return nil, err
	}
	scope, err := readPlanningBudgetScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	selected := []string{}
	for _, meal := range week.Meals {
		if meal.Kind == "recipe" && meal.Status != "cancelled" {
			selected = append(selected, meal.ID)
		}
	}
	components, err := readDemandComponents(ctx, tx, id, selected, &PlanningDemand{})
	if err != nil {
		return nil, err
	}
	bundles := map[string]RecipeBundle{}
	byMeal := map[string][]planningDemandComponent{}
	for _, component := range components {
		bundle, parseErr := recipeBundle(component.snapshot)
		if parseErr != nil {
			return nil, parseErr
		}
		bundles[component.id] = bundle
		byMeal[component.meal] = append(byMeal[component.meal], component)
	}
	catalogs, err := readPlanningBudgetCatalogs(ctx, tx, id, scope, planningCatalogIDs(bundles))
	if err != nil {
		return nil, err
	}
	leftovers, err := readPlanningBudgetLeftovers(ctx, tx, id, pid, week.Days)
	if err != nil {
		return nil, err
	}
	total := new(big.Rat)
	unknown := 0
	meals := []Record{}
	for _, meal := range week.Meals {
		known := new(big.Rat)
		missing := 0
		lines := []Record{}
		reason := ""
		switch {
		case meal.Status == "cancelled":
			reason = "cancelled"
		case meal.Kind == "recipe":
			if len(byMeal[meal.ID]) == 0 {
				missing++
				reason = "components_missing"
			}
			for _, component := range byMeal[meal.ID] {
				bundle := bundles[component.id]
				componentLines, amount, count, costErr := planningBundleCost(bundle, component.servings, component.sourceGeneration, scope, catalogs)
				if costErr != nil {
					return nil, costErr
				}
				for _, line := range componentLines {
					line["component_id"], line["meal_id"] = component.id, meal.ID
					lines = append(lines, line)
				}
				known.Add(known, amount)
				missing += count
			}
		case meal.Kind == "leftovers":
			link := leftovers[meal.ID]
			reason = "leftover_source_unreviewed"
			missing++
			if link != nil && link["current"] == true {
				reason = "leftover_production_counted_at_source"
				if link["source_in_week"] != true {
					reason = "leftover_production_outside_week"
				}
				portion, portionOK := recipePositiveDecimal(String(link["portions"]))
				servings, servingsOK := recipePositiveDecimal(meal.Servings)
				if portionOK && servingsOK && portion.Cmp(servings) == 0 {
					missing = 0
				} else {
					reason = "leftover_partial_production_at_source"
				}
			}
			if link != nil {
				lines = append(lines, link)
			}
		default:
			estimate, exists := settings.ManualEstimates[meal.ID]
			if exists && estimate.MealVersion == meal.Version {
				minor, parseErr := planningMinor(estimate.Minor)
				if parseErr != nil {
					return nil, parseErr
				}
				known.SetInt64(minor)
				reason = "reviewed_manual_estimate"
			} else {
				missing++
				reason = "manual_estimate_missing"
				if exists {
					reason = "manual_estimate_stale"
				}
			}
		}
		minor, approximate := recipeMoneyMinor(known)
		meals = append(meals, Record{"meal_id": meal.ID, "meal_version": meal.Version, "title": meal.Title,
			"planned_on": meal.PlannedOn, "kind": meal.Kind, "known_minor": minor,
			"approximate": approximate, "unknown_count": missing, "complete": missing == 0,
			"reason": reason, "lines": lines})
		total.Add(total, known)
		unknown += missing
	}
	minor, approximate := recipeMoneyMinor(total)
	status := "no_target"
	if settings.BudgetMinor != nil {
		status = "unknown"
		if unknown == 0 {
			target, parseErr := planningMinor(*settings.BudgetMinor)
			if parseErr != nil {
				return nil, parseErr
			}
			status = "within"
			if total.Cmp(big.NewRat(target, 1)) > 0 {
				status = "over"
			}
		}
	}
	return Record{"generation": generation, "plan_version": Int(plan["version"]), "week": start,
		"settings": settings, "currency": "EUR", "calculated_at": scope.ReadAt, "known_minor": minor,
		"approximate": approximate, "unknown_count": unknown, "complete": unknown == 0,
		"budget_status": status, "meals": meals, "cost_basis": "ingredient_use_not_checkout",
		"recipes_enabled": scope.RecipesEnabled, "shopping_enabled": scope.ShoppingEnabled}, nil
}

func planningBundleCost(bundle RecipeBundle, servings string, sourceGeneration int64, scope planningBudgetScope, catalogs map[string]Record) ([]Record, *big.Rat, int, error) {
	selected, selectedOK := recipePositiveDecimal(servings)
	base, baseOK := recipePositiveDecimal(String(bundle.Recipe["base_servings"]))
	if !selectedOK || !baseOK {
		return nil, nil, 0, Invalid{"Gespeicherte Produktions- und Basisportionen prüfen"}
	}
	lines := []Record{}
	known := new(big.Rat)
	unknown := 0
	if len(bundle.Ingredients) == 0 {
		return lines, known, 1, nil
	}
	for _, ingredient := range bundle.Ingredients {
		catalog := catalogs[String(ingredient["catalog_id"])]
		blocked := ""
		switch {
		case !scope.RecipesEnabled:
			blocked = "recipes_disabled"
		case !scope.ShoppingEnabled:
			blocked = "shopping_disabled"
		case sourceGeneration != scope.RecipesGeneration:
			blocked = "source_generation_changed"
		}
		if blocked != "" {
			catalog = nil
		}
		line, _, amount := recipeIngredientInsight(ingredient, catalog, selected, base, scope.ShoppingGeneration)
		if blocked != "" {
			line["status"] = blocked
		}
		if amount != nil {
			observed, dateErr := time.Parse(time.DateOnly, String(catalog["price_observed_on"]))
			today, todayErr := time.Parse(time.DateOnly, scope.Today)
			if dateErr != nil || todayErr != nil {
				line["status"], amount = "price_date_unknown", nil
			} else {
				age := int(today.Sub(observed).Hours() / 24)
				line["price_age_days"] = age
				if age > 90 {
					line["previous_estimate_minor"] = line["amount_minor"]
					delete(line, "amount_minor")
					line["status"], amount = "price_stale", nil
				}
			}
		}
		lines = append(lines, line)
		if amount == nil {
			unknown++
		} else {
			known.Add(known, amount)
		}
	}
	return lines, known, unknown, nil
}

func readPlanningBudgetLeftovers(ctx context.Context, tx pgx.Tx, id auth.Identity, pid string, days []string) (map[string]Record, error) {
	out := map[string]Record{}
	rows, err := tx.Query(ctx, `SELECT a.target_meal_id,jsonb_build_object('source_meal_id',s.source_meal_id,
 'source_component_id',s.source_component_id,'portions',a.servings::text,
 'source_in_week',m.planned_on BETWEEN $3::date AND $4::date,
 'current',a.reviewed_source_version=s.version AND s.reviewed_meal_version=m.version AND s.reviewed_component_version=c.version
 AND s.source_hash=sha256(convert_to(c.snapshot::text,'UTF8'))
 AND m.kind='recipe' AND m.servings_mode='manual' AND s.expected_servings<=c.servings
 AND a.reviewed_target_version=t.version AND t.kind='leftovers' AND a.servings<=t.servings
 AND m.status<>'cancelled' AND t.status<>'cancelled')
 FROM mealplan.leftover_allocations a JOIN mealplan.leftover_sources s ON s.household_id=a.household_id AND s.id=a.source_id
 JOIN mealplan.meals m ON m.household_id=s.household_id AND m.id=s.source_meal_id
 JOIN mealplan.components c ON c.household_id=s.household_id AND c.id=s.source_component_id
 JOIN mealplan.meals t ON t.household_id=a.household_id AND t.id=a.target_meal_id
 WHERE a.household_id=$1 AND a.plan_id=$2 AND t.planned_on BETWEEN $3::date AND $4::date LIMIT 43`,
		id.HouseholdID, pid, days[0], days[6])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		var raw []byte
		if err = rows.Scan(&mid, &raw); err != nil {
			return nil, err
		}
		record, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out[mid] = record
	}
	return out, rows.Err()
}

func copyPlanningWeekSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, oldPlan, newPlan, oldWeek, newWeek string, mealIDs map[string]string) error {
	settings, err := readPlanningWeekSettings(ctx, tx, id, oldPlan, oldWeek)
	if err != nil || settings.Version == 0 {
		return err
	}
	manual := map[string]PlanningManualEstimate{}
	for oldID, estimate := range settings.ManualEstimates {
		newID := mealIDs[oldID]
		if newID == "" {
			continue
		}
		var oldVersion, newVersion int64
		if err = tx.QueryRow(ctx, `SELECT old.version,new.version FROM mealplan.meals old JOIN mealplan.meals new
 ON new.household_id=old.household_id WHERE old.household_id=$1 AND old.id=$2 AND new.id=$3`, id.HouseholdID, oldID, newID).
			Scan(&oldVersion, &newVersion); err != nil {
			return err
		}
		if estimate.MealVersion == oldVersion {
			estimate.MealVersion = newVersion
			manual[newID] = estimate
		}
	}
	encoded, err := json.Marshal(manual)
	if err != nil {
		return err
	}
	rules, err := json.Marshal(settings.Rules)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mealplan.weekly_settings
 (household_id,plan_id,week_start,budget_minor,manual_estimates,rules,created_by,changed_by)
 VALUES($1,$2,$3::date,$4::bigint,$5,$6,$7,$7)`, id.HouseholdID, newPlan, newWeek, settings.BudgetMinor, encoded, rules, id.UserID)
	return err
}
