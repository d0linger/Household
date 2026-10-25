package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningPurchaseSelection struct {
	PlanID  string   `json:"plan_id"`
	MealIDs []string `json:"meal_ids"`
}

type PlanningPurchasePlan struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

type PlanningPurchaseRequirement struct {
	ID                 string `json:"id"`
	PlanID             string `json:"plan_id"`
	MealID             string `json:"meal_id"`
	ComponentID        string `json:"component_id"`
	IngredientID       string `json:"ingredient_id"`
	Version            int64  `json:"version"`
	Active             bool   `json:"active"`
	ReviewReason       string `json:"review_reason"`
	Disposition        string `json:"disposition"`
	PendingQuantity    string `json:"pending_quantity"`
	ShoppingGeneration int64  `json:"shopping_generation"`
	ListID             string `json:"list_id"`
	ItemID             string `json:"item_id"`
	ItemVersion        int64  `json:"item_version"`
	ItemFingerprint    string `json:"item_fingerprint"`
}

type PlanningPurchaseLine struct {
	MealID           string           `json:"meal_id"`
	ComponentID      string           `json:"component_id"`
	IngredientID     string           `json:"ingredient_id"`
	RequirementID    string           `json:"requirement_id"`
	Decision         string           `json:"decision"`
	RequiredDecision string           `json:"required_decision"`
	ReviewReasons    []string         `json:"review_reasons"`
	Basis            string           `json:"basis"`
	StockCoverage    IngredientAmount `json:"stock_coverage"`
	Desired          IngredientAmount `json:"desired"`
	Delta            IngredientAmount `json:"delta"`
	Additional       IngredientAmount `json:"additional"`
	Bookable         bool             `json:"bookable"`
	Action           string           `json:"action"`
	Reason           string           `json:"reason"`
	CurrentItem      Record           `json:"current_item,omitempty"`
}

type PlanningPurchasesPreview struct {
	Generation         int64                         `json:"generation"`
	PantryGeneration   int64                         `json:"pantry_generation"`
	ShoppingGeneration int64                         `json:"shopping_generation"`
	ReadAt             time.Time                     `json:"read_at"`
	Fingerprint        string                        `json:"fingerprint"`
	Ready              bool                          `json:"ready"`
	Plans              []PlanningPurchasePlan        `json:"plans"`
	Requirements       []PlanningPurchaseRequirement `json:"requirements"`
	EndedMeals         []string                      `json:"ended_meals"`
	Demand             PlanningDemand                `json:"demand"`
	Lines              []PlanningPurchaseLine        `json:"lines"`
	Issues             []Record                      `json:"issues"`
}

type planningPurchaseScope struct {
	view         PlanningPurchasesPreview
	requirements map[string]Record
	items        map[string]Record
	decisions    map[string]string
	endedMeals   map[string]bool
}

func planningPurchaseSelections(selected []PlanningPurchaseSelection) (map[string][]string, error) {
	if len(selected) > planningMealLimit {
		return nil, Invalid{"Höchstens 42 ganze Mahlzeiten auswählen"}
	}
	plans := make(map[string][]string)
	seen := make(map[string]bool)
	for _, selection := range selected {
		if !value.ValidUUID(selection.PlanID) || len(selection.MealIDs) == 0 || len(selection.MealIDs) > planningMealLimit {
			return nil, Invalid{"Plan und ausdrücklich ausgewählte ganze Mahlzeiten erforderlich"}
		}
		for _, mid := range selection.MealIDs {
			if !value.ValidUUID(mid) || seen[mid] || len(seen) >= planningMealLimit {
				return nil, Invalid{"1 bis 42 unterschiedliche ganze Mahlzeiten auswählen"}
			}
			seen[mid] = true
			plans[selection.PlanID] = append(plans[selection.PlanID], mid)
		}
	}
	return plans, nil
}

// PlanningPurchases includes the complete active household scope, even when a
// previous source needed no purchase or its purchase has already been stored.
// This method is read-only and does not activate the selected meals.
func (s *Store) PlanningPurchases(ctx context.Context, id auth.Identity, selected []PlanningPurchaseSelection) (PlanningPurchasesPreview, error) {
	return s.ReviewPlanningPurchases(ctx, id, selected, nil)
}

// ReviewPlanningPurchases previews explicitly named lifecycle decisions; it
// never persists them. The mutation verifies the same complete fingerprint.
func (s *Store) ReviewPlanningPurchases(ctx context.Context, id auth.Identity, selected []PlanningPurchaseSelection, choices []PlanningPurchaseDecision) (PlanningPurchasesPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	plans, err := planningPurchaseSelections(selected)
	if err != nil {
		return PlanningPurchasesPreview{}, err
	}
	_, decisions, err := planningPurchaseDecisions(choices)
	if err != nil {
		return PlanningPurchasesPreview{}, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlanningPurchasesPreview{}, err
	}
	defer rollback(tx)
	scope, err := readPlanningPurchaseScope(ctx, tx, id, plans, decisions, nil)
	if err != nil {
		return scope.view, err
	}
	return scope.view, tx.Commit(ctx)
}

// Mutation callers own the scope mutex and all source/product locks before
// invoking this reader with their one post-lock expiry instant.
func readPlanningPurchaseScope(ctx context.Context, tx pgx.Tx, id auth.Identity, selected map[string][]string, decisions map[string]string, readAt *time.Time) (planningPurchaseScope, error) {
	out := planningPurchaseScope{view: PlanningPurchasesPreview{Plans: []PlanningPurchasePlan{}, Requirements: []PlanningPurchaseRequirement{}, EndedMeals: []string{}, Lines: []PlanningPurchaseLine{}, Issues: []Record{}},
		requirements: make(map[string]Record), items: make(map[string]Record), decisions: decisions, endedMeals: make(map[string]bool)}
	demand := &out.view.Demand
	*demand = PlanningDemand{ShoppingCoverage: "typed_scope", Meals: []PlanningDemandMeal{}, Lines: []PlanningDemandLine{}, Products: []*PlanningDemandProduct{}, Issues: []Record{}}
	if readAt != nil && readAt.IsZero() {
		return out, Invalid{"Gemeinsamen gültigen Lesezeitpunkt nach den Sperren verwenden"}
	}
	var recipesEnabled, enabled bool
	var recipesGeneration int64
	err := tx.QueryRow(ctx, `SELECT ms.data_generation,ps.data_generation,ss.data_generation,
 ms.state='enabled' AND ps.state='enabled' AND ss.state='enabled',h.timezone,coalesce($2::timestamptz,transaction_timestamp()),
 rs.state='enabled',rs.data_generation FROM core.households h
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='mealplan'
 JOIN core.module_states ps ON ps.household_id=h.id AND ps.module_id='pantry'
 JOIN core.module_states ss ON ss.household_id=h.id AND ss.module_id='shopping'
 JOIN core.module_states rs ON rs.household_id=h.id AND rs.module_id='recipes' WHERE h.id=$1`, id.HouseholdID, readAt).
		Scan(&out.view.Generation, &out.view.PantryGeneration, &out.view.ShoppingGeneration, &enabled, &demand.Timezone, &out.view.ReadAt, &recipesEnabled, &recipesGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !enabled {
		return out, ErrNotFound
	}
	demand.Generation, demand.PantryGeneration, demand.PantryEnabled, demand.ReadAt = out.view.Generation, out.view.PantryGeneration, true, out.view.ReadAt
	plans := make(map[string][]string, len(selected))
	selectedMeals := make(map[string]bool)
	for pid, meals := range selected {
		plans[pid] = append([]string(nil), meals...)
		for _, mid := range meals {
			selectedMeals[mid] = true
		}
	}
	if err = readPlanningPurchaseRequirements(ctx, tx, id, &out, plans, selectedMeals); err != nil {
		return out, err
	}
	if err = readPlanningPurchaseEndedMeals(ctx, tx, id, &out, selectedMeals); err != nil {
		return out, err
	}
	allMeals := make([]string, 0, planningMealLimit)
	planIDs := make([]string, 0, len(plans))
	for pid, meals := range plans {
		sort.Strings(meals)
		allMeals = append(allMeals, meals...)
		planIDs = append(planIDs, pid)
	}
	if len(allMeals) == 0 || len(allMeals) > planningMealLimit {
		return out, Invalid{"Der vollständige aktive Planungsumfang muss 1 bis 42 ganze Mahlzeiten enthalten"}
	}
	sort.Strings(planIDs)
	for _, pid := range planIDs {
		var plan PlanningPurchasePlan
		var template bool
		err = tx.QueryRow(ctx, "SELECT id,name,version,is_template FROM mealplan.plans WHERE household_id=$1 AND id=$2", id.HouseholdID, pid).Scan(&plan.ID, &plan.Name, &plan.Version, &template)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, PlanningConflict{"Ein aktiver Plan fehlt. Den betroffenen Mahlzeitenbedarf ausdrücklich beenden; vorhandene Einkäufe bleiben erhalten."}
		}
		if err != nil {
			return out, err
		}
		if template {
			return out, Invalid{"Planvorlagen nicht in den aktiven Einkaufsbedarf aufnehmen"}
		}
		out.view.Plans = append(out.view.Plans, plan)
		if err = readDemandMeals(ctx, tx, id, pid, plans[pid], demand); err != nil {
			return out, err
		}
	}
	components, err := readDemandComponents(ctx, tx, id, allMeals, demand)
	if err != nil {
		return out, err
	}
	if err = readPlanningDemandContents(ctx, tx, id, allMeals, components, recipesEnabled, recipesGeneration, demand); err != nil {
		return out, err
	}
	if err = readPlanningPurchaseItems(ctx, tx, id, &out); err != nil {
		return out, err
	}
	err = finishPlanningPurchaseScope(&out)
	return out, err
}

func readPlanningPurchaseEndedMeals(ctx context.Context, tx pgx.Tx, id auth.Identity, out *planningPurchaseScope, selected map[string]bool) error {
	mids := make([]string, 0, len(selected))
	for mid := range selected {
		mids = append(mids, mid)
	}
	rows, err := tx.Query(ctx, `SELECT mid FROM unnest($3::uuid[]) mid WHERE EXISTS(
 SELECT 1 FROM core.planning_purchase_requirements r WHERE r.household_id=$1 AND r.mealplan_generation=$2 AND r.meal_id=mid AND NOT r.active)
 AND NOT EXISTS(SELECT 1 FROM core.planning_purchase_requirements r WHERE r.household_id=$1 AND r.mealplan_generation=$2 AND r.meal_id=mid AND r.active)
 ORDER BY mid LIMIT 43`, id.HouseholdID, out.view.Generation, mids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		if err = rows.Scan(&mid); err != nil {
			return err
		}
		out.endedMeals[mid] = true
		out.view.EndedMeals = append(out.view.EndedMeals, mid)
	}
	return rows.Err()
}

func readPlanningPurchaseRequirements(ctx context.Context, tx pgx.Tx, id auth.Identity, out *planningPurchaseScope, plans map[string][]string, selected map[string]bool) error {
	mids := make([]string, 0, len(selected))
	for mid := range selected {
		mids = append(mids, mid)
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(r)||jsonb_build_object('pending_quantity',r.pending_quantity::text,
 'item_fingerprint',encode(r.item_fingerprint,'hex'),'source_hash',encode(r.source_hash,'hex'),'reviewed_basis_hash',encode(r.reviewed_basis_hash,'hex'))
 FROM core.planning_purchase_requirements r WHERE household_id=$1 AND (active OR (meal_id=ANY($2::uuid[]) AND EXISTS(
 SELECT 1 FROM mealplan.components c CROSS JOIN LATERAL jsonb_array_elements(c.snapshot->'ingredients') ingredient
 WHERE c.household_id=r.household_id AND c.id=r.component_id AND ingredient->>'id'=r.ingredient_id::text)))
 ORDER BY plan_id,meal_id,component_id,ingredient_id LIMIT 501`, id.HouseholdID, mids)
	if err != nil {
		return err
	}
	defer rows.Close()
	seenMeals := make(map[string]string)
	for pid, meals := range plans {
		for _, mid := range meals {
			seenMeals[mid] = pid
		}
	}
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		r, decodeErr := decode(raw)
		if decodeErr != nil {
			return decodeErr
		}
		if len(out.requirements) >= planningIngredientLimit {
			return Invalid{"Der vollständige aktive Planungsumfang überschreitet 500 Zutaten"}
		}
		pid, mid := String(r["plan_id"]), String(r["meal_id"])
		if prior := seenMeals[mid]; prior != "" && prior != pid {
			return ErrConflict
		}
		if seenMeals[mid] == "" {
			plans[pid] = append(plans[pid], mid)
			seenMeals[mid] = pid
		}
		if Int(r["mealplan_generation"]) != out.view.Generation {
			return PlanningConflict{"Ein aktiver Bedarf gehört zu einem früheren Planungsstand und muss ausdrücklich beendet werden."}
		}
		key := String(r["component_id"]) + "/" + String(r["ingredient_id"])
		out.requirements[key] = r
		out.view.Requirements = append(out.view.Requirements, PlanningPurchaseRequirement{ID: String(r["id"]), PlanID: pid, MealID: mid,
			ComponentID: String(r["component_id"]), IngredientID: String(r["ingredient_id"]), Version: Int(r["version"]), Active: r["active"] == true,
			ReviewReason: String(r["review_reason"]), Disposition: String(r["procurement_disposition"]), PendingQuantity: String(r["pending_quantity"]),
			ShoppingGeneration: Int(r["shopping_generation"]), ListID: String(r["list_id"]), ItemID: String(r["item_id"]),
			ItemVersion: Int(r["item_version"]), ItemFingerprint: String(r["item_fingerprint"])})
	}
	return rows.Err()
}

func readPlanningPurchaseItems(ctx context.Context, tx pgx.Tx, id auth.Identity, out *planningPurchaseScope) error {
	ids := make([]string, 0, len(out.requirements))
	for _, r := range out.requirements {
		if item := String(r["item_id"]); item != "" {
			ids = append(ids, item)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT jsonb_build_object('id',i.id,'name',i.name,'note',i.note,'version',i.version,'quantity',i.quantity::text,'unit_code',i.unit_code,
 'fingerprint',encode(sha256(convert_to(to_jsonb(i)::text,'UTF8')),'hex'),'checked',i.checked_at IS NOT NULL,
 'list_id',l.id,'list_name',l.name,'list_version',l.version,'list_open',l.archived_at IS NULL AND NOT l.is_template
  AND NOT EXISTS(SELECT 1 FROM shopping.checkouts c WHERE c.household_id=l.household_id AND c.list_id=l.id))
 FROM shopping.items i JOIN shopping.lists l ON l.household_id=i.household_id AND l.id=i.list_id
 WHERE i.household_id=$1 AND i.id=ANY($2::uuid[]) ORDER BY i.id LIMIT 501`, id.HouseholdID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		item, decodeErr := decode(raw)
		if decodeErr != nil {
			return decodeErr
		}
		out.items[String(item["id"])] = item
	}
	return rows.Err()
}
