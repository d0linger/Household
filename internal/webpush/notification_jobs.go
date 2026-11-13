package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/notification"
	"household/internal/secretbox"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type notificationJob struct {
	Schema     int    `json:"schema"`
	DeliveryID string `json:"delivery_id"`
}

type notificationClaim struct {
	job, lease, delivery, intentID string
	intent                         notification.Intent
	row                            storedSubscription
	data                           envelope
	attempts, maxAttempts          int
}

// ScheduleNotifications is bounded and restart-safe. Existing core.jobs owns
// every delivery lease; a duplicate scheduler cannot create a second device job.
func (s *Service) ScheduleNotifications(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.reconcileNotifications(ctx); err != nil {
		return err
	}
	if s.state != "ready" {
		return nil
	}
	rows, err := s.pool.Query(ctx, `WITH eligible AS NOT MATERIALIZED(SELECT i.id,i.category,i.not_before FROM core.notification_intents i WHERE i.state='pending'
 AND i.not_before<=clock_timestamp() AND i.expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM core.notification_preferences p WHERE p.household_id=i.household_id AND p.user_id=i.user_id
 AND p.category=i.category AND p.enabled AND p.enabled_since<=i.event_at)
 AND NOT EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id)
 AND EXISTS(SELECT 1 FROM core.notification_device_preferences p JOIN core.push_subscriptions s ON s.id=p.subscription_id
 WHERE p.household_id=i.household_id AND p.user_id=i.user_id AND p.category=i.category AND p.enabled AND NOT p.paused AND p.enabled_since<=i.event_at AND s.disabled_at IS NULL)),
 urgent AS(SELECT id,not_before FROM eligible WHERE category IN('cooking_timer','security','test') ORDER BY not_before,id LIMIT 24),
 ordinary AS(SELECT id,not_before FROM eligible WHERE category NOT IN('cooking_timer','security','test') ORDER BY not_before,id LIMIT 8)
 SELECT id::text FROM(SELECT * FROM urgent UNION ALL SELECT * FROM ordinary) selected ORDER BY not_before,id`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if err = s.stageNotification(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) stageNotification(ctx context.Context, intentID string) error {
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	i, err := store.ReadNotificationIntent(ctx, tx, intentID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, i.HouseholdID); err != nil {
		return err
	}
	eligible, cutoff, err := s.notificationAuthority(ctx, tx, i, "")
	if err != nil {
		return err
	}
	eligible = eligible && !cutoff.IsZero() && !cutoff.After(i.EventAt)
	var prefVersion int64
	var prefSince time.Time
	err = tx.QueryRow(ctx, `SELECT version,enabled_since FROM core.notification_preferences
	WHERE household_id=$1 AND user_id=$2 AND category=$3 AND enabled AND enabled_since<=$4 FOR SHARE`, i.HouseholdID, i.UserID, i.Category, i.EventAt).Scan(&prefVersion, &prefSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT clock_timestamp() FROM core.notification_intents WHERE id=$1 AND state='pending'
 AND not_before<=clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE SKIP LOCKED`, intentID).Scan(&now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !eligible || prefSince.After(cutoff) {
		_, err = tx.Exec(ctx, `UPDATE core.notification_intents SET state='cancelled',updated_at=clock_timestamp(),version=version+1 WHERE id=$1`, intentID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	// Stage quiet-period work as delayed jobs so it cannot monopolize the next
	// bounded scheduler page. Keep the source event/deadline immutable.
	availableAt := i.NotBefore
	next, err := s.store.NotificationNextAllowed(ctx, tx, i, now)
	if err != nil {
		return err
	}
	if next.After(now) {
		availableAt = next
		if availableAt.After(i.ExpiresAt) {
			availableAt = i.ExpiresAt
		}
	}
	rows, err := tx.Query(ctx, `SELECT s.id::text,p.version FROM core.push_subscriptions s
 JOIN core.notification_device_preferences p ON p.subscription_id=s.id AND p.household_id=s.household_id AND p.user_id=s.user_id
 WHERE s.household_id=$1 AND s.user_id=$2 AND s.disabled_at IS NULL AND p.category=$3 AND p.enabled AND NOT p.paused AND p.enabled_since<=$4
 AND($3<>'test' OR s.id=$5) ORDER BY s.created_at DESC,s.id LIMIT 128`, i.HouseholdID, i.UserID, i.Category, cutoff, i.ResourceID)
	if err != nil {
		return err
	}
	type device struct {
		id      string
		version int64
	}
	devices := []device{}
	for rows.Next() {
		var d device
		if err = rows.Scan(&d.id, &d.version); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	staged := 0
	for _, d := range devices {
		row, e := loadSubscription(ctx, tx, i.HouseholdID, i.UserID, d.id, false)
		if errors.Is(e, store.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		data, e := s.open(row)
		if errors.Is(e, secretbox.ErrKeyUnavailable) {
			return e
		}
		if e != nil || data.VAPIDKeyID != s.vapidID || !data.ExpiresAt.After(now) || data.ConsentAt.After(cutoff) {
			continue
		}
		current, e := currentEnvelope(ctx, tx, data)
		if e != nil {
			return e
		}
		if !current {
			continue
		}
		if i.Category == "expenses" {
			identity, valid, e := financeEnvelopeIdentity(ctx, tx, data)
			if e != nil {
				return e
			}
			if !valid {
				continue
			}
			actualCutoff, e := s.store.FinanceNotificationAuthority(ctx, tx, identity, i)
			if e != nil {
				return e
			}
			if actualCutoff.IsZero() || !actualCutoff.Equal(cutoff) {
				continue
			}
		}
		if _, e = s.createNotificationDelivery(ctx, tx, intentID, i, d.id, prefVersion, d.version, availableAt); e != nil {
			return e
		}
		staged++
		if staged >= DeviceLimit {
			break
		}
	}
	if staged == 0 {
		if _, err = tx.Exec(ctx, `UPDATE core.notification_intents SET state='cancelled',version=version+1,updated_at=clock_timestamp() WHERE id=$1`, intentID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) createNotificationDelivery(ctx context.Context, tx pgx.Tx, intentID string, i notification.Intent, subscription string, prefVersion, deviceVersion int64, availableAt time.Time) (string, error) {
	var delivery string
	err := tx.QueryRow(ctx, `INSERT INTO core.notification_deliveries(household_id,user_id,intent_id,subscription_id,preference_version,device_version)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(intent_id,subscription_id) DO NOTHING RETURNING id::text`, i.HouseholdID, i.UserID, intentID, subscription, prefVersion, deviceVersion).Scan(&delivery)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(notificationJob{Schema: 1, DeliveryID: delivery})
	if err != nil {
		return "", err
	}
	p, _ := notification.Lookup(i.Category)
	var job string
	err = tx.QueryRow(ctx, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload_version,payload,dedupe_key,available_at,max_attempts)
 VALUES($1,nullif($2,''),$3,$4,1,$5,$6,$7,$8) RETURNING id::text`, i.HouseholdID, i.Module, i.Generation, notification.JobKind, payload, "notification-delivery/"+delivery, availableAt, p.Attempts).Scan(&job)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE core.notification_deliveries SET job_id=$2 WHERE id=$1`, delivery, job)
	return delivery, err
}

func (s *Service) reconcileNotifications(ctx context.Context) error {
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, query := range []string{
		`WITH batch AS(SELECT i.id FROM core.notification_intents i WHERE i.state='pending'
 AND EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id)
 AND NOT EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id AND d.state='pending')
 ORDER BY i.not_before,i.id LIMIT 256 FOR UPDATE SKIP LOCKED)
 UPDATE core.notification_intents i SET state=CASE WHEN EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id AND d.state='accepted') THEN 'accepted' ELSE 'failed' END,
 updated_at=clock_timestamp(),version=version+1 FROM batch WHERE i.id=batch.id`,
		`WITH batch AS(SELECT i.id FROM core.notification_intents i WHERE i.state='pending' AND(i.expires_at<=clock_timestamp()
 OR NOT EXISTS(SELECT 1 FROM core.notification_preferences p WHERE p.household_id=i.household_id AND p.user_id=i.user_id AND p.category=i.category AND p.enabled AND p.enabled_since<=i.event_at)
 OR(i.module_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.module_states m WHERE m.household_id=i.household_id AND m.module_id=i.module_id AND m.state='enabled' AND m.data_generation=i.module_generation)))
 ORDER BY i.not_before,i.id LIMIT 256 FOR UPDATE SKIP LOCKED)
 UPDATE core.notification_intents i SET state=CASE WHEN expires_at<=clock_timestamp() THEN 'expired' ELSE 'cancelled' END,updated_at=clock_timestamp(),version=version+1 FROM batch WHERE i.id=batch.id`,
		`WITH batch AS(SELECT d.id,i.expires_at<=clock_timestamp() AS expired FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 LEFT JOIN core.jobs j ON j.id=d.job_id WHERE d.state='pending' AND(i.state IN('expired','cancelled','failed') OR j.state IN('failed','paused','done'))
 ORDER BY d.created_at,d.id LIMIT 256 FOR UPDATE OF d SKIP LOCKED)
 UPDATE core.notification_deliveries d SET state=CASE WHEN batch.expired THEN 'expired' ELSE 'cancelled' END,
 safe_code=CASE WHEN batch.expired THEN 'notification_expired' ELSE 'intent_or_job_ended' END,
 finished_at=clock_timestamp(),updated_at=clock_timestamp() FROM batch WHERE d.id=batch.id`,
		`WITH batch AS(SELECT j.id FROM core.jobs j JOIN core.notification_deliveries d ON d.job_id=j.id
 WHERE j.kind='notification_push_v1' AND j.state='pending' AND d.state<>'pending' ORDER BY j.available_at,j.id LIMIT 256 FOR UPDATE OF j SKIP LOCKED)
 UPDATE core.jobs j SET state='done',last_error='notification_retired',finished_at=clock_timestamp() FROM batch WHERE j.id=batch.id`,
		`DELETE FROM core.notification_intents WHERE id IN(SELECT id FROM core.notification_intents WHERE state<>'pending' AND updated_at<clock_timestamp()-interval '30 days' ORDER BY updated_at,id LIMIT 128)`,
		`DELETE FROM core.notifications WHERE id IN(SELECT id FROM core.notifications WHERE created_at<clock_timestamp()-interval '90 days' ORDER BY created_at,id LIMIT 128)`,
	} {
		if _, err = tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) SendNotificationJob(ctx context.Context, job, lease string) error {
	if s == nil || s.pool == nil || s.store == nil {
		return ErrUnavailable
	}
	if !canonicalUUID(job) || !canonicalUUID(lease) {
		return store.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	claim, valid, err := s.notificationPreflight(ctx, job, lease, true)
	if err != nil {
		return err
	}
	if !valid {
		return nil
	}
	payload, err := json.Marshal(pushPayload{Schema: 2, Kind: claim.intent.Category, ID: claim.delivery, ExpiresAt: claim.intent.ExpiresAt.Unix(), AudienceScope: claim.data.AuthScope})
	if err != nil {
		return err
	}
	var guardErr error
	result := s.transport.sendAuthorized(ctx, claim.data.Subscription, payload, digest(claim.delivery)[:32], func(ctx context.Context) error {
		current, ok, e := s.notificationPreflight(ctx, job, lease, false)
		if e != nil {
			guardErr = e
			return e
		}
		if !ok || !equalBytes(current.row.cipher, claim.row.cipher) {
			return store.ErrForbidden
		}
		return nil
	})
	if guardErr != nil {
		return guardErr
	}
	if result.status == 429 {
		endpoint, e := validateEndpoint(claim.data.Subscription.Endpoint)
		if e != nil {
			return e
		}
		if e = s.cooldown(ctx, endpoint.Hostname(), result.retryAt); e != nil {
			return e
		}
	}
	return s.finishNotificationAttempt(ctx, claim, result)
}

func (s *Service) notificationPreflight(ctx context.Context, job, lease string, reserve bool) (notificationClaim, bool, error) {
	c := notificationClaim{job: job, lease: lease}
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return c, false, err
	}
	defer rollback(tx)
	var raw []byte
	var household string
	var generation int64
	var module *string
	err = tx.QueryRow(ctx, `SELECT household_id::text FROM core.jobs WHERE id=$1 AND kind=$2`, job, notification.JobKind).Scan(&household)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return c, false, err
	}
	err = tx.QueryRow(ctx, `SELECT household_id::text,module_id,module_generation,payload,attempts,max_attempts FROM core.jobs
 WHERE id=$1 AND kind=$3 AND payload_version=1 AND state='running' AND lease_token=$2 AND lease_until>clock_timestamp()
 AND octet_length(payload::text)<=1024 FOR UPDATE`, job, lease, notification.JobKind).Scan(&household, &module, &generation, &raw, &c.attempts, &c.maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	var ref notificationJob
	if strictJSON(raw, &ref) != nil || ref.Schema != 1 || !canonicalUUID(ref.DeliveryID) {
		return c, false, s.endNotification(ctx, tx, c, "failed", "invalid_reference", time.Time{})
	}
	c.delivery = ref.DeliveryID
	var subscription string
	var prefVersion, deviceVersion int64
	err = tx.QueryRow(ctx, `SELECT intent_id::text,subscription_id::text,preference_version,device_version FROM core.notification_deliveries
 WHERE id=$1 AND job_id=$2 AND household_id=$3 AND state='pending' FOR UPDATE`, c.delivery, job, household).Scan(&c.intentID, &subscription, &prefVersion, &deviceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "delivery_ended", time.Time{})
	}
	if err != nil {
		return c, false, err
	}
	c.intent, err = store.ReadNotificationIntent(ctx, tx, c.intentID)
	if errors.Is(err, store.ErrNotFound) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "intent_missing", time.Time{})
	}
	if err != nil {
		return c, false, err
	}
	i := c.intent
	if i.HouseholdID != household || i.Generation != generation || (module == nil) != (i.Module == "") || (module != nil && *module != i.Module) {
		return c, false, s.endNotification(ctx, tx, c, "failed", "invalid_authority", time.Time{})
	}
	var state string
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT state,clock_timestamp() FROM core.notification_intents WHERE id=$1`, c.intentID).Scan(&state, &now); err != nil {
		return c, false, err
	}
	if !i.ExpiresAt.After(now) {
		return c, false, s.endNotification(ctx, tx, c, "expired", "notification_expired", time.Time{})
	}
	if state != "pending" {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "intent_or_job_ended", time.Time{})
	}
	if s.state != "ready" {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "push_not_configured", time.Time{})
	}
	var consent bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category=$3 AND enabled AND version=$4 AND enabled_since<=$5`, i.HouseholdID, i.UserID, i.Category, prefVersion, i.EventAt).Scan(&consent)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "account_consent_changed", time.Time{})
	}
	if err != nil {
		return c, false, err
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_device_preferences WHERE subscription_id=$1
 AND household_id=$2 AND user_id=$3 AND category=$4 AND enabled AND NOT paused AND version=$5 AND enabled_since<=$6)`, subscription, i.HouseholdID, i.UserID, i.Category, deviceVersion, i.EventAt).Scan(&consent)
	if err != nil {
		return c, false, err
	}
	if !consent {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "device_consent_changed", time.Time{})
	}
	eligible, cutoff, err := s.notificationAuthority(ctx, tx, i, subscription)
	if err != nil {
		return c, false, err
	}
	if !eligible {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "source_superseded", time.Time{})
	}
	if cutoff.IsZero() || cutoff.After(i.EventAt) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "source_superseded", time.Time{})
	}
	if cutoff.Before(i.EventAt) {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_preferences p
 JOIN core.notification_device_preferences d ON d.household_id=p.household_id AND d.user_id=p.user_id AND d.category=p.category
 WHERE p.household_id=$1 AND p.user_id=$2 AND p.category=$3 AND p.enabled AND p.version=$4 AND p.enabled_since<=$5
 AND d.subscription_id=$6 AND d.enabled AND NOT d.paused AND d.version=$7 AND d.enabled_since<=$5)`,
			i.HouseholdID, i.UserID, i.Category, prefVersion, cutoff, subscription, deviceVersion).Scan(&consent)
		if err != nil {
			return c, false, err
		}
		if !consent {
			return c, false, s.endNotification(ctx, tx, c, "cancelled", "source_consent_changed", time.Time{})
		}
	}
	next, err := s.store.NotificationNextAllowed(ctx, tx, i, now)
	if err != nil {
		return c, false, err
	}
	if next.After(now) {
		return c, false, s.endNotification(ctx, tx, c, "pending", "quiet_hours", next)
	}
	if now.Before(i.NotBefore) {
		return c, false, s.endNotification(ctx, tx, c, "pending", "not_due", i.NotBefore)
	}
	c.row, err = loadSubscription(ctx, tx, i.HouseholdID, i.UserID, subscription, false)
	if errors.Is(err, store.ErrNotFound) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "connection_missing", time.Time{})
	}
	if err != nil {
		return c, false, err
	}
	c.data, err = s.open(c.row)
	if errors.Is(err, secretbox.ErrKeyUnavailable) {
		return c, false, err
	}
	if err != nil || c.row.disabled != nil || c.data.VAPIDKeyID != s.vapidID || !c.data.ExpiresAt.After(now) || c.data.ConsentAt.After(cutoff) {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "connection_superseded", time.Time{})
	}
	id := auth.Identity{UserID: c.data.UserID, HouseholdID: c.data.HouseholdID, SessionID: c.data.SessionID, AuthVersion: c.data.AuthVersion, Role: c.data.Role}
	if err = auth.LockSession(ctx, tx, id); err != nil {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "registering_session_ended", time.Time{})
	}
	current, err := currentEnvelope(ctx, tx, c.data)
	if err != nil {
		return c, false, err
	}
	if !current {
		return c, false, s.endNotification(ctx, tx, c, "cancelled", "registering_authority_ended", time.Time{})
	}
	if i.Category == "expenses" {
		identity, valid, e := financeEnvelopeIdentity(ctx, tx, c.data)
		if e != nil {
			return c, false, e
		}
		if !valid {
			return c, false, s.endNotification(ctx, tx, c, "cancelled", "registering_authority_ended", time.Time{})
		}
		actualCutoff, e := s.store.FinanceNotificationAuthority(ctx, tx, identity, i)
		if e != nil {
			return c, false, e
		}
		if actualCutoff.IsZero() || !actualCutoff.Equal(cutoff) {
			return c, false, s.endNotification(ctx, tx, c, "cancelled", "source_superseded", time.Time{})
		}
	}
	if reserve {
		endpoint, e := validateEndpoint(c.data.Subscription.Endpoint)
		if e != nil {
			return c, false, s.endNotification(ctx, tx, c, "cancelled", "invalid_endpoint", time.Time{})
		}
		next, e := s.reserveSend(ctx, tx, c.data.UserID, endpoint.Hostname())
		if errors.Is(e, ErrLimited) {
			return c, false, s.endNotification(ctx, tx, c, "pending", "send_budget_limited", next)
		}
		if e != nil {
			return c, false, e
		}
		if _, err = tx.Exec(ctx, `UPDATE core.notification_deliveries SET attempts=$2,last_attempt_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, c.delivery, c.attempts); err != nil {
			return c, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return c, false, err
	}
	return c, true, nil
}

func (s *Service) endNotification(ctx context.Context, tx pgx.Tx, c notificationClaim, state, code string, next time.Time) error {
	if state == "pending" && (c.attempts >= c.maxAttempts || !next.Before(c.intent.ExpiresAt)) {
		state = "expired"
		code = "retry_window_exhausted"
	}
	jobState := "done"
	var available any
	switch state {
	case "pending":
		jobState = "pending"
		available = next
	case "failed":
		jobState = "failed"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,last_error=$4,available_at=coalesce($5::timestamptz,available_at),
 finished_at=CASE WHEN $3='pending' THEN NULL ELSE clock_timestamp() END,lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, c.job, c.lease, jobState, code, available)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return store.ErrConflict
	}
	if c.delivery != "" {
		_, err = tx.Exec(ctx, `UPDATE core.notification_deliveries SET state=$2,safe_code=$3,
 accepted_at=CASE WHEN $2='accepted' THEN clock_timestamp() ELSE accepted_at END,
 finished_at=CASE WHEN $2='pending' THEN NULL ELSE clock_timestamp() END,updated_at=clock_timestamp()
 WHERE id=$1 AND job_id=$4 AND state='pending'`, c.delivery, state, code, c.job)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) finishNotificationAttempt(ctx context.Context, c notificationClaim, result outcome) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var owned bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.jobs WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>clock_timestamp() FOR UPDATE`, c.job, c.lease).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.code == "connection_expired" {
		_, err = tx.Exec(ctx, `UPDATE core.push_subscriptions SET disabled_at=coalesce(disabled_at,clock_timestamp()) WHERE id=$1 AND encrypted_subscription=$2 AND encryption_format=$3 AND encryption_key_id=$4`, c.row.id, c.row.cipher, c.row.format, c.row.keyID)
		if err != nil {
			return err
		}
	}
	switch result.code {
	case "gateway_accepted":
		return s.endNotification(ctx, tx, c, "accepted", result.code, time.Time{})
	case "gateway_limited":
		return s.endNotification(ctx, tx, c, "pending", result.code, result.retryAt)
	case "network_timeout", "network_uncertain", "gateway_temporary", "response_bound":
		delay := retryDelay(c.attempts, c.delivery)
		if c.intent.Category == "cooking_timer" || c.intent.Category == "test" {
			delay = 5*time.Second + time.Duration(c.delivery[0]%5)*time.Second
		}
		return s.endNotification(ctx, tx, c, "pending", result.code, time.Now().Add(delay))
	case "authority_superseded":
		return s.endNotification(ctx, tx, c, "cancelled", result.code, time.Time{})
	default:
		return s.endNotification(ctx, tx, c, "failed", result.code, time.Time{})
	}
}

func (s *Service) QueueTest(ctx context.Context, id auth.Identity, subscription string) (DeliveryView, error) {
	if s == nil || s.state != "ready" {
		return DeliveryView{}, ErrUnavailable
	}
	if !canonicalUUID(subscription) {
		return DeliveryView{}, store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.managementBudget(ctx, id, "notification-device-test-v1", 3); err != nil {
		return DeliveryView{}, err
	}
	tx, err := s.begin(ctx, id)
	if err != nil {
		return DeliveryView{}, err
	}
	defer rollback(tx)
	// The explicit test action grants only the test category. Module choices are
	// untouched. A new test supersedes an earlier unprocessed test from this user.
	var prefVersion, deviceVersion int64
	err = tx.QueryRow(ctx, `INSERT INTO core.notification_preferences(household_id,user_id,category,enabled,timezone,enabled_since)
 VALUES($1,$2,'test',true,'UTC',clock_timestamp()) ON CONFLICT(household_id,user_id,category) DO UPDATE SET enabled=true,
 version=core.notification_preferences.version+1,enabled_since=clock_timestamp(),updated_at=clock_timestamp(),quiet_start=NULL,quiet_end=NULL RETURNING version`, id.HouseholdID, id.UserID).Scan(&prefVersion)
	if err != nil {
		return DeliveryView{}, err
	}
	row, err := loadSubscription(ctx, tx, id.HouseholdID, id.UserID, subscription, true)
	if err != nil {
		return DeliveryView{}, err
	}
	data, err := s.open(row)
	if err != nil {
		return DeliveryView{}, err
	}
	current, err := currentEnvelope(ctx, tx, data)
	if err != nil {
		return DeliveryView{}, err
	}
	if !current || row.disabled != nil || data.VAPIDKeyID != s.vapidID || !data.ExpiresAt.After(time.Now()) {
		return DeliveryView{}, store.ErrConflict
	}
	err = tx.QueryRow(ctx, `INSERT INTO core.notification_device_preferences(subscription_id,household_id,user_id,category,enabled,enabled_since)
 VALUES($1,$2,$3,'test',true,clock_timestamp()) ON CONFLICT(subscription_id,category) DO UPDATE SET enabled=true,paused=false,
 version=core.notification_device_preferences.version+1,enabled_since=clock_timestamp(),updated_at=clock_timestamp() RETURNING version`, subscription, id.HouseholdID, id.UserID).Scan(&deviceVersion)
	if err != nil {
		return DeliveryView{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return DeliveryView{}, err
	}
	i := notification.Intent{Schema: 1, HouseholdID: id.HouseholdID, UserID: id.UserID, ResourceType: "push_subscriptions", ResourceID: subscription, Revision: 1, Occurrence: value.UUID(), Category: "test", EventAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute)}
	intent, err := store.EnqueueNotificationIntent(ctx, tx, i)
	if err != nil {
		return DeliveryView{}, err
	}
	if intent == "" {
		return DeliveryView{}, store.ErrConflict
	}
	delivery, err := s.createNotificationDelivery(ctx, tx, intent, i, subscription, prefVersion, deviceVersion, i.NotBefore)
	if err != nil {
		return DeliveryView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DeliveryView{}, err
	}
	return DeliveryView{ID: delivery, State: "pending", UpdatedAt: now}, nil
}
