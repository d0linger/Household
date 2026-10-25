package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type pantryBatchEffect struct {
	Input  PantryBatchLine
	Lot    Record
	Data   Record
	Pack   Record
	Before string
	Count  string
	Delta  string
}

func (s *Store) validatePantryBatch(ctx context.Context, tx pgx.Tx, id auth.Identity, in PantryBatchDraftInput) ([]pantryBatchEffect, error) {
	if len(in.Lines) == 0 {
		return nil, Invalid{"Mindestens eine ausdrücklich geprüfte Zeile erforderlich"}
	}
	if in.Kind == "inventory" && !value.ValidUUID(in.LocationID) {
		return nil, Invalid{"Inventur-Lagerort ausdrücklich auswählen"}
	}
	// Inventory acquires each parent in the same deterministic order. The
	// incumbent stock writer uses the same lot lock and fresh projection read.
	lines := slices.Clone(in.Lines)
	slices.SortFunc(lines, func(a, b PantryBatchLine) int {
		if in.Kind == "inventory" {
			return strings.Compare(a.LotID, b.LotID)
		}
		return strings.Compare(a.ProductID+a.LocationID+a.PackID+a.ID, b.ProductID+b.LocationID+b.PackID+b.ID)
	})
	effects := make(map[string]pantryBatchEffect, len(lines))
	for _, line := range lines {
		var effect pantryBatchEffect
		var err error
		if in.Kind == "intake" {
			effect, err = s.validatePantryIntake(ctx, tx, id, line)
		} else {
			effect, err = validatePantryCount(ctx, tx, id, line, in.LocationID)
		}
		if err != nil {
			return nil, err
		}
		effects[line.ID] = effect
	}
	result := make([]pantryBatchEffect, 0, len(lines))
	for _, line := range in.Lines {
		result = append(result, effects[line.ID])
	}
	return result, nil
}

func (s *Store) validatePantryIntake(ctx context.Context, tx pgx.Tx, id auth.Identity, line PantryBatchLine) (pantryBatchEffect, error) {
	effect := pantryBatchEffect{Input: line, Before: "0.000000"}
	if !value.ValidUUID(line.ProductID) || !value.ValidUUID(line.LocationID) || line.ProductVersion < 1 || line.LocationVersion < 1 {
		return effect, Invalid{"Produkt, Lagerort und deren geprüfte Versionsstände erforderlich"}
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('product_id',p.id,'product_name',p.name,'product_version',p.version,'unit',p.unit_code,
 'integer_quantities',p.integer_quantities,'location_id',l.id,'location',l.name,'location_version',l.version,
 'product_archived',p.archived_at IS NOT NULL,'location_archived',l.archived_at IS NOT NULL)
 FROM pantry.products p JOIN pantry.locations l ON l.household_id=p.household_id
 WHERE p.household_id=$1 AND p.id=$2 AND l.id=$3 FOR SHARE OF p,l NOWAIT`, id.HouseholdID, line.ProductID, line.LocationID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return effect, ErrNotFound
	}
	if err != nil {
		return effect, shoppingContention(err)
	}
	effect.Lot, err = decode(raw)
	if err != nil {
		return effect, err
	}
	if line.ProductVersion != Int(effect.Lot["product_version"]) || line.LocationVersion != Int(effect.Lot["location_version"]) {
		return effect, PantryStockConflict{Message: "Produkt oder Lagerort wurde geändert. Einlagerungszeile erneut prüfen.", Current: effect.Lot}
	}
	if effect.Lot["product_archived"] == true || effect.Lot["location_archived"] == true {
		return effect, Invalid{"Neue Einlagerung benötigt aktive Produkte und Lagerorte"}
	}
	if line.PackID != "" {
		if line.PackVersion < 1 || line.Unit != "" {
			return effect, Invalid{"Packungsanzahl mit geprüftem Packungsstand und ohne zusätzliche Mengeneinheit angeben"}
		}
		effect.Pack, err = pantryConsumptionPack(ctx, tx, id, effect.Lot, PantryStockInput{Mode: "pack", PackID: line.PackID, PackVersion: line.PackVersion})
		if err != nil {
			return effect, err
		}
	} else if line.PackVersion != 0 {
		return effect, Invalid{"Packungsstand ohne ausgewählte Packung ist ungültig"}
	}
	integers, _ := effect.Lot["integer_quantities"].(bool)
	effect.Delta, err = pantryIntakeAmount(line.Quantity, line.Unit, String(effect.Lot["unit"]), effect.Pack, integers)
	if err != nil {
		return effect, err
	}
	effect.Count = effect.Delta
	effect.Data, err = s.pantryIntakeLotData(line)
	return effect, err
}

func (s *Store) pantryIntakeLotData(line PantryBatchLine) (Record, error) {
	m, _ := s.Module("pantry")
	e, _ := m.Entity("lots")
	data := Record{"product_id": line.ProductID, "location_id": line.LocationID, "received_on": line.ReceivedOn, "notes": strings.TrimSpace(line.Label),
		"after_open_days": line.AfterOpenDays, "after_open_source": line.AfterOpenSource, "after_open_note": line.AfterOpenNote}
	for key, date := range map[string]string{"best_before_on": line.BestBefore, "use_by_on": line.UseBy, "opened_on": line.OpenedOn} {
		if date != "" {
			data[key] = date
		}
	}
	rawData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(rawData, &fields); err != nil {
		return nil, err
	}
	normalized, err := e.Normalize(fields, false)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	if err = validatePantryOpening(normalized, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func validatePantryCount(ctx context.Context, tx pgx.Tx, id auth.Identity, line PantryBatchLine, location string) (pantryBatchEffect, error) {
	effect := pantryBatchEffect{Input: line}
	if !value.ValidUUID(line.LotID) || !value.ValidUUID(line.ProductID) || line.LotVersion < 1 || line.ProductVersion < 1 || line.LocationVersion < 1 || line.LocationID != location {
		return effect, Invalid{"Charge, Produkt und fester Lagerort mit geprüftem Stand erforderlich"}
	}
	lot, err := lockPantryLot(ctx, tx, id, line.LotID)
	if err != nil {
		return effect, err
	}
	if line.Unit != "" && line.Unit != String(lot["unit"]) {
		return effect, Invalid{"Inventur zählt ausschließlich in der Basiseinheit dieser Charge. Mengeneinheit erneut prüfen."}
	}
	integers, _ := lot["integer_quantities"].(bool)
	balance, err := pantryCountAmount(line.Balance, integers)
	if err != nil {
		return effect, err
	}
	currentBalance, err := pantryCountAmount(String(lot["stock"]), integers)
	if err != nil {
		return effect, err
	}
	if String(lot["product_id"]) != line.ProductID || String(lot["location_id"]) != location || Int(lot["version"]) != line.LotVersion ||
		Int(lot["product_version"]) != line.ProductVersion || Int(lot["location_version"]) != line.LocationVersion ||
		Int(lot["movement_count"]) != line.MovementCount || currentBalance != balance {
		return effect, PantryStockConflict{Message: "Der geprüfte Chargenstand wurde geändert. Die physische Zählung bleibt erhalten; Buchbestand und Zählung ausdrücklich erneut prüfen.", Current: lot}
	}
	effect.Lot, effect.Before = lot, balance
	effect.Count, err = pantryCountAmount(line.Quantity, integers)
	if err != nil {
		return effect, err
	}
	effect.Delta, err = pantryInventoryDelta(balance, effect.Count, integers)
	if err != nil {
		return effect, err
	}
	if effect.Delta != "0.000000" && strings.TrimSpace(line.Reason) == "" {
		return effect, Invalid{"Jede positive oder negative Zähldifferenz ausdrücklich begründen"}
	}
	// Archived records remain countable without silently reactivating them.
	return effect, nil
}
