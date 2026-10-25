package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type planningPurchaseEvidence struct {
	RequirementID string          `json:"requirement_id"`
	Version       string          `json:"requirement_version"`
	ComponentID   string          `json:"component_id"`
	IngredientID  string          `json:"ingredient_id"`
	Changed       bool            `json:"changed"`
	Action        string          `json:"item_action"`
	Decision      string          `json:"decision,omitempty"`
	Before        json.RawMessage `json:"requirement_before"`
	After         json.RawMessage `json:"requirement_after"`
	ItemBefore    json.RawMessage `json:"item_before"`
	ItemAfter     json.RawMessage `json:"item_after"`
}

type planningCheckoutRequirement struct {
	before, item json.RawMessage
	row          Record
}

// The existing checkout owns its list/items and actual intake effects. A
// contested contribution fails immediately rather than waiting in reverse
// plan/product/item order. The meal remains active after every disposition.
func recordPlanningCheckout(ctx context.Context, tx pgx.Tx, id auth.Identity, checkoutID string, in CheckoutInput, results []Record, requestHash []byte) error {
	decisions := make(map[string]Record, len(results))
	itemIDs := make([]string, 0, len(results))
	for _, result := range results {
		itemID := String(result["item_id"])
		decisions[itemID] = result
		itemIDs = append(itemIDs, itemID)
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(r),to_jsonb(i) FROM core.planning_purchase_requirements r
 JOIN shopping.items i ON i.household_id=r.household_id AND i.id=r.item_id
 WHERE r.household_id=$1 AND r.shopping_generation=$2 AND r.item_id=ANY($3::uuid[])
 ORDER BY r.id LIMIT 501 FOR UPDATE OF r NOWAIT`, id.HouseholdID, in.Generation, itemIDs)
	if err != nil {
		return shoppingContention(err)
	}
	defer rows.Close()
	current := make([]planningCheckoutRequirement, 0)
	for rows.Next() {
		var r planningCheckoutRequirement
		if err = rows.Scan(&r.before, &r.item); err != nil {
			return err
		}
		r.row, err = decode(r.before)
		if err != nil {
			return err
		}
		current = append(current, r)
		if len(current) > planningIngredientLimit {
			return Invalid{"Höchstens 500 aktive Plan-Zutaten gemeinsam abschließen"}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(current) == 0 {
		return nil
	}
	var mealplanGeneration, pantryGeneration, shoppingGeneration int64
	err = tx.QueryRow(ctx, `SELECT max(data_generation) FILTER(WHERE module_id='mealplan'),max(data_generation) FILTER(WHERE module_id='pantry'),
 max(data_generation) FILTER(WHERE module_id='shopping') FROM core.module_states
 WHERE household_id=$1 AND module_id IN('mealplan','pantry','shopping') HAVING count(*)=3`, id.HouseholdID).
		Scan(&mealplanGeneration, &pantryGeneration, &shoppingGeneration)
	if err != nil {
		return err
	}
	scopeHash, err := planningPurchaseFingerprint(struct {
		Checkout string
		Before   []planningPurchaseEvidence
		Results  []Record
	}{Checkout: checkoutID, Before: planningCheckoutBefore(current), Results: results})
	if err != nil {
		return err
	}
	hash, err := hex.DecodeString(scopeHash)
	if err != nil {
		return err
	}
	op, receipt := value.UUID(), value.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO core.planning_purchase_operations(id,household_id,created_by,request_key,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,receipt_id,kind,source_count,change_count,created_count,updated_count,removed_count)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'checkout',$11,$11,0,0,0)`, op, id.HouseholdID, id.UserID, checkoutID, requestHash, hash,
		mealplanGeneration, pantryGeneration, shoppingGeneration, receipt, len(current))
	if err != nil {
		return shoppingContention(err)
	}
	sources := make([]planningPurchaseEvidence, 0, len(current))
	for _, r := range current {
		result := decisions[String(r.row["item_id"])]
		disposition := planningCheckoutDisposition(r.row, result, in.PantryGeneration)
		reason := String(r.row["review_reason"])
		if disposition != "stored" {
			reason = "unresolved_checkout"
		}
		var after json.RawMessage
		err = tx.QueryRow(ctx, `UPDATE core.planning_purchase_requirements r SET shopping_generation=NULL,list_id=NULL,item_id=NULL,
 item_version=NULL,item_fingerprint=NULL,pending_quantity=0,procurement_disposition=$3,review_reason=$4,last_sync_operation_id=$5,
 last_checkout_generation=$6,last_checkout_id=$7,last_checkout_item_id=$8,changed_by=$9
 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(r)`, id.HouseholdID, r.row["id"], disposition, reason, op,
			in.Generation, checkoutID, r.row["item_id"], id.UserID).Scan(&after)
		if err != nil {
			return shoppingContention(err)
		}
		sources = append(sources, planningPurchaseEvidence{RequirementID: String(r.row["id"]), Version: strconv.FormatInt(Int(r.row["version"])+1, 10),
			ComponentID: String(r.row["component_id"]), IngredientID: String(r.row["ingredient_id"]), Changed: true, Action: "none",
			Before: r.before, After: after, ItemBefore: r.item, ItemAfter: r.item})
	}
	snapshot, err := json.Marshal(Record{"checkout_id": checkoutID, "sources": sources})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO shopping.planning_purchase_receipts(id,household_id,operation_id,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,snapshot,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, receipt, id.HouseholdID,
		op, requestHash, hash, mealplanGeneration, pantryGeneration, shoppingGeneration, snapshot, id.UserID)
	return err
}

func planningCheckoutBefore(current []planningCheckoutRequirement) []planningPurchaseEvidence {
	out := make([]planningPurchaseEvidence, 0, len(current))
	for _, r := range current {
		out = append(out, planningPurchaseEvidence{RequirementID: String(r.row["id"]), Before: r.before, ItemBefore: r.item})
	}
	return out
}

func planningCheckoutDisposition(requirement, result Record, pantryGeneration int64) string {
	switch result["decision"] {
	case "no_stock":
		return "no_stock"
	case "skip":
		return "skipped"
	case "store":
		stock, ok := result["stock"].(Record)
		if ok && String(stock["product_id"]) == String(requirement["product_id"]) &&
			String(stock["unit"]) == String(requirement["unit_code"]) && pantryGeneration == Int(requirement["pantry_generation"]) {
			return "stored"
		}
	}
	return "unresolved"
}
