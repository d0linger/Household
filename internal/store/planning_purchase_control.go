package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningPurchaseControlInput struct {
	Generation         int64    `json:"generation"`
	PantryGeneration   int64    `json:"pantry_generation"`
	ShoppingGeneration int64    `json:"shopping_generation"`
	Fingerprint        string   `json:"fingerprint"`
	Action             string   `json:"action"`
	MealIDs            []string `json:"meal_ids"`
	RequirementIDs     []string `json:"requirement_ids"`
}

func normalizePlanningPurchaseControl(in PlanningPurchaseControlInput) (PlanningPurchaseControlInput, error) {
	base, _, err := normalizePlanningPurchase(PlanningPurchaseInput{Generation: in.Generation, PantryGeneration: in.PantryGeneration,
		ShoppingGeneration: in.ShoppingGeneration, Fingerprint: in.Fingerprint})
	if err != nil {
		return in, err
	}
	in.Fingerprint = base.Fingerprint
	ids, limit := in.MealIDs, planningMealLimit
	switch in.Action {
	case "end":
		if len(in.RequirementIDs) != 0 {
			return in, Invalid{"Bedarf nur für ausdrücklich gewählte ganze Mahlzeiten beenden"}
		}
	case "detach":
		if len(in.MealIDs) != 0 {
			return in, Invalid{"Einkaufszuordnungen einzeln auswählen"}
		}
		ids, limit = in.RequirementIDs, planningIngredientLimit
	default:
		return in, Invalid{"Bedarf beenden oder Einkaufszuordnung lösen auswählen"}
	}
	if len(ids) == 0 || len(ids) > limit {
		return in, Invalid{"Gültigen begrenzten Umfang ausdrücklich auswählen"}
	}
	for _, id := range ids {
		if !value.ValidUUID(id) {
			return in, Invalid{"Gültige Quellkennungen auswählen"}
		}
	}
	unique := planningPurchaseUniqueIDs(ids)
	if len(unique) != len(ids) {
		return in, Invalid{"Jede Quelle nur einmal auswählen"}
	}
	in.MealIDs, in.RequirementIDs = []string{}, []string{}
	if in.Action == "end" {
		in.MealIDs = unique
	} else {
		in.RequirementIDs = unique
	}
	return in, nil
}

// ControlPlanningPurchases never edits or removes a shopping item. Ending
// releases whole meals; detaching retains active demand for explicit renewal.
func (s *Store) ControlPlanningPurchases(ctx context.Context, id auth.Identity, key, requestID string, input PlanningPurchaseControlInput) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Vorgangskennung erforderlich"}
	}
	in, err := normalizePlanningPurchaseControl(input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Kind  string
		Input PlanningPurchaseControlInput
	}{"control", in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); err != nil {
		return nil, err
	}
	if err = checkSession(ctx, tx, id); err != nil {
		return nil, err
	}
	for _, module := range []struct {
		name string
		gen  int64
	}{{"mealplan", in.Generation}, {"pantry", in.PantryGeneration}, {"shopping", in.ShoppingGeneration}} {
		var generation int64
		if err = tx.QueryRow(ctx, "SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2 FOR SHARE NOWAIT", id.HouseholdID, module.name).Scan(&generation); err != nil {
			return nil, shoppingContention(err)
		}
		if generation != module.gen {
			return nil, ErrConflict
		}
	}
	for attempt := range 2 {
		prior, priorHash, priorErr := readPlanningPurchaseOperation(ctx, tx, id, key)
		if priorErr == nil {
			if !bytes.Equal(priorHash, hash[:]) {
				return nil, ErrConflict
			}
			return prior, nil
		}
		if !errors.Is(priorErr, pgx.ErrNoRows) {
			return nil, priorErr
		}
		if attempt == 0 {
			if err = lockPlanningPurchaseScope(ctx, tx, id); err != nil {
				return nil, err
			}
		}
	}
	scope, err := readPlanningPurchaseControlScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	requirements, items := []string{}, []string{}
	for _, entry := range scope.rows {
		requirements = append(requirements, String(entry.row["id"]))
		if itemID := String(entry.row["item_id"]); itemID != "" {
			items = append(items, itemID)
		}
	}
	if err = lockPlanningPurchaseIDs(ctx, tx, id, [2]string{"core", "planning_purchase_requirements"}, planningPurchaseUniqueIDs(requirements), false); err != nil {
		return nil, err
	}
	if err = lockPlanningPurchaseIDs(ctx, tx, id, [2]string{"shopping", "items"}, planningPurchaseUniqueIDs(items), true); err != nil {
		return nil, err
	}
	current, err := readPlanningPurchaseControlScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if current.view.Fingerprint != scope.view.Fingerprint || current.view.Fingerprint != in.Fingerprint {
		return nil, PlanningConflict{"Aktiven Umfang erneut laden und die ausdrückliche Entscheidung prüfen."}
	}
	op, receipt := value.UUID(), value.UUID()
	sources, err := applyPlanningPurchaseControl(ctx, tx, id, in, current, op)
	if err != nil {
		return nil, err
	}
	basis := PlanningPurchaseInput{Generation: in.Generation, PantryGeneration: in.PantryGeneration, ShoppingGeneration: in.ShoppingGeneration, Fingerprint: in.Fingerprint}
	if err = writePlanningPurchaseEvidence(ctx, tx, id, key, op, receipt, in.Action, basis, hash[:], sources, current.view); err != nil {
		return nil, err
	}
	result, _, err := readPlanningPurchaseOperation(ctx, tx, id, key)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func applyPlanningPurchaseControl(ctx context.Context, tx pgx.Tx, id auth.Identity, in PlanningPurchaseControlInput, scope planningPurchaseControlScope, op string) ([]planningPurchaseEvidence, error) {
	selected := make(map[string]bool)
	for _, identifier := range append(append([]string{}, in.MealIDs...), in.RequirementIDs...) {
		selected[identifier] = false
	}
	out := make([]planningPurchaseEvidence, 0)
	for _, entry := range scope.rows {
		row := entry.row
		selector := String(row["id"])
		if in.Action == "end" {
			selector = String(row["meal_id"])
		}
		if _, wanted := selected[selector]; !wanted {
			continue
		}
		if Int(row["mealplan_generation"]) != in.Generation || in.Action == "detach" && String(row["item_id"]) == "" {
			return nil, PlanningConflict{"Die ausgewählte aktive Einkaufszuordnung hat sich geändert."}
		}
		selected[selector] = true
		var after json.RawMessage
		var version int64
		err := tx.QueryRow(ctx, `UPDATE core.planning_purchase_requirements r SET active=CASE WHEN $4='end' THEN false ELSE active END,
 review_reason=CASE WHEN $4='end' THEN '' ELSE review_reason END,shopping_generation=NULL,list_id=NULL,item_id=NULL,item_version=NULL,item_fingerprint=NULL,
 pending_quantity=0,procurement_disposition='detached',last_sync_operation_id=$5,changed_by=$6
 WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING to_jsonb(r),version`, id.HouseholdID, row["id"], Int(row["version"]), in.Action, op, id.UserID).Scan(&after, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrConflict
		}
		if err != nil {
			return nil, shoppingContention(err)
		}
		out = append(out, planningPurchaseEvidence{RequirementID: String(row["id"]), Version: strconv.FormatInt(version, 10), ComponentID: String(row["component_id"]),
			IngredientID: String(row["ingredient_id"]), Changed: true, Action: "none", Decision: in.Action, Before: entry.before, After: after, ItemBefore: entry.item, ItemAfter: entry.item})
	}
	for _, found := range selected {
		if !found {
			return nil, PlanningConflict{"Eine ausgewählte aktive Quelle fehlt. Aktuellen Umfang erneut prüfen."}
		}
	}
	return out, nil
}
