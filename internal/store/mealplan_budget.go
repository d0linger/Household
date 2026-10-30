package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningManualEstimate struct {
	MealVersion int64  `json:"meal_version"`
	Minor       string `json:"minor"`
}

type PlanningAutoplanRules struct {
	Stock      int `json:"stock"`
	Urgency    int `json:"urgency"`
	Effort     int `json:"effort"`
	Cost       int `json:"cost"`
	Repetition int `json:"repetition"`
}

type PlanningBudgetInput struct {
	PlanningExtensionCommand
	SettingsVersion int64                             `json:"settings_version"`
	BudgetMinor     *string                           `json:"budget_minor"`
	ManualEstimates map[string]PlanningManualEstimate `json:"manual_estimates"`
	Rules           PlanningAutoplanRules             `json:"rules"`
}

type planningWeekSettings struct {
	Version         int64                             `json:"version"`
	BudgetMinor     *string                           `json:"budget_minor"`
	ManualEstimates map[string]PlanningManualEstimate `json:"manual_estimates"`
	Rules           PlanningAutoplanRules             `json:"rules"`
}

func planningDefaultRules() PlanningAutoplanRules {
	return PlanningAutoplanRules{Stock: 50, Urgency: 30, Effort: 15, Cost: 20, Repetition: 25}
}

func validatePlanningRules(rules PlanningAutoplanRules) error {
	for _, weight := range []int{rules.Stock, rules.Urgency, rules.Effort, rules.Cost, rules.Repetition} {
		if weight < 0 || weight > 100 {
			return Invalid{"Planungsgewichte müssen ganze Zahlen von 0 bis 100 sein"}
		}
	}
	return nil
}

func planningMinor(raw string) (int64, error) {
	minor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || minor < 0 || minor > 1000000000000 || strconv.FormatInt(minor, 10) != raw {
		return 0, Invalid{"Kosten als nichtnegative ganze Eurocent bis 1000000000000 angeben"}
	}
	return minor, nil
}

func readPlanningWeekSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, pid, week string) (planningWeekSettings, error) {
	out := planningWeekSettings{ManualEstimates: map[string]PlanningManualEstimate{}, Rules: planningDefaultRules()}
	var manual, rules []byte
	err := tx.QueryRow(ctx, `SELECT version,budget_minor::text,manual_estimates,rules FROM mealplan.weekly_settings
 WHERE household_id=$1 AND plan_id=$2 AND week_start=$3::date`, id.HouseholdID, pid, week).
		Scan(&out.Version, &out.BudgetMinor, &manual, &rules)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(manual) > 16384 || json.Unmarshal(manual, &out.ManualEstimates) != nil || len(out.ManualEstimates) > planningMealLimit {
		return out, Invalid{"Gespeicherte manuelle Kostenschätzungen erneut prüfen"}
	}
	if string(rules) != "{}" {
		if err = json.Unmarshal(rules, &out.Rules); err != nil {
			return out, err
		}
	}
	return out, validatePlanningRules(out.Rules)
}

func (s *Store) PlanningBudget(ctx context.Context, id auth.Identity, pid, week string) (Record, error) {
	tx, plan, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	out, err := readPlanningBudget(ctx, tx, id, plan, week, generation)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func (s *Store) MutatePlanningBudget(ctx context.Context, id auth.Identity, pid, key, requestID string, in PlanningBudgetInput) (Record, error) {
	if err := validatePlanningRules(in.Rules); err != nil {
		return nil, err
	}
	if in.BudgetMinor != nil {
		if _, err := planningMinor(*in.BudgetMinor); err != nil {
			return nil, err
		}
	}
	if in.SettingsVersion < 0 || len(in.ManualEstimates) > planningMealLimit {
		return nil, Invalid{"Einstellungsstand und höchstens 42 manuelle Schätzungen prüfen"}
	}
	return s.planningExtensionWrite(ctx, id, pid, key, requestID, "budget", in.PlanningExtensionCommand, in,
		func(ctx context.Context, tx pgx.Tx, plan Record) (Record, error) {
			if _, err := planningExtensionWindow(plan, in.Week); err != nil {
				return nil, err
			}
			current, err := readPlanningWeekSettings(ctx, tx, id, pid, in.Week)
			if err != nil {
				return nil, err
			}
			if current.Version != in.SettingsVersion {
				return nil, PlanningConflict{"Das Wochenbudget wurde geändert. Aktuellen Stand erneut prüfen."}
			}
			week, err := readPlanningWeek(ctx, tx, id, pid, in.Week)
			if err != nil {
				return nil, err
			}
			meals := map[string]PlanningMeal{}
			for _, meal := range week.Meals {
				meals[meal.ID] = meal
			}
			for mid, estimate := range in.ManualEstimates {
				meal, found := meals[mid]
				if !value.ValidUUID(mid) || !found || estimate.MealVersion != meal.Version ||
					(meal.Kind != "free" && meal.Kind != "out" && meal.Kind != "order") {
					return nil, PlanningConflict{"Manuelle Schätzung an eine aktuelle freie, auswärtige oder bestellte Mahlzeit dieser Woche binden"}
				}
				if _, err = planningMinor(estimate.Minor); err != nil {
					return nil, err
				}
			}
			if in.ManualEstimates == nil {
				in.ManualEstimates = map[string]PlanningManualEstimate{}
			}
			manual, err := json.Marshal(in.ManualEstimates)
			if err != nil || len(manual) > 16384 {
				return nil, Invalid{"Manuelle Schätzungen auf 16 KiB begrenzen"}
			}
			rules, err := json.Marshal(in.Rules)
			if err != nil {
				return nil, err
			}
			var version int64
			err = tx.QueryRow(ctx, `INSERT INTO mealplan.weekly_settings
 (household_id,plan_id,week_start,budget_minor,manual_estimates,rules,created_by,changed_by)
 VALUES($1,$2,$3::date,$4::bigint,$5,$6,$7,$7)
 ON CONFLICT(household_id,plan_id,week_start) DO UPDATE SET budget_minor=EXCLUDED.budget_minor,
 manual_estimates=EXCLUDED.manual_estimates,rules=EXCLUDED.rules,changed_by=EXCLUDED.changed_by
 RETURNING version`, id.HouseholdID, pid, in.Week, in.BudgetMinor, manual, rules, id.UserID).Scan(&version)
			if err != nil {
				return nil, err
			}
			return Record{"plan_id": pid, "week": in.Week, "settings_version": version}, nil
		})
}

type planningBudgetScope struct {
	RecipesEnabled, ShoppingEnabled, PantryEnabled          bool
	RecipesGeneration, ShoppingGeneration, PantryGeneration int64
	ReadAt                                                  time.Time
	Timezone, Today                                         string
}

func readPlanningBudgetScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (planningBudgetScope, error) {
	var out planningBudgetScope
	err := tx.QueryRow(ctx, `SELECT r.state='enabled',r.data_generation,s.state='enabled',s.data_generation,
 p.state='enabled',p.data_generation,transaction_timestamp(),h.timezone,
 (transaction_timestamp() AT TIME ZONE h.timezone)::date::text
 FROM core.households h JOIN core.module_states r ON r.household_id=h.id AND r.module_id='recipes'
 JOIN core.module_states s ON s.household_id=h.id AND s.module_id='shopping'
 JOIN core.module_states p ON p.household_id=h.id AND p.module_id='pantry' WHERE h.id=$1`, id.HouseholdID).
		Scan(&out.RecipesEnabled, &out.RecipesGeneration, &out.ShoppingEnabled, &out.ShoppingGeneration,
			&out.PantryEnabled, &out.PantryGeneration, &out.ReadAt, &out.Timezone, &out.Today)
	return out, err
}

func readPlanningBudgetCatalogs(ctx context.Context, tx pgx.Tx, id auth.Identity, scope planningBudgetScope, ids []string) (map[string]Record, error) {
	out := map[string]Record{}
	if len(ids) == 0 || !scope.RecipesEnabled || !scope.ShoppingEnabled {
		return out, nil
	}
	if len(ids) > planningIngredientLimit {
		return nil, Invalid{"Kostenprüfung auf 500 Zutaten begrenzen"}
	}
	rows, err := tx.Query(ctx, `SELECT c.id,jsonb_build_object('catalog_version',c.version,
 'density_g_per_ml',c.density_g_per_ml::text,'density_source',c.density_source,'density_observed_on',c.density_observed_on,
 'binding_id',b.id,'binding_version',b.version,'product_id',p.id,'product_version',p.version,
 'price_minor',o.price_minor::text,'price_quantity',o.quantity::text,'price_unit_code',o.unit_code,
 'price_observed_on',o.observed_on,'price_store',o.store_name)
 FROM recipes.ingredient_catalog c LEFT JOIN core.ingredient_product_bindings b
 ON b.household_id=c.household_id AND b.catalog_id=c.id AND b.source_generation=$3
 LEFT JOIN shopping.products p ON p.household_id=c.household_id AND p.id=b.shopping_product_id AND b.shopping_generation=$4
 LEFT JOIN LATERAL(SELECT price.price_minor,price.quantity,price.unit_code,price.observed_on,st.name store_name
 FROM shopping.price_observations price JOIN shopping.stores st ON st.household_id=price.household_id AND st.id=price.store_id
 WHERE price.household_id=c.household_id AND price.product_id=p.id AND price.observed_on<=$5::date
 ORDER BY price.observed_on DESC,price.created_at DESC,price.id DESC LIMIT 1) o ON p.id IS NOT NULL
 WHERE c.household_id=$1 AND c.id=ANY($2::uuid[]) AND NOT c.archived LIMIT 501`,
		id.HouseholdID, ids, scope.RecipesGeneration, scope.ShoppingGeneration, scope.Today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid string
		var raw []byte
		if err = rows.Scan(&cid, &raw); err != nil {
			return nil, err
		}
		catalog, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out[cid] = catalog
	}
	return out, rows.Err()
}

func planningCatalogIDs(bundles map[string]RecipeBundle) []string {
	seen := map[string]bool{}
	for _, bundle := range bundles {
		for _, ingredient := range bundle.Ingredients {
			if cid := String(ingredient["catalog_id"]); value.ValidUUID(cid) {
				seen[cid] = true
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for cid := range seen {
		ids = append(ids, cid)
	}
	sort.Strings(ids)
	return ids
}
