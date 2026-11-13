package webpush

import (
	"context"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/notification"
	"household/internal/secretbox"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

// Financial reads use an actual opted-in device's registering session. This
// does not renew that session or give background work recurring-write authority.
func (s *Service) financeNotificationIdentity(ctx context.Context, tx pgx.Tx, household, user, subscription string) (auth.Identity, bool, error) {
	rows, err := tx.Query(ctx, `SELECT s.id::text FROM core.push_subscriptions s
 JOIN core.notification_device_preferences p ON p.subscription_id=s.id AND p.household_id=s.household_id AND p.user_id=s.user_id
 WHERE s.household_id=$1 AND s.user_id=$2 AND s.disabled_at IS NULL AND p.category='expenses' AND p.enabled AND NOT p.paused
 AND(nullif($3,'')::uuid IS NULL OR s.id=nullif($3,'')::uuid) ORDER BY s.created_at DESC,s.id LIMIT $4`, household, user, subscription, DeviceLimit)
	if err != nil {
		return auth.Identity{}, false, err
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
		return auth.Identity{}, false, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return auth.Identity{}, false, err
	}
	for _, key := range ids {
		row, e := loadSubscription(ctx, tx, household, user, key, false)
		if errors.Is(e, store.ErrNotFound) {
			continue
		}
		if e != nil {
			return auth.Identity{}, false, e
		}
		data, e := s.open(row)
		if errors.Is(e, secretbox.ErrKeyUnavailable) {
			return auth.Identity{}, false, e
		}
		if e != nil || row.disabled != nil || data.VAPIDKeyID != s.vapidID || !data.ExpiresAt.After(now) {
			continue
		}
		id, current, e := financeEnvelopeIdentity(ctx, tx, data)
		if e != nil {
			return auth.Identity{}, false, e
		}
		if current {
			return id, true, nil
		}
	}
	return auth.Identity{}, false, nil
}

func financeEnvelopeIdentity(ctx context.Context, tx pgx.Tx, data envelope) (auth.Identity, bool, error) {
	id := auth.Identity{UserID: data.UserID, HouseholdID: data.HouseholdID, SessionID: data.SessionID, AuthVersion: data.AuthVersion, Role: data.Role}
	current, err := currentEnvelope(ctx, tx, data)
	if err != nil || !current {
		return id, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id::text FROM core.memberships WHERE household_id=$1 AND user_id=$2 AND role=$3 AND inactive_at IS NULL`, id.HouseholdID, id.UserID, id.Role).Scan(&id.MembershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return id, false, nil
	}
	return id, err == nil, err
}

// notificationAuthority keeps private finance out of the generic membership
// guard. Claim/pre-send passes its exact subscription; staging validates every
// selected subscription again before creating its delivery.
func (s *Service) notificationAuthority(ctx context.Context, tx pgx.Tx, i notification.Intent, subscription string) (bool, time.Time, error) {
	if i.Category == "expenses" {
		id, current, err := s.financeNotificationIdentity(ctx, tx, i.HouseholdID, i.UserID, subscription)
		if err != nil || !current {
			return false, time.Time{}, err
		}
		cutoff, err := s.store.FinanceNotificationAuthority(ctx, tx, id, i)
		return !cutoff.IsZero(), cutoff, err
	}
	allowed, err := s.store.NotificationAudience(ctx, tx, i)
	if err != nil || !allowed {
		return false, time.Time{}, err
	}
	cutoff, err := s.store.NotificationConsentCutoff(ctx, tx, i)
	return !cutoff.IsZero(), cutoff, err
}

// StageFinanceNotifications advances one user's durable page in a consistent
// snapshot. Only current, explicitly enabled device sessions can supply identity.
func (s *Service) StageFinanceNotifications(ctx context.Context, limit int) (int, error) {
	if s == nil || s.state != "ready" {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	var household, user string
	err := s.pool.QueryRow(ctx, `SELECT p.household_id::text,p.user_id::text FROM core.notification_preferences p
 JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='expenses' AND m.state='enabled'
 JOIN core.memberships member ON member.household_id=p.household_id AND member.user_id=p.user_id AND member.inactive_at IS NULL
 JOIN core.users u ON u.id=p.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password
 LEFT JOIN core.jobs j ON j.dedupe_key='finance-scan-v1/'||p.household_id::text||'/'||p.user_id::text||'/'||m.data_generation::text
 WHERE p.category='expenses' AND p.enabled
 AND(j.id IS NULL OR(j.kind=$1 AND j.state='pending' AND j.available_at<=clock_timestamp()))
 AND EXISTS(SELECT 1 FROM core.notification_device_preferences d JOIN core.push_subscriptions s ON s.id=d.subscription_id
 WHERE d.household_id=p.household_id AND d.user_id=p.user_id AND d.category='expenses' AND d.enabled AND NOT d.paused AND s.disabled_at IS NULL)
 ORDER BY j.available_at NULLS FIRST,p.household_id,p.user_id LIMIT 1`, store.FinanceNotificationScanJobKind).Scan(&household, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if err = audit.Bind(audit.WithHousehold(ctx, household), tx); err != nil {
		return 0, err
	}
	id, current, err := s.financeNotificationIdentity(ctx, tx, household, user, "")
	if err != nil {
		return 0, err
	}
	if !current {
		// Invalid encrypted/expired sessions must not starve another household.
		if err = s.store.DeferFinanceNotificationScan(ctx, tx, household, user); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}
	worked, err := s.store.StageFinanceNotificationPage(ctx, tx, id, limit)
	if err != nil {
		rollback(tx)
		return 0, errors.Join(err, s.deferFailedFinanceScan(ctx, household, user))
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, errors.Join(err, s.deferFailedFinanceScan(ctx, household, user))
	}
	return worked, nil
}

func (s *Service) deferFailedFinanceScan(ctx context.Context, household, user string) error {
	cleanup, cancel := context.WithTimeout(audit.WithHousehold(audit.WithSystem(context.WithoutCancel(ctx), "worker"), household), 3*time.Second)
	defer cancel()
	tx, err := audit.Begin(cleanup, s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = s.store.DeferFinanceNotificationScan(cleanup, tx, household, user); err != nil {
		return err
	}
	return tx.Commit(cleanup)
}
