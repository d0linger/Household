package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type IngredientBindingInput struct {
	Generation       int64  `json:"generation"`
	PantryGeneration int64  `json:"pantry_generation"`
	CatalogVersion   int64  `json:"catalog_version"`
	BindingID        string `json:"binding_id"`
	Version          int64  `json:"version"`
	ProductID        string `json:"product_id"`
	ProductVersion   int64  `json:"product_version"`
}

type IngredientBindingConflict struct {
	Message string
	Current Record
}

func (e IngredientBindingConflict) Error() string { return e.Message }
func (e IngredientBindingConflict) Unwrap() error { return ErrConflict }

func publicIngredientBinding(binding Record, targetEnabled bool) Record {
	if binding == nil {
		return Record{"id": "", "version": int64(0), "assigned": false, "product_id": nil}
	}
	result := Record{"id": binding["id"], "version": binding["version"], "assigned": binding["product_id"] != nil, "product_id": nil}
	if targetEnabled {
		result["product_id"] = binding["product_id"]
	}
	return result
}

func (s *Store) SetIngredientBinding(ctx context.Context, id auth.Identity, catalog, key, requestID string, in IngredientBindingInput) (Record, error) {
	if !value.ValidUUID(catalog) || !value.ValidUUID(key) || in.CatalogVersion <= 0 || in.PantryGeneration <= 0 || in.Version < 0 ||
		in.BindingID != "" && !value.ValidUUID(in.BindingID) || in.ProductID != "" && (!value.ValidUUID(in.ProductID) || in.ProductVersion <= 0) ||
		(in.BindingID == "") != (in.Version == 0) {
		return nil, Invalid{"Gültige Zutaten-, Produkt- und Zuordnungsstände erforderlich"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "recipes", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var targetState string
	var targetGeneration int64
	err = tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry' FOR SHARE", id.HouseholdID).Scan(&targetState, &targetGeneration)
	if err != nil {
		return nil, err
	}
	if targetGeneration != in.PantryGeneration {
		return nil, ErrConflict
	}
	if targetState != "enabled" && in.ProductID != "" {
		return nil, ErrNotFound
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	op := "recipes/ingredient-binding/" + catalog
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var catalogVersion int64
	var catalogArchived bool
	err = tx.QueryRow(ctx, "SELECT version,archived FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, catalog).Scan(&catalogVersion, &catalogArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	var productVersion int64
	var productArchived bool
	if in.ProductID != "" {
		err = tx.QueryRow(ctx, "SELECT version,archived_at IS NOT NULL FROM pantry.products WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, in.ProductID).Scan(&productVersion, &productArchived)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, shoppingContention(err)
		}
	}
	// A deleted or disabled endpoint cannot be revealed by replaying an old reply.
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT to_jsonb(b) FROM core.ingredient_product_bindings b WHERE household_id=$1 AND catalog_id=$2 AND source_generation=$3 FOR UPDATE NOWAIT", id.HouseholdID, catalog, in.Generation).Scan(&raw)
	var current Record
	if err == nil {
		current, err = decode(raw)
	} else if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	public := publicIngredientBinding(current, targetState == "enabled")
	if String(public["id"]) != in.BindingID || Int(public["version"]) != in.Version || catalogVersion != in.CatalogVersion ||
		in.ProductID != "" && productVersion != in.ProductVersion {
		return nil, IngredientBindingConflict{Message: "Katalog, Produkt oder Zuordnung wurde geändert. Auswahl mit aktuellem Stand erneut prüfen.", Current: public}
	}
	if in.ProductID != "" && (catalogArchived || productArchived) {
		return nil, Invalid{"Aktive Katalogzutat und aktives Vorratsprodukt auswählen"}
	}
	var target any
	if in.ProductID != "" {
		target = in.PantryGeneration
	}
	if current == nil {
		if in.ProductID == "" {
			return nil, Invalid{"Diese Katalogzutat hat keine Produktzuordnung"}
		}
		err = tx.QueryRow(ctx, `INSERT INTO core.ingredient_product_bindings(household_id,catalog_id,source_generation,product_id,target_generation,created_by,changed_by)
 VALUES($1,$2,$3,$4,$5,$6,$6) RETURNING to_jsonb(ingredient_product_bindings)`, id.HouseholdID, catalog, in.Generation, in.ProductID, target, id.UserID).Scan(&raw)
	} else {
		err = tx.QueryRow(ctx, `UPDATE core.ingredient_product_bindings SET product_id=nullif($4,'')::uuid,target_generation=$5,changed_by=$6
 WHERE household_id=$1 AND catalog_id=$2 AND source_generation=$3 RETURNING to_jsonb(ingredient_product_bindings)`, id.HouseholdID, catalog, in.Generation, in.ProductID, target, id.UserID).Scan(&raw)
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	updated, err := decode(raw)
	if err != nil {
		return nil, err
	}
	result := publicIngredientBinding(updated, targetState == "enabled")
	if err = Audit(ctx, tx, id, "recipes", "ingredient_catalog", catalog, "record_write", requestID); err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	targets, err := json.Marshal(Record{"pantry": in.PantryGeneration})
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,target_generations,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'recipes',$6,$7,200,$8)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, targets, response)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}
