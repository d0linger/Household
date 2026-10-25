package store

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningDemandMeal struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Date    string `json:"planned_on"`
	Slot    string `json:"slot"`
	Version int64  `json:"version"`
}

type PlanningDemandLine struct {
	MealID           string            `json:"meal_id"`
	ComponentID      string            `json:"component_id"`
	ComponentTitle   string            `json:"component_title"`
	ComponentVersion int64             `json:"component_version"`
	SourceHash       string            `json:"source_hash"`
	IngredientID     string            `json:"ingredient_id"`
	Name             string            `json:"name"`
	Quantity         string            `json:"source_quantity"`
	SourceUnit       string            `json:"source_unit"`
	Servings         string            `json:"servings"`
	BaseServings     string            `json:"base_servings"`
	Reason           string            `json:"reason"`
	ProductID        string            `json:"product_id,omitempty"`
	Binding          Record            `json:"binding"`
	Suggestion       Record            `json:"suggestion,omitempty"`
	Demand           *IngredientAmount `json:"demand,omitempty"`
}

type PlanningDemandProduct struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Unit                  string            `json:"unit"`
	IntegerQuantities     bool              `json:"integer_quantities"`
	Complete              bool              `json:"complete"`
	Demand                IngredientAmount  `json:"demand"`
	Physical              string            `json:"physical"`
	Eligible              string            `json:"eligible"`
	Own                   string            `json:"own"`
	Other                 string            `json:"other"`
	Reserved              string            `json:"reserved"`
	Free                  string            `json:"free"`
	AvailableForSelection string            `json:"available_for_selection"`
	Undercovered          string            `json:"undercovered"`
	Shortage              *IngredientAmount `json:"shortage,omitempty"`
	Bookable              bool              `json:"bookable"`
}

type PlanningDemand struct {
	PlanID           string                   `json:"plan_id"`
	PlanName         string                   `json:"plan_name"`
	PlanVersion      int64                    `json:"plan_version"`
	Generation       int64                    `json:"generation"`
	PantryGeneration int64                    `json:"pantry_generation"`
	PantryEnabled    bool                     `json:"pantry_enabled"`
	ReadAt           time.Time                `json:"read_at"`
	Timezone         string                   `json:"timezone"`
	Outcome          string                   `json:"outcome"`
	ShoppingCoverage string                   `json:"shopping_coverage"`
	Meals            []PlanningDemandMeal     `json:"meals"`
	Lines            []PlanningDemandLine     `json:"lines"`
	Products         []*PlanningDemandProduct `json:"products"`
	Issues           []Record                 `json:"issues"`
}

type planningDemandComponent struct {
	id, meal, servings string
	version            int64
	sourceGeneration   int64
	hash               string
	snapshot           []byte
}

// PlanningDemand is read-only. It considers whole meals because manual claims
// cannot be attributed safely to an individual ingredient of a selected meal.
func (s *Store) PlanningDemand(ctx context.Context, id auth.Identity, pid string, selected []string) (PlanningDemand, error) {
	out := PlanningDemand{PlanID: pid, ShoppingCoverage: "unlinked", Meals: []PlanningDemandMeal{}, Lines: []PlanningDemandLine{}, Products: []*PlanningDemandProduct{}, Issues: []Record{}}
	if !value.ValidUUID(pid) || len(selected) == 0 || len(selected) > planningMealLimit {
		return out, Invalid{"1 bis 42 ganze Mahlzeiten desselben Plans auswählen"}
	}
	seen := make(map[string]bool, len(selected))
	for _, mid := range selected {
		if !value.ValidUUID(mid) || seen[mid] {
			return out, Invalid{"Mahlzeiten ausdrücklich und ohne doppelte Kennungen auswählen"}
		}
		seen[mid] = true
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	var template, recipesEnabled bool
	var recipesGeneration int64
	err = tx.QueryRow(ctx, `SELECT p.name,p.version,p.is_template,ms.data_generation,ps.data_generation,ps.state='enabled',h.timezone,transaction_timestamp(),
 rs.state='enabled',rs.data_generation FROM mealplan.plans p JOIN core.households h ON h.id=p.household_id
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='mealplan' AND ms.state='enabled'
 JOIN core.module_states ps ON ps.household_id=h.id AND ps.module_id='pantry'
 JOIN core.module_states rs ON rs.household_id=h.id AND rs.module_id='recipes'
 WHERE p.household_id=$1 AND p.id=$2`, id.HouseholdID, pid).Scan(&out.PlanName, &out.PlanVersion, &template, &out.Generation,
		&out.PantryGeneration, &out.PantryEnabled, &out.Timezone, &out.ReadAt, &recipesEnabled, &recipesGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if template {
		return out, Invalid{"Eine Planvorlage erzeugt keinen aktuellen Vorratsbedarf. Zuerst als eigene Woche übernehmen."}
	}
	if err = readDemandMeals(ctx, tx, id, pid, selected, &out); err != nil {
		return out, err
	}
	components, err := readDemandComponents(ctx, tx, id, selected, &out)
	if err != nil {
		return out, err
	}
	bindings, err := readDemandBindings(ctx, tx, id, components, out.Generation, out.PantryGeneration, out.PantryEnabled)
	if err != nil {
		return out, err
	}
	quantities := make([]planningDemandQuantity, 0, planningIngredientLimit)
	products := make(map[string]*PlanningDemandProduct)
	catalogs := make([]string, 0)
	lineCatalogs := make(map[int]string)
	for _, component := range components {
		bundle, parseErr := recipeBundle(component.snapshot)
		if parseErr != nil {
			return out, Invalid{"Gespeicherter Plan-Rezeptstand ist ungültig. Quelle in der Planung erneut prüfen."}
		}
		base, baseErr := value.Decimal(String(bundle.Recipe["base_servings"]))
		servings, portionsErr := value.Decimal(component.servings)
		if baseErr != nil || portionsErr != nil || base.Sign() <= 0 || servings.Sign() <= 0 {
			return out, Invalid{"Gespeicherte Basis- oder Komponentenportionen sind ungültig"}
		}
		if len(bundle.Ingredients) == 0 {
			out.Issues = append(out.Issues, Record{"meal_id": component.meal, "component_id": component.id, "reason": "ingredients_missing"})
		}
		if len(bundle.Ingredients) > 0 {
			if _, parseErr = planningIngredientSource(component.snapshot, String(bundle.Ingredients[0]["id"])); parseErr != nil {
				return out, parseErr
			}
		}
		for _, ingredient := range bundle.Ingredients {
			line := PlanningDemandLine{MealID: component.meal, ComponentID: component.id, ComponentTitle: String(bundle.Recipe["title"]),
				ComponentVersion: component.version, SourceHash: component.hash, IngredientID: String(ingredient["id"]), Name: String(ingredient["name"]),
				Quantity: String(ingredient["quantity"]), SourceUnit: String(ingredient["unit_code"]), Servings: component.servings, BaseServings: String(bundle.Recipe["base_servings"])}
			binding := bindings[component.id+"/"+line.IngredientID]
			line.Binding = demandBindingView(binding, out.PantryEnabled, out.PantryGeneration)
			line.Reason = planningMappingReason(binding, component.hash, out.PantryEnabled)
			var quantity *big.Rat
			if binding != nil && binding["product_id"] != nil {
				line.ProductID = String(binding["product_id"])
				if products[line.ProductID] == nil {
					products[line.ProductID] = &PlanningDemandProduct{ID: line.ProductID, Name: String(binding["product_name"]),
						Unit: String(binding["unit"]), IntegerQuantities: binding["integer_quantities"] == true}
				}
			}
			if line.Reason == "" {
				q, quantityErr := value.Decimal(line.Quantity)
				if quantityErr != nil || q.Sign() <= 0 {
					line.Reason = "quantity_unknown"
				} else {
					quantity, quantityErr = ingredientDemand(line.Quantity, line.SourceUnit, String(binding["unit"]), base, servings)
					if quantityErr != nil {
						line.Reason = "unit_incompatible"
					} else {
						amount, amountErr := ingredientAmount(quantity)
						if amountErr != nil {
							return out, amountErr
						}
						line.Demand = &amount
					}
				}
			}
			unit := ""
			if line.ProductID != "" {
				unit = products[line.ProductID].Unit
			}
			quantities = append(quantities, planningDemandQuantity{ProductID: line.ProductID, Unit: unit, Quantity: quantity})
			catalog := String(ingredient["catalog_id"])
			if binding == nil && recipesEnabled && component.sourceGeneration == recipesGeneration && value.ValidUUID(catalog) {
				catalogs = append(catalogs, catalog)
				lineCatalogs[len(out.Lines)] = catalog
			}
			out.Lines = append(out.Lines, line)
		}
	}
	if len(catalogs) > 0 {
		suggestions, suggestionErr := comparisonMappings(ctx, tx, id, catalogs, recipesGeneration, out.PantryGeneration, out.PantryEnabled)
		if suggestionErr != nil {
			return out, suggestionErr
		}
		for i, catalog := range lineCatalogs {
			if suggestion := suggestions[catalog]; comparisonMappingReason(catalog, suggestion, out.PantryEnabled) == "" {
				out.Lines[i].Suggestion = Record{"product_id": suggestion["product_id"], "product_name": suggestion["product_name"],
					"product_version": suggestion["product_version"], "unit": suggestion["unit"]}
			}
		}
	}
	groups, complete, err := groupPlanningDemand(quantities)
	if err != nil {
		return out, err
	}
	if err = planningDemandStock(ctx, tx, id, selected, &out, products); err != nil {
		return out, err
	}
	out.Outcome = "sufficient"
	for pid, group := range groups {
		product := products[pid]
		product.Complete = group.Complete
		product.Demand, err = ingredientAmount(group.Quantity)
		if err != nil {
			return out, err
		}
		product.Shortage, err = planningDemandShortage(group, product.AvailableForSelection)
		if err != nil {
			return out, err
		}
		if product.Shortage != nil {
			shortage, ok := new(big.Rat).SetString(product.Shortage.Rational)
			if !ok {
				return out, Invalid{"Bedarf konnte nicht exakt dargestellt werden"}
			}
			product.Bookable = shortage.Sign() == 0
			if shortage.Sign() > 0 {
				_, bookingErr := pantryExactAmount(shortage, product.IntegerQuantities)
				product.Bookable = bookingErr == nil
				out.Outcome = "shortage"
			}
		}
		out.Products = append(out.Products, product)
	}
	if !complete || len(out.Issues) > 0 || !out.PantryEnabled {
		out.Outcome = "incomplete"
	}
	sort.Slice(out.Products, func(i, j int) bool {
		if out.Products[i].Name == out.Products[j].Name {
			return out.Products[i].ID < out.Products[j].ID
		}
		return out.Products[i].Name < out.Products[j].Name
	})
	return out, tx.Commit(ctx)
}
