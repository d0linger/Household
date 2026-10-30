package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) applyPlanningPurchaseScope(ctx context.Context, tx pgx.Tx, id auth.Identity, scope planningPurchaseScope, in PlanningPurchaseInput, op string) ([]planningPurchaseEvidence, error) {
	sources := make(map[string]PlanningDemandLine, len(scope.view.Demand.Lines))
	plans := make(map[string]string, len(scope.view.Demand.Meals))
	for _, source := range scope.view.Demand.Lines {
		sources[source.ComponentID+"/"+source.IngredientID] = source
	}
	for _, meal := range scope.view.Demand.Meals {
		plans[meal.ID] = meal.PlanID
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	result := make([]planningPurchaseEvidence, 0, len(scope.view.Lines))
	removed := false
	for _, line := range scope.view.Lines {
		source := sources[line.ComponentID+"/"+line.IngredientID]
		var before, after, itemBefore, itemAfter json.RawMessage
		var item Record
		rid, version := line.RequirementID, int64(0)
		if rid != "" {
			if err := tx.QueryRow(ctx, "SELECT to_jsonb(r),version FROM core.planning_purchase_requirements r WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&before, &version); err != nil {
				return nil, err
			}
			current := scope.requirements[line.ComponentID+"/"+line.IngredientID]
			if itemID := String(current["item_id"]); itemID != "" {
				var err error
				itemBefore, item, _, err = readPlanningPurchaseItem(ctx, tx, id, itemID)
				if err != nil {
					return nil, err
				}
			}
		} else {
			rid = value.UUID()
		}
		changed := before == nil || line.Action != "none" || line.Decision != ""
		switch line.Action {
		case "none", "remove":
		case "create":
			var err error
			item, err = save(ctx, tx, id, m, e, value.UUID(), Record{"list_id": in.ListID, "name": source.Name, "quantity": line.Desired.Value,
				"unit_code": source.Binding["unit"], "group_name": "Wochenplanung"}, true, 0)
			if err != nil {
				return nil, err
			}
		case "update":
			if item == nil {
				return nil, ErrConflict
			}
			var err error
			item, err = save(ctx, tx, id, m, e, String(item["id"]), Record{"quantity": line.Desired.Value}, false, Int(item["version"]))
			if err != nil {
				return nil, err
			}
		default:
			return nil, PlanningConflict{"Blockierte Einkaufsentscheidung erneut prüfen"}
		}
		var itemHash []byte
		if item != nil {
			var err error
			itemAfter, item, itemHash, err = readPlanningPurchaseItem(ctx, tx, id, String(item["id"]))
			if err != nil {
				return nil, err
			}
		}
		if line.Action == "remove" {
			var linked bool
			err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.attachments WHERE household_id=$1 AND module_id='shopping' AND resource_type='items' AND resource_id=$2)
 OR EXISTS(SELECT 1 FROM core.resource_links WHERE household_id=$1 AND ((source_module='shopping' AND source_type='items' AND source_id=$2)
 OR (target_module='shopping' AND target_type='items' AND target_id=$2)))`, id.HouseholdID, item["id"]).Scan(&linked)
			if err != nil {
				return nil, err
			}
			if linked {
				return nil, PlanningConflict{"Eine entfallende Einkaufsposition hat Dateien oder weitere Verknüpfungen. Einkaufszuordnung ausdrücklich lösen und die Position gesondert prüfen."}
			}
		}
		if changed {
			var err error
			after, version, err = writePlanningPurchaseRequirement(ctx, tx, id, in, op, rid, version, plans[source.MealID], source, line, item, itemHash)
			if err != nil {
				return nil, err
			}
		} else {
			after = before
		}
		switch line.Action {
		case "remove":
			// Clear the typed contribution first. The ordinary deletion hook must
			// not create an intermediate missing version of this same requirement.
			tag, err := tx.Exec(ctx, "DELETE FROM shopping.items WHERE household_id=$1 AND id=$2 AND version=$3", id.HouseholdID, item["id"], Int(item["version"]))
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() != 1 {
				return nil, ErrConflict
			}
			if err = emitShoppingDeletion(ctx, tx, id, "items", item, in.ShoppingGeneration); err != nil {
				return nil, err
			}
			itemAfter, removed = nil, true
		case "create", "update":
			if err := s.afterWrite(ctx, tx, id, m, e, item, in.ShoppingGeneration); err != nil {
				return nil, err
			}
		}
		result = append(result, planningPurchaseEvidence{RequirementID: rid, Version: strconv.FormatInt(version, 10), ComponentID: line.ComponentID,
			IngredientID: line.IngredientID, Changed: changed, Action: line.Action, Decision: line.Decision, Before: before, After: after, ItemBefore: itemBefore, ItemAfter: itemAfter})
	}
	if removed {
		if err := s.cleanupOrphans(ctx, tx, id, m); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func readPlanningPurchaseItem(ctx context.Context, tx pgx.Tx, id auth.Identity, itemID string) (json.RawMessage, Record, []byte, error) {
	var raw json.RawMessage
	var hash []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(i),sha256(convert_to(to_jsonb(i)::text,'UTF8')) FROM shopping.items i WHERE household_id=$1 AND id=$2", id.HouseholdID, itemID).Scan(&raw, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, ErrConflict
	}
	if err != nil {
		return nil, nil, nil, err
	}
	item, err := decode(raw)
	return raw, item, hash, err
}

func writePlanningPurchaseRequirement(ctx context.Context, tx pgx.Tx, id auth.Identity, in PlanningPurchaseInput, op, rid string, version int64, plan string, source PlanningDemandLine, line PlanningPurchaseLine, item Record, itemHash []byte) (json.RawMessage, int64, error) {
	basis, err := hex.DecodeString(line.Basis)
	if err != nil {
		return nil, 0, err
	}
	var generation, listID, itemID, itemVersion any
	var fingerprint []byte
	quantity, disposition := "0", "none"
	if item != nil && line.Action != "remove" {
		generation, listID, itemID, itemVersion, fingerprint = in.ShoppingGeneration, item["list_id"], item["id"], Int(item["version"]), itemHash
		quantity, disposition = line.Desired.Value, "managed"
	}
	var raw json.RawMessage
	var next int64
	if version == 0 {
		sourceHash, decodeErr := hex.DecodeString(source.SourceHash)
		if decodeErr != nil {
			return nil, 0, decodeErr
		}
		err = tx.QueryRow(ctx, `INSERT INTO core.planning_purchase_requirements AS r(id,household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,
 source_hash,reviewed_basis_hash,binding_id,binding_version,product_id,pantry_generation,unit_code,
 shopping_generation,list_id,item_id,item_version,item_fingerprint,pending_quantity,procurement_disposition,last_sync_operation_id,created_by,changed_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$23) RETURNING to_jsonb(r),version`,
			rid, id.HouseholdID, in.Generation, plan, source.MealID, source.ComponentID, source.IngredientID, sourceHash, basis, source.Binding["id"],
			Int(source.Binding["version"]), source.ProductID, in.PantryGeneration, source.Binding["unit"], generation, listID, itemID, itemVersion, fingerprint,
			quantity, disposition, op, id.UserID).Scan(&raw, &next)
	} else {
		err = tx.QueryRow(ctx, `UPDATE core.planning_purchase_requirements r SET active=true,reviewed_basis_hash=$4,review_reason='',binding_id=$5,binding_version=$6,
 product_id=$7,pantry_generation=$8,unit_code=$9,shopping_generation=$10,list_id=$11,item_id=$12,item_version=$13,item_fingerprint=$14,
		 pending_quantity=$15,procurement_disposition=CASE WHEN $16='none' AND $19='accept' AND procurement_disposition<>'managed' THEN procurement_disposition ELSE $16 END,last_sync_operation_id=$17,changed_by=$18
		 WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING to_jsonb(r),version`, id.HouseholdID, rid, version, basis, source.Binding["id"], Int(source.Binding["version"]),
			source.ProductID, in.PantryGeneration, source.Binding["unit"], generation, listID, itemID, itemVersion, fingerprint, quantity, disposition, op, id.UserID, line.Decision).Scan(&raw, &next)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrConflict
	}
	return raw, next, shoppingContention(err)
}
