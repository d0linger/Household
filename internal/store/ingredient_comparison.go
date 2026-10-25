package store

import (
	"context"
	"errors"
	"math/big"
	"sort"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type IngredientComparisonLine struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	CatalogID    string            `json:"catalog_id"`
	CatalogLabel string            `json:"catalog_label"`
	ProductID    string            `json:"product_id,omitempty"`
	Reason       string            `json:"reason"`
	Demand       *IngredientAmount `json:"demand,omitempty"`
}

type IngredientComparisonProduct struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Unit      string            `json:"unit"`
	Complete  bool              `json:"complete"`
	Demand    IngredientAmount  `json:"demand"`
	Stock     IngredientAmount  `json:"stock"`
	Shortage  *IngredientAmount `json:"shortage,omitempty"`
	LotCount  int64             `json:"lot_count"`
	LotSample []Record          `json:"lot_sample"`
	quantity  *big.Rat
}

type IngredientComparison struct {
	RecipeID        string                         `json:"recipe_id"`
	Title           string                         `json:"title"`
	SourceVersionID string                         `json:"source_version_id"`
	Revision        string                         `json:"revision"`
	Historical      bool                           `json:"historical"`
	Servings        string                         `json:"servings"`
	BaseServings    string                         `json:"base_servings"`
	PantryEnabled   bool                           `json:"pantry_enabled"`
	Outcome         string                         `json:"outcome"`
	Lines           []IngredientComparisonLine     `json:"lines"`
	Products        []*IngredientComparisonProduct `json:"products"`
}

// RecipePantryComparison reads one bounded source and current assignments/stock
// in a repeatable-read snapshot. It never reserves, suggests substitutions or
// books quantities; rounded displays retain their exact rational alongside them.
func (s *Store) RecipePantryComparison(ctx context.Context, id auth.Identity, rid, vid, portions string) (IngredientComparison, error) {
	out := IngredientComparison{RecipeID: rid, Historical: vid != "", Servings: portions,
		Lines: []IngredientComparisonLine{}, Products: []*IngredientComparisonProduct{}}
	servings, err := value.Decimal(portions)
	if err != nil || servings.Sign() <= 0 {
		return out, Invalid{"Positive Portionenzahl mit höchstens sechs Nachkommastellen erforderlich"}
	}
	if !value.ValidUUID(rid) || vid != "" && !value.ValidUUID(vid) {
		return out, ErrNotFound
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	var ingredientsRaw []byte
	var generation, pantryGeneration int64
	err = tx.QueryRow(ctx, `SELECT v.id,v.revision::text,v.snapshot->'recipe'->>'title',v.snapshot->'recipe'->>'base_servings',
 coalesce((SELECT jsonb_agg(jsonb_build_object('id',x.item->>'id','name',x.item->>'name','catalog_id',x.item->>'catalog_id',
 'catalog_label',x.item->>'catalog_label','quantity',x.item->>'quantity','unit_code',x.item->>'unit_code') ORDER BY x.ordinal)
 FROM (SELECT item,ordinal FROM jsonb_array_elements(v.snapshot->'ingredients') WITH ORDINALITY a(item,ordinal) LIMIT 501) x),'[]'::jsonb),
 rs.data_generation,coalesce(ps.state='enabled',false),coalesce(ps.data_generation,0)
 FROM recipes.recipes r JOIN core.module_states rs ON rs.household_id=r.household_id AND rs.module_id='recipes' AND rs.state='enabled'
 LEFT JOIN core.module_states ps ON ps.household_id=r.household_id AND ps.module_id='pantry'
 JOIN LATERAL (SELECT * FROM recipes.versions ver WHERE ver.household_id=r.household_id AND ver.recipe_id=r.id
 AND (nullif($3,'')::uuid IS NULL OR ver.id=nullif($3,'')::uuid) ORDER BY ver.revision DESC LIMIT 1) v ON true
 WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, rid, vid).Scan(&out.SourceVersionID, &out.Revision, &out.Title, &out.BaseServings,
		&ingredientsRaw, &generation, &out.PantryEnabled, &pantryGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	base, err := value.Decimal(out.BaseServings)
	if err != nil || base.Sign() <= 0 {
		return out, Invalid{"Die Rezeptversion enthält keine gültige Basisportionenzahl"}
	}
	parsed, err := decode([]byte(`{"rows":` + string(ingredientsRaw) + `}`))
	if err != nil {
		return out, err
	}
	rows, ok := parsed["rows"].([]any)
	if !ok || len(rows) > 500 {
		return out, Invalid{"Vergleich auf höchstens 500 Zutaten begrenzen"}
	}
	catalogIDs := []string{}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, valid := raw.(map[string]any)
		if !valid {
			return out, Invalid{"Ungültige Zutaten in der Rezeptversion"}
		}
		cid := String(row["catalog_id"])
		if value.ValidUUID(cid) && !seen[cid] {
			catalogIDs = append(catalogIDs, cid)
			seen[cid] = true
		}
	}
	mappings, err := comparisonMappings(ctx, tx, id, catalogIDs, generation, pantryGeneration, out.PantryEnabled)
	if err != nil {
		return out, err
	}
	groups := map[string]*IngredientComparisonProduct{}
	complete := len(rows) > 0
	for _, raw := range rows {
		row := raw.(map[string]any)
		line := IngredientComparisonLine{ID: String(row["id"]), Name: String(row["name"]), CatalogID: String(row["catalog_id"]), CatalogLabel: String(row["catalog_label"])}
		mapping := mappings[line.CatalogID]
		line.Reason = comparisonMappingReason(line.CatalogID, mapping, out.PantryEnabled)
		if line.Reason == "" {
			pid := String(mapping["product_id"])
			line.ProductID = pid
			group := groups[pid]
			if group == nil {
				group = &IngredientComparisonProduct{ID: pid, Name: String(mapping["product_name"]), Unit: String(mapping["unit"]),
					Complete: true, quantity: new(big.Rat), LotSample: []Record{}}
				groups[pid] = group
			}
			q, quantityErr := value.Decimal(String(row["quantity"]))
			if quantityErr != nil || q.Sign() <= 0 {
				line.Reason = "quantity_unknown"
			} else {
				demand, demandErr := ingredientDemand(String(row["quantity"]), String(row["unit_code"]), group.Unit, base, servings)
				if demandErr != nil {
					line.Reason = "unit_incompatible"
				} else {
					amount, amountErr := ingredientAmount(demand)
					if amountErr != nil {
						return out, amountErr
					}
					line.Demand = &amount
					group.quantity.Add(group.quantity, demand)
				}
			}
			if line.Reason != "" {
				group.Complete = false
			}
		}
		if line.Reason != "" {
			complete = false
		}
		out.Lines = append(out.Lines, line)
	}
	if err = comparisonStock(ctx, tx, id, groups); err != nil {
		return out, err
	}
	out.Outcome = "sufficient"
	for _, group := range groups {
		if group.Complete && group.Shortage != nil && group.Shortage.Rational != "0" {
			out.Outcome = "shortage"
		}
		out.Products = append(out.Products, group)
	}
	if !complete {
		out.Outcome = "incomplete"
	}
	if len(rows) == 0 {
		out.Outcome = "empty"
	}
	sort.Slice(out.Products, func(i, j int) bool {
		if out.Products[i].Name == out.Products[j].Name {
			return out.Products[i].ID < out.Products[j].ID
		}
		return out.Products[i].Name < out.Products[j].Name
	})
	return out, tx.Commit(ctx)
}

func comparisonMappingReason(cid string, m Record, enabled bool) string {
	switch {
	case cid == "":
		return "catalog_unassigned"
	case m == nil:
		return "catalog_missing"
	case m["catalog_archived"] == true:
		return "catalog_archived"
	case !enabled:
		return "pantry_disabled"
	case m["binding_id"] == nil || m["assigned"] != true:
		return "product_unassigned"
	case m["target_current"] != true:
		return "binding_stale"
	case m["product_id"] == nil:
		return "product_missing"
	case m["product_archived"] == true:
		return "product_archived"
	default:
		return ""
	}
}

func comparisonMappings(ctx context.Context, tx pgx.Tx, id auth.Identity, catalogs []string, generation, targetGeneration int64, enabled bool) (map[string]Record, error) {
	rows, err := tx.Query(ctx, `SELECT c.id,jsonb_build_object('catalog_archived',c.archived,'binding_id',b.id,'assigned',b.product_id IS NOT NULL,
 'target_current',b.target_generation=$4,'product_id',p.id,'product_name',p.name,'unit',p.unit_code,'product_archived',p.archived_at IS NOT NULL)
 FROM recipes.ingredient_catalog c LEFT JOIN core.ingredient_product_bindings b
 ON b.household_id=c.household_id AND b.catalog_id=c.id AND b.source_generation=$3
 LEFT JOIN pantry.products p ON $5 AND b.target_generation=$4 AND p.household_id=b.household_id AND p.id=b.product_id
 WHERE c.household_id=$1 AND c.id=ANY($2::uuid[])`, id.HouseholdID, catalogs, generation, targetGeneration, enabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Record{}
	for rows.Next() {
		var cid string
		var raw []byte
		if err = rows.Scan(&cid, &raw); err != nil {
			return nil, err
		}
		mapping, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out[cid] = mapping
	}
	return out, rows.Err()
}
