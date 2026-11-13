package store

import (
	"context"
	"errors"
	"math/big"
	"time"

	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type pantryNotificationStockSample struct {
	product, quantity, unit string
	threshold               *string
	eligible                bool
	at                      time.Time
}

type pantryNotificationStockObservation struct {
	generation                     int64
	activated, observed            time.Time
	threshold                      *string
	unit, state                    string
	episode                        int64
	healthy, detected, consentFrom *time.Time
}

// ObservePantryNotificationStock records bounded, committed physical-stock
// observations. The caller owns the transaction and durable scan cursor, and
// acquires household/module authority before any worker job lock. This method
// creates neither recipients nor notifications. A first low sample is only a
// baseline; a later healthy-to-low observation supplies a conservative episode.
func (s *Store) ObservePantryNotificationStock(
	ctx context.Context, tx pgx.Tx, household string, generation int64, afterProductID string, limit int,
) (lastID string, done bool, err error) {
	if !value.ValidUUID(household) || generation < 1 || generation > notification.MaxRevision ||
		afterProductID != "" && !value.ValidUUID(afterProductID) || limit < 1 || limit > 50 {
		return "", false, Invalid{"Ungültiger Vorrats-Beobachtungsbereich"}
	}
	var activated time.Time
	err = tx.QueryRow(ctx, `SELECT changed_at FROM core.module_states
 WHERE household_id=$1 AND module_id='pantry' AND state='enabled' AND data_generation=$2 FOR SHARE`,
		household, generation).Scan(&activated)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrConflict
	}
	if err != nil {
		return "", false, err
	}
	// statement_timestamp precedes this statement's MVCC snapshot. A clock read
	// after a row-lock wait could incorrectly date an old healthy sample after a
	// shortage or a new consent. Aggregate only the selected product page.
	rows, err := tx.Query(ctx, `WITH candidates AS MATERIALIZED(
 SELECT id,low_stock_threshold,unit_code,archived_at FROM pantry.products
 WHERE household_id=$1 AND(nullif($2,'')::uuid IS NULL OR id>nullif($2,'')::uuid)
 ORDER BY id LIMIT $3)
 SELECT p.id::text,coalesce(stock.quantity,0)::text,p.low_stock_threshold::text,p.unit_code,
 p.archived_at IS NULL AND p.low_stock_threshold IS NOT NULL,statement_timestamp()
 FROM candidates p LEFT JOIN LATERAL(
 SELECT sum(b.quantity) quantity FROM pantry.lots l JOIN pantry.lot_balances b
 ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE l.household_id=$1 AND l.product_id=p.id) stock ON true ORDER BY p.id`,
		household, afterProductID, limit+1)
	if err != nil {
		return "", false, err
	}
	samples := make([]pantryNotificationStockSample, 0, limit+1)
	for rows.Next() {
		var sample pantryNotificationStockSample
		if err = rows.Scan(&sample.product, &sample.quantity, &sample.threshold, &sample.unit, &sample.eligible, &sample.at); err != nil {
			rows.Close()
			return "", false, err
		}
		samples = append(samples, sample)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", false, err
	}
	lastID, done = afterProductID, len(samples) <= limit
	for _, sample := range samples[:min(limit, len(samples))] {
		if err = observePantryNotificationStock(ctx, tx, household, generation, activated, sample); err != nil {
			return "", false, err
		}
		lastID = sample.product
	}
	return lastID, done, nil
}

func pantryNotificationInitialState(sample pantryNotificationStockSample) (string, error) {
	if !sample.eligible {
		return "ineligible", nil
	}
	quantity, ok := new(big.Rat).SetString(sample.quantity)
	if !ok || quantity.Sign() < 0 || sample.threshold == nil {
		return "", ErrConflict
	}
	threshold, ok := new(big.Rat).SetString(*sample.threshold)
	if !ok || threshold.Sign() < 0 {
		return "", ErrConflict
	}
	if quantity.Cmp(threshold) >= 0 {
		return "healthy", nil
	}
	return "baseline", nil
}

func observePantryNotificationStock(
	ctx context.Context, tx pgx.Tx, household string, generation int64, activated time.Time,
	sample pantryNotificationStockSample,
) error {
	initial, err := pantryNotificationInitialState(sample)
	if err != nil {
		return err
	}
	var healthy *time.Time
	if initial == "healthy" {
		healthy = &sample.at
	}
	tag, err := tx.Exec(ctx, `INSERT INTO pantry.notification_stock_observations
 (household_id,product_id,module_generation,module_activated_at,observed_quantity,observed_threshold,
 unit_code,observed_at,last_healthy_at,state)
 VALUES($1,$2,$3,$4,$5::numeric,$6::numeric,$7,$8,$9,$10) ON CONFLICT(household_id,product_id) DO NOTHING`,
		household, sample.product, generation, activated, sample.quantity, sample.threshold, sample.unit, sample.at, healthy, initial)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var previous pantryNotificationStockObservation
	err = tx.QueryRow(ctx, `SELECT module_generation,module_activated_at,observed_threshold::text,unit_code,
 observed_at,last_healthy_at,state,episode,detected_at,consent_cutoff FROM pantry.notification_stock_observations
 WHERE household_id=$1 AND product_id=$2 FOR UPDATE`, household, sample.product).Scan(
		&previous.generation, &previous.activated, &previous.threshold, &previous.unit,
		&previous.observed, &previous.healthy, &previous.state, &previous.episode, &previous.detected, &previous.consentFrom)
	if err != nil {
		return err
	}
	// An older concurrent batch must never replace a newer observation after
	// waiting for its row lock. Its cursor can still advance normally.
	if sample.at.Before(previous.observed) {
		return nil
	}
	state, episode := initial, previous.episode
	var detected, consentFrom *time.Time
	reset := previous.generation != generation || !previous.activated.Equal(activated) ||
		previous.unit != sample.unit || !samePantryNotificationThreshold(previous.threshold, sample.threshold) ||
		previous.state == "ineligible" || initial == "ineligible"
	if !reset && initial == "baseline" {
		switch previous.state {
		case "healthy":
			if previous.healthy == nil || episode >= notification.MaxRevision {
				return ErrConflict
			}
			state, episode = "low", episode+1
			healthy, consentFrom, detected = previous.healthy, previous.healthy, &sample.at
		case "low":
			state = "low"
			healthy, consentFrom, detected = previous.healthy, previous.consentFrom, previous.detected
		}
	}
	_, err = tx.Exec(ctx, `UPDATE pantry.notification_stock_observations SET module_generation=$3,module_activated_at=$4,
 observed_quantity=$5::numeric,observed_threshold=$6::numeric,unit_code=$7,observed_at=$8,last_healthy_at=$9,
 state=$10,episode=$11,detected_at=$12,consent_cutoff=$13 WHERE household_id=$1 AND product_id=$2`,
		household, sample.product, generation, activated, sample.quantity, sample.threshold, sample.unit,
		sample.at, healthy, state, episode, detected, consentFrom)
	return err
}

func samePantryNotificationThreshold(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
