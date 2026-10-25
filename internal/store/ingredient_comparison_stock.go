package store

import (
	"context"
	"math/big"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func comparisonStock(ctx context.Context, tx pgx.Tx, id auth.Identity, groups map[string]*IngredientComparisonProduct) error {
	ids := make([]string, 0, len(groups))
	for pid := range groups {
		ids = append(ids, pid)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT p.id,coalesce(sum(b.quantity),0)::text,count(*) FILTER(WHERE b.quantity>0),
 coalesce((SELECT jsonb_agg(x.item ORDER BY x.priority ASC NULLS LAST,x.id) FROM (
  SELECT l.id,least(l.use_by_on,l.opened_on+l.after_open_days,l.best_before_on) priority,
   jsonb_build_object('id',l.id,'location',loc.name,'stock',b.quantity::text,
   'best_before_on',l.best_before_on,'use_by_on',l.use_by_on,'opened_on',l.opened_on,'after_open_days',l.after_open_days) item
  FROM pantry.lots l JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id AND loc.archived_at IS NULL
  JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id AND b.quantity>0
  WHERE l.household_id=p.household_id AND l.product_id=p.id
  ORDER BY priority ASC NULLS LAST,l.id LIMIT 3) x),'[]'::jsonb)
 FROM pantry.products p LEFT JOIN pantry.lots l ON l.household_id=p.household_id AND l.product_id=p.id
 LEFT JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id AND loc.archived_at IS NULL
 LEFT JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id AND loc.id IS NOT NULL
 WHERE p.household_id=$1 AND p.id=ANY($2::uuid[]) GROUP BY p.household_id,p.id`, id.HouseholdID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, stockRaw string
		var count int64
		var sampleRaw []byte
		if err = rows.Scan(&pid, &stockRaw, &count, &sampleRaw); err != nil {
			return err
		}
		group := groups[pid]
		stock, parseErr := stockDecimal(stockRaw)
		if parseErr != nil {
			return parseErr
		}
		group.Demand, err = ingredientAmount(group.quantity)
		if err != nil {
			return err
		}
		group.Stock, err = ingredientAmount(stock)
		if err != nil {
			return err
		}
		group.LotCount = count
		parsed, decodeErr := decode([]byte(`{"rows":` + string(sampleRaw) + `}`))
		if decodeErr != nil {
			return decodeErr
		}
		for _, raw := range parsed["rows"].([]any) {
			lot := Record(raw.(map[string]any))
			if err = pantryDates(lot); err != nil {
				return err
			}
			group.LotSample = append(group.LotSample, lot)
		}
		if group.Complete {
			shortage := new(big.Rat).Sub(group.quantity, stock)
			if shortage.Sign() < 0 {
				shortage.SetInt64(0)
			}
			amount, amountErr := ingredientAmount(shortage)
			if amountErr != nil {
				return amountErr
			}
			group.Shortage = &amount
		}
	}
	return rows.Err()
}
