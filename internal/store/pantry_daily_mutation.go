package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) MutatePantryStock(ctx context.Context, id auth.Identity, lid, key, requestID string, in PantryStockInput) (Record, error) {
	if !value.ValidUUID(lid) || !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Chargen- und Vorgangskennung erforderlich"}
	}
	if in.Action != "consume" && in.Action != "undo" {
		return nil, Invalid{"Verbrauch oder Rücknahme auswählen"}
	}
	if len(in.Quantity) > 40 || len(in.Mode) > 10 || len(in.PackID) > 36 || len(in.MovementID) > 36 {
		return nil, Invalid{"Verbrauchsangaben überschreiten die zulässige Länge"}
	}
	if in.LotVersion < 1 || in.ProductVersion < 1 || in.LocationVersion < 1 || in.MovementCount < 0 {
		return nil, Invalid{"Geprüften Chargenstand und Bewegungszähler übermitteln"}
	}
	expected, err := stockDecimal(in.Balance)
	if err != nil || expected.Sign() < 0 {
		return nil, Invalid{"Geprüften Restbestand als exakte Menge übermitteln"}
	}
	in.Balance = expected.FloatString(6)
	if in.Action == "undo" && !value.ValidUUID(in.MovementID) {
		return nil, Invalid{"Ursprünglichen Verbrauch auswählen"}
	}
	if in.Action == "consume" && in.Mode == "pack" && (!value.ValidUUID(in.PackID) || in.PackVersion < 1) {
		return nil, Invalid{"Geprüfte Packung auswählen"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	encoded, err := json.Marshal(struct {
		Lot   string           `json:"lot"`
		Input PantryStockInput `json:"input"`
	}{lid, in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+id.UserID+"pantry-stock"+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,snapshot FROM pantry.stock_operations WHERE household_id=$1 AND created_by=$2 AND request_key=$3", id.HouseholdID, id.UserID, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(hash[:], priorHash) {
			return nil, PantryStockConflict{Message: "Diese Vorgangskennung gehört bereits zu anderen Angaben"}
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	lot, err := lockPantryLot(ctx, tx, id, lid)
	if err != nil {
		return nil, err
	}
	stock, err := stockDecimal(String(lot["stock"]))
	if err != nil {
		return nil, err
	}
	if in.LotVersion != Int(lot["version"]) || in.ProductVersion != Int(lot["product_version"]) || in.LocationVersion != Int(lot["location_version"]) || in.MovementCount != Int(lot["movement_count"]) || expected.Cmp(stock) != 0 {
		return nil, PantryStockConflict{Message: "Charge oder Bestand wurde geändert. Verfügbar: " + String(lot["stock"]) + " " + String(lot["unit"]) + ". Menge und gespeicherten Stand ausdrücklich prüfen.", Current: lot}
	}
	quantity, reason, reverses, pack := "", "consume", "", Record(nil)
	if in.Action == "consume" {
		if lot["product_archived"] == true || lot["location_archived"] == true {
			return nil, Invalid{"Produkt oder Lagerort ist archiviert. Aktive Charge auswählen."}
		}
		pack, err = pantryConsumptionPack(ctx, tx, id, lot, in)
		if err != nil {
			return nil, err
		}
		integers, _ := lot["integer_quantities"].(bool)
		quantity, err = pantryConsumption(in.Mode, in.Quantity, String(lot["stock"]), String(pack["quantity"]), String(pack["unit_code"]), String(lot["unit"]), integers)
		if err != nil {
			return nil, err
		}
	} else {
		quantity, err = pantryUndoQuantity(ctx, tx, id, lid, in.MovementID, lot)
		if err != nil {
			return nil, err
		}
		reason, reverses = "reversal", in.MovementID
	}
	delta := quantity
	if in.Action == "consume" {
		delta = "-" + quantity
	}
	var movement string
	err = tx.QueryRow(ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,reverses_movement_id,note,created_by)
 VALUES($1,$2,$3::numeric,$4,nullif($5,'')::uuid,$6,$7) RETURNING id`, id.HouseholdID, lid, delta, reason, reverses, map[string]string{"consume": "Geprüfter Schnellverbrauch", "undo": "Rücknahme des geprüften Verbrauchs"}[in.Action], id.UserID).Scan(&movement)
	if err != nil {
		return nil, shoppingContention(err)
	}
	after, err := readPantryLot(ctx, tx, id, lid)
	if err != nil {
		return nil, err
	}
	var recorded time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&recorded); err != nil {
		return nil, err
	}
	oid := value.UUID()
	result := Record{"id": oid, "kind": in.Action, "lot_id": lid, "movement_id": movement, "reverses_movement_id": reverses,
		"quantity": quantity, "unit": lot["unit"], "product_name": lot["product_name"], "location": lot["location"], "lot_label": lot["label"],
		"stock_after": after["stock"], "movement_count": after["movement_count"], "recorded_at": recorded.UTC().Format(time.RFC3339Nano), "pack": pack}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pantry.stock_operations(id,household_id,lot_id,movement_id,kind,request_key,request_hash,snapshot,created_by,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, oid, id.HouseholdID, lid, movement, in.Action, key, hash[:], snapshot, id.UserID, recorded); err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT to_jsonb(m) FROM pantry.stock_movements m WHERE household_id=$1 AND id=$2", id.HouseholdID, movement).Scan(&raw); err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	m, _ := s.Module("pantry")
	e, _ := m.Entity("stock_movements")
	if err = s.afterWrite(ctx, tx, id, m, e, record, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "pantry", "stock_movements", movement, "record_write", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func pantryConsumptionPack(ctx context.Context, tx pgx.Tx, id auth.Identity, lot Record, in PantryStockInput) (Record, error) {
	if in.Mode != "pack" {
		return nil, nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(p) FROM pantry.packs p WHERE household_id=$1 AND product_id=$2 AND id=$3 FOR SHARE NOWAIT", id.HouseholdID, lot["product_id"], in.PackID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Invalid{"Packung gehört nicht zu diesem Produkt oder wurde entfernt"}
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	pack, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(pack["version"]) != in.PackVersion || pack["disabled"] == true {
		return nil, PantryStockConflict{Message: "Packung wurde geändert oder deaktiviert. Aktuellen Inhalt erneut prüfen.", Current: lot}
	}
	// Store exact quantities as strings in the durable receipt as well.
	pack["quantity"] = String(pack["quantity"])
	return pack, nil
}

func pantryUndoQuantity(ctx context.Context, tx pgx.Tx, id auth.Identity, lid, movement string, lot Record) (string, error) {
	var delta, reason string
	err := tx.QueryRow(ctx, "SELECT quantity_delta::text,reason FROM pantry.stock_movements WHERE household_id=$1 AND lot_id=$2 AND id=$3", id.HouseholdID, lid, movement).Scan(&delta, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	original, err := value.Decimal(delta)
	if err != nil || reason != "consume" || original.Sign() >= 0 {
		return "", Invalid{"Hier kann ausschließlich ein ursprünglicher Verbrauch rückgängig gemacht werden"}
	}
	var prior string
	err = tx.QueryRow(ctx, "SELECT id FROM pantry.stock_movements WHERE household_id=$1 AND reverses_movement_id=$2", id.HouseholdID, movement).Scan(&prior)
	if err == nil {
		lot["existing_reversal_id"] = prior
		return "", PantryStockConflict{Message: "Dieser Verbrauch wurde bereits rückgängig gemacht. Gespeicherte Gegenbuchung prüfen.", Current: lot}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return new(big.Rat).Neg(original).FloatString(6), nil
}
