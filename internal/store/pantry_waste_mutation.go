package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const pantryWasteNamespace = "pantry-waste-v1"

func normalizePantryWasteInput(in PantryWasteInput) (PantryWasteInput, error) {
	if in.Generation <= 0 || in.LotVersion <= 0 || in.ProductVersion <= 0 || in.LocationVersion <= 0 || in.MovementCount < 0 || in.MovementCount == 1<<63-1 || len(in.Balance) > 40 || len(in.Quantity) > 40 {
		return in, Invalid{"Aktuellen Chargenstand und exakte Menge erneut prüfen"}
	}
	balance, err := stockDecimal(in.Balance)
	if err != nil || balance.Sign() < 0 {
		return in, Invalid{"Aktuellen Restbestand erneut laden"}
	}
	in.Balance = balance.FloatString(6)
	switch in.Action {
	case "waste":
		if pantryWasteReasons[in.Reason] == "" || in.Reason == "unknown" || in.MovementID != "" {
			return in, Invalid{"Verlustgrund ausdrücklich auswählen"}
		}
		quantity, err := value.Decimal(in.Quantity)
		if err != nil {
			return in, Invalid{"Positive Verlustmenge mit höchstens sechs Nachkommastellen eingeben"}
		}
		in.Quantity, err = pantryExactAmount(quantity, false)
		if err != nil {
			return in, err
		}
	case "undo":
		if !value.ValidUUID(in.MovementID) || in.Quantity != "" || in.Reason != "" {
			return in, Invalid{"Ursprüngliche Verlustbuchung ohne neue Menge oder Grund auswählen"}
		}
		in.MovementID = strings.ToLower(in.MovementID)
	default:
		return in, Invalid{"Verlust oder ausdrückliche Rücknahme auswählen"}
	}
	return in, nil
}

func (s *Store) MutatePantryWaste(ctx context.Context, id auth.Identity, lotID, key, requestID string, in PantryWasteInput) (Record, error) {
	if !value.ValidUUID(lotID) || !value.ValidUUID(key) {
		return nil, Invalid{"Charge und Vorgangskennung erneut prüfen"}
	}
	lotID, key = strings.ToLower(lotID), strings.ToLower(key)
	in, err := normalizePantryWasteInput(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", pantryWasteNamespace+"/"+id.HouseholdID+"/"+id.UserID+"/"+key).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, ErrConflict
	}
	body, err := json.Marshal(struct {
		Namespace string           `json:"namespace"`
		Lot       string           `json:"lot"`
		Input     PantryWasteInput `json:"input"`
	}{pantryWasteNamespace, lotID, in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	operation := pantryWasteNamespace + "/lots/" + lotID
	prior, exists, err := replayPantryWaste(ctx, tx, id, lotID, operation, key, in.Generation, hash[:])
	if err != nil || exists {
		return prior, shoppingContention(err)
	}
	lot, err := lockPantryLot(ctx, tx, id, lotID)
	if err != nil {
		return nil, err
	}
	if in.LotVersion != Int(lot["version"]) || in.ProductVersion != Int(lot["product_version"]) || in.LocationVersion != Int(lot["location_version"]) || in.MovementCount != Int(lot["movement_count"]) || in.Balance != String(lot["stock"]) {
		return nil, PantryStockConflict{Message: "Charge oder Bestand wurde geändert. Menge und Grund mit dem aktuellen Stand ausdrücklich neu prüfen.", Current: lot}
	}
	quantity, reason, source, code, err := pantryWasteEffect(ctx, tx, id, lotID, lot, in)
	if err != nil {
		return nil, err
	}
	stock, err := stockDecimal(String(lot["stock"]))
	if err != nil {
		return nil, err
	}
	amount, err := value.Decimal(quantity)
	if err != nil || amount.Sign() <= 0 {
		return nil, Invalid{"Gespeicherte Verlustmenge erneut prüfen"}
	}
	delta := new(big.Rat).Set(amount)
	if reason == "waste" {
		delta.Neg(delta)
	}
	after := new(big.Rat).Add(stock, delta)
	checkedAfter, err := stockDecimal(after.FloatString(6))
	if err != nil || checkedAfter.Sign() < 0 || checkedAfter.Cmp(after) != 0 || len(strings.SplitN(after.FloatString(6), ".", 2)[0]) > 24 {
		return nil, Invalid{"Buchung überschreitet die exakten Bestandsgrenzen"}
	}
	var recorded time.Time
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&recorded); err != nil {
		return nil, err
	}
	movement := value.UUID()
	result := Record{"namespace": pantryWasteNamespace, "movement_id": movement, "lot_id": lotID, "kind": in.Action, "generation": in.Generation,
		"source_movement_id": source, "quantity": quantity, "unit": lot["unit"], "reason": code, "stock_after": after.FloatString(6),
		"movement_count": in.MovementCount + 1, "product_name": lot["product_name"], "location": lot["location"], "recorded_at": recorded.UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	resultHash := sha256.Sum256(encoded)
	var storedID, storedDelta, storedReason, storedSource, storedCode, storedKey, storedCreator, storedHousehold, storedLot string
	var storedGeneration int64
	var storedAt time.Time
	var storedRequest, storedResult []byte
	err = tx.QueryRow(ctx, `INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,reverses_movement_id,
 waste_reason,waste_operation_key,waste_generation,waste_request_hash,waste_result_hash,note,created_by,created_at)
 VALUES($1,$2,$3,$4::numeric,$5,nullif($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13,$14)
 RETURNING id,quantity_delta::text,reason,created_at,waste_request_hash,waste_result_hash,
 coalesce(reverses_movement_id::text,''),coalesce(waste_reason,'unknown'),waste_operation_key,waste_generation,created_by,household_id,lot_id`, movement, id.HouseholdID, lotID, delta.FloatString(6), reason, source,
		optionalWasteReason(reason, code), key, in.Generation, hash[:], resultHash[:], map[string]string{"waste": "Geprüfter Verlust", "reversal": "Rücknahme des geprüften Verlusts"}[reason], id.UserID, recorded).
		Scan(&storedID, &storedDelta, &storedReason, &storedAt, &storedRequest, &storedResult, &storedSource, &storedCode, &storedKey, &storedGeneration, &storedCreator, &storedHousehold, &storedLot)
	if err != nil {
		return nil, pantryWasteMutationError(err, lot)
	}
	actual, err := readPantryLot(ctx, tx, id, lotID)
	if err != nil {
		return nil, err
	}
	if storedID != movement || storedDelta != delta.FloatString(6) || storedReason != reason || !storedAt.Equal(recorded) || !bytes.Equal(storedRequest, hash[:]) || !bytes.Equal(storedResult, resultHash[:]) || String(actual["stock"]) != String(result["stock_after"]) || Int(actual["movement_count"]) != in.MovementCount+1 {
		return nil, ErrConflict
	}
	expectedCode := code
	if reason == "reversal" {
		expectedCode = "unknown"
	}
	if storedSource != source || storedCode != expectedCode || storedKey != key || storedGeneration != in.Generation || storedCreator != id.UserID || storedHousehold != id.HouseholdID || storedLot != lotID {
		return nil, ErrConflict
	}
	for _, field := range []string{"id", "version", "product_id", "product_version", "product_name", "unit", "integer_quantities", "product_archived", "location_id", "location_version", "location", "location_archived"} {
		if String(actual[field]) != String(lot[field]) {
			return nil, ErrConflict
		}
	}
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT "+pantryMovementRecordJSON("pantry", "stock_movements")+" FROM pantry.stock_movements t WHERE household_id=$1 AND id=$2", id.HouseholdID, movement).Scan(&raw); err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	module, _ := s.Module("pantry")
	entity, _ := module.Entity("stock_movements")
	if err = s.afterWrite(ctx, tx, id, module, entity, record, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "pantry", "stock_movements", movement, "record_write", requestID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'pantry',$6,200,$7)`, id.HouseholdID, id.UserID, operation, key, hash[:], in.Generation, encoded); err != nil {
		return nil, err
	}
	return result, pantryWasteMutationError(tx.Commit(ctx), lot)
}

func optionalWasteReason(kind, code string) any {
	if kind != "waste" || code == "unknown" {
		return nil
	}
	return code
}

func pantryWasteMutationError(err error, lot Record) error {
	var failure *pgconn.PgError
	if errors.As(err, &failure) {
		if failure.Code == "23514" {
			return PantryStockConflict{Message: "Buchung verletzt eine Bestands- oder Reservierungsgrenze. Aktuellen Bestand und Reservierungen ausdrücklich prüfen.", Current: lot}
		}
		if failure.Code == "23505" {
			return ErrConflict
		}
	}
	return shoppingContention(err)
}

func pantryWasteEffect(ctx context.Context, tx pgx.Tx, id auth.Identity, lotID string, lot Record, in PantryWasteInput) (string, string, string, string, error) {
	if in.Action == "waste" {
		if lot["product_archived"] == true || lot["location_archived"] == true {
			return "", "", "", "", Invalid{"Produkt oder Lagerort ist archiviert; aktive Charge auswählen"}
		}
		integers, _ := lot["integer_quantities"].(bool)
		quantity, err := pantryConsumption("amount", in.Quantity, String(lot["stock"]), "", "", String(lot["unit"]), integers)
		return quantity, "waste", "", in.Reason, err
	}
	var delta, code string
	var reversed bool
	err := tx.QueryRow(ctx, `SELECT m.quantity_delta::text,coalesce(m.waste_reason,'unknown'),EXISTS(
 SELECT 1 FROM pantry.stock_movements r WHERE r.household_id=m.household_id AND r.reverses_movement_id=m.id)
 FROM pantry.stock_movements m WHERE m.household_id=$1 AND m.lot_id=$2 AND m.id=$3 AND m.reason='waste'`, id.HouseholdID, lotID, in.MovementID).Scan(&delta, &code, &reversed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", ErrNotFound
	}
	if err != nil {
		return "", "", "", "", err
	}
	if reversed {
		return "", "", "", "", PantryStockConflict{Message: "Diese Verlustbuchung wurde bereits zurückgenommen. Gespeicherte Gegenbuchung prüfen.", Current: lot}
	}
	original, err := value.Decimal(delta)
	if err != nil || original.Sign() >= 0 || pantryWasteReasons[code] == "" {
		return "", "", "", "", Invalid{"Ursprüngliche Verlustbuchung ist nicht eindeutig auswertbar"}
	}
	return original.Neg(original).FloatString(6), "reversal", in.MovementID, code, nil
}

func replayPantryWaste(ctx context.Context, tx pgx.Tx, id auth.Identity, lotID, operation, key string, generation int64, requestHash []byte) (Record, bool, error) {
	var movement, storedLot, reason string
	var proofRequest, proofResult []byte
	err := tx.QueryRow(ctx, `SELECT id,lot_id,reason,waste_request_hash,waste_result_hash FROM pantry.stock_movements
 WHERE household_id=$1 AND created_by=$2 AND waste_generation=$3 AND waste_operation_key=$4`, id.HouseholdID, id.UserID, generation, key).
		Scan(&movement, &storedLot, &reason, &proofRequest, &proofResult)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	proved := err == nil
	if proved && (!bytes.Equal(proofRequest, requestHash) || storedLot != lotID) {
		return nil, true, ErrConflict
	}
	var cacheHash []byte
	var size int
	var validCache bool
	// Lock the cache row before the size gate so pruning or replacement cannot
	// substitute a larger or different payload between the bounded reads.
	err = tx.QueryRow(ctx, `SELECT request_hash,octet_length(response_payload::text),response_status=200 AND expires_at>transaction_timestamp()
 FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4
 AND module_id='pantry' AND module_generation=$5 FOR SHARE NOWAIT`, id.HouseholdID, id.UserID, operation, key, generation).Scan(&cacheHash, &size, &validCache)
	if !proved {
		if err == nil {
			return nil, true, ErrNotFound
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!validCache || size <= 0 || size > 65536 || len(proofResult) != 32 || !bytes.Equal(cacheHash, proofRequest) || reason != "waste" && reason != "reversal") {
		return nil, true, ErrNotFound
	}
	if err != nil {
		return nil, true, err
	}
	var payload []byte
	if err = tx.QueryRow(ctx, `SELECT response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4 AND module_id='pantry' AND module_generation=$5`, id.HouseholdID, id.UserID, operation, key, generation).Scan(&payload); err != nil {
		return nil, true, err
	}
	result, err := decode(payload)
	if err != nil || String(result["namespace"]) != pantryWasteNamespace || String(result["movement_id"]) != movement || String(result["lot_id"]) != lotID || Int(result["generation"]) != generation {
		return nil, true, ErrNotFound
	}
	// JSONB changes whitespace and key order. Re-encode the number-preserving
	// DTO exactly as the original response before comparing its durable digest.
	canonical, err := json.Marshal(result)
	if err != nil {
		return nil, true, ErrNotFound
	}
	hash := sha256.Sum256(canonical)
	if !bytes.Equal(hash[:], proofResult) {
		return nil, true, ErrNotFound
	}
	return result, true, nil
}
