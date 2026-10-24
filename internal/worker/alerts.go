package worker

import (
	"context"
	"errors"
	"fmt"

	"household/internal/audit"

	"github.com/jackc/pgx/v5"
)

func (w *Worker) lowStock(ctx context.Context) error {
	ctx = audit.WithSystem(ctx, "worker")
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT p.household_id,p.id,p.name,coalesce(sum(sm.quantity_delta),0)<p.low_stock_threshold FROM pantry.products p JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='pantry' AND m.state='enabled' LEFT JOIN pantry.lots l ON l.household_id=p.household_id AND l.product_id=p.id LEFT JOIN pantry.stock_movements sm ON sm.household_id=l.household_id AND sm.lot_id=l.id WHERE p.low_stock_threshold IS NOT NULL AND p.archived_at IS NULL GROUP BY p.household_id,p.id,p.name,p.low_stock_threshold LIMIT 10000`)
	if err != nil {
		return err
	}
	type stock struct {
		household, id, name string
		low                 bool
	}
	items := []stock{}
	for rows.Next() {
		var x stock
		if err = rows.Scan(&x.household, &x.id, &x.name, &x.low); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range items {
		var episode int64
		err = tx.QueryRow(ctx, `INSERT INTO core.alert_states(household_id,module_id,resource_id,kind,active,episode) VALUES($1,'pantry',$2,'low_stock',$3,CASE WHEN $3 THEN 1 ELSE 0 END) ON CONFLICT(household_id,module_id,resource_id,kind) DO UPDATE SET active=excluded.active,episode=core.alert_states.episode+CASE WHEN excluded.active THEN 1 ELSE 0 END,changed_at=now() WHERE core.alert_states.active IS DISTINCT FROM excluded.active RETURNING episode`, x.household, x.id, x.low).Scan(&episode)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !x.low {
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key) SELECT m.household_id,m.user_id,'pantry','low_stock',$2,$3,$4||'/'||m.user_id::text FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.inactive_at IS NULL AND u.disabled_at IS NULL ON CONFLICT DO NOTHING`, x.household, "Mindestbestand unterschritten: "+x.name, "/m/pantry/products/"+x.id, fmt.Sprintf("low-stock/%s/%d", x.id, episode))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
