package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PantryBatchReceipt(ctx context.Context, id auth.Identity, receipt string) (Record, error) {
	if !value.ValidUUID(receipt) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT r.snapshot FROM pantry.stock_batch_receipts r JOIN core.module_states m ON m.household_id=r.household_id
 AND m.module_id='pantry' AND m.state='enabled' AND m.data_generation=r.generation WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, receipt).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) PantryInventoryLots(ctx context.Context, id auth.Identity, location, query string, archived bool, after time.Time, afterID string) ([]PantryPriorityRow, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(location) || !batchText(query, 300) || afterID != "" && !value.ValidUUID(afterID) {
		return nil, Invalid{"Gültigen Lagerort und Suchbegriff auswählen"}
	}
	var anchor any
	if afterID != "" {
		anchor = after
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+pantryLotJSON+`,l.created_at FROM pantry.lots l
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE l.household_id=$1 AND l.location_id=$2 AND ($3='' OR strpos(lower(p.name),lower($3))>0 OR strpos(lower(l.notes),lower($3))>0)
 AND ($4 OR p.archived_at IS NULL AND loc.archived_at IS NULL)
 AND ($5::timestamptz IS NULL OR (l.created_at,l.id)<($5,nullif($6,'')::uuid))
 ORDER BY l.created_at DESC,l.id DESC LIMIT 51`, id.HouseholdID, location, strings.TrimSpace(query), archived, anchor, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PantryPriorityRow, 0, 51)
	for rows.Next() {
		var raw []byte
		var at time.Time
		if err = rows.Scan(&raw, &at); err != nil {
			return nil, err
		}
		lot, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err = pantryDates(lot); err != nil {
			return nil, err
		}
		result = append(result, PantryPriorityRow{Lot: lot, CreatedAt: at})
	}
	return result, rows.Err()
}

func (s *Store) PantryIntakeState(ctx context.Context, id auth.Identity, product, location string) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(product) || !value.ValidUUID(location) {
		return nil, Invalid{"Produkt und Lagerort ausdrücklich auswählen"}
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('product',to_jsonb(p),'location',to_jsonb(l),
 'packs',coalesce((SELECT jsonb_agg(x.item ORDER BY x.created_at,x.id) FROM
 (SELECT pack.created_at,pack.id,to_jsonb(pack)||jsonb_build_object('quantity',pack.quantity::text) item FROM pantry.packs pack
 WHERE pack.household_id=p.household_id AND pack.product_id=p.id ORDER BY pack.created_at,pack.id LIMIT 51) x),'[]'::jsonb))
 FROM pantry.products p JOIN pantry.locations l ON l.household_id=p.household_id
 WHERE p.household_id=$1 AND p.id=$2 AND l.id=$3`, id.HouseholdID, product, location).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	result, err := decode(raw)
	if err == nil && len(result["packs"].([]any)) > 50 {
		return nil, Invalid{"Packungsgrenze überschritten; Produktverwaltung prüfen"}
	}
	return result, err
}
