package store

import (
	"context"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type NotificationPreference struct {
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
	notification.QuietHours
	Version      int64      `json:"version"`
	EnabledSince *time.Time `json:"enabled_since"`
}

// NotificationPreferences always returns every compiled category, including
// explicit disabled defaults. Merely reading this endpoint never grants consent.
func (s *Store) NotificationPreferences(ctx context.Context, id auth.Identity) ([]NotificationPreference, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithActor(ctx, id.UserID, id.HouseholdID), s.Pool)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = auth.LockSession(ctx, tx, id); err != nil || id.MustChange {
		return nil, ErrForbidden
	}
	var timezone string
	if err = tx.QueryRow(ctx, `SELECT h.timezone FROM core.households h JOIN core.users u ON u.id=$2 WHERE h.id=$1 AND NOT u.must_change_password`, id.HouseholdID, id.UserID).Scan(&timezone); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT category,enabled,timezone,quiet_start,quiet_end,version,enabled_since
 FROM core.notification_preferences WHERE household_id=$1 AND user_id=$2 ORDER BY category LIMIT 13`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	saved := map[string]NotificationPreference{}
	for rows.Next() {
		var p NotificationPreference
		if err = rows.Scan(&p.Category, &p.Enabled, &p.Timezone, &p.Start, &p.End, &p.Version, &p.EnabledSince); err != nil {
			return nil, err
		}
		saved[p.Category] = p
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := make([]NotificationPreference, 0, len(notification.Categories()))
	for _, category := range notification.Categories() {
		p, exists := saved[category.Category]
		if !exists {
			p = NotificationPreference{Category: category.Category, QuietHours: notification.QuietHours{Timezone: timezone}}
		}
		result = append(result, p)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// SaveNotificationPreference uses optimistic versions and locks the account's
// membership, including the first insert. Revocation fences already queued work.
func (s *Store) SaveNotificationPreference(ctx context.Context, id auth.Identity, p NotificationPreference) (NotificationPreference, error) {
	if _, ok := notification.Lookup(p.Category); !ok || p.Version < 0 || p.Version >= notification.MaxRevision || p.Validate() != nil {
		return NotificationPreference{}, Invalid{"Ungültige Benachrichtigungseinstellung"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithActor(ctx, id.UserID, id.HouseholdID), s.Pool)
	if err != nil {
		return NotificationPreference{}, err
	}
	defer rollback(tx)
	// Serialize even a first preference insert without upgrading a membership
	// SHARE lock held by LockSession (two such upgrades would deadlock).
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/notification-preferences/'||$1::text||'/'||$2::text,0))`, id.HouseholdID, id.UserID); err != nil {
		return NotificationPreference{}, err
	}
	if err = auth.LockSession(ctx, tx, id); err != nil || id.MustChange {
		return NotificationPreference{}, ErrForbidden
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.user_id=$2 AND m.inactive_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u`, id.HouseholdID, id.UserID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationPreference{}, ErrForbidden
	}
	if err != nil {
		return NotificationPreference{}, err
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM core.notification_preferences WHERE household_id=$1 AND user_id=$2 AND category=$3 FOR UPDATE`, id.HouseholdID, id.UserID, p.Category).Scan(&version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NotificationPreference{}, err
	}
	if version != p.Version {
		return NotificationPreference{}, ErrConflict
	}
	err = tx.QueryRow(ctx, `INSERT INTO core.notification_preferences
 (household_id,user_id,category,enabled,timezone,quiet_start,quiet_end,enabled_since)
 VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $4 THEN clock_timestamp() END)
 ON CONFLICT(household_id,user_id,category) DO UPDATE SET enabled=excluded.enabled,timezone=excluded.timezone,
 quiet_start=excluded.quiet_start,quiet_end=excluded.quiet_end,version=core.notification_preferences.version+1,
 enabled_since=CASE WHEN NOT excluded.enabled THEN NULL WHEN core.notification_preferences.enabled THEN core.notification_preferences.enabled_since ELSE clock_timestamp() END,
 updated_at=clock_timestamp() RETURNING version,enabled_since`, id.HouseholdID, id.UserID, p.Category, p.Enabled, p.Timezone, p.Start, p.End).Scan(&p.Version, &p.EnabledSince)
	if err != nil {
		return NotificationPreference{}, err
	}
	if !p.Enabled {
		// The consent flip is the authority fence, regardless of backlog size.
		// Remaining stale rows are retired by bounded worker reconciliation.
		_, err = tx.Exec(ctx, `WITH batch AS(SELECT id FROM core.notification_intents
 WHERE household_id=$1 AND user_id=$2 AND category=$3 AND state='pending' ORDER BY id LIMIT 256 FOR UPDATE SKIP LOCKED)
 UPDATE core.notification_intents i SET state='cancelled',version=version+1,updated_at=clock_timestamp()
 FROM batch WHERE i.id=batch.id`, id.HouseholdID, id.UserID, p.Category)
		if err != nil {
			return NotificationPreference{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return NotificationPreference{}, err
	}
	return p, nil
}

// EnqueueNotificationIntent is called within a module's existing mutation
// transaction, after that module proves current resource visibility and revision.
// It never expands household membership into broadcast recipients. The sender
// must repeat the module-specific eligibility check immediately before delivery.
// Lock order: membership/module authority, consent, then intent rows. Producers
// that also replace prior intents acquire consent before those source row locks.
func EnqueueNotificationIntent(ctx context.Context, tx pgx.Tx, intent notification.Intent) (string, error) {
	if err := intent.Validate(); err != nil {
		return "", err
	}
	var consent int64
	err := tx.QueryRow(ctx, `SELECT version FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category=$3 AND enabled AND enabled_since<=$4 FOR SHARE`,
		intent.HouseholdID, intent.UserID, intent.Category, intent.EventAt).Scan(&consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO core.notification_intents
 (household_id,user_id,module_id,module_generation,resource_type,resource_id,source_revision,occurrence,category,
 event_at,not_before,expires_at,dedupe_key,schema_version)
 SELECT $1,$2,nullif($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
 WHERE $10<=clock_timestamp() AND $12>clock_timestamp()
 AND EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.user_id=$2 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password)
 AND($3='' OR EXISTS(SELECT 1 FROM core.module_states ms WHERE ms.household_id=$1 AND ms.module_id=$3
 AND ms.state='enabled' AND ms.data_generation=$4 AND ms.changed_at<=$10))
 AND EXISTS(SELECT 1 FROM core.notification_preferences p WHERE p.household_id=$1 AND p.user_id=$2 AND p.category=$9
 AND p.enabled AND p.enabled_since<=$10)
 ON CONFLICT(dedupe_key) DO NOTHING RETURNING id::text`, intent.HouseholdID, intent.UserID, intent.Module, intent.Generation,
		intent.ResourceType, intent.ResourceID, intent.Revision, intent.Occurrence, intent.Category,
		intent.EventAt, intent.NotBefore, intent.ExpiresAt, intent.DedupeKey(), intent.Schema).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// CancelNotificationSource participates in the source mutation's transaction.
// Passing a current revision cancels older work; zero cancels all source work.
func CancelNotificationSource(ctx context.Context, tx pgx.Tx, household, module, resourceType, resourceID string, currentRevision int64) error {
	if !value.ValidUUID(household) || !value.ValidUUID(resourceID) || currentRevision < 0 {
		return ErrForbidden
	}
	_, err := tx.Exec(ctx, `UPDATE core.notification_intents SET state='cancelled',version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND module_id IS NOT DISTINCT FROM nullif($2,'') AND resource_type=$3 AND resource_id=$4 AND state='pending'
 AND($5=0 OR source_revision<>$5)`, household, module, resourceType, resourceID, currentRevision)
	return err
}
