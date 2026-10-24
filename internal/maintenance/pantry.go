package maintenance

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PantryProjection compares every projected lot with the immutable journal.
// Repair requires the operator's database role; the runtime role has SELECT only.
func PantryProjection(ctx context.Context, pool *pgxpool.Pool, repair bool) (int64, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if repair {
		// Writers and purge acquire the household barrier before any lot lock.
		// Follow that order before locking the journal: rebuilding inserts FK
		// references to lots and must not wait on a writer blocked by our lock.
		if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
			return 0, err
		}
		// Lock before reading: concurrent movements finish first or wait until the
		// replacement is committed. The journal itself is never changed.
		if _, err = tx.Exec(ctx, "LOCK TABLE pantry.stock_movements IN SHARE MODE"); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM pantry.lot_balances"); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO pantry.lot_balances(household_id,lot_id,quantity,movement_count,last_recorded_at)
			SELECT household_id,lot_id,sum(quantity_delta),count(*),max(created_at)
			FROM pantry.stock_movements GROUP BY household_id,lot_id`); err != nil {
			return 0, err
		}
	}
	var drift int64
	err = tx.QueryRow(ctx, `WITH journal AS (
		SELECT household_id,lot_id,sum(quantity_delta) quantity,count(*) movement_count,max(created_at) last_recorded_at
		FROM pantry.stock_movements GROUP BY household_id,lot_id)
		SELECT count(*) FROM journal j FULL JOIN pantry.lot_balances b USING(household_id,lot_id)
		WHERE j.quantity IS DISTINCT FROM b.quantity OR j.movement_count IS DISTINCT FROM b.movement_count
		OR j.last_recorded_at IS DISTINCT FROM b.last_recorded_at`).Scan(&drift)
	if err != nil {
		return 0, fmt.Errorf("reconcile stock projection: %w", err)
	}
	return drift, tx.Commit(ctx)
}
