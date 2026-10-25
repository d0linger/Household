package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func lockReservationSource(ctx context.Context, tx pgx.Tx, id auth.Identity, pid, mid string) error {
	var found string
	err := tx.QueryRow(ctx, "SELECT id FROM mealplan.plans WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, pid).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return shoppingContention(err)
	}
	err = tx.QueryRow(ctx, "SELECT id FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id=$3 FOR UPDATE NOWAIT", id.HouseholdID, pid, mid).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return shoppingContention(err)
}

func (s *Store) ReservePlanningStock(ctx context.Context, id auth.Identity, pid, mid, key, requestID string, input ReservationInput) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) || !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Plan-, Mahlzeiten- und Vorgangskennungen erforderlich"}
	}
	in, err := normalizeReservation(input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Plan, Meal string
		Input      ReservationInput
	}{pid, mid, in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "mealplan", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	pantryGeneration, err := requireModule(ctx, tx, id, "pantry")
	if err != nil {
		return nil, err
	}
	if pantryGeneration != in.PantryGeneration {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+id.UserID+"meal-reservation/"+key); err != nil {
		return nil, err
	}
	var priorID string
	var priorHash []byte
	err = tx.QueryRow(ctx, "SELECT id,request_hash FROM core.meal_reservations WHERE household_id=$1 AND created_by=$2 AND request_key=$3", id.HouseholdID, id.UserID, key).Scan(&priorID, &priorHash)
	if err == nil {
		if !bytes.Equal(hash[:], priorHash) {
			return nil, ErrConflict
		}
		// Replay exposes the original identity and its current terminal state;
		// it never writes another lease, even after general receipt pruning.
		return readReservationReceipt(ctx, tx, id, priorID, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err = lockReservationSource(ctx, tx, id, pid, mid); err != nil {
		return nil, err
	}
	source, err := readReservationSource(ctx, tx, id, pid, mid)
	if err != nil {
		return nil, err
	}
	if source["allowed"] != true || source["source_basis"] != in.SourceBasis {
		return nil, PlanningConflict{"Mahlzeit oder Reservierungsgrundlage wurde geändert. Aktuellen Stand erneut prüfen."}
	}
	expiry, _ := time.Parse(time.RFC3339Nano, in.ExpiresAt)
	var validExpiry bool
	if err = tx.QueryRow(ctx, `SELECT $1::timestamptz>clock_timestamp() AND $1::timestamptz<=clock_timestamp()+interval '31 days'
 AND $1::timestamptz<=((m.planned_on+1)::timestamp AT TIME ZONE h.timezone)
 FROM mealplan.meals m JOIN core.households h ON h.id=m.household_id WHERE m.household_id=$2 AND m.id=$3`, expiry, id.HouseholdID, mid).Scan(&validExpiry); err != nil {
		return nil, err
	}
	if !validExpiry {
		return nil, Invalid{"Ablauf muss in der Zukunft, innerhalb von 31 Tagen und spätestens am Ende des geplanten Haushaltstages liegen"}
	}
	for _, a := range in.Allocations {
		lot, lockErr := lockPantryLot(ctx, tx, id, a.LotID)
		if lockErr != nil {
			return nil, lockErr
		}
		coverage, coverageErr := reservationLotCoverage(ctx, tx, id, a.LotID, mid)
		if coverageErr != nil {
			return nil, coverageErr
		}
		if lot["product_archived"] == true || lot["location_archived"] == true || String(lot["product_id"]) != a.ProductID || String(lot["unit"]) != a.Unit ||
			Int(lot["version"]) != a.LotVersion || Int(lot["product_version"]) != a.ProductVersion || Int(lot["location_version"]) != a.LocationVersion ||
			Int(lot["movement_count"]) != a.MovementCount || coverage.Physical != a.Balance || coverage.Other != a.OtherReserved {
			return nil, PlanningConflict{"Charge, Bestand oder Fremdreservierung wurde geändert. Auswahl und Mengen erneut prüfen."}
		}
		quantity, _ := value.Decimal(a.Quantity)
		available, _ := stockDecimal(coverage.UsableForOwn)
		if quantity.Cmp(available) > 0 || lot["integer_quantities"] == true && !quantity.IsInt() {
			return nil, Invalid{"Reservierung überschreitet den freien Anteil oder die ganze Basiseinheit dieser Charge"}
		}
	}
	currentID, err := latestReservation(ctx, tx, id, mid, in.Generation, true)
	if err != nil {
		return nil, err
	}
	if currentID != in.PreviousID {
		return nil, PlanningConflict{"Reservierung wurde inzwischen ersetzt. Aktuellen Stand prüfen."}
	}
	if currentID != "" {
		current, readErr := readReservationReceipt(ctx, tx, id, currentID, false)
		if readErr != nil {
			return nil, readErr
		}
		if Int(current["version"]) != in.PreviousVersion {
			return nil, ErrConflict
		}
		if current["recorded_state"] == "active" {
			if _, err = tx.Exec(ctx, "UPDATE core.meal_reservations SET state='released',release_reason='replaced' WHERE household_id=$1 AND id=$2", id.HouseholdID, currentID); err != nil {
				return nil, shoppingContention(err)
			}
		}
	}
	rid := value.UUID()
	basis, _ := hex.DecodeString(in.SourceBasis)
	if _, err = tx.Exec(ctx, `INSERT INTO core.meal_reservations(id,household_id,mealplan_generation,plan_id,meal_id,pantry_generation,source_basis,
 created_by,request_key,request_hash,allocation_count,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		rid, id.HouseholdID, in.Generation, pid, mid, in.PantryGeneration, basis, id.UserID, key, hash[:], len(in.Allocations), expiry); err != nil {
		return nil, shoppingContention(err)
	}
	for _, a := range in.Allocations {
		if _, err = tx.Exec(ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 VALUES($1,$2,$3,$4,$5,$6::numeric)`, id.HouseholdID, rid, a.ProductID, a.LotID, a.Unit, a.Quantity); err != nil {
			return nil, shoppingContention(err)
		}
	}
	result, err := readReservationReceipt(ctx, tx, id, rid, true)
	if err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "mealplan", "meals", mid, "record_write", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func (s *Store) ReleasePlanningStock(ctx context.Context, id auth.Identity, pid, mid, rid, requestID string, generation, version int64) (Record, error) {
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) || !value.ValidUUID(rid) || version < 1 {
		return nil, Invalid{"Gültige Plan-, Mahlzeiten- und Reservierungsstände erforderlich"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "mealplan", generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = lockReservationSource(ctx, tx, id, pid, mid); err != nil {
		return nil, err
	}
	var currentVersion int64
	var state string
	err = tx.QueryRow(ctx, `SELECT version,state FROM core.meal_reservations WHERE household_id=$1 AND mealplan_generation=$2 AND plan_id=$3 AND meal_id=$4 AND id=$5 FOR UPDATE NOWAIT`, id.HouseholdID, generation, pid, mid, rid).Scan(&currentVersion, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if state == "active" {
		if currentVersion != version {
			return nil, ErrConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE core.meal_reservations SET state='released',release_reason='manual' WHERE household_id=$1 AND id=$2", id.HouseholdID, rid); err != nil {
			return nil, err
		}
	}
	// An exact resource transition is naturally repeatable. A retry addresses
	// this immutable lease identity and cannot release its replacement.
	result, err := readReservationReceipt(ctx, tx, id, rid, true)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
