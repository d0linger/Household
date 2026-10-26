package store

import (
	"context"
	"math/big"
	"sort"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// The caller owns one read-only repeatable-read transaction and derives readAt
// from that transaction. All current claims are foreign for a new alternative;
// no nullable selected-meal array is involved in this reader.
func readSuggestionLots(ctx context.Context, tx pgx.Tx, id auth.Identity, products map[string]string, mealplanGeneration, pantryGeneration int64, readAt time.Time) ([]suggestionDateLot, error) {
	result := []suggestionDateLot{}
	if len(products) == 0 {
		return result, nil
	}
	if len(products) > planningIngredientLimit || mealplanGeneration <= 0 || pantryGeneration <= 0 || readAt.IsZero() {
		return nil, Invalid{"Begrenzte aktuelle Produktzuordnungen und gemeinsamer Lesezeitpunkt erforderlich"}
	}
	ids := make([]string, 0, len(products))
	for pid, unit := range products {
		if !value.ValidUUID(pid) {
			return nil, Invalid{"Gültige bestätigte Produktkennung erforderlich"}
		}
		if _, err := value.Convert(new(big.Rat), unit, unit); err != nil {
			return nil, Invalid{"Gültige Produktbasiseinheit erforderlich"}
		}
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	if err := checkSuggestionProducts(ctx, tx, id, ids, products); err != nil {
		return nil, err
	}
	var dependencies int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (
 SELECT 1 FROM core.meal_reservation_allocations a JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND a.product_id=ANY($2::uuid[]) AND r.mealplan_generation=$3 AND r.pantry_generation=$4
 AND r.state='active' AND r.expires_at>$5 LIMIT 5001) bounded`, id.HouseholdID, ids, mealplanGeneration, pantryGeneration, readAt).Scan(&dependencies); err != nil {
		return nil, err
	}
	if dependencies > suggestionLotLimit {
		return nil, Invalid{"Mehr als5000 aktive Reservierungspositionen; Rezeptauswahl eingrenzen"}
	}
	rows, err := tx.Query(ctx, `WITH claims AS (
 SELECT a.lot_id,sum(a.quantity) quantity FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND a.product_id=ANY($2::uuid[]) AND r.mealplan_generation=$3 AND r.pantry_generation=$4
 AND r.state='active' AND r.expires_at>$5 GROUP BY a.lot_id)
 SELECT l.id,l.product_id,greatest(b.quantity-coalesce(c.quantity,0),0)::text,
 coalesce(l.best_before_on::text,''),coalesce(l.use_by_on::text,''),coalesce(l.opened_on::text,''),l.after_open_days
 FROM pantry.lots l JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id AND p.archived_at IS NULL
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id AND loc.archived_at IS NULL
 JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 LEFT JOIN claims c ON c.lot_id=l.id WHERE l.household_id=$1 AND l.product_id=ANY($2::uuid[]) AND b.quantity>0
 ORDER BY l.product_id,l.id LIMIT 5001`, id.HouseholdID, ids, mealplanGeneration, pantryGeneration, readAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var lot suggestionDateLot
		if err = rows.Scan(&lot.ID, &lot.ProductID, &lot.Free, &lot.BestBefore, &lot.UseBy, &lot.OpenedOn, &lot.AfterOpenDays); err != nil {
			return nil, err
		}
		result = append(result, lot)
		if len(result) > suggestionLotLimit {
			return nil, Invalid{"Mehr als5000 positive Chargen in der Auswahl; Produkte oder Rezepte eingrenzen"}
		}
	}
	return result, rows.Err()
}

func checkSuggestionProducts(ctx context.Context, tx pgx.Tx, id auth.Identity, ids []string, products map[string]string) error {
	rows, err := tx.Query(ctx, "SELECT id,unit_code,archived_at IS NULL FROM pantry.products WHERE household_id=$1 AND id=ANY($2::uuid[])", id.HouseholdID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var pid, unit string
		var active bool
		if err = rows.Scan(&pid, &unit, &active); err != nil {
			return err
		}
		if !active || products[pid] != unit {
			return ErrConflict
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != len(products) {
		return ErrConflict
	}
	return nil
}
