package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func shoppingValidChange(event shoppingChangeEvent) bool {
	if !shoppingCanonicalUUID(event.ID) || event.Shopping == nil || event.Shopping.Schema != 1 {
		return false
	}
	c := event.Shopping
	if len(c.ListIDs) < 1 || len(c.ListIDs) > 2 {
		return false
	}
	for i, lid := range c.ListIDs {
		if !shoppingCanonicalUUID(lid) || i > 0 && c.ListIDs[i-1] >= lid {
			return false
		}
	}
	switch event.Entity + "/" + c.Kind {
	case "items/item_changed":
		return true
	case "items/item_deleted":
		return len(c.ListIDs) == 1
	case "lists/list_changed", "lists/list_deleted":
		return len(c.ListIDs) == 1 && c.ListIDs[0] == event.ID
	default:
		return false
	}
}

func shoppingValidReference(r ShoppingNotificationReference) bool {
	rev, err := shoppingRevision(r.WatchRevision)
	return r.Schema == 1 && shoppingCanonicalUUID(r.HouseholdID) && shoppingCanonicalUUID(r.RecipientID) && shoppingCanonicalUUID(r.ListID) &&
		r.Generation > 0 && r.Generation <= shoppingSafeInteger && err == nil && rev > 0 && r.Bucket > 0 && r.Bucket <= shoppingSafeInteger &&
		r.EventAt.Year() >= 2000 && r.EventAt.Year() <= 9999 && r.ExpiresAt.After(r.EventAt) && r.ExpiresAt.Year() <= 9999 &&
		r.ExpiresAt.Unix()/60 == r.Bucket+24*60 && r.EventAt.Unix()/60 <= r.Bucket && r.ExpiresAt.Sub(r.EventAt) <= 48*time.Hour &&
		(r.PushEnabledSince.IsZero() || r.PushEnabledSince.Year() >= 2000 && r.PushEnabledSince.Year() <= 9999) &&
		(r.NotificationID == "" || shoppingCanonicalUUID(r.NotificationID)) && (r.SubscriptionID == "" || shoppingCanonicalUUID(r.SubscriptionID))
}

func shoppingNoticeKey(r ShoppingNotificationReference) string {
	kind := "changed"
	if r.Deleted {
		kind = "deleted"
	}
	return strings.Join([]string{"shopping-notice-v1", r.HouseholdID, r.RecipientID, strconv.FormatInt(r.Generation, 10), r.ListID, strconv.FormatInt(r.Bucket, 10), kind}, "/")
}

func shoppingParseNoticeKey(key string) (ShoppingNotificationReference, error) {
	var r ShoppingNotificationReference
	p := strings.Split(key, "/")
	if len(p) != 7 || p[0] != "shopping-notice-v1" || !shoppingCanonicalUUID(p[1]) || !shoppingCanonicalUUID(p[2]) || !shoppingCanonicalUUID(p[4]) {
		return r, ErrNotFound
	}
	g, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil || g <= 0 || g > shoppingSafeInteger || strconv.FormatInt(g, 10) != p[3] {
		return r, ErrNotFound
	}
	b, err := strconv.ParseInt(p[5], 10, 64)
	if err != nil || b <= 0 || b > shoppingSafeInteger || strconv.FormatInt(b, 10) != p[5] {
		return r, ErrNotFound
	}
	if p[6] != "changed" && p[6] != "deleted" {
		return r, ErrNotFound
	}
	r.Schema, r.HouseholdID, r.RecipientID, r.ListID, r.Generation, r.Bucket, r.Deleted = 1, p[1], p[2], p[4], g, b, p[6] == "deleted"
	return r, nil
}

// ShoppingNotificationAudience validates current recipient/watch authority. Push
// additionally validates the decrypted registering session in its own service.
func (s *Store) ShoppingNotificationAudience(ctx context.Context, tx pgx.Tx, ref ShoppingNotificationReference) (ShoppingAudience, error) {
	var out ShoppingAudience
	if !shoppingValidReference(ref) {
		return out, Invalid{"Ungültiger Benachrichtigungsbezug"}
	}
	var generation int64
	var state string
	var changed, now time.Time
	err := tx.QueryRow(ctx, `SELECT state,data_generation,changed_at,clock_timestamp() FROM core.module_states
 WHERE household_id=$1 AND module_id='shopping' FOR SHARE`, ref.HouseholdID).Scan(&state, &generation, &changed, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if state != "enabled" || generation != ref.Generation || !now.Before(ref.ExpiresAt) || ref.EventAt.Before(changed) || ref.EventAt.After(now) {
		return out, nil
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.user_id=$2 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u`, ref.HouseholdID, ref.RecipientID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	settings, err := shoppingReadSettings(ctx, tx, ref.HouseholdID, generation)
	if err != nil {
		return out, err
	}
	user, ok := settings.Users[ref.RecipientID]
	if !ok || user.Revision != ref.WatchRevision {
		return out, nil
	}
	watch, ok := user.Watches[ref.ListID]
	if !ok || !watch.Enabled || ref.EventAt.Before(watch.EnabledSince) || watch.MutedUntil != nil && (now.Before(*watch.MutedUntil) || ref.EventAt.Before(*watch.MutedUntil)) {
		return out, nil
	}
	list, err := shoppingReadList(ctx, tx, ref.HouseholdID, ref.ListID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if ref.Deleted {
		if err == nil {
			return out, nil
		}
	} else if errors.Is(err, ErrNotFound) || list.Archived || list.IsTemplate {
		return out, nil
	}
	if ref.NotificationID != "" {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notifications WHERE id=$1 AND household_id=$2 AND user_id=$3
 AND module_id='shopping' AND kind=$4 AND dedupe_key=$5 AND dismissed_at IS NULL)`, ref.NotificationID, ref.HouseholdID, ref.RecipientID, shoppingNotificationKind, shoppingNoticeKey(ref)).Scan(&valid); err != nil {
			return out, err
		}
		if !valid {
			return out, nil
		}
	}
	out.Eligible = true
	out.PushEligible = watch.PushEnabled && watch.PushEnabledSince != nil && !ref.PushEnabledSince.IsZero() &&
		watch.PushEnabledSince.Equal(ref.PushEnabledSince) && !ref.EventAt.Before(*watch.PushEnabledSince)
	return out, nil
}

func (s *Store) ShoppingNotificationProjection(ctx context.Context, id auth.Identity, notificationID string) (ShoppingInboxNotice, error) {
	var out ShoppingInboxNotice
	if !value.ValidUUID(notificationID) {
		return out, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer shoppingRollback(tx)
	scope, err := shoppingScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	var key string
	var created time.Time
	err = tx.QueryRow(ctx, `SELECT dedupe_key,created_at FROM core.notifications WHERE id=$1 AND household_id=$2 AND user_id=$3
 AND module_id='shopping' AND kind=$4 AND dismissed_at IS NULL AND octet_length(dedupe_key)<=400`, notificationID, id.HouseholdID, id.UserID, shoppingNotificationKind).Scan(&key, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	ref, err := shoppingParseNoticeKey(key)
	if err != nil || ref.HouseholdID != id.HouseholdID || ref.RecipientID != id.UserID || ref.Generation != scope.Generation {
		return out, ErrNotFound
	}
	if created.Unix() < ref.Bucket*60 || created.Unix() >= (ref.Bucket+24*60+1)*60 {
		return out, ErrNotFound
	}
	settings, err := shoppingReadSettings(ctx, tx, id.HouseholdID, scope.Generation)
	if err != nil {
		return out, err
	}
	user, ok := settings.Users[id.UserID]
	if !ok {
		return out, ErrNotFound
	}
	watch, ok := user.Watches[ref.ListID]
	if !ok || !watch.Enabled || created.Before(watch.EnabledSince) || created.After(scope.ServerTime) || watch.MutedUntil != nil && (scope.ServerTime.Before(*watch.MutedUntil) || created.Before(*watch.MutedUntil)) {
		return out, ErrNotFound
	}
	out.ID = notificationID
	if ref.Deleted {
		if _, err = shoppingReadList(ctx, tx, id.HouseholdID, ref.ListID); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return out, err
			}
			return out, ErrNotFound
		}
		out.Title, out.TargetPath = "Eine abonnierte Einkaufsliste wurde entfernt", "/notifications"
	} else {
		list, e := shoppingReadList(ctx, tx, id.HouseholdID, ref.ListID)
		if errors.Is(e, ErrNotFound) {
			return out, ErrNotFound
		}
		if e != nil {
			return out, e
		}
		if list.Archived || list.IsTemplate {
			return out, ErrNotFound
		}
		out.Title, out.TargetPath = "Änderungen in einer Einkaufsliste", "/m/shopping/lists/"+list.ID
	}
	return out, tx.Commit(ctx)
}

// StageShoppingNotifications only stages committed changes. Its event-row lock
// and durable cursor make each bounded recipient pass restartable.
func (s *Store) StageShoppingNotifications(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 20 {
		return 0, Invalid{"Benachrichtigungsbatch außerhalb der Grenze"}
	}
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 20*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT e.id,e.household_id FROM core.outbox_events e
 JOIN core.module_states m ON m.household_id=e.household_id AND m.module_id='shopping' AND m.state='enabled' AND m.data_generation=e.module_generation
 LEFT JOIN core.event_deliveries d ON d.event_id=e.id AND d.consumer_id=$1
 WHERE e.module_id='shopping' AND e.event_type='record_changed' AND e.event_version=2 AND (d.event_id IS NULL OR d.state='pending')
 ORDER BY e.created_at,e.id LIMIT $2`, shoppingNotificationConsumer, limit)
	if err != nil {
		return 0, err
	}
	type candidate struct{ id, household string }
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.household); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, c := range candidates {
		n, e := s.shoppingStageEvent(ctx, c.id, c.household)
		if e != nil {
			return processed, e
		}
		processed += n
	}
	if err = s.shoppingNotificationRetention(ctx); err != nil {
		return processed, err
	}
	if err = s.shoppingPruneWatches(ctx); err != nil {
		return processed, err
	}
	return processed, nil
}

func (s *Store) shoppingStageEvent(ctx context.Context, eventID, household string) (int, error) {
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return 0, err
	}
	defer shoppingRollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return 0, err
	}
	var generation int64
	var changed time.Time
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT data_generation,changed_at,state='enabled' FROM core.module_states WHERE household_id=$1 AND module_id='shopping' FOR SHARE", household).Scan(&generation, &changed, &enabled)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, nil
	}
	var payload []byte
	var actor *string
	var eventAt time.Time
	var eventGeneration int64
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=1024 THEN payload END,actor_user_id,created_at,module_generation
 FROM core.outbox_events WHERE id=$1 AND household_id=$2 AND module_id='shopping' AND event_type='record_changed' AND event_version=2 FOR UPDATE SKIP LOCKED`, eventID, household).
		Scan(&payload, &actor, &eventAt, &eventGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.event_deliveries(event_id,consumer_id) VALUES($1,$2) ON CONFLICT DO NOTHING", eventID, shoppingNotificationConsumer); err != nil {
		return 0, err
	}
	var state string
	var progress *string
	if err = tx.QueryRow(ctx, "SELECT state,last_error FROM core.event_deliveries WHERE event_id=$1 AND consumer_id=$2 FOR UPDATE", eventID, shoppingNotificationConsumer).Scan(&state, &progress); err != nil {
		return 0, err
	}
	if state != "pending" {
		return 0, nil
	}
	finish := func(reason string) (int, error) {
		_, e := tx.Exec(ctx, "UPDATE core.event_deliveries SET state='done',delivered_at=clock_timestamp(),attempts=attempts+1,last_error=$3 WHERE event_id=$1 AND consumer_id=$2", eventID, shoppingNotificationConsumer, reason)
		if e != nil {
			return 0, e
		}
		return 1, tx.Commit(ctx)
	}
	var event shoppingChangeEvent
	if len(payload) == 0 || shoppingDecodeClosed(payload, &event) != nil || !shoppingValidChange(event) {
		return finish("invalid_contract")
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return 0, err
	}
	now = now.UTC()
	if eventGeneration != generation || eventAt.Before(changed) || eventAt.Before(now.Add(-24*time.Hour)) || eventAt.After(now) {
		return finish("obsolete_event")
	}
	settings, err := shoppingReadSettings(ctx, tx, household, generation)
	if err != nil {
		return 0, err
	}
	cursor := ""
	if progress != nil && *progress != "" {
		if !strings.HasPrefix(*progress, "cursor:") {
			return 0, Invalid{"Benachrichtigungsfortsetzung ungültig"}
		}
		cursor = strings.TrimPrefix(*progress, "cursor:")
		parts := strings.Split(cursor, "/")
		if len(parts) != 2 || !shoppingCanonicalUUID(parts[0]) || !shoppingCanonicalUUID(parts[1]) {
			return 0, Invalid{"Benachrichtigungsfortsetzung ungültig"}
		}
	}
	type recipient struct {
		key string
		ref ShoppingNotificationReference
	}
	recipients := []recipient{}
	bucket := now.Unix() / 60
	for uid, user := range settings.Users {
		if actor != nil && uid == *actor {
			continue
		}
		for _, lid := range event.Shopping.ListIDs {
			watch, ok := user.Watches[lid]
			if !ok {
				continue
			}
			key := uid + "/" + lid
			if key <= cursor {
				continue
			}
			ref := ShoppingNotificationReference{Schema: 1, HouseholdID: household, RecipientID: uid, ListID: lid, Generation: generation, WatchRevision: user.Revision, EventAt: eventAt.UTC(), Bucket: bucket, ExpiresAt: now.Add(24 * time.Hour), Deleted: event.Shopping.Kind == "list_deleted"}
			if watch.PushEnabledSince != nil {
				ref.PushEnabledSince = *watch.PushEnabledSince
			}
			recipients = append(recipients, recipient{key, ref})
		}
	}
	sort.Slice(recipients, func(i, j int) bool { return recipients[i].key < recipients[j].key })
	if len(recipients) == 0 {
		return finish("")
	}
	count := min(50, len(recipients))
	for _, recipient := range recipients[:count] {
		ref := recipient.ref
		audience, e := s.ShoppingNotificationAudience(ctx, tx, ref)
		if e != nil {
			return 0, e
		}
		if audience.Eligible {
			if err = s.shoppingStageDigest(ctx, tx, ref); err != nil {
				return 0, err
			}
		}
		cursor = recipient.key
	}
	if count == len(recipients) {
		return finish("")
	}
	_, err = tx.Exec(ctx, "UPDATE core.event_deliveries SET attempts=attempts+1,last_error=$3 WHERE event_id=$1 AND consumer_id=$2", eventID, shoppingNotificationConsumer, "cursor:"+cursor)
	if err != nil {
		return 0, err
	}
	return 1, tx.Commit(ctx)
}

func (s *Store) shoppingStageDigest(ctx context.Context, tx pgx.Tx, ref ShoppingNotificationReference) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	key := "shopping-digest-v1/" + strings.TrimPrefix(shoppingNoticeKey(ref), "shopping-notice-v1/")
	// A late event never reopens a running/completed bucket. All pending events
	// in a window coalesce to the newest event, with current revision retained.
	_, err = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload_version,payload,dedupe_key,available_at,max_attempts)
 VALUES($1,'shopping',$2,$3,1,$4,$5,$6,5)
 ON CONFLICT(dedupe_key) DO NOTHING`, ref.HouseholdID, ref.Generation, ShoppingDigestJobKind, raw, key, time.Unix((ref.Bucket+1)*60, 0).UTC())
	if err != nil {
		return err
	}
	var job, state, kind, household string
	var module *string
	var generation *int64
	var version int
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT id,state,kind,household_id,module_id,module_generation,payload_version,
 CASE WHEN octet_length(payload::text)<=4096 THEN payload END FROM core.jobs WHERE dedupe_key=$1 FOR UPDATE`, key).
		Scan(&job, &state, &kind, &household, &module, &generation, &version, &previous)
	if err != nil {
		return err
	}
	if kind != ShoppingDigestJobKind || household != ref.HouseholdID || module == nil || *module != "shopping" || generation == nil || *generation != ref.Generation {
		return ErrConflict
	}
	if state != "pending" {
		return nil
	}
	var old ShoppingNotificationReference
	if version != 1 || len(previous) == 0 || shoppingDecodeClosed(previous, &old) != nil || !shoppingValidReference(old) || old.NotificationID != "" || old.SubscriptionID != "" ||
		old.HouseholdID != ref.HouseholdID || old.RecipientID != ref.RecipientID || old.ListID != ref.ListID || old.Generation != ref.Generation || old.Bucket != ref.Bucket || old.Deleted != ref.Deleted {
		_, err = tx.Exec(ctx, "UPDATE core.jobs SET state='failed',last_error='invalid_contract',finished_at=clock_timestamp() WHERE id=$1 AND state='pending'", job)
		return err
	}
	if old.WatchRevision != ref.WatchRevision || !old.EventAt.After(ref.EventAt) {
		_, err = tx.Exec(ctx, "UPDATE core.jobs SET payload=$2 WHERE id=$1 AND state='pending'", job, raw)
	}
	return err
}

func (s *Store) CompleteShoppingNotificationJob(ctx context.Context, jobID, lease string) error {
	if !value.ValidUUID(jobID) || !value.ValidUUID(lease) {
		return ErrConflict
	}
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var household string
	err := s.Pool.QueryRow(ctx, "SELECT household_id FROM core.jobs WHERE id=$1 AND kind=$2", jobID, ShoppingDigestJobKind).Scan(&household)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer shoppingRollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT module_id FROM core.module_states WHERE household_id=$1 AND module_id='shopping' FOR SHARE", household); err != nil {
		return err
	}
	var raw []byte
	var generation int64
	var key string
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=4096 THEN payload END,module_generation,dedupe_key FROM core.jobs
 WHERE id=$1 AND household_id=$2 AND kind=$3 AND payload_version=1 AND state='running' AND lease_token=$4 AND lease_until>clock_timestamp() FOR UPDATE`, jobID, household, ShoppingDigestJobKind, lease).Scan(&raw, &generation, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var ref ShoppingNotificationReference
	valid := len(raw) > 0 && shoppingDecodeClosed(raw, &ref) == nil && shoppingValidReference(ref) && ref.HouseholdID == household && ref.Generation == generation && ref.NotificationID == "" && ref.SubscriptionID == "" && key == "shopping-digest-v1/"+strings.TrimPrefix(shoppingNoticeKey(ref), "shopping-notice-v1/")
	reason := "obsolete_audience"
	if valid {
		audience, e := s.ShoppingNotificationAudience(ctx, tx, ref)
		if e != nil {
			return e
		}
		if audience.Eligible {
			title, target := "Änderungen in einer Einkaufsliste", "/m/shopping/lists/"+ref.ListID
			if ref.Deleted {
				title, target = "Eine abonnierte Einkaufsliste wurde entfernt", "/notifications"
			}
			var notification string
			err = tx.QueryRow(ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key,created_at)
 VALUES($1,$2,'shopping',$3,$4,$5,$6,clock_timestamp()) ON CONFLICT(dedupe_key) DO UPDATE SET dedupe_key=excluded.dedupe_key
 RETURNING id`, household, ref.RecipientID, shoppingNotificationKind, title, target, shoppingNoticeKey(ref)).Scan(&notification)
			if err != nil {
				return err
			}
			if audience.PushEligible {
				rows, e := tx.Query(ctx, "SELECT id FROM core.push_subscriptions WHERE household_id=$1 AND user_id=$2 AND disabled_at IS NULL ORDER BY id LIMIT 6", household, ref.RecipientID)
				if e != nil {
					return e
				}
				ids := []string{}
				for rows.Next() {
					var sid string
					if e = rows.Scan(&sid); e != nil {
						rows.Close()
						return e
					}
					ids = append(ids, sid)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				// Optional push corruption cannot roll back a valid in-app notice.
				// The push service retires stale devices; do not partially fan out
				// a six-row overflow to an arbitrary subset here.
				if len(ids) > 5 {
					ids = nil
				}
				for _, sid := range ids {
					push := ref
					push.NotificationID, push.SubscriptionID = notification, sid
					payload, e := json.Marshal(push)
					if e != nil {
						return e
					}
					_, e = tx.Exec(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload_version,payload,dedupe_key,max_attempts)
 VALUES($1,'shopping',$2,$3,1,$4,$5,5) ON CONFLICT(dedupe_key) DO NOTHING`, household, generation, ShoppingPushJobKind, payload, "shopping-push-v1/"+notification+"/"+sid)
					if e != nil {
						return e
					}
				}
			}
			reason = ""
		}
	} else {
		reason = "invalid_contract"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state='done',finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL,last_error=nullif($3,'')
 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>clock_timestamp()`, jobID, lease, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Store) shoppingNotificationRetention(ctx context.Context) error {
	// Only owned terminal namespaces. Removing old outbox rows also removes the
	// corresponding FK consumer receipts, preventing accidental reprocessing.
	rows, err := s.Pool.Query(ctx, "SELECT household_id FROM core.module_states WHERE module_id='shopping' ORDER BY household_id LIMIT 4")
	if err != nil {
		return err
	}
	households := []string{}
	for rows.Next() {
		var household string
		if err = rows.Scan(&household); err != nil {
			rows.Close()
			return err
		}
		households = append(households, household)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, household := range households {
		if err = s.shoppingRetainHousehold(ctx, household); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) shoppingRetainHousehold(ctx context.Context, household string) error {
	tx, err := audit.Begin(audit.WithHousehold(ctx, household), s.Pool)
	if err != nil {
		return err
	}
	defer shoppingRollback(tx)
	// Same maintenance barrier as mutations: never hold job locks while waiting
	// for a purge which already owns the household row and needs those jobs.
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return err
	}
	if err = shoppingExpireNotificationJobs(ctx, tx, household); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM core.jobs WHERE id IN(SELECT id FROM core.jobs WHERE household_id=$1 AND kind IN('shopping_digest_v1','shopping_push_v1') AND state IN('done','failed') AND finished_at<clock_timestamp()-interval '30 days' ORDER BY finished_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)`,
		`DELETE FROM core.notifications WHERE id IN(SELECT id FROM core.notifications WHERE household_id=$1 AND module_id='shopping' AND kind='shopping_list_changed_v1' AND created_at<clock_timestamp()-interval '30 days' ORDER BY created_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)`,
		`DELETE FROM core.outbox_events WHERE id IN(SELECT e.id FROM core.outbox_events e WHERE household_id=$1 AND module_id='shopping' AND event_type='record_changed' AND event_version=2 AND created_at<clock_timestamp()-interval '30 days' AND EXISTS(SELECT 1 FROM core.event_deliveries d WHERE d.event_id=e.id AND d.consumer_id='shopping-list-notifications-v1' AND d.state='done') AND NOT EXISTS(SELECT 1 FROM core.event_deliveries d WHERE d.event_id=e.id AND d.state<>'done') ORDER BY created_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)`,
	} {
		if _, err = tx.Exec(ctx, q, household); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func shoppingExpireNotificationJobs(ctx context.Context, tx pgx.Tx, household string) error {
	// Paused transport/configuration work has no future dispatch. Its fixed
	// authority lifetime still expires, without resurrecting a notification.
	rows, err := tx.Query(ctx, `SELECT id,CASE WHEN octet_length(payload::text)<=4096 THEN payload END,clock_timestamp()
 FROM core.jobs WHERE household_id=$1 AND kind IN('shopping_digest_v1','shopping_push_v1')
 AND(state IN('pending','paused') OR state='running' AND lease_until<clock_timestamp()) ORDER BY created_at,id LIMIT 100 FOR UPDATE SKIP LOCKED`, household)
	if err != nil {
		return err
	}
	type expiry struct {
		id     string
		reason string
	}
	expired := []expiry{}
	for rows.Next() {
		var id string
		var raw []byte
		var now time.Time
		if err = rows.Scan(&id, &raw, &now); err != nil {
			rows.Close()
			return err
		}
		var ref ShoppingNotificationReference
		if len(raw) == 0 || shoppingDecodeClosed(raw, &ref) != nil || !shoppingValidReference(ref) || ref.HouseholdID != household {
			expired = append(expired, expiry{id, "invalid_contract"})
		} else if !ref.ExpiresAt.After(now) {
			expired = append(expired, expiry{id, "notification_expired"})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range expired {
		if _, err = tx.Exec(ctx, `UPDATE core.jobs SET state='done',last_error=$2,finished_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND(state IN('pending','paused') OR state='running' AND lease_until<clock_timestamp())`, e.id, e.reason); err != nil {
			return err
		}
	}
	return nil
}
