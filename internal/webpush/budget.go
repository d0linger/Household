package webpush

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"household/internal/audit"

	"github.com/jackc/pgx/v5"
)

func counter(ctx context.Context, tx pgx.Tx, scope, subject string, limit int) (time.Time, error) {
	hash := sha256.Sum256([]byte(subject))
	var until time.Time
	err := tx.QueryRow(ctx, `WITH w AS(SELECT to_timestamp(floor(extract(epoch FROM clock_timestamp())/60)*60) AS starts)
 INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end,attempt_count)
 SELECT $1,$2,starts,starts+interval '60 seconds',1 FROM w
 ON CONFLICT(scope,subject_hash,window_start) DO UPDATE SET attempt_count=core.rate_limit_buckets.attempt_count+1,updated_at=clock_timestamp()
 WHERE core.rate_limit_buckets.attempt_count<$3 RETURNING window_end`, scope, hash[:], limit).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Now().Add(time.Minute), ErrLimited
	}
	return until, err
}

func (s *Service) reserveSend(ctx context.Context, tx pgx.Tx, user, host string) (time.Time, error) {
	// Brief sender contention is not a provider cooldown. Wait within the
	// existing bounded send context before reading the actual cooldown.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "webpush-cooldown-v1/"+host); err != nil {
		return time.Time{}, err
	}
	var cooldown time.Time
	subject := sha256.Sum256([]byte("webpush-host-v1/" + host))
	err := tx.QueryRow(ctx, `SELECT window_end FROM core.rate_limit_buckets WHERE scope='webpush-cooldown-v1' AND subject_hash=$1 AND window_start=to_timestamp(0) AND window_end>clock_timestamp()`, subject[:]).Scan(&cooldown)
	if err == nil {
		return cooldown, ErrLimited
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, err
	}
	until, err := counter(ctx, tx, "webpush-send-installation-v1", "household-installation-webpush-v1", 60)
	if err != nil {
		return until, err
	}
	return counter(ctx, tx, "webpush-send-user-v1", user, 20)
}

// Housekeeping removes only old push-owned encrypted rows in bounded batches.
func (s *Service) Housekeeping(ctx context.Context, limit int) error {
	if s == nil || s.pool == nil {
		return nil
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM core.push_subscriptions WHERE id IN(
 SELECT id FROM core.push_subscriptions WHERE ((encryption_format=2 AND encryption_key_id=ANY($1::text[]))
 OR (encryption_format=1 AND encryption_key_id=$3 AND $3<>''))
 AND(created_at<clock_timestamp()-interval '30 days' OR disabled_at IS NOT NULL)
 ORDER BY created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED)`, s.keys.ReadableIDs(), limit, s.keys.LegacyID())
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,household_id::text,user_id::text,endpoint_hash,encrypted_subscription,
 encryption_key_id,encryption_format,disabled_at,created_at FROM core.push_subscriptions WHERE disabled_at IS NULL
 AND octet_length(encrypted_subscription)<=$1 ORDER BY created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, maxCipher, limit)
	if err != nil {
		return err
	}
	candidates := []storedSubscription{}
	for rows.Next() {
		var row storedSubscription
		if err = rows.Scan(&row.id, &row.household, &row.user, &row.hash, &row.cipher, &row.keyID, &row.format, &row.disabled, &row.created); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range candidates {
		data, decodeErr := s.open(row)
		if decodeErr != nil {
			return decodeErr
		}
		current := false
		if data.ExpiresAt.After(time.Now()) && data.VAPIDKeyID == s.vapidID {
			current, err = currentEnvelope(ctx, tx, data)
			if err != nil {
				return err
			}
		}
		if !current {
			if _, err = tx.Exec(ctx, "UPDATE core.push_subscriptions SET disabled_at=clock_timestamp() WHERE id=$1", row.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) cooldown(ctx context.Context, host string, until time.Time) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "webpush-cooldown-v1/"+host); err != nil {
		return err
	}
	subject := sha256.Sum256([]byte("webpush-host-v1/" + host))
	_, err = tx.Exec(ctx, `INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end,attempt_count)
 VALUES('webpush-cooldown-v1',$1,to_timestamp(0),$2,1) ON CONFLICT(scope,subject_hash,window_start)
 DO UPDATE SET window_end=greatest(core.rate_limit_buckets.window_end,excluded.window_end),updated_at=clock_timestamp()`, subject[:], until)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
