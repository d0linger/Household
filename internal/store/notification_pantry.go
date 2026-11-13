package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

const PantryNotificationScanJobKind = "pantry_notification_scan_v1"
const PantryNotificationDigestJobKind = "pantry_digest_v1"
const pantryNotificationReferenceLimit = 50
const pantryNotificationPayloadLimit = 16384

type pantryNotificationReference struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Episode   int64     `json:"episode"`
	EventAt   time.Time `json:"event_at"`
	Cutoff    time.Time `json:"consent_cutoff"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pantryNotificationDigest struct {
	Schema     int                           `json:"schema"`
	Household  string                        `json:"household_id"`
	User       string                        `json:"user_id"`
	Generation int64                         `json:"generation"`
	Day        string                        `json:"local_day"`
	Timezone   string                        `json:"timezone"`
	References []pantryNotificationReference `json:"references"`
}

// Opening hints remain advisory Pantry UI data. Expiry notifications retain the
// existing use-by, otherwise best-before, 09:00 household-timezone contract.
// Stock uses all physical balances, including stock in archived locations.
const pantryNotificationSources = `
 SELECT o.household_id,o.module_generation AS generation,'low_stock'::text AS kind,o.product_id AS id,o.episode,
 o.detected_at AS event_at,o.consent_cutoff,o.detected_at+interval '24 hours' AS expires_at
 FROM pantry.notification_stock_observations o
 JOIN pantry.products p ON p.household_id=o.household_id AND p.id=o.product_id
 JOIN core.module_states ms ON ms.household_id=o.household_id AND ms.module_id='pantry'
 WHERE o.state='low' AND o.detected_at IS NOT NULL AND o.consent_cutoff IS NOT NULL
 AND ms.state='enabled' AND ms.data_generation=o.module_generation AND ms.changed_at=o.module_activated_at
 AND p.archived_at IS NULL AND p.low_stock_threshold=o.observed_threshold AND p.unit_code=o.unit_code
 AND coalesce((SELECT sum(b.quantity) FROM pantry.lots l JOIN pantry.lot_balances b
 ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE l.household_id=p.household_id AND l.product_id=p.id),0)<p.low_stock_threshold
 UNION ALL
 SELECT r.household_id,r.module_generation,'expiry',r.id,1::bigint,r.scheduled_at,r.scheduled_at,
 r.scheduled_at+interval '24 hours'
 FROM core.reminders r
 JOIN core.households h ON h.id=r.household_id
 JOIN core.module_states ms ON ms.household_id=r.household_id AND ms.module_id='pantry'
 JOIN pantry.lots l ON l.household_id=r.household_id AND l.id=r.resource_id
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations location ON location.household_id=l.household_id AND location.id=l.location_id
 JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id
 WHERE r.module_id='pantry' AND r.resource_type='lots' AND r.kind='stock_expiry'
 AND r.recipient_user_id IS NULL AND r.state IN('pending','delivered')
 AND ms.state='enabled' AND ms.data_generation=r.module_generation AND ms.changed_at<=r.scheduled_at
 AND p.archived_at IS NULL AND location.archived_at IS NULL AND b.quantity>0
 AND r.scheduled_at=((coalesce(l.use_by_on,l.best_before_on)::timestamp+interval '9 hours') AT TIME ZONE h.timezone)
`

func pantryDigestKey(d pantryNotificationDigest) string {
	return "pantry-digest-v1/" + d.Household + "/" + d.User + "/" + strconv.FormatInt(d.Generation, 10) + "/" + d.Day
}

func pantryReferenceKey(r pantryNotificationReference) string {
	return r.Kind + "/" + r.ID + "/" + strconv.FormatInt(r.Episode, 10) + "/" + r.EventAt.UTC().Format(time.RFC3339Nano)
}

func validPantryDigest(d pantryNotificationDigest) bool {
	if d.Schema != 1 || !shoppingCanonicalUUID(d.Household) || !shoppingCanonicalUUID(d.User) ||
		d.Generation < 1 || d.Generation > notification.MaxRevision || d.Timezone == "" || len(d.Timezone) > 100 || d.Timezone == "Local" ||
		len(d.References) < 1 || len(d.References) > pantryNotificationReferenceLimit {
		return false
	}
	if _, err := time.LoadLocation(d.Timezone); err != nil {
		return false
	}
	day, err := time.Parse(time.DateOnly, d.Day)
	if err != nil || day.Year() < 2000 || day.Year() > 9999 {
		return false
	}
	seen := make(map[string]bool, len(d.References))
	for _, r := range d.References {
		if !shoppingCanonicalUUID(r.ID) || r.Episode < 1 || r.Episode > notification.MaxRevision ||
			(r.Kind != "low_stock" && r.Kind != "expiry") || (r.Kind == "expiry" && r.Episode != 1) ||
			r.EventAt.Year() < 2000 || r.ExpiresAt.Year() > 9999 || r.Cutoff.IsZero() || r.Cutoff.After(r.EventAt) ||
			!r.ExpiresAt.Equal(r.EventAt.Add(24*time.Hour)) || seen[pantryReferenceKey(r)] {
			return false
		}
		seen[pantryReferenceKey(r)] = true
	}
	return true
}

func pantryNotificationCandidates(ctx context.Context, tx pgx.Tx, household string, generation int64, user string, since, deliverAt time.Time) ([]pantryNotificationReference, error) {
	rows, err := tx.Query(ctx, `SELECT kind,id::text,episode,event_at,consent_cutoff,expires_at FROM (`+pantryNotificationSources+`) sources
 WHERE household_id=$1 AND generation=$2 AND event_at<=clock_timestamp()
 AND consent_cutoff>=$3 AND expires_at>$4
 AND NOT EXISTS(SELECT 1 FROM core.jobs used WHERE used.household_id=$1 AND used.module_id='pantry'
 AND used.module_generation=$2 AND used.kind='pantry_digest_v1' AND used.payload_version=1
 AND used.payload->>'user_id'=$5 AND used.state IN('running','done')
 AND used.created_at>=sources.event_at-interval '3 days' AND EXISTS(SELECT 1 FROM jsonb_to_recordset(
 CASE WHEN octet_length(used.payload::text)<=16384 AND jsonb_typeof(used.payload->'references')='array'
 THEN used.payload->'references' ELSE '[]'::jsonb END)
 AS r(kind text,id uuid,episode bigint,event_at timestamptz)
 WHERE r.kind=sources.kind AND r.id=sources.id AND r.episode=sources.episode AND r.event_at=sources.event_at))
 ORDER BY expires_at,kind,id LIMIT 50`, household, generation, since, deliverAt, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]pantryNotificationReference, 0, pantryNotificationReferenceLimit)
	for rows.Next() {
		var r pantryNotificationReference
		if err = rows.Scan(&r.Kind, &r.ID, &r.Episode, &r.EventAt, &r.Cutoff, &r.ExpiresAt); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// Matching is batched: a maximum of fifty closed references is checked against
// current balances/dates/episodes in one query. No names or quantities enter jobs.
func currentPantryReferences(ctx context.Context, tx pgx.Tx, d pantryNotificationDigest) ([]pantryNotificationReference, error) {
	if !validPantryDigest(d) {
		return nil, ErrConflict
	}
	raw, err := json.Marshal(d.References)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH expected AS(SELECT * FROM jsonb_to_recordset($3::jsonb)
 AS x(kind text,id uuid,episode bigint,event_at timestamptz,consent_cutoff timestamptz,expires_at timestamptz))
 SELECT s.kind,s.id::text,s.episode,s.event_at,s.consent_cutoff,s.expires_at FROM (`+pantryNotificationSources+`) s
 JOIN expected e ON e.kind=s.kind AND e.id=s.id AND e.episode=s.episode
 AND e.event_at=s.event_at AND e.consent_cutoff=s.consent_cutoff AND e.expires_at=s.expires_at
 WHERE s.household_id=$1 AND s.generation=$2 AND s.event_at<=clock_timestamp() AND s.expires_at>clock_timestamp()
 ORDER BY s.expires_at,s.kind,s.id LIMIT 50`, d.Household, d.Generation, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]pantryNotificationReference, 0, len(d.References))
	for rows.Next() {
		var r pantryNotificationReference
		if err = rows.Scan(&r.Kind, &r.ID, &r.Episode, &r.EventAt, &r.Cutoff, &r.ExpiresAt); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

func pantryDigestTimes(refs []pantryNotificationReference, available time.Time) (event, notBefore, expiry, cutoff time.Time) {
	notBefore = available
	for _, r := range refs {
		if r.EventAt.After(event) {
			event = r.EventAt
		}
		if r.ExpiresAt.After(expiry) {
			expiry = r.ExpiresAt
		}
		if r.Cutoff.After(cutoff) {
			cutoff = r.Cutoff
		}
	}
	if event.After(notBefore) {
		notBefore = event
	}
	return
}

// A quiet-hours delay may not carry yesterday's group into today's delivery
// window. Source TTLs remain unchanged; the daily envelope has an earlier cap.
func pantryDigestIntentTimes(d pantryNotificationDigest, available time.Time) (event, notBefore, expiry time.Time, err error) {
	location, err := time.LoadLocation(d.Timezone)
	if err != nil {
		return event, notBefore, expiry, err
	}
	day, err := time.ParseInLocation(time.DateOnly, d.Day, location)
	if err != nil {
		return event, notBefore, expiry, err
	}
	event, notBefore, expiry, _ = pantryDigestTimes(d.References, available)
	end := day.AddDate(0, 0, 1)
	if end.Before(expiry) {
		expiry = end
	}
	return event, notBefore, expiry, nil
}

func (s *Store) pantryNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Category != "pantry" || i.ResourceType != "pantry_digest" || i.Revision != 1 || i.Validate() != nil {
		return time.Time{}, nil
	}
	var raw []byte
	var key string
	var available time.Time
	err := tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=16384 THEN payload END,dedupe_key,available_at
 FROM core.jobs WHERE id=$1 AND household_id=$2 AND module_id='pantry' AND module_generation=$3
 AND kind=$4 AND payload_version=1 AND state='done' AND finished_at IS NOT NULL AND last_error IS NULL`,
		i.ResourceID, i.HouseholdID, i.Generation, PantryNotificationDigestJobKind).Scan(&raw, &key, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var d pantryNotificationDigest
	if len(raw) == 0 || shoppingDecodeClosed(raw, &d) != nil || !validPantryDigest(d) ||
		d.Household != i.HouseholdID || d.User != i.UserID || d.Generation != i.Generation ||
		key != pantryDigestKey(d) || i.Occurrence != "day/"+d.Day {
		return time.Time{}, nil
	}
	event, notBefore, expiry, err := pantryDigestIntentTimes(d, available)
	if err != nil {
		return time.Time{}, err
	}
	if !event.Equal(i.EventAt) || !notBefore.Equal(i.NotBefore) || !expiry.Equal(i.ExpiresAt) {
		return time.Time{}, nil
	}
	var zone string
	if err = tx.QueryRow(ctx, `SELECT timezone FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category='pantry' AND enabled`, i.HouseholdID, i.UserID).Scan(&zone); errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil || zone != d.Timezone {
		return time.Time{}, err
	}
	refs, err := currentPantryReferences(ctx, tx, d)
	if err != nil {
		return time.Time{}, err
	}
	_, _, _, cutoff := pantryDigestTimes(refs, available)
	return cutoff, nil
}

func mergePantryReferences(existing, incoming []pantryNotificationReference, available time.Time) []pantryNotificationReference {
	byKey := make(map[string]pantryNotificationReference, len(existing)+len(incoming))
	for _, group := range [][]pantryNotificationReference{existing, incoming} {
		for _, r := range group {
			if r.ExpiresAt.After(available) {
				byKey[pantryReferenceKey(r)] = r
			}
		}
	}
	merged := make([]pantryNotificationReference, 0, len(byKey))
	for _, r := range byKey {
		merged = append(merged, r)
	}
	sort.Slice(merged, func(a, b int) bool {
		if !merged[a].ExpiresAt.Equal(merged[b].ExpiresAt) {
			return merged[a].ExpiresAt.Before(merged[b].ExpiresAt)
		}
		return pantryReferenceKey(merged[a]) < pantryReferenceKey(merged[b])
	})
	if len(merged) > pantryNotificationReferenceLimit {
		merged = merged[:pantryNotificationReferenceLimit]
	}
	return merged
}

func newPantryDigest(household, user string, generation int64, zone string, at time.Time, refs []pantryNotificationReference) (pantryNotificationDigest, error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return pantryNotificationDigest{}, err
	}
	d := pantryNotificationDigest{Schema: 1, Household: household, User: user, Generation: generation,
		Day: at.In(location).Format(time.DateOnly), Timezone: zone, References: refs}
	if !validPantryDigest(d) {
		return d, ErrConflict
	}
	return d, nil
}
