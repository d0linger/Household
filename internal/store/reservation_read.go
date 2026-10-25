package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func readReservationSource(ctx context.Context, tx pgx.Tx, id auth.Identity, pid, mid string) (Record, error) {
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('plan_id',p.id,'plan_name',p.name,'meal_id',m.id,'title',m.title,'planned_on',m.planned_on,
 'slot',m.meal_slot,'kind',m.kind,'status',m.status,'generation',ms.data_generation,'pantry_generation',ps.data_generation,'pantry_enabled',ps.state='enabled',
 'source_basis',encode(mealplan.reservation_basis(m.household_id,m.id),'hex'),'timezone',h.timezone,
 'suggested_expiry',least((m.planned_on+1)::timestamp AT TIME ZONE h.timezone,statement_timestamp()+interval '31 days'),
 'allowed',NOT p.is_template AND m.kind IN('recipe','free','leftovers') AND m.status NOT IN('cooked','cancelled')
  AND (m.planned_on+1)::timestamp AT TIME ZONE h.timezone>statement_timestamp() AND ps.state='enabled')
 FROM mealplan.plans p JOIN mealplan.meals m ON m.household_id=p.household_id AND m.plan_id=p.id
 JOIN core.households h ON h.id=m.household_id
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='mealplan' AND ms.state='enabled'
 JOIN core.module_states ps ON ps.household_id=h.id AND ps.module_id='pantry'
 WHERE m.household_id=$1 AND p.id=$2 AND m.id=$3`, id.HouseholdID, pid, mid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func readReservationReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, details bool) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',r.id,'plan_id',r.plan_id,'meal_id',r.meal_id,'generation',r.mealplan_generation,
 'pantry_generation',r.pantry_generation,'version',r.version,'revision',r.revision,'created_at',r.created_at,'expires_at',r.expires_at,'allocation_count',r.allocation_count,
 'state',CASE WHEN r.state='active' AND r.expires_at<=statement_timestamp() THEN 'expired' ELSE r.state END,
 'recorded_state',r.state,'release_reason',r.release_reason,'closed_at',r.closed_at,
 'allocations',CASE WHEN $3 AND EXISTS(SELECT 1 FROM core.module_states ps WHERE ps.household_id=r.household_id AND ps.module_id='pantry' AND ps.state='enabled' AND ps.data_generation=r.pantry_generation) THEN coalesce((SELECT jsonb_agg(x.item ORDER BY x.lot_id) FROM (
 SELECT a.lot_id,jsonb_build_object('lot_id',a.lot_id,'product_id',a.product_id,'quantity',a.quantity::text,'unit',a.unit_code,
 'product_name',p.name,'location',loc.name,'lot_label',left(l.notes,500),'physical',coalesce(b.quantity,0)::text,'reserved',coalesce(cv.quantity,0)::text,
 'free',greatest(coalesce(b.quantity,0)-coalesce(cv.quantity,0),0)::text,'undercovered',greatest(coalesce(cv.quantity,0)-coalesce(b.quantity,0),0)::text) item
 FROM core.meal_reservation_allocations a LEFT JOIN pantry.lots l ON l.household_id=a.household_id AND l.id=a.lot_id
 LEFT JOIN pantry.products p ON p.household_id=a.household_id AND p.id=a.product_id
 LEFT JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 LEFT JOIN LATERAL(SELECT sum(a2.quantity) quantity FROM core.meal_reservation_allocations a2
 JOIN core.meal_reservations r2 ON r2.household_id=a2.household_id AND r2.id=a2.reservation_id
 JOIN core.module_states ms ON ms.household_id=r2.household_id AND ms.module_id='mealplan' AND ms.state='enabled' AND ms.data_generation=r2.mealplan_generation
 JOIN core.module_states ps ON ps.household_id=r2.household_id AND ps.module_id='pantry' AND ps.state='enabled' AND ps.data_generation=r2.pantry_generation
 WHERE a2.household_id=a.household_id AND a2.lot_id=a.lot_id AND r2.state='active' AND r2.expires_at>statement_timestamp()) cv ON true
 WHERE a.household_id=r.household_id AND a.reservation_id=r.id ORDER BY a.lot_id LIMIT 101) x),'[]'::jsonb) ELSE '[]'::jsonb END)
 FROM core.meal_reservations r WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, rid, details).Scan(&raw)
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
	allocations, ok := result["allocations"].([]any)
	if !ok || len(allocations) > reservationAllocationLimit {
		return nil, Invalid{"Reservierungsbeleg überschreitet die zulässige Größe"}
	}
	result["details_available"] = details && int64(len(allocations)) == Int(result["allocation_count"])
	return result, nil
}

func latestReservation(ctx context.Context, tx pgx.Tx, id auth.Identity, mid string, generation int64, lock bool) (string, error) {
	query := "SELECT id FROM core.meal_reservations WHERE household_id=$1 AND mealplan_generation=$2 AND meal_id=$3 ORDER BY revision DESC LIMIT 1"
	if lock {
		query += " FOR UPDATE NOWAIT"
	}
	var rid string
	err := tx.QueryRow(ctx, query, id.HouseholdID, generation, mid).Scan(&rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return rid, shoppingContention(err)
}

func (s *Store) PlanningReservation(ctx context.Context, id auth.Identity, pid, mid string) (Record, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	source, err := readReservationSource(ctx, tx, id, pid, mid)
	if err != nil {
		return nil, err
	}
	rid, err := latestReservation(ctx, tx, id, mid, Int(source["generation"]), false)
	if err != nil {
		return nil, err
	}
	result := Record{"source": source, "reservation": nil}
	if rid != "" {
		result["reservation"], err = readReservationReceipt(ctx, tx, id, rid, source["pantry_enabled"] == true)
		if err != nil {
			return nil, err
		}
	}
	return result, tx.Commit(ctx)
}

func reservationLotCoverage(ctx context.Context, tx pgx.Tx, id auth.Identity, lid, mid string) (ReservationCoverage, error) {
	var physical, own, other string
	err := tx.QueryRow(ctx, `SELECT coalesce((SELECT quantity FROM pantry.lot_balances WHERE household_id=$1 AND lot_id=$2),0)::text,
 coalesce(sum(a.quantity) FILTER(WHERE r.meal_id=$3::uuid),0)::text,coalesce(sum(a.quantity) FILTER(WHERE r.meal_id<>$3::uuid),0)::text
 FROM core.meal_reservation_allocations a JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 JOIN core.module_states m ON m.household_id=r.household_id AND m.module_id='mealplan' AND m.state='enabled' AND m.data_generation=r.mealplan_generation
 JOIN core.module_states p ON p.household_id=r.household_id AND p.module_id='pantry' AND p.state='enabled' AND p.data_generation=r.pantry_generation
 WHERE a.household_id=$1 AND a.lot_id=$2 AND r.state='active' AND r.expires_at>statement_timestamp()`, id.HouseholdID, lid, mid).Scan(&physical, &own, &other)
	if err != nil {
		return ReservationCoverage{}, err
	}
	return reservationCoverage(physical, own, other)
}

func (s *Store) PlanningReservationLot(ctx context.Context, id auth.Identity, pid, mid, lid string) (Record, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	source, err := readReservationSource(ctx, tx, id, pid, mid)
	if err != nil {
		return nil, err
	}
	if source["pantry_enabled"] != true {
		return nil, ErrNotFound
	}
	lot, err := readPantryLot(ctx, tx, id, lid)
	if err != nil {
		return nil, err
	}
	coverage, err := reservationLotCoverage(ctx, tx, id, lid, mid)
	if err != nil {
		return nil, err
	}
	return Record{"lot": lot, "coverage": coverage}, tx.Commit(ctx)
}
