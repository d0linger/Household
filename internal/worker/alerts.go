package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"household/internal/audit"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const pantryLowStockScanJobKind = "pantry_low_stock_scan_v1"

// This cursor preserves the existing in-app alerts independently of the opt-in
// device channel. Each tick observes one bounded page; available_at rotates
// households, while the UUID cursor guarantees progress beyond the first page.
func (w *Worker) lowStock(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 10*time.Second)
	defer cancel()
	job, err := w.lowStockPage(ctx)
	if err == nil || job == "" {
		return err
	}
	// A malformed cursor or unavailable product must not monopolize every tick.
	// lowStockPage has rolled back before this short, separate retry update.
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	_, retryErr := audit.Exec(cleanup, w.Pool, `UPDATE core.jobs SET available_at=clock_timestamp()+interval '1 minute',
 last_error='pantry stock scan failed' WHERE id=$1 AND kind=$2 AND state='pending'`, job, pantryLowStockScanJobKind)
	return errors.Join(err, retryErr)
}

func (w *Worker) lowStockPage(ctx context.Context) (job string, result error) {
	tx, err := audit.Begin(ctx, w.Pool)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var household string
	var generation int64
	// Lock authority before the cursor. A new household has no cursor and is
	// visited first; completed pages move behind the other ready households.
	err = tx.QueryRow(ctx, `SELECT h.id::text,m.data_generation FROM core.households h
 JOIN core.module_states m ON m.household_id=h.id AND m.module_id='pantry' AND m.state='enabled'
 LEFT JOIN core.jobs j ON j.dedupe_key='pantry-low-stock-scan-v1/'||h.id::text||'/'||m.data_generation::text
 WHERE j.id IS NULL OR(j.kind=$1 AND j.state='pending' AND j.available_at<=clock_timestamp())
 ORDER BY j.available_at NULLS FIRST,h.id LIMIT 1 FOR SHARE OF h,m SKIP LOCKED`, pantryLowStockScanJobKind).Scan(&household, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("pantry-low-stock-scan-v1/%s/%d", household, generation)
	if _, err = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,dedupe_key,payload)
 VALUES($1,'pantry',$2,$3,$4,'{"after":""}') ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, pantryLowStockScanJobKind, key); err != nil {
		return "", err
	}
	var raw []byte
	var payloadVersion int
	err = tx.QueryRow(ctx, `SELECT id::text,payload,payload_version FROM core.jobs WHERE dedupe_key=$1 AND household_id=$2
 AND module_id='pantry' AND module_generation=$3 AND kind=$4 AND state='pending'
 AND available_at<=clock_timestamp() FOR UPDATE SKIP LOCKED`, key, household, generation, pantryLowStockScanJobKind).Scan(&job, &raw, &payloadVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return job, err
	}
	// JSONB has canonical unique keys; reject altered or oversized cursor data.
	var cursor map[string]json.RawMessage
	if payloadVersion != 1 || len(raw) > 128 || json.Unmarshal(raw, &cursor) != nil || len(cursor) != 1 {
		return job, errors.New("invalid pantry stock cursor")
	}
	var after string
	field := cursor["after"]
	if len(field) == 0 || field[0] != '"' || json.Unmarshal(field, &after) != nil || after != "" && !value.ValidUUID(after) {
		return job, errors.New("invalid pantry stock cursor")
	}
	after, err = lowStockProducts(ctx, tx, household, after)
	if err != nil {
		return job, err
	}
	if _, err = tx.Exec(ctx, `UPDATE core.jobs SET payload=jsonb_build_object('after',$2::text),
 available_at=clock_timestamp(),last_error=NULL WHERE id=$1`, job, after); err != nil {
		return job, err
	}
	return job, tx.Commit(ctx)
}

func lowStockProducts(ctx context.Context, tx pgx.Tx, household, after string) (string, error) {
	rows, err := tx.Query(ctx, `WITH products AS MATERIALIZED(
 SELECT id,name,low_stock_threshold,archived_at FROM pantry.products
 WHERE household_id=$1 AND(nullif($2,'')::uuid IS NULL OR id>nullif($2,'')::uuid) ORDER BY id LIMIT 51)
 SELECT p.id::text,p.name,coalesce(p.archived_at IS NULL AND p.low_stock_threshold IS NOT NULL
 AND coalesce(stock.quantity,0)<p.low_stock_threshold,false)
 FROM products p LEFT JOIN LATERAL(SELECT sum(b.quantity) quantity FROM pantry.lots l
 JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE l.household_id=$1 AND l.product_id=p.id) stock ON true ORDER BY p.id`, household, after)
	if err != nil {
		return "", err
	}
	type stock struct {
		id, name string
		low      bool
	}
	items := make([]stock, 0, 51)
	for rows.Next() {
		var item stock
		if err = rows.Scan(&item.id, &item.name, &item.low); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, item := range items[:min(50, len(items))] {
		var episode int64
		err = tx.QueryRow(ctx, `INSERT INTO core.alert_states(household_id,module_id,resource_id,kind,active,episode)
 VALUES($1,'pantry',$2,'low_stock',$3,CASE WHEN $3 THEN 1 ELSE 0 END)
 ON CONFLICT(household_id,module_id,resource_id,kind) DO UPDATE SET active=excluded.active,
 episode=core.alert_states.episode+CASE WHEN excluded.active THEN 1 ELSE 0 END,changed_at=clock_timestamp()
 WHERE core.alert_states.active IS DISTINCT FROM excluded.active RETURNING episode`, household, item.id, item.low).Scan(&episode)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !item.low {
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 SELECT m.household_id,m.user_id,'pantry','low_stock',$2,$3,$4||'/'||m.user_id::text
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.inactive_at IS NULL AND u.disabled_at IS NULL ON CONFLICT DO NOTHING`,
			household, "Mindestbestand unterschritten: "+item.name, "/m/pantry/products/"+item.id,
			fmt.Sprintf("low-stock/%s/%d", item.id, episode))
		if err != nil {
			return "", err
		}
	}
	if len(items) > 50 {
		return items[49].id, nil
	}
	return "", nil
}
