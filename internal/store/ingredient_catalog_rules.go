package store

import (
	"context"
	"errors"
	"unicode/utf8"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func validateIngredientCatalog(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, rid string, data Record) error {
	var name string
	if err := tx.QueryRow(ctx, "SELECT recipes.ingredient_key($1)", data["name"]).Scan(&name); err != nil {
		return err
	}
	if name == "" || utf8.RuneCountInString(name) > 200 {
		return Invalid{"Zutatenname oder Alias: 1 bis 200 Zeichen erforderlich"}
	}
	if entity == "ingredient_aliases" {
		var archived bool
		err := tx.QueryRow(ctx, "SELECT archived FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, data["catalog_id"]).Scan(&archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return shoppingContention(err)
		}
		if archived {
			return Invalid{"Katalogzutat zuerst wieder aktivieren"}
		}
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM recipes.ingredient_aliases WHERE household_id=$1 AND catalog_id=$2 AND id<>coalesce(nullif($3,'')::uuid,'00000000-0000-0000-0000-000000000000')", id.HouseholdID, data["catalog_id"], rid).Scan(&count); err != nil {
			return err
		}
		if count >= 50 {
			return Invalid{"Höchstens 50 Aliase pro Katalogzutat; vorhandene Schreibweisen zuerst prüfen"}
		}
		if rid != "" {
			var parent string
			if err = tx.QueryRow(ctx, "SELECT catalog_id FROM recipes.ingredient_aliases WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&parent); err != nil {
				return err
			}
			if parent != String(data["catalog_id"]) {
				return Invalid{"Die Katalogzutat dieses Alias ist fest. Für eine andere Zutat einen neuen Alias anlegen."}
			}
		}
	}
	var duplicate bool
	// Only the two compiled descriptors can select this identifier.
	if entity != "ingredient_catalog" && entity != "ingredient_aliases" {
		return ErrNotFound
	}
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM recipes."+pgx.Identifier{entity}.Sanitize()+" WHERE household_id=$1 AND name_key=$2 AND id<>coalesce(nullif($3,'')::uuid,'00000000-0000-0000-0000-000000000000'))", id.HouseholdID, name, rid).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return Invalid{"Diese normalisierte Schreibweise ist bereits vorhanden. Bestehenden Katalogeintrag prüfen."}
	}
	return nil
}

func cleanupDeletedIngredientBinding(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid string) error {
	switch mid + "/" + entity {
	case "recipes/ingredient_catalog":
		_, err := tx.Exec(ctx, "DELETE FROM core.ingredient_product_bindings WHERE household_id=$1 AND catalog_id=$2", id.HouseholdID, rid)
		return err
	case "pantry/products":
		// Match the identity actually deleted. A broad NOT EXISTS sweep can use
		// an old statement snapshot after waiting for a concurrent rebind.
		_, err := tx.Exec(ctx, "UPDATE core.ingredient_product_bindings SET product_id=NULL,target_generation=NULL,changed_by=$3 WHERE household_id=$1 AND product_id=$2", id.HouseholdID, rid, id.UserID)
		return err
	default:
		return nil
	}
}
