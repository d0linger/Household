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

type pantryNotificationCursor struct {
	Schema int    `json:"schema"`
	Phase  string `json:"phase"`
	After  string `json:"after"`
}

// StagePantryNotifications advances one durable page. Completing the page and
// moving its cursor share a transaction; a restart cannot skip its effects.
func (s *Store) StagePantryNotifications(ctx context.Context, limit int) (int, error) {
	limit = max(1, min(limit, 50))
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var household string
	var generation int64
	err := s.Pool.QueryRow(ctx, `SELECT m.household_id::text,m.data_generation FROM core.module_states m
 LEFT JOIN core.jobs j ON j.dedupe_key='pantry-scan-v1/'||m.household_id::text||'/'||m.data_generation::text
 WHERE m.module_id='pantry' AND m.state='enabled'
 AND(j.id IS NULL OR(j.kind=$1 AND j.state='pending' AND j.available_at<=clock_timestamp()))
 ORDER BY j.available_at NULLS FIRST,m.household_id LIMIT 1`, PantryNotificationScanJobKind).Scan(&household, &generation)
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
	if err = lockPantryNotificationModule(ctx, tx, household, generation); err != nil {
		return 0, err
	}
	key := "pantry-scan-v1/" + household + "/" + strconv.FormatInt(generation, 10)
	_, err = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
 VALUES($1,'pantry',$2,$3,'{"schema":1,"phase":"stock","after":""}',$4) ON CONFLICT(dedupe_key) DO NOTHING`,
		household, generation, PantryNotificationScanJobKind, key)
	if err != nil {
		return 0, err
	}
	var job string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT id::text,CASE WHEN octet_length(payload::text)<=512 THEN payload END
 FROM core.jobs WHERE dedupe_key=$1 AND state='pending' AND available_at<=clock_timestamp()
 FOR UPDATE SKIP LOCKED`, key).Scan(&job, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var cursor pantryNotificationCursor
	if shoppingDecodeClosed(raw, &cursor) != nil || cursor.Schema != 1 ||
		(cursor.Phase != "stock" && cursor.Phase != "recipients") ||
		(cursor.After != "" && !shoppingCanonicalUUID(cursor.After)) {
		return 0, ErrConflict
	}
	if cursor.Phase == "stock" {
		var done bool
		cursor.After, done, err = s.ObservePantryNotificationStock(ctx, tx, household, generation, cursor.After, limit)
		if done {
			cursor.Phase, cursor.After = "recipients", ""
		}
	} else {
		cursor, err = stagePantryRecipients(ctx, tx, household, generation, cursor)
	}
	if err != nil {
		return 0, err
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

func lockPantryNotificationModule(ctx context.Context, tx pgx.Tx, household string, generation int64) error {
	if _, err := tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return err
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT true FROM core.module_states WHERE household_id=$1 AND module_id='pantry'
 AND state='enabled' AND data_generation=$2 FOR SHARE`, household, generation).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

type pantryNotificationRecipient struct {
	user, zone string
	since      time.Time
}

func stagePantryRecipients(ctx context.Context, tx pgx.Tx, household string, generation int64, cursor pantryNotificationCursor) (pantryNotificationCursor, error) {
	rows, err := tx.Query(ctx, `SELECT p.user_id::text,p.timezone,p.enabled_since FROM core.notification_preferences p
 JOIN core.memberships m ON m.household_id=p.household_id AND m.user_id=p.user_id
 JOIN core.users u ON u.id=p.user_id WHERE p.household_id=$1 AND p.category='pantry' AND p.enabled
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND(nullif($2,'')::uuid IS NULL OR p.user_id>nullif($2,'')::uuid)
 ORDER BY p.user_id LIMIT 17 FOR SHARE OF p`, household, cursor.After)
	if err != nil {
		return cursor, err
	}
	var recipients []pantryNotificationRecipient
	for rows.Next() {
		var r pantryNotificationRecipient
		if err = rows.Scan(&r.user, &r.zone, &r.since); err != nil {
			rows.Close()
			return cursor, err
		}
		recipients = append(recipients, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return cursor, err
	}
	more := len(recipients) > 16
	if more {
		recipients = recipients[:16]
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return cursor, err
	}
	for _, r := range recipients {
		if err = stagePantryDigest(ctx, tx, household, generation, r, now); err != nil {
			return cursor, err
		}
		cursor.After = r.user
	}
	if !more {
		cursor.Phase, cursor.After = "stock", ""
	}
	return cursor, nil
}

func stagePantryDigest(ctx context.Context, tx pgx.Tx, household string, generation int64, recipient pantryNotificationRecipient, now time.Time) error {
	refs, err := pantryNotificationCandidates(ctx, tx, household, generation, recipient.user, recipient.since, now)
	if err != nil || len(refs) == 0 {
		return err
	}
	location, err := time.LoadLocation(recipient.zone)
	if err != nil {
		return err
	}
	available := now
	for range 2 {
		d, e := newPantryDigest(household, recipient.user, generation, recipient.zone, available, refs)
		if e != nil {
			return e
		}
		var oldRaw []byte
		var state, id string
		var oldAvailable time.Time
		e = tx.QueryRow(ctx, `SELECT id::text,state,CASE WHEN octet_length(payload::text)<=16384 THEN payload END,available_at
 FROM core.jobs WHERE dedupe_key=$1 FOR UPDATE`, pantryDigestKey(d)).Scan(&id, &state, &oldRaw, &oldAvailable)
		if errors.Is(e, pgx.ErrNoRows) {
			raw, marshalErr := json.Marshal(d)
			if marshalErr != nil {
				return marshalErr
			}
			if len(raw) > pantryNotificationPayloadLimit {
				return ErrConflict
			}
			_, e = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key,available_at)
 VALUES($1,'pantry',$2,$3,$4,$5,$6) ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, PantryNotificationDigestJobKind, raw, pantryDigestKey(d), available)
			return e
		}
		if e != nil {
			return e
		}
		var old pantryNotificationDigest
		if shoppingDecodeClosed(oldRaw, &old) != nil || !validPantryDigest(old) || pantryDigestKey(old) != pantryDigestKey(d) {
			return ErrConflict
		}
		if state == "pending" && old.Timezone == d.Timezone {
			currentRefs, currentErr := currentPantryReferences(ctx, tx, old)
			if currentErr != nil {
				return currentErr
			}
			freshAfter := now
			if oldAvailable.After(freshAfter) {
				freshAfter = oldAvailable
			}
			d.References = mergePantryReferences(currentRefs, refs, freshAfter)
			raw, marshalErr := json.Marshal(d)
			if marshalErr != nil {
				return marshalErr
			}
			if len(raw) > pantryNotificationPayloadLimit {
				return ErrConflict
			}
			_, e = tx.Exec(ctx, `UPDATE core.jobs SET payload=$2 WHERE id=$1 AND state='pending'`, id, raw)
			return e
		}
		// A finalized/running day's sources never become tomorrow's duplicate.
		used := make(map[string]bool, len(old.References))
		for _, ref := range old.References {
			used[pantryReferenceKey(ref)] = true
		}
		local := now.In(location)
		available = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
		remaining := make([]pantryNotificationReference, 0, len(refs))
		for _, ref := range refs {
			if !used[pantryReferenceKey(ref)] && ref.ExpiresAt.After(available) {
				remaining = append(remaining, ref)
			}
		}
		refs = remaining
		if len(refs) == 0 {
			return nil
		}
	}
	return nil
}

// CompletePantryNotificationDigest freezes current sources and the core intent
// atomically under the same unexpired job lease. It never extends source TTLs.
func (s *Store) CompletePantryNotificationDigest(ctx context.Context, jobID, lease string) error {
	if !shoppingCanonicalUUID(jobID) || !shoppingCanonicalUUID(lease) {
		return ErrConflict
	}
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=16384 THEN payload END
 FROM core.jobs WHERE id=$1 AND kind=$2`, jobID, PantryNotificationDigestJobKind).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var d pantryNotificationDigest
	if shoppingDecodeClosed(raw, &d) != nil || !validPantryDigest(d) {
		return ErrConflict
	}
	tx, err := audit.Begin(audit.WithHousehold(ctx, d.Household), s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = lockPantryNotificationModule(ctx, tx, d.Household, d.Generation); err != nil {
		return err
	}
	var since time.Time
	var zone string
	err = tx.QueryRow(ctx, `SELECT timezone,enabled_since FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category='pantry' AND enabled FOR SHARE`, d.Household, d.User).Scan(&zone, &since)
	consenting := err == nil && zone == d.Timezone
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var key string
	var available time.Time
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=16384 THEN payload END,dedupe_key,available_at
 FROM core.jobs WHERE id=$1 AND household_id=$2 AND module_id='pantry' AND module_generation=$3
 AND kind=$4 AND payload_version=1 AND state='running' AND lease_token=$5
 AND lease_until>clock_timestamp() AND available_at<=clock_timestamp() FOR UPDATE`,
		jobID, d.Household, d.Generation, PantryNotificationDigestJobKind, lease).Scan(&raw, &key, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var current pantryNotificationDigest
	if shoppingDecodeClosed(raw, &current) != nil || !validPantryDigest(current) || key != pantryDigestKey(current) ||
		current.Household != d.Household || current.User != d.User || current.Generation != d.Generation || current.Timezone != d.Timezone {
		return ErrConflict
	}
	d = current
	refs, err := currentPantryReferences(ctx, tx, d)
	if err != nil {
		return err
	}
	filtered := make([]pantryNotificationReference, 0, len(refs))
	for _, r := range refs {
		if consenting && !since.After(r.Cutoff) {
			filtered = append(filtered, r)
		}
	}
	var reason *string
	if len(filtered) == 0 {
		v := "obsolete_audience"
		reason = &v
	} else {
		d.References = filtered
		raw, err = json.Marshal(d)
		if err != nil {
			return err
		}
		event, notBefore, expiry, timeErr := pantryDigestIntentTimes(d, available)
		if timeErr != nil {
			return timeErr
		}
		i := notification.Intent{Schema: notification.Schema, HouseholdID: d.Household, UserID: d.User,
			Module: "pantry", Generation: d.Generation, ResourceType: "pantry_digest", ResourceID: jobID,
			Revision: 1, Occurrence: "day/" + d.Day, Category: "pantry", EventAt: event, NotBefore: notBefore, ExpiresAt: expiry}
		if expiry.After(notBefore) {
			if _, err = EnqueueNotificationIntent(ctx, tx, i); err != nil {
				return err
			}
		} else {
			v := "expired_daily_group"
			reason = &v
		}
	}
	result, err := tx.Exec(ctx, `UPDATE core.jobs SET state='done',payload=$3,finished_at=clock_timestamp(),last_error=$4,
 lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>clock_timestamp()`, jobID, lease, raw, reason)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
