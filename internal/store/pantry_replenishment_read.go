package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PantryReplenishmentView struct {
	Product       Record              `json:"product"`
	Balance       string              `json:"balance"`
	MovementCount string              `json:"movement_count"`
	Generation    int64               `json:"generation"`
	Packs         []Record            `json:"packs"`
	PackID        string              `json:"pack_id"`
	PackVersion   int64               `json:"pack_version"`
	Suggestion    PantryReplenishment `json:"suggestion"`
}

func (s *Store) PantryReplenishment(ctx context.Context, id auth.Identity, product, pack string) (PantryReplenishmentView, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PantryReplenishmentView{}, err
	}
	defer rollback(tx)
	out, err := readPantryReplenishment(ctx, tx, id, product, pack)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func readPantryReplenishment(ctx context.Context, tx pgx.Tx, id auth.Identity, product, packID string) (PantryReplenishmentView, error) {
	out := PantryReplenishmentView{Packs: []Record{}, PackID: packID}
	if !value.ValidUUID(product) || packID != "" && !value.ValidUUID(packID) {
		return out, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'version',p.version,'name',p.name,
 'unit_code',p.unit_code,'integer_quantities',p.integer_quantities,'archived_at',p.archived_at,
 'low_stock_threshold',p.low_stock_threshold::text,'target_stock',p.target_stock::text),ms.data_generation,
 coalesce((SELECT sum(b.quantity) FROM pantry.lots l JOIN pantry.lot_balances b
 ON b.household_id=l.household_id AND b.lot_id=l.id WHERE l.household_id=p.household_id AND l.product_id=p.id),0)::text,
 coalesce((SELECT sum(b.movement_count) FROM pantry.lots l JOIN pantry.lot_balances b
 ON b.household_id=l.household_id AND b.lot_id=l.id WHERE l.household_id=p.household_id AND l.product_id=p.id),0)::text
 FROM pantry.products p JOIN core.module_states ms ON ms.household_id=p.household_id AND ms.module_id='pantry' AND ms.state='enabled'
 WHERE p.household_id=$1 AND p.id=$2`, id.HouseholdID, product).Scan(&raw, &out.Generation, &out.Balance, &out.MovementCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Product, err = decode(raw)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT jsonb_build_object('id',id,'version',version,'label',label,
 'quantity',quantity::text,'unit_code',unit_code,'disabled',disabled) FROM pantry.packs
 WHERE household_id=$1 AND product_id=$2 ORDER BY created_at,id LIMIT 51`, id.HouseholdID, product)
	if err != nil {
		return out, err
	}
	var selected Record
	for rows.Next() {
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var pack Record
		pack, err = decode(raw)
		if err != nil {
			break
		}
		out.Packs = append(out.Packs, pack)
		if String(pack["id"]) == packID {
			selected, out.PackVersion = pack, Int(pack["version"])
		}
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return out, err
	}
	if len(out.Packs) > 50 {
		return out, Invalid{"Mehr als 50 Packungen vorhanden; Produktkonfiguration zuerst prüfen"}
	}
	if packID != "" && (selected == nil || selected["disabled"] == true) {
		return out, Invalid{"Die ausgewählte Packung ist nicht mehr für dieses Produkt verfügbar. Auswahl prüfen."}
	}
	out.Suggestion, err = pantryReplenishment(out.Product, out.Balance, selected)
	return out, err
}
