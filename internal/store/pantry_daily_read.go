package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type pantryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const pantryLotJSON = `jsonb_build_object('id',l.id,'version',l.version,'label',left(l.notes,500),
 'product_id',p.id,'product_version',p.version,'product_name',p.name,'unit',p.unit_code,'integer_quantities',p.integer_quantities,'product_archived',p.archived_at IS NOT NULL,
 'location_id',loc.id,'location_version',loc.version,'location',loc.name,'location_archived',loc.archived_at IS NOT NULL,
 'received_on',l.received_on,'best_before_on',l.best_before_on,'use_by_on',l.use_by_on,'opened_on',l.opened_on,
 'after_open_days',l.after_open_days,'after_open_source',l.after_open_source,'after_open_note',l.after_open_note,
 'stock',coalesce(b.quantity,0)::text,'movement_count',coalesce(b.movement_count,0))`

const pantryLotFrom = ` FROM pantry.lots l JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE l.household_id=$1 AND l.id=$2`

func pantryDates(lot Record) error {
	var days *int64
	if lot["after_open_days"] != nil {
		n := Int(lot["after_open_days"])
		days = &n
	}
	hint, err := pantryOpeningHint(String(lot["opened_on"]), days)
	if err != nil {
		return err
	}
	lot["opening_hint_on"] = hint
	lot["priority_date"], lot["priority_kind"] = pantryPriority(String(lot["best_before_on"]), String(lot["use_by_on"]), hint)
	return nil
}

func readPantryLot(ctx context.Context, q pantryQuerier, id auth.Identity, lid string) (Record, error) {
	if !value.ValidUUID(lid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := q.QueryRow(ctx, "SELECT "+pantryLotJSON+pantryLotFrom, id.HouseholdID, lid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	lot, err := decode(raw)
	if err != nil {
		return nil, err
	}
	return lot, pantryDates(lot)
}

func lockPantryLot(ctx context.Context, tx pgx.Tx, id auth.Identity, lid string) (Record, error) {
	var locked string
	err := tx.QueryRow(ctx, `SELECT l.id FROM pantry.lots l
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 WHERE l.household_id=$1 AND l.id=$2 FOR UPDATE OF l NOWAIT FOR SHARE OF p,loc NOWAIT`, id.HouseholdID, lid).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	// A lock does not refresh an already established statement snapshot. Read
	// the projection in a new READ COMMITTED statement after holding its parent.
	return readPantryLot(ctx, tx, id, lid)
}

func (s *Store) PantryLotState(ctx context.Context, id auth.Identity, lid string) (Record, error) {
	return readPantryLot(ctx, s.Pool, id, lid)
}

// One statement supplies a consistent bounded review; full history remains in
// the journal. Operation summaries never load their complete stored responses.
func (s *Store) PantryConsumptionView(ctx context.Context, id auth.Identity, lid string) (Record, error) {
	if !value.ValidUUID(lid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('lot',`+pantryLotJSON+`,
 'legacy_barcode',left(p.barcode,300),'legacy_barcode_copied',EXISTS(SELECT 1 FROM pantry.barcodes bc WHERE bc.household_id=p.household_id AND bc.product_id=p.id AND bc.code=btrim(p.barcode)),
 'packs',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at,x.id) FROM
  (SELECT c.id,c.created_at,jsonb_build_object('id',c.id,'version',c.version,'label',c.label,'quantity',c.quantity::text,'unit',c.unit_code,'disabled',c.disabled) item
   FROM pantry.packs c WHERE c.household_id=p.household_id AND c.product_id=p.id ORDER BY c.created_at,c.id LIMIT 51) x),'[]'::jsonb),
 'barcodes',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at,x.id) FROM
  (SELECT c.id,c.created_at,jsonb_build_object('id',c.id,'version',c.version,'code',c.code,'pack_id',c.pack_id) item
   FROM pantry.barcodes c WHERE c.household_id=p.household_id AND c.product_id=p.id ORDER BY c.created_at,c.id LIMIT 101) x),'[]'::jsonb),
 'operations',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at DESC,x.id DESC) FROM
  (SELECT o.id,o.created_at,jsonb_build_object('id',o.id,'kind',o.kind,'movement_id',o.movement_id,'created_at',o.created_at,
   'quantity',o.snapshot->>'quantity','unit',o.snapshot->>'unit','undone',EXISTS(SELECT 1 FROM pantry.stock_movements r WHERE r.household_id=o.household_id AND r.reverses_movement_id=o.movement_id)) item
   FROM pantry.stock_operations o WHERE o.household_id=l.household_id AND o.lot_id=l.id ORDER BY o.created_at DESC,o.id DESC LIMIT 21) x),'[]'::jsonb),
 'batches',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at DESC,x.id DESC) FROM
  (SELECT r.id,r.created_at,jsonb_build_object('id',r.id,'kind',r.kind,'created_at',r.created_at,'before',bl.before_quantity::text,
   'counted',bl.counted_quantity::text,'delta',bl.quantity_delta::text,'unit',bl.unit_code) item
   FROM pantry.stock_batch_lines bl JOIN pantry.stock_batch_receipts r ON r.household_id=bl.household_id AND r.id=bl.receipt_id
   WHERE bl.household_id=l.household_id AND bl.lot_id=l.id ORDER BY r.created_at DESC,r.id DESC LIMIT 20) x),'[]'::jsonb))`+pantryLotFrom, id.HouseholdID, lid).Scan(&raw)
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
	if len(result["packs"].([]any)) > 50 || len(result["barcodes"].([]any)) > 100 {
		return nil, Invalid{"Packungs- oder Barcodegrenze überschritten. Einzelverwaltung prüfen."}
	}
	lot, ok := result["lot"].(map[string]any)
	if !ok {
		return nil, Invalid{"Chargenstand konnte nicht gelesen werden"}
	}
	if err = pantryDates(lot); err != nil {
		return nil, err
	}
	operations := result["operations"].([]any)
	result["more_operations"] = len(operations) > 20
	if len(operations) > 20 {
		result["operations"] = operations[:20]
	}
	return result, nil
}

func (s *Store) PantryStockOperation(ctx context.Context, id auth.Identity, oid string) (Record, error) {
	if !value.ValidUUID(oid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, "SELECT snapshot FROM pantry.stock_operations WHERE household_id=$1 AND id=$2", id.HouseholdID, oid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) PantryBarcode(ctx context.Context, id auth.Identity, raw string) (Record, error) {
	code, err := pantryBarcode(raw)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('product_id',p.id,'name',p.name,'unit',p.unit_code,'archived',p.archived_at IS NOT NULL,
 'pack_id',b.pack_id,'pack_label',pack.label,'pack_disabled',pack.disabled,'pack_version',pack.version)
 FROM pantry.barcodes b JOIN pantry.products p ON p.household_id=b.household_id AND p.id=b.product_id
 LEFT JOIN pantry.packs pack ON pack.household_id=b.household_id AND pack.product_id=b.product_id AND pack.id=b.pack_id
 WHERE b.household_id=$1 AND b.code=$2`, id.HouseholdID, code).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(data)
}
