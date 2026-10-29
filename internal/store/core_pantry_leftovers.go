package store

import (
	"context"
	"errors"
	"strconv"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

// This Core boundary is the only new Pantry reader of Recipes facts. It returns
// immutable source metadata without cooking notes, steps or ingredient payloads.
func pantryLeftoversCoreSource(ctx context.Context, tx pgx.Tx, id auth.Identity, in PantryLeftoversInput) (Record, error) {
	if in.SourceKind == "manual" {
		return Record{"kind": "manual", "actual_servings": "", "available": true}, nil
	}
	var generation int64
	err := tx.QueryRow(ctx, `SELECT data_generation FROM core.module_states WHERE household_id=$1
 AND module_id='recipes' AND state='enabled' FOR SHARE NOWAIT`, id.HouseholdID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if generation != in.RecipesGeneration {
		return nil, ErrConflict
	}
	var raw []byte
	if in.SourceKind == "cooking" {
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('kind','cooking','id',c.id,'source_version_id',v.id,
 'title',left(v.snapshot->'recipe'->>'title',200),'cooked_on',c.cooked_on,'actual_servings',c.actual_servings::text,'available',true)
 FROM recipes.cooking_records c JOIN recipes.cooking_sessions s ON s.household_id=c.household_id AND s.id=c.session_id AND s.state='completed'
 JOIN recipes.versions v ON v.household_id=c.household_id AND v.id=c.source_version_id
 WHERE c.household_id=$1 AND c.id=$2`, id.HouseholdID, in.SourceID).Scan(&raw)
	} else {
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('kind','recipe_version','id',v.id,'source_version_id',v.id,
 'title',left(v.snapshot->'recipe'->>'title',200),'actual_servings','','available',true)
 FROM recipes.versions v WHERE v.household_id=$1 AND v.id=$2`, id.HouseholdID, in.SourceID).Scan(&raw)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	source, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if in.SourceVersionID != "" && in.SourceVersionID != String(source["source_version_id"]) {
		return nil, ErrConflict
	}
	source["generation"] = strconv.FormatInt(generation, 10)
	return source, nil
}

func pantryLeftoversCoreLink(ctx context.Context, tx pgx.Tx, id auth.Identity, in PantryLeftoversInput, lot string) error {
	if in.SourceKind == "manual" {
		return nil
	}
	entity := "versions"
	if in.SourceKind == "cooking" {
		entity = "cooking_records"
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'recipes',$2,$3,'pantry','lots',$4,'prepared_leftovers') ON CONFLICT DO NOTHING`, id.HouseholdID, entity, in.SourceID, lot)
	return err
}

func pantryLeftoversCoreLiveSource(ctx context.Context, tx pgx.Tx, id auth.Identity, lot string) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT CASE WHEN l.source_type='cooking_records' THEN
 (SELECT jsonb_build_object('kind','cooking','id',c.id,'source_version_id',v.id,'title',left(v.snapshot->'recipe'->>'title',200),'available',true)
 FROM recipes.cooking_records c JOIN recipes.versions v ON v.household_id=c.household_id AND v.id=c.source_version_id
 WHERE c.household_id=l.household_id AND c.id=l.source_id)
 ELSE(SELECT jsonb_build_object('kind','recipe_version','id',v.id,'source_version_id',v.id,'title',left(v.snapshot->'recipe'->>'title',200),'available',true)
 FROM recipes.versions v WHERE v.household_id=l.household_id AND v.id=l.source_id) END
 FROM core.resource_links l JOIN core.module_states s ON s.household_id=l.household_id AND s.module_id='recipes' AND s.state='enabled'
 WHERE l.household_id=$1 AND l.source_module='recipes' AND l.source_type IN('versions','cooking_records')
 AND l.target_module='pantry' AND l.target_type='lots' AND l.target_id=$2 AND l.relation='prepared_leftovers'
 ORDER BY l.id LIMIT 1`, id.HouseholdID, lot).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && len(raw) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}
