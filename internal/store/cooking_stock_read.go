package store

import (
	"context"
	"errors"
	"math/big"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// Only the completed record's captured ingredients are loaded. Live recipes,
// preparation text and private cooking notes are not stock-booking inputs.
func readCookingStockSource(ctx context.Context, q pantryQuerier, id auth.Identity, record string) (Record, error) {
	if !value.ValidUUID(record) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT jsonb_build_object('cooking_record_id',c.id,'source_version_id',v.id,'revision',v.revision::text,
 'title',v.snapshot->'recipe'->>'title','base_servings',v.snapshot->'recipe'->>'base_servings','actual_servings',c.actual_servings::text,'cooked_on',c.cooked_on,
 'ingredients',coalesce((SELECT jsonb_agg(jsonb_build_object('id',x.item->>'id','name',x.item->>'name','quantity',x.item->>'quantity',
 'unit_code',x.item->>'unit_code','catalog_id',x.item->>'catalog_id','catalog_label',x.item->>'catalog_label') ORDER BY x.ordinal)
 FROM (SELECT item,ordinal FROM jsonb_array_elements(v.snapshot->'ingredients') WITH ORDINALITY a(item,ordinal) LIMIT 501) x),'[]'::jsonb))
 FROM recipes.cooking_records c JOIN recipes.cooking_sessions s ON s.household_id=c.household_id AND s.id=c.session_id AND s.state='completed'
 JOIN recipes.versions v ON v.household_id=c.household_id AND v.id=c.source_version_id
 WHERE c.household_id=$1 AND c.id=$2`, id.HouseholdID, record).Scan(&raw)
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
	rows, ok := source["ingredients"].([]any)
	if !ok || len(rows) > 500 {
		return nil, Invalid{"Höchstens 500 Quellzutaten können gemeinsam geprüft werden"}
	}
	return source, nil
}

func cookingStockPortions(source Record) (*big.Rat, *big.Rat, error) {
	base, e1 := value.Decimal(String(source["base_servings"]))
	actual, e2 := value.Decimal(String(source["actual_servings"]))
	if e1 != nil || e2 != nil || base.Sign() <= 0 || actual.Sign() <= 0 {
		return nil, nil, Invalid{"Gespeicherte Portionen sind ungültig"}
	}
	return base, actual, nil
}

func (s *Store) CookingStockPreview(ctx context.Context, id auth.Identity, record string) (Record, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var generation, targetGeneration int64
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT r.data_generation,p.data_generation,p.state='enabled' FROM core.module_states r JOIN core.module_states p ON p.household_id=r.household_id AND p.module_id='pantry'
 WHERE r.household_id=$1 AND r.module_id='recipes' AND r.state='enabled'`, id.HouseholdID).Scan(&generation, &targetGeneration, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	source, err := readCookingStockSource(ctx, tx, id, record)
	if err != nil {
		return nil, err
	}
	base, servings, err := cookingStockPortions(source)
	if err != nil {
		return nil, err
	}
	rows := source["ingredients"].([]any)
	catalogs := []string{}
	for _, raw := range rows {
		if cid := String(raw.(map[string]any)["catalog_id"]); value.ValidUUID(cid) {
			catalogs = append(catalogs, cid)
		}
	}
	mappings, err := comparisonMappings(ctx, tx, id, catalogs, generation, targetGeneration, enabled)
	if err != nil {
		return nil, err
	}
	for _, raw := range rows {
		row := raw.(map[string]any)
		cid := String(row["catalog_id"])
		mapping := mappings[cid]
		row["reason"] = comparisonMappingReason(cid, mapping, enabled)
		if row["reason"] == "" {
			row["mapping"] = mapping
			demand, e := ingredientDemand(String(row["quantity"]), String(row["unit_code"]), String(mapping["unit"]), base, servings)
			if e != nil {
				row["reason"] = "quantity_or_unit_unknown"
			} else {
				row["suggested"], err = ingredientAmount(demand)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	proof, _, err := readCookingStockProof(ctx, tx, id, record, generation)
	if err != nil {
		return nil, err
	}
	var completion Record
	if proof != nil {
		completion, err = visibleCookingStockReceipt(ctx, tx, id, proof, enabled, targetGeneration)
	}
	if err != nil {
		return nil, err
	}
	out := Record{"source": source, "generation": generation, "pantry_generation": targetGeneration, "pantry_enabled": enabled, "completion": completion}
	return out, tx.Commit(ctx)
}

func readCookingStockProof(ctx context.Context, q pantryQuerier, id auth.Identity, record string, generation int64) (Record, []byte, error) {
	var raw, hash []byte
	err := q.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'mode',mode,'pantry_generation',pantry_generation,'created_at',created_at,'movement_count',movement_count),request_hash
 FROM core.cooking_stock_postings WHERE household_id=$1 AND recipes_generation=$2 AND cooking_record_id=$3`, id.HouseholdID, generation, record).Scan(&raw, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	proof, err := decode(raw)
	return proof, hash, err
}

func visibleCookingStockReceipt(ctx context.Context, q pantryQuerier, id auth.Identity, proof Record, enabled bool, generation int64) (Record, error) {
	out := Record{"id": proof["id"], "mode": proof["mode"], "created_at": proof["created_at"], "details_available": false}
	if !enabled || Int(proof["pantry_generation"]) != generation {
		return out, nil
	}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT r.snapshot||jsonb_build_object('id',r.id,'details_available',true,'movements',
 coalesce((SELECT jsonb_agg(jsonb_build_object('id',m.id,'lot_id',m.lot_id,'operation_id',m.operation_id,'quantity',m.quantity::text,'unit',m.unit_code,
 'product_name',o.snapshot->>'product_name','location',o.snapshot->>'location','lot_label',o.snapshot->>'lot_label','stock_after',o.snapshot->>'stock_after',
 'reversed',EXISTS(SELECT 1 FROM pantry.stock_movements x WHERE x.household_id=m.household_id AND x.reverses_movement_id=m.id)) ORDER BY m.lot_id)
 FROM pantry.cooking_stock_movements m JOIN pantry.stock_operations o ON o.household_id=m.household_id AND o.id=m.operation_id WHERE m.household_id=r.household_id AND m.posting_id=r.id),'[]'::jsonb))
 FROM pantry.cooking_stock_receipts r WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, proof["id"]).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

// Pantry-owned receipt remains readable after Recipes purge.
func (s *Store) CookingStockReceipt(ctx context.Context, id auth.Identity, posting string) (Record, error) {
	if !value.ValidUUID(posting) {
		return nil, ErrNotFound
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var raw []byte
	var generation int64
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'mode',p.mode,'created_at',p.created_at,'pantry_generation',p.pantry_generation),m.data_generation
 FROM core.cooking_stock_postings p JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='pantry' AND m.state='enabled' AND m.data_generation=p.pantry_generation
 WHERE p.household_id=$1 AND p.id=$2`, id.HouseholdID, posting).Scan(&raw, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	proof, err := decode(raw)
	if err != nil {
		return nil, err
	}
	result, err := visibleCookingStockReceipt(ctx, tx, id, proof, true, generation)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
