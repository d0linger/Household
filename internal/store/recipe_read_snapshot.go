package store

import (
	"context"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// RecipeReadSnapshot returns only explicitly permitted, bounded, current
// reading fields. The caller may add locally authorized small image bytes; no
// live price, stock, replacement rule, or attachment storage key is included.
func (s *Store) RecipeReadSnapshot(ctx context.Context, id auth.Identity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer recipeRollback(tx)
	scope, err := recipeScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if !scope.OfflineAllowed {
		return nil, ErrForbidden
	}
	var raw []byte
	var ingredientFingerprint string
	err = tx.QueryRow(ctx, `SELECT recipes.content_snapshot(r.household_id,r.id),
	 recipes.ingredient_profile_fingerprint(r.household_id,r.id)
	 FROM recipes.recipes r WHERE r.household_id=$1 AND r.id=$2 AND r.archived_at IS NULL`,
		id.HouseholdID, rid).Scan(&raw, &ingredientFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	bundle, err := recipeBundle(raw)
	if err != nil {
		return nil, err
	}
	r := bundle.Recipe
	portable, err := portableRecipeFromSnapshot(raw)
	if err != nil {
		return nil, err
	}
	profile, _ := r["nutrition_profile"].(map[string]any)
	nutrition := Record{"basis": "", "source": "", "source_date": "", "stale": false,
		"nutrients": map[string]string{}}
	allergens := Record{"source": "", "source_date": "", "stale": false,
		"status_by_code": map[string]string{}}
	if len(profile) > 0 {
		stale := String(profile["ingredient_fingerprint"]) != ingredientFingerprint
		nutrition["basis"], nutrition["source"], nutrition["source_date"], nutrition["stale"] =
			String(profile["basis"]), String(profile["source"]), String(profile["source_date"]), stale
		allergens["source"], allergens["source_date"], allergens["stale"] =
			String(profile["source"]), String(profile["source_date"]), stale
		if values, ok := profile["nutrients"].(map[string]any); ok {
			known := map[string]string{}
			for code, amount := range values {
				known[code] = String(amount)
			}
			nutrition["nutrients"] = known
		}
		if values, ok := profile["allergens"].(map[string]any); ok {
			known := map[string]string{}
			for code, status := range values {
				known[code] = String(status)
			}
			allergens["status_by_code"] = known
		}
	}
	ingredients := make([]Record, 0, len(portable.Ingredients))
	for _, ingredient := range portable.Ingredients {
		ingredients = append(ingredients, Record{
			"name": ingredient.Name, "section_name": ingredient.Section,
			"quantity": ingredient.Quantity, "unit_code": ingredient.Unit,
			"quantity_note":    ingredient.QuantityNote,
			"preparation_note": ingredient.PreparationNote,
		})
	}
	steps := make([]Record, 0, len(portable.Steps))
	for _, step := range portable.Steps {
		steps = append(steps, Record{"instruction": step.Instruction, "timer_seconds": step.TimerSeconds})
	}
	captured := scope.ServerTime.UTC()
	if captured.IsZero() {
		captured = time.Now().UTC()
	}
	snapshot := Record{
		"recipe": Record{"id": rid, "title": portable.Title, "summary": portable.Summary,
			"source_url": portable.SourceURL, "base_servings": portable.Servings,
			"prep_minutes": portable.PrepMinutes, "cook_minutes": portable.CookMinutes,
			"notes": portable.Notes, "version": String(r["content_version"])},
		"ingredients": ingredients, "steps": steps, "tags": portable.Tags,
		"profiles": Record{"nutrition": nutrition, "allergens": allergens},
		"images":   []Record{}, "version": String(r["content_version"]),
		"captured_at": captured.Format(time.RFC3339Nano),
		"expires_at":  captured.Add(30 * 24 * time.Hour).Format(time.RFC3339Nano),
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return Record{"context": scope, "snapshot": snapshot}, nil
}
