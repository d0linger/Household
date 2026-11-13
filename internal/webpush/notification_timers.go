package webpush

import (
	"context"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/secretbox"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

// ScheduleCookingTimers only scans due timer intents. Expired and obsolete work
// remains subject to the ordinary bounded reconciliation/retention sweep.
func (s *Service) ScheduleCookingTimers(ctx context.Context) error {
	if s == nil || s.pool == nil || s.state != "ready" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT i.id::text FROM core.notification_intents i
 WHERE i.state='pending' AND i.category='cooking_timer' AND i.not_before<=clock_timestamp() AND i.expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM core.notification_preferences p WHERE p.household_id=i.household_id AND p.user_id=i.user_id
 AND p.category=i.category AND p.enabled AND p.enabled_since<=i.event_at)
 AND NOT EXISTS(SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id)
 AND EXISTS(SELECT 1 FROM core.notification_device_preferences p JOIN core.push_subscriptions s ON s.id=p.subscription_id
 WHERE p.household_id=i.household_id AND p.user_id=i.user_id AND p.category=i.category AND p.enabled AND NOT p.paused
 AND p.enabled_since<=i.event_at AND s.disabled_at IS NULL)
 ORDER BY i.not_before,i.id LIMIT 24`)
	if err != nil {
		return err
	}
	ids := make([]string, 0, 24)
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

type CookingDeliveryContext struct {
	ID        string    `json:"id"`
	AuthScope string    `json:"auth_scope"`
	TimerID   string    `json:"timer_id"`
	SessionID string    `json:"session_id"`
	Revision  int64     `json:"revision"`
	Deadline  time.Time `json:"deadline"`
	ServerNow time.Time `json:"server_now"`
}

// CookingDelivery binds a foreground handshake to the registering session and
// current source. It never acknowledges a timer or claims an OS notification was
// displayed. Pending delivery is permitted for receipt-before-response races.
func (s *Service) CookingDelivery(ctx context.Context, id auth.Identity, delivery string) (CookingDeliveryContext, error) {
	v := CookingDeliveryContext{}
	if !canonicalUUID(delivery) || s == nil || s.state != "ready" {
		return v, store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	tx, err := s.begin(ctx, id)
	if err != nil {
		return v, err
	}
	defer rollback(tx)
	var intent, subscription string
	var preferenceVersion, deviceVersion int64
	err = tx.QueryRow(ctx, `SELECT d.intent_id::text,d.subscription_id::text,d.preference_version,d.device_version,clock_timestamp()
 FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE d.id=$1 AND d.household_id=$2 AND d.user_id=$3 AND d.state IN('pending','accepted')
 AND i.category='cooking_timer' AND i.state IN('pending','accepted') AND i.not_before<=clock_timestamp() AND i.expires_at>clock_timestamp()`,
		delivery, id.HouseholdID, id.UserID).Scan(&intent, &subscription, &preferenceVersion, &deviceVersion, &v.ServerNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, store.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	i, err := store.ReadNotificationIntent(ctx, tx, intent)
	if err != nil {
		return v, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_preferences p
 JOIN core.notification_device_preferences d ON d.household_id=p.household_id AND d.user_id=p.user_id AND d.category=p.category
 WHERE p.household_id=$1 AND p.user_id=$2 AND p.category='cooking_timer' AND p.enabled AND p.version=$3 AND p.enabled_since<=$4
 AND d.subscription_id=$5 AND d.enabled AND NOT d.paused AND d.version=$6 AND d.enabled_since<=$4)`,
		id.HouseholdID, id.UserID, preferenceVersion, i.EventAt, subscription, deviceVersion).Scan(&allowed)
	if err != nil {
		return v, err
	}
	if !allowed {
		return v, store.ErrNotFound
	}
	row, err := loadSubscription(ctx, tx, id.HouseholdID, id.UserID, subscription, false)
	if err != nil {
		return v, err
	}
	data, err := s.open(row)
	if errors.Is(err, secretbox.ErrKeyUnavailable) {
		return v, err
	}
	scope := personal(id)
	if err != nil || row.disabled != nil || data.SessionID != id.SessionID || data.AuthScope != scope.AuthScope ||
		data.VAPIDKeyID != s.vapidID || !data.ExpiresAt.After(v.ServerNow) || data.ConsentAt.After(i.EventAt) {
		return v, store.ErrNotFound
	}
	next, err := s.store.NotificationNextAllowed(ctx, tx, i, v.ServerNow)
	if err != nil {
		return v, err
	}
	if next.After(v.ServerNow) {
		return v, store.ErrNotFound
	}
	details, err := s.store.CookingNotificationDetails(ctx, tx, i)
	if err != nil {
		return v, err
	}
	v.ID, v.AuthScope = delivery, scope.AuthScope
	v.TimerID, v.SessionID, v.Revision, v.Deadline = details.TimerID, details.SessionID, details.Revision, details.Deadline
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&v.ServerNow); err != nil {
		return CookingDeliveryContext{}, err
	}
	if v.ServerNow.Before(i.NotBefore) || !i.ExpiresAt.After(v.ServerNow) {
		return CookingDeliveryContext{}, store.ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return CookingDeliveryContext{}, err
	}
	return v, nil
}
