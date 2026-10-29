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

func (s *Store) PantryPreparedLot(ctx context.Context, id auth.Identity, lotID string) (Record, error) {
	if !value.ValidUUID(lotID) {
		return nil, ErrNotFound
	}
	lotID = strings.ToLower(lotID)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer pantryLeftoversRollback(tx)
	scope, err := readPantryWasteScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object('dish_name',p.dish_name,'prepared_on',p.prepared_on,'initial_portions',coalesce(t.quantity,p.initial_portions)::text,
 'root_initial_portions',p.initial_portions::text,'root_lot_id',p.lot_id,'source_lot_id',t.source_lot_id,'transferred',t.lot_id IS NOT NULL,
 'prepared_portions',p.prepared_portions::text,'portions_unknown',p.portions_unknown,'source_kind',p.source_kind,
 'initial_location_id',p.location_id,'initial_label',p.label,'received_on',p.received_on,'best_before_on',p.best_before_on,'use_by_on',p.use_by_on)
 FROM pantry.prepared_lots p LEFT JOIN pantry.prepared_transfers t ON t.household_id=p.household_id AND t.root_lot_id=p.lot_id AND t.lot_id=$2
 WHERE p.household_id=$1 AND p.lot_id=coalesce((SELECT root_lot_id FROM pantry.prepared_transfers
 WHERE household_id=$1 AND lot_id=$2),$2::uuid)`, id.HouseholdID, lotID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	prepared, err := decode(raw)
	if err != nil {
		return nil, err
	}
	lot, err := readPantryLot(ctx, tx, id, lotID)
	if err != nil {
		return nil, err
	}
	source := Record{"kind": prepared["source_kind"], "available": false}
	if prepared["source_kind"] != "manual" {
		if live, err := pantryLeftoversCoreLiveSource(ctx, tx, id, String(prepared["root_lot_id"])); err != nil {
			return nil, err
		} else if live != nil {
			source = live
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return Record{"context": scope, "lot": lot, "prepared": prepared, "source": source, "unit": "portion"}, nil
}
