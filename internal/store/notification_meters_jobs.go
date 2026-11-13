package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"household/internal/audit"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

// StageMeterNotifications processes one bounded page in a consistent snapshot.
// Neither an empty page nor a failing household monopolizes the durable cursor.
func (s *Store) StageMeterNotifications(ctx context.Context, limit int) (int, error) {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var household string
	var generation int64
	err := s.Pool.QueryRow(ctx, `SELECT m.household_id::text,m.data_generation FROM core.module_states m
 LEFT JOIN core.jobs j ON j.dedupe_key='meter-scan-v1/'||m.household_id::text||'/'||m.data_generation::text
 WHERE m.module_id='meters' AND m.state='enabled' AND(j.id IS NULL OR(j.kind=$1 AND j.state='pending' AND j.available_at<=clock_timestamp()))
 ORDER BY j.available_at NULLS FIRST,m.household_id LIMIT 1`, MeterNotificationScanJobKind).Scan(&household, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	ctx = audit.WithHousehold(ctx, household)
	key := "meter-scan-v1/" + household + "/" + strconv.FormatInt(generation, 10)
	// Establish the retry/cursor identity before the page transaction so a failed
	// first page can be delayed without inventing progress.
	_, err = audit.Exec(ctx, s.Pool, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
 SELECT $1,'meters',$2,$3,'{"after":""}',$4 WHERE EXISTS(SELECT 1 FROM core.module_states WHERE household_id=$1
 AND module_id='meters' AND state='enabled' AND data_generation=$2) ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, MeterNotificationScanJobKind, key)
	if err != nil {
		return 0, err
	}
	n, err := s.stageMeterNotificationPage(ctx, household, generation, key, max(1, min(limit, 10)))
	if err == nil {
		return n, nil
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	_, retryErr := audit.Exec(cleanup, s.Pool, `UPDATE core.jobs SET available_at=clock_timestamp()+interval '1 minute',last_error='meter notification scan failed'
 WHERE dedupe_key=$1 AND kind=$2 AND state='pending'`, key, MeterNotificationScanJobKind)
	return n, errors.Join(err, retryErr)
}

func (s *Store) stageMeterNotificationPage(ctx context.Context, household string, generation int64, key string, limit int) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return 0, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.module_states WHERE household_id=$1 AND module_id='meters' AND data_generation=$2
 AND state='enabled' FOR SHARE`, household, generation).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var job string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id::text,CASE WHEN octet_length(payload::text)<=128 THEN payload END FROM core.jobs
 WHERE dedupe_key=$1 AND kind=$2 AND payload_version=1 AND state='pending' AND available_at<=clock_timestamp() FOR UPDATE SKIP LOCKED`, key, MeterNotificationScanJobKind).Scan(&job, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cursor struct {
		After string `json:"after"`
	}
	if shoppingDecodeClosed(raw, &cursor) != nil || (cursor.After != "" && !shoppingCanonicalUUID(cursor.After)) {
		return 0, ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM meters.notification_settings WHERE household_id=$1
 AND(nullif($2,'')::uuid IS NULL OR id>nullif($2,'')::uuid) ORDER BY id LIMIT $3`, household, cursor.After, limit+1)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	var sampled time.Time
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&sampled); err != nil {
		return 0, err
	}
	for _, id := range ids[:min(limit, len(ids))] {
		source, e := readMeterNotificationSource(ctx, tx, household, id)
		if e != nil {
			return 0, e
		}
		intents, e := meterReadingIntents(ctx, tx, household, source, sampled)
		if e != nil {
			return 0, e
		}
		for _, kind := range []string{"consumption", "base_load", "adapter"} {
			intent, e := observeMeterNotification(ctx, tx, household, source, kind, sampled)
			if e != nil {
				return 0, e
			}
			if intent != nil {
				intents = append(intents, *intent)
			}
		}
		for _, intent := range intents {
			if err = s.stageMeterNotificationIntent(ctx, tx, intent); err != nil {
				return 0, err
			}
		}
		cursor.After = id
	}
	delay := 0
	if len(ids) <= limit {
		cursor.After = ""
		delay = 30
	}
	raw, err = json.Marshal(cursor)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET payload=$2,available_at=clock_timestamp()+make_interval(secs=>$3),last_error=NULL
 WHERE id=$1`, job, raw, delay)
	if err != nil {
		return 0, err
	}
	return 1, tx.Commit(ctx)
}

func (s *Store) stageMeterNotificationIntent(ctx context.Context, tx pgx.Tx, i notification.Intent) error {
	cutoff, err := s.meterNotificationCutoff(ctx, tx, i)
	if err != nil || cutoff.IsZero() {
		return err
	}
	var consent bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.notification_preferences WHERE household_id=$1 AND user_id=$2
 AND category='meters' AND enabled AND enabled_since<=$3 FOR SHARE`, i.HouseholdID, i.UserID, cutoff).Scan(&consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_intents i WHERE household_id=$1 AND user_id=$2
 AND module_id='meters' AND module_generation=$3 AND resource_type=$4 AND resource_id=$5 AND occurrence=$6
 AND(source_revision=$7 OR state='accepted' OR EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id AND d.state='accepted')))`,
		i.HouseholdID, i.UserID, i.Generation, i.ResourceType, i.ResourceID, i.Occurrence, i.Revision).Scan(&exists)
	if err != nil || exists {
		return err
	}
	_, err = EnqueueNotificationIntent(ctx, tx, i)
	return err
}
