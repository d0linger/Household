package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningIngredientBindingInput struct {
	Generation       int64  `json:"generation"`
	PantryGeneration int64  `json:"pantry_generation"`
	PlanVersion      int64  `json:"plan_version"`
	MealVersion      int64  `json:"meal_version"`
	ComponentVersion int64  `json:"component_version"`
	SourceHash       string `json:"source_hash"`
	BindingID        string `json:"binding_id"`
	Version          int64  `json:"version"`
	ProductID        string `json:"product_id"`
	ProductVersion   int64  `json:"product_version"`
	Unit             string `json:"unit"`
}

func normalizePlanningBinding(in *PlanningIngredientBindingInput) ([]byte, error) {
	hash, err := hex.DecodeString(in.SourceHash)
	if err != nil || len(hash) != sha256.Size || in.Generation <= 0 || in.PantryGeneration <= 0 ||
		in.PlanVersion <= 0 || in.MealVersion <= 0 || in.ComponentVersion <= 0 || in.Version < 0 ||
		(in.BindingID == "") != (in.Version == 0) || in.BindingID != "" && !value.ValidUUID(in.BindingID) ||
		in.ProductID != "" && (!value.ValidUUID(in.ProductID) || in.ProductVersion <= 0 || in.Unit == "") ||
		in.ProductID == "" && (in.ProductVersion != 0 || in.Unit != "") {
		return nil, Invalid{"Gültige Plan-, Zutaten-, Zuordnungs- und Produktstände erforderlich"}
	}
	in.SourceHash = strings.ToLower(in.SourceHash)
	return hash, nil
}

func publicPlanningBinding(binding Record, enabled bool, generation int64) Record {
	if binding == nil {
		return Record{"id": "", "version": int64(0), "assigned": false, "target_current": false, "product_id": nil, "unit": nil}
	}
	current := enabled && Int(binding["pantry_generation"]) == generation && binding["product_id"] != nil
	result := Record{"id": binding["id"], "version": binding["version"], "assigned": binding["product_id"] != nil,
		"target_current": current, "product_id": nil, "unit": nil}
	if current {
		result["product_id"], result["unit"] = binding["product_id"], binding["unit_code"]
	}
	return result
}

func readPlanningBinding(ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, cid, ingredient string, lock bool) (Record, error) {
	query := `SELECT to_jsonb(b) FROM core.planning_ingredient_bindings b
 WHERE household_id=$1 AND mealplan_generation=$2 AND component_id=$3 AND ingredient_id=$4`
	if lock {
		query += " FOR UPDATE NOWAIT"
	}
	var raw []byte
	err := tx.QueryRow(ctx, query, id.HouseholdID, generation, cid, ingredient).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	return decode(raw)
}

func (s *Store) SetPlanningIngredientBinding(ctx context.Context, id auth.Identity, pid, mid, cid, ingredient, key, requestID string, in PlanningIngredientBindingInput) (Record, error) {
	for _, identifier := range []string{pid, mid, cid, ingredient, key} {
		if !value.ValidUUID(identifier) {
			return nil, Invalid{"Gültige Plan-, Quellen- und Vorgangskennung erforderlich"}
		}
	}
	sourceHash, err := normalizePlanningBinding(&in)
	if err != nil {
		return nil, err
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "mealplan", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var pantryState string
	var pantryGeneration int64
	err = tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry' FOR SHARE", id.HouseholdID).Scan(&pantryState, &pantryGeneration)
	if err != nil {
		return nil, err
	}
	if pantryGeneration != in.PantryGeneration {
		return nil, ErrConflict
	}
	if in.ProductID != "" && pantryState != "enabled" {
		return nil, ErrNotFound
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	requestHash := sha256.Sum256(encoded)
	op := "mealplan/ingredient-binding/" + pid + "/" + mid + "/" + cid + "/" + ingredient
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var planVersion int64
	if err = tx.QueryRow(ctx, "SELECT version FROM mealplan.plans WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, pid).Scan(&planVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, shoppingContention(err)
	}
	var mealVersion int64
	if err = tx.QueryRow(ctx, "SELECT version FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id=$3 FOR UPDATE NOWAIT", id.HouseholdID, pid, mid).Scan(&mealVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, shoppingContention(err)
	}
	var componentVersion int64
	var storedHash, snapshot []byte
	err = tx.QueryRow(ctx, `SELECT version,sha256(convert_to(snapshot::text,'UTF8')),snapshot FROM mealplan.components
 WHERE household_id=$1 AND meal_id=$2 AND id=$3 FOR UPDATE NOWAIT`, id.HouseholdID, mid, cid).Scan(&componentVersion, &storedHash, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if _, err = planningIngredientSource(snapshot, ingredient); err != nil {
		return nil, err
	}
	var priorHash, priorRaw []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &priorRaw)
	if err == nil {
		if !bytes.Equal(priorHash, requestHash[:]) {
			return nil, ErrConflict
		}
		prior, decodeErr := decode(priorRaw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		current, readErr := readPlanningBinding(ctx, tx, id, in.Generation, cid, ingredient, false)
		if readErr != nil {
			return nil, readErr
		}
		return Record{"applied_id": prior["applied_id"], "applied_version": prior["applied_version"], "replayed": true,
			"binding": publicPlanningBinding(current, pantryState == "enabled", pantryGeneration)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var productVersion int64
	var productUnit string
	var archived bool
	if in.ProductID != "" {
		err = tx.QueryRow(ctx, "SELECT version,unit_code,archived_at IS NOT NULL FROM pantry.products WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, in.ProductID).Scan(&productVersion, &productUnit, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, shoppingContention(err)
		}
	}
	current, err := readPlanningBinding(ctx, tx, id, in.Generation, cid, ingredient, true)
	if err != nil {
		return nil, err
	}
	if in.ProductID != "" && archived {
		return nil, Invalid{"Aktives Vorratsprodukt auswählen"}
	}
	if planVersion != in.PlanVersion || mealVersion != in.MealVersion || componentVersion != in.ComponentVersion || !bytes.Equal(storedHash, sourceHash) ||
		String(current["id"]) != in.BindingID || Int(current["version"]) != in.Version ||
		in.ProductID != "" && (productVersion != in.ProductVersion || productUnit != in.Unit) {
		return nil, PlanningConflict{"Plan, Zutat, Produkt oder Zuordnung wurde geändert. Auswahl mit aktuellem Stand erneut prüfen."}
	}
	if current == nil && in.ProductID == "" {
		return nil, Invalid{"Diese Plan-Zutat hat keine Produktzuordnung"}
	}
	var raw []byte
	if current == nil {
		err = tx.QueryRow(ctx, `INSERT INTO core.planning_ingredient_bindings(household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,product_id,pantry_generation,unit_code,created_by,changed_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11) RETURNING to_jsonb(planning_ingredient_bindings)`,
			id.HouseholdID, in.Generation, pid, mid, cid, ingredient, sourceHash, in.ProductID, pantryGeneration, in.Unit, id.UserID).Scan(&raw)
	} else {
		var target, unit any
		if in.ProductID != "" {
			target, unit = pantryGeneration, in.Unit
		}
		err = tx.QueryRow(ctx, `UPDATE core.planning_ingredient_bindings SET product_id=nullif($2,'')::uuid,pantry_generation=$3,unit_code=$4,changed_by=$5
 WHERE household_id=$1 AND id=$6 RETURNING to_jsonb(planning_ingredient_bindings)`, id.HouseholdID, in.ProductID, target, unit, id.UserID, current["id"]).Scan(&raw)
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	updated, err := decode(raw)
	if err != nil {
		return nil, err
	}
	result := Record{"applied_id": updated["id"], "applied_version": updated["version"], "replayed": false,
		"binding": publicPlanningBinding(updated, pantryState == "enabled", pantryGeneration)}
	if err = Audit(ctx, tx, id, "mealplan", "meals", mid, "record_write", requestID); err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	targets, err := json.Marshal(Record{"pantry": pantryGeneration})
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,target_generations,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'mealplan',$6,$7,200,$8)`, id.HouseholdID, id.UserID, op, key, requestHash[:], in.Generation, targets, response)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func planningIngredientSource(snapshot []byte, ingredient string) (Record, error) {
	if len(snapshot) > planningComponentBytes {
		return nil, Invalid{"Plan-Rezeptstand überschreitet die zulässige Größe"}
	}
	bundle, err := recipeBundle(snapshot)
	if err != nil {
		return nil, Invalid{"Plan-Rezeptstand ist unvollständig oder ungültig"}
	}
	seen := make(map[string]bool, len(bundle.Ingredients))
	var found Record
	for _, row := range bundle.Ingredients {
		iid := String(row["id"])
		name, named := row["name"].(string)
		if !value.ValidUUID(iid) || iid != strings.ToLower(iid) || seen[iid] || !named || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 200 {
			return nil, Invalid{"Plan-Zutaten benötigen eindeutige gültige Kennungen und Namen"}
		}
		seen[iid] = true
		if iid == ingredient {
			found = row
		}
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}
