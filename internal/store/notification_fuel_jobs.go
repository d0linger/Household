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

type fuelNotificationCursor struct {
	Schema int    `json:"schema"`
	Phase  string `json:"phase"`
	After  string `json:"after"`
}

// StageFuelNotifications advances one bounded durable page, committing its
// observations, intents and cursor together. Jobs are consumed only here.
func (s *Store) StageFuelNotifications(ctx context.Context, limit int) (int, error) {
	limit = max(1, min(limit, 50))
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var household string
	var generation int64
	err := s.Pool.QueryRow(ctx, `SELECT m.household_id::text,m.data_generation FROM core.module_states m
 LEFT JOIN core.jobs j ON j.dedupe_key='fuel-scan-v1/'||m.household_id::text||'/'||m.data_generation::text
 WHERE m.module_id='fuel' AND m.state='enabled'
 AND(j.id IS NULL OR(j.kind=$1 AND j.state='pending' AND j.available_at<=clock_timestamp()))
 ORDER BY j.available_at NULLS FIRST,m.household_id LIMIT 1`, FuelNotificationScanJobKind).Scan(&household, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	tx, err := audit.Begin(audit.WithHousehold(ctx, household), s.Pool)
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return 0, err
	}
	var activated time.Time
	err = tx.QueryRow(ctx, `SELECT changed_at FROM core.module_states WHERE household_id=$1 AND module_id='fuel'
 AND state='enabled' AND data_generation=$2 FOR SHARE`, household, generation).Scan(&activated)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	key := "fuel-scan-v1/" + household + "/" + strconv.FormatInt(generation, 10)
	_, err = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
 VALUES($1,'fuel',$2,$3,'{"schema":1,"phase":"vehicles","after":""}',$4) ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, FuelNotificationScanJobKind, key)
	if err != nil {
		return 0, err
	}
	var job string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id::text,CASE WHEN octet_length(payload::text)<=512 THEN payload END FROM core.jobs
 WHERE dedupe_key=$1 AND state='pending' AND available_at<=clock_timestamp() FOR UPDATE SKIP LOCKED`, key).Scan(&job, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cursor fuelNotificationCursor
	if shoppingDecodeClosed(raw, &cursor) != nil || cursor.Schema != 1 || (cursor.Phase != "vehicles" && cursor.Phase != "maintenance") ||
		(cursor.After != "" && !shoppingCanonicalUUID(cursor.After)) {
		return 0, ErrConflict
	}
	entity := "vehicles"
	if cursor.Phase == "maintenance" {
		entity = "maintenance_plans"
	}
	// The identifier comes solely from the closed phase above.
	rows, err := tx.Query(ctx, `SELECT id::text FROM `+pgx.Identifier{"fuel", entity}.Sanitize()+`
 WHERE household_id=$1 AND(nullif($2,'')::uuid IS NULL OR id>nullif($2,'')::uuid) ORDER BY id LIMIT $3`, household, cursor.After, limit+1)
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
	for _, id := range ids[:min(limit, len(ids))] {
		// Capture before source reads: a later MVCC snapshot cannot backdate new
		// consent into an already-observed healthy interval.
		var sampled time.Time
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&sampled); err != nil {
			return 0, err
		}
		source, readErr := readFuelNotificationSource(ctx, tx, household, entity, id)
		if errors.Is(readErr, ErrNotFound) {
			cursor.After = id
			continue
		}
		if readErr != nil {
			return 0, readErr
		}
		intents, e := fuelDateIntents(source, household, entity, generation, sampled)
		if e != nil {
			return 0, e
		}
		if entity == "maintenance_plans" {
			mileage, e := observeFuelNotificationMileage(ctx, tx, household, generation, source, sampled)
			if e != nil {
				return 0, e
			}
			if mileage != nil {
				intents = append(intents, *mileage)
			}
		}
		for _, intent := range intents {
			if err = s.stageFuelNotificationIntent(ctx, tx, intent); err != nil {
				return 0, err
			}
		}
		cursor.After = id
	}
	if len(ids) <= limit {
		cursor.After = ""
		if cursor.Phase == "vehicles" {
			cursor.Phase = "maintenance"
		} else {
			cursor.Phase = "vehicles"
		}
	}
	raw, err = json.Marshal(cursor)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET payload=$2,available_at=clock_timestamp(),attempts=0 WHERE id=$1`, job, raw)
	if err != nil {
		return 0, err
	}
	return 1, tx.Commit(ctx)
}

func (s *Store) stageFuelNotificationIntent(ctx context.Context, tx pgx.Tx, i notification.Intent) error {
	cutoff, err := s.fuelNotificationCutoff(ctx, tx, i)
	if err != nil || cutoff.IsZero() {
		return err
	}
	var consent bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category='fuel' AND enabled AND enabled_since<=$3 FOR SHARE`,
		i.HouseholdID, i.UserID, cutoff).Scan(&consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var exists bool
	// A logical date stage survives unrelated source revisions. A cancelled old
	// revision can be replaced, but a pending/current or accepted one cannot.
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_intents i
 WHERE household_id=$1 AND user_id=$2 AND module_id='fuel' AND module_generation=$3
 AND resource_type=$4 AND resource_id=$5 AND occurrence=$6 AND category='fuel'
 AND(i.source_revision=$7 OR i.state='accepted' OR EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id AND d.state='accepted')))`,
		i.HouseholdID, i.UserID, i.Generation, i.ResourceType, i.ResourceID, i.Occurrence, i.Revision).Scan(&exists)
	if err != nil || exists {
		return err
	}
	_, err = EnqueueNotificationIntent(ctx, tx, i)
	return err
}

func observeFuelNotificationMileage(ctx context.Context, tx pgx.Tx, household string, generation int64, s fuelNotificationSource, at time.Time) (*notification.Intent, error) {
	eligible, due, err := fuelNotificationMileage(ctx, tx, household, s, at)
	if err != nil {
		return nil, err
	}
	state := "ineligible"
	var healthy *time.Time
	if eligible {
		state = "baseline"
		if !due {
			state = "healthy"
			healthy = &at
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO fuel.notification_maintenance_observations
 (household_id,plan_id,module_generation,module_activated_at,plan_version,vehicle_version,observed_at,last_healthy_at,state)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(household_id,plan_id) DO NOTHING`, household, s.id, generation, s.activated, s.version, s.vehicleVersion, at, healthy, state)
	if err != nil {
		return nil, err
	}
	var oldGeneration, oldPlan, oldVehicle, episode int64
	var oldActivated, observed time.Time
	var oldHealthy, detected, cutoff *time.Time
	var previous string
	err = tx.QueryRow(ctx, `SELECT module_generation,module_activated_at,plan_version,vehicle_version,observed_at,last_healthy_at,
 state,episode,detected_at,consent_cutoff FROM fuel.notification_maintenance_observations WHERE household_id=$1 AND plan_id=$2 FOR UPDATE`, household, s.id).
		Scan(&oldGeneration, &oldActivated, &oldPlan, &oldVehicle, &observed, &oldHealthy, &previous, &episode, &detected, &cutoff)
	if err != nil {
		return nil, err
	}
	if at.Before(observed) {
		return nil, nil
	}
	reset := oldGeneration != generation || !oldActivated.Equal(s.activated) || oldPlan != s.version || oldVehicle != s.vehicleVersion || previous == "ineligible" || !eligible
	if !reset && due {
		switch previous {
		case "healthy":
			if oldHealthy == nil || episode >= notification.MaxRevision {
				return nil, ErrConflict
			}
			episode++
			state = "due"
			healthy = oldHealthy
			cutoff = oldHealthy
			detected = &at
		case "due":
			state = "due"
			healthy = oldHealthy
		}
	}
	if state != "due" {
		detected = nil
		cutoff = nil
	}
	_, err = tx.Exec(ctx, `UPDATE fuel.notification_maintenance_observations SET module_generation=$3,module_activated_at=$4,
 plan_version=$5,vehicle_version=$6,observed_at=$7,last_healthy_at=$8,state=$9,episode=$10,detected_at=$11,consent_cutoff=$12
 WHERE household_id=$1 AND plan_id=$2`, household, s.id, generation, s.activated, s.version, s.vehicleVersion, at, healthy, state, episode, detected, cutoff)
	if err != nil || state != "due" || detected == nil || cutoff == nil || !detected.Add(24*time.Hour).After(at) || s.since.After(*cutoff) {
		return nil, err
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: s.user, Module: "fuel", Generation: generation,
		ResourceType: "maintenance_plans", ResourceID: s.id, Revision: s.version, Category: "fuel", Occurrence: fuelDistanceOccurrence(s.cycle, episode),
		EventAt: *detected, NotBefore: *detected, ExpiresAt: detected.Add(24 * time.Hour)}, nil
}
