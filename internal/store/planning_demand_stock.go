package store

import (
	"context"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func planningDemandStock(ctx context.Context, tx pgx.Tx, id auth.Identity, selected []string, out *PlanningDemand, products map[string]*PlanningDemandProduct) error {
	if len(products) == 0 {
		return nil
	}
	ids := make([]string, 0, len(products))
	for pid := range products {
		ids = append(ids, pid)
	}
	rows, err := tx.Query(ctx, `WITH claims AS (
 SELECT a.lot_id,coalesce(sum(a.quantity) FILTER(WHERE r.meal_id=ANY($3::uuid[])),0) own,
 coalesce(sum(a.quantity) FILTER(WHERE NOT(r.meal_id=ANY($3::uuid[]))),0) other
 FROM core.meal_reservation_allocations a JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE a.household_id=$1 AND a.product_id=ANY($2::uuid[]) AND r.mealplan_generation=$4 AND r.pantry_generation=$5
 AND r.state='active' AND r.expires_at>$6 GROUP BY a.lot_id), lots AS (
 SELECT p.id,coalesce(b.quantity,0) physical,coalesce(c.own,0) own,coalesce(c.other,0) other,
 p.archived_at IS NULL AND loc.id IS NOT NULL AND loc.archived_at IS NULL eligible
 FROM pantry.products p LEFT JOIN pantry.lots l ON l.household_id=p.household_id AND l.product_id=p.id
 LEFT JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id LEFT JOIN claims c ON c.lot_id=l.id
 WHERE p.household_id=$1 AND p.id=ANY($2::uuid[]))
 SELECT id,sum(physical)::text,coalesce(sum(physical) FILTER(WHERE eligible),0)::text,sum(own)::text,sum(other)::text,sum(own+other)::text,
 coalesce(sum(greatest(physical-own-other,0)) FILTER(WHERE eligible),0)::text,
 coalesce(sum(greatest(physical-other,0)) FILTER(WHERE eligible),0)::text,sum(greatest(own+other-physical,0))::text
 FROM lots GROUP BY id`, id.HouseholdID, ids, selected, out.Generation, out.PantryGeneration, out.ReadAt)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var pid string
		var fields [8]string
		if err = rows.Scan(&pid, &fields[0], &fields[1], &fields[2], &fields[3], &fields[4], &fields[5], &fields[6], &fields[7]); err != nil {
			return err
		}
		product := products[pid]
		if product == nil {
			return ErrConflict
		}
		product.Physical, product.Eligible, product.Own, product.Other = fields[0], fields[1], fields[2], fields[3]
		product.Reserved, product.Free, product.AvailableForSelection, product.Undercovered = fields[4], fields[5], fields[6], fields[7]
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
