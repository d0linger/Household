package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) IngredientCatalogView(ctx context.Context, id auth.Identity, catalog string) (Record, error) {
	if !value.ValidUUID(catalog) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('catalog',to_jsonb(c),'generation',rs.data_generation,
 'pantry_enabled',coalesce(ps.state='enabled',false),'pantry_generation',ps.data_generation,
 'aliases',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at,x.id) FROM
  (SELECT a.id,a.created_at,jsonb_build_object('id',a.id,'name',a.name,'version',a.version) item FROM recipes.ingredient_aliases a
   WHERE a.household_id=c.household_id AND a.catalog_id=c.id ORDER BY a.created_at,a.id LIMIT 51) x),'[]'::jsonb),
 'binding',coalesce((SELECT jsonb_build_object('id',b.id,'version',b.version,'assigned',b.product_id IS NOT NULL,
  'target_current',coalesce(ps.state='enabled' AND b.target_generation=ps.data_generation,false),
  'product_id',p.id,'product_name',p.name,'product_version',p.version,'unit',p.unit_code,'archived',p.archived_at IS NOT NULL)
 FROM core.ingredient_product_bindings b LEFT JOIN pantry.products p ON ps.state='enabled' AND b.target_generation=ps.data_generation
  AND p.household_id=b.household_id AND p.id=b.product_id
 WHERE b.household_id=c.household_id AND b.catalog_id=c.id AND b.source_generation=rs.data_generation),
 jsonb_build_object('id','','version',0,'assigned',false,'product_id',NULL)))
 FROM recipes.ingredient_catalog c JOIN core.module_states rs ON rs.household_id=c.household_id AND rs.module_id='recipes' AND rs.state='enabled'
 LEFT JOIN core.module_states ps ON ps.household_id=c.household_id AND ps.module_id='pantry'
 WHERE c.household_id=$1 AND c.id=$2`, id.HouseholdID, catalog).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	result, err := decode(raw)
	if err != nil {
		return nil, err
	}
	aliases, ok := result["aliases"].([]any)
	if !ok || len(aliases) > 50 {
		return nil, Invalid{"Aliasgrenze überschritten; Katalogverwaltung prüfen"}
	}
	return result, nil
}
