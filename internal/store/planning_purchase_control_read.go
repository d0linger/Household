package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

type PlanningPurchaseControlScope struct {
	Generation         int64    `json:"generation"`
	PantryGeneration   int64    `json:"pantry_generation"`
	ShoppingGeneration int64    `json:"shopping_generation"`
	MealplanEnabled    bool     `json:"mealplan_enabled"`
	PantryEnabled      bool     `json:"pantry_enabled"`
	ShoppingEnabled    bool     `json:"shopping_enabled"`
	Fingerprint        string   `json:"fingerprint"`
	Requirements       []Record `json:"requirements"`
}

type planningPurchaseControlRow struct {
	before, item json.RawMessage
	row          Record
}

type planningPurchaseControlScope struct {
	view PlanningPurchaseControlScope
	rows []planningPurchaseControlRow
}

// PlanningPurchaseScope remains available when a source is gone or a target
// module is disabled. Private target content follows the owning module state.
func (s *Store) PlanningPurchaseScope(ctx context.Context, id auth.Identity) (PlanningPurchaseControlScope, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlanningPurchaseControlScope{}, err
	}
	defer rollback(tx)
	scope, err := readPlanningPurchaseControlScope(ctx, tx, id)
	if err != nil {
		return scope.view, err
	}
	return scope.view, tx.Commit(ctx)
}

func readPlanningPurchaseControlScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (planningPurchaseControlScope, error) {
	out := planningPurchaseControlScope{view: PlanningPurchaseControlScope{Requirements: []Record{}}, rows: []planningPurchaseControlRow{}}
	err := tx.QueryRow(ctx, `SELECT m.data_generation,p.data_generation,s.data_generation,m.state='enabled',p.state='enabled',s.state='enabled'
 FROM core.module_states m JOIN core.module_states p ON p.household_id=m.household_id AND p.module_id='pantry'
 JOIN core.module_states s ON s.household_id=m.household_id AND s.module_id='shopping' WHERE m.household_id=$1 AND m.module_id='mealplan'`, id.HouseholdID).
		Scan(&out.view.Generation, &out.view.PantryGeneration, &out.view.ShoppingGeneration, &out.view.MealplanEnabled, &out.view.PantryEnabled, &out.view.ShoppingEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(r),to_jsonb(i),jsonb_build_object(
 'id',r.id,'plan_id',r.plan_id,'meal_id',r.meal_id,'component_id',r.component_id,'ingredient_id',r.ingredient_id,
 'version',r.version,'active',r.active,'review_reason',r.review_reason,'disposition',r.procurement_disposition,'pending_quantity',r.pending_quantity::text,
 'plan_name',CASE WHEN $5 AND r.mealplan_generation=$2 THEN p.name END,
 'meal_name',CASE WHEN $5 AND r.mealplan_generation=$2 THEN m.title END,
 'item_name',CASE WHEN $3 AND r.shopping_generation=$4 THEN i.name END,
 'item_quantity',CASE WHEN $3 AND r.shopping_generation=$4 THEN i.quantity::text END,
 'item_unit',CASE WHEN $3 AND r.shopping_generation=$4 THEN i.unit_code END,
 'item_list_id',CASE WHEN $3 AND r.shopping_generation=$4 THEN i.list_id END,
 'list_name',CASE WHEN $3 AND r.shopping_generation=$4 THEN l.name END,
 'item_id',r.item_id,'list_id',r.list_id)
 FROM core.planning_purchase_requirements r
 LEFT JOIN mealplan.plans p ON p.household_id=r.household_id AND p.id=r.plan_id
 LEFT JOIN mealplan.meals m ON m.household_id=r.household_id AND m.id=r.meal_id
 LEFT JOIN shopping.items i ON i.household_id=r.household_id AND i.id=r.item_id
 LEFT JOIN shopping.lists l ON l.household_id=i.household_id AND l.id=i.list_id
 WHERE r.household_id=$1 AND r.active ORDER BY r.plan_id,r.meal_id,r.component_id,r.ingredient_id LIMIT 501`,
		id.HouseholdID, out.view.Generation, out.view.ShoppingEnabled, out.view.ShoppingGeneration, out.view.MealplanEnabled)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	// Include actual private item states in the opaque comparison hash, even
	// when their labels are hidden. They are never returned as public content.
	basis := make([][2]json.RawMessage, 0)
	for rows.Next() {
		var entry planningPurchaseControlRow
		var public json.RawMessage
		if err = rows.Scan(&entry.before, &entry.item, &public); err != nil {
			return out, err
		}
		if len(out.rows) >= planningIngredientLimit {
			return out, Invalid{"Aktiver Planungsumfang überschreitet 500 Zutaten"}
		}
		entry.row, err = decode(entry.before)
		if err != nil {
			return out, err
		}
		visible, decodeErr := decode(public)
		if decodeErr != nil {
			return out, decodeErr
		}
		basis = append(basis, [2]json.RawMessage{entry.before, entry.item})
		out.rows = append(out.rows, entry)
		out.view.Requirements = append(out.view.Requirements, visible)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.view.Fingerprint, err = planningPurchaseFingerprint(struct {
		View  PlanningPurchaseControlScope
		Basis [][2]json.RawMessage
	}{out.view, basis})
	return out, err
}
