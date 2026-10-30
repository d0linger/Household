package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type sendClaim struct {
	job, lease            string
	ref                   store.ShoppingNotificationReference
	row                   storedSubscription
	data                  envelope
	attempts, maxAttempts int
}

func validSendReference(ref store.ShoppingNotificationReference, household string, generation int64, now time.Time) bool {
	validIDs := canonicalUUID(ref.HouseholdID) && canonicalUUID(ref.RecipientID) && canonicalUUID(ref.ListID) && canonicalUUID(ref.NotificationID) && canonicalUUID(ref.SubscriptionID)
	validTime := !ref.EventAt.IsZero() && !ref.EventAt.After(now) && ref.ExpiresAt.After(now) && !ref.ExpiresAt.After(now.Add(24*time.Hour))
	return ref.Schema == 1 && validIDs && ref.HouseholdID == household && ref.Generation == generation && generation > 0 && ref.Bucket > 0 && validTime && ref.WatchRevision != "" && !ref.PushEnabledSince.IsZero()
}

func (s *Service) SendJob(ctx context.Context, jobID, lease string) error {
	if s == nil || s.pool == nil || s.store == nil || s.auth == nil {
		return ErrUnavailable
	}
	if !value.ValidUUID(jobID) || !value.ValidUUID(lease) {
		return store.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var household string
	err := s.pool.QueryRow(ctx, "SELECT household_id::text FROM core.jobs WHERE id=$1 AND kind=$2", jobID, store.ShoppingPushJobKind).Scan(&household)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return err
	}
	claim := sendClaim{job: jobID, lease: lease}
	var raw []byte
	var generation int64
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=4096 THEN payload END,module_generation,attempts,max_attempts,clock_timestamp()
 FROM core.jobs WHERE id=$1 AND household_id=$2 AND module_id='shopping' AND kind=$3 AND payload_version=1
 AND state='running' AND lease_token=$4 AND lease_until>clock_timestamp() FOR UPDATE`, jobID, household, store.ShoppingPushJobKind, lease).Scan(&raw, &generation, &claim.attempts, &claim.maxAttempts, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 || strictJSON(raw, &claim.ref) != nil || !validSendReference(claim.ref, household, generation, now) {
		return finishJob(ctx, tx, claim, "done", "invalid_or_expired_reference", time.Time{})
	}
	if s.state != "ready" {
		return finishJob(ctx, tx, claim, "done", "push_not_configured", time.Time{})
	}
	audience, err := s.store.ShoppingNotificationAudience(ctx, tx, claim.ref)
	if err != nil {
		return err
	}
	if !audience.Eligible || !audience.PushEligible {
		return finishJob(ctx, tx, claim, "done", "superseded_authority", time.Time{})
	}
	err = tx.QueryRow(ctx, `SELECT id::text,household_id::text,user_id::text,endpoint_hash,encrypted_subscription,encryption_key_id,disabled_at,created_at
 FROM core.push_subscriptions WHERE id=$1 AND household_id=$2 AND user_id=$3 AND octet_length(encrypted_subscription)<=$4
 FOR SHARE`, claim.ref.SubscriptionID, household, claim.ref.RecipientID, maxCipher).Scan(&claim.row.id, &claim.row.household, &claim.row.user, &claim.row.hash, &claim.row.cipher, &claim.row.keyID, &claim.row.disabled, &claim.row.created)
	if errors.Is(err, pgx.ErrNoRows) {
		return finishJob(ctx, tx, claim, "done", "connection_missing", time.Time{})
	}
	if err != nil {
		return err
	}
	claim.data, err = s.open(claim.row)
	if err != nil || claim.row.disabled != nil || claim.data.VAPIDKeyID != s.vapidID || !claim.data.ExpiresAt.After(now) || claim.data.ConsentAt.After(claim.ref.EventAt) {
		return finishJob(ctx, tx, claim, "done", "connection_superseded", time.Time{})
	}
	identity := auth.Identity{UserID: claim.data.UserID, HouseholdID: claim.data.HouseholdID, SessionID: claim.data.SessionID, AuthVersion: claim.data.AuthVersion, Role: claim.data.Role}
	if err = auth.LockSession(ctx, tx, identity); err != nil {
		return finishJob(ctx, tx, claim, "done", "registering_session_ended", time.Time{})
	}
	current, err := currentEnvelope(ctx, tx, claim.data)
	if err != nil {
		return err
	}
	if !current {
		return finishJob(ctx, tx, claim, "done", "registering_authority_ended", time.Time{})
	}
	endpoint, _ := validateEndpoint(claim.data.Subscription.Endpoint)
	next, err := s.reserveSend(ctx, tx, claim.data.UserID, endpoint.Hostname())
	if errors.Is(err, ErrLimited) {
		return finishJob(ctx, tx, claim, "pending", "send_budget_limited", next)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE core.jobs SET lease_until=greatest(lease_until,clock_timestamp()+interval '15 seconds') WHERE id=$1 AND state='running' AND lease_token=$2`, jobID, lease); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Authorization and reservations are committed before any vendor request.
	payload, err := json.Marshal(pushPayload{Schema: 1, Kind: "shopping_notice", ID: claim.ref.NotificationID, ExpiresAt: claim.ref.ExpiresAt.Unix(), AudienceScope: claim.data.AuthScope})
	if err != nil {
		return errors.New("encode fixed push payload failed")
	}
	result := s.transport.send(ctx, claim.data.Subscription, payload, digest(claim.ref.NotificationID + "/" + claim.ref.SubscriptionID)[:32])
	var cooldownErr error
	if result.status == 429 {
		cooldownErr = s.cooldown(ctx, endpoint.Hostname(), result.retryAt)
	}
	finishErr := s.finishAttempt(ctx, claim, result)
	if cooldownErr != nil {
		return errors.Join(errors.New("persist push cooldown failed"), finishErr)
	}
	return finishErr
}

// finishJob is the only completion path, and always fences the actual lease.
func finishJob(ctx context.Context, tx pgx.Tx, claim sendClaim, state, reason string, next time.Time) error {
	if state == "pending" && claim.attempts >= claim.maxAttempts {
		state = "failed"
		reason = "attempt_budget_exhausted"
	}
	if state == "pending" && (!next.Before(claim.ref.ExpiresAt) || !next.After(time.Now())) {
		state = "done"
		reason = "notification_expired"
	}
	var available any
	if state == "pending" {
		available = next
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,last_error=$4,
 available_at=coalesce($5::timestamptz,available_at),finished_at=CASE WHEN $3='pending' THEN NULL ELSE clock_timestamp() END,
 lease_token=NULL,lease_until=NULL WHERE id=$1 AND state='running' AND lease_token=$2 AND lease_until>clock_timestamp()`, claim.job, claim.lease, state, reason, available)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return store.ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Service) finishAttempt(ctx context.Context, claim sendClaim, result outcome) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", claim.ref.HouseholdID); err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.jobs WHERE id=$1 AND module_id='shopping' AND module_generation=$2
 AND kind=$3 AND state='running' AND lease_token=$4 AND lease_until>clock_timestamp() FOR UPDATE`, claim.job, claim.ref.Generation, store.ShoppingPushJobKind, claim.lease).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	audience, err := s.store.ShoppingNotificationAudience(ctx, tx, claim.ref)
	if err != nil {
		return err
	}
	current, err := currentEnvelope(ctx, tx, claim.data)
	if err != nil {
		return err
	}
	if !audience.PushEligible || !current {
		return finishJob(ctx, tx, claim, "done", "superseded_after_attempt", time.Time{})
	}
	identity := auth.Identity{UserID: claim.data.UserID, HouseholdID: claim.data.HouseholdID, SessionID: claim.data.SessionID, AuthVersion: claim.data.AuthVersion, Role: claim.data.Role}
	if err = auth.LockSession(ctx, tx, identity); err != nil {
		return finishJob(ctx, tx, claim, "done", "registering_session_ended", time.Time{})
	}
	var unchanged bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.push_subscriptions WHERE id=$1 AND household_id=$2
 AND user_id=$3 AND endpoint_hash=$4 AND encrypted_subscription=$5 AND disabled_at IS NULL)`, claim.row.id, claim.row.household, claim.row.user, claim.row.hash, claim.row.cipher).Scan(&unchanged); err != nil {
		return err
	}
	if !unchanged {
		return finishJob(ctx, tx, claim, "done", "connection_replaced_after_attempt", time.Time{})
	}
	if result.code == "connection_expired" {
		// An old HTTP result must never disable a newly registered replacement.
		if _, err = tx.Exec(ctx, `UPDATE core.push_subscriptions SET disabled_at=coalesce(disabled_at,clock_timestamp())
 WHERE id=$1 AND household_id=$2 AND user_id=$3 AND endpoint_hash=$4 AND encrypted_subscription=$5`, claim.row.id, claim.row.household, claim.row.user, claim.row.hash, claim.row.cipher); err != nil {
			return err
		}
		return finishJob(ctx, tx, claim, "done", "connection_expired", time.Time{})
	}
	switch result.code {
	case "gateway_accepted":
		return finishJob(ctx, tx, claim, "done", "gateway_accepted", time.Time{})
	case "vapid_rejected":
		return finishJob(ctx, tx, claim, "paused", "vapid_rejected", time.Time{})
	case "gateway_limited":
		return finishJob(ctx, tx, claim, "pending", "gateway_limited", result.retryAt)
	case "network_timeout", "network_uncertain", "gateway_temporary", "response_bound":
		return finishJob(ctx, tx, claim, "pending", result.code, time.Now().Add(retryDelay(claim.attempts, claim.job)))
	default:
		return finishJob(ctx, tx, claim, "done", "gateway_refused", time.Time{})
	}
}

func retryDelay(attempt int, key string) time.Duration {
	delays := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	// Stable per-job jitter survives restarts and needs no unbounded RNG retry.
	hash := digest(key)
	jitter := time.Duration(hash[0]%16) * time.Second
	return delays[attempt-1] + jitter
}
