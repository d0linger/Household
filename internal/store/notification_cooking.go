package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// CookingNotificationContext identifies the current timer without disclosing
// recipe names, timer labels or other cooking content to the push channel.
type CookingNotificationContext struct {
	TimerID   string    `json:"timer_id"`
	SessionID string    `json:"session_id"`
	Revision  int64     `json:"revision"`
	Deadline  time.Time `json:"deadline"`
}

func cookingNotificationOccurrence(deadline time.Time) string {
	return "deadline/" + strconv.FormatInt(deadline.UnixMicro(), 10)
}

// The caller holds account/module authority. Consent precedes session/timer
// locks so replacing a timer cannot invert the preference writer's lock order.
func lockCookingNotificationConsent(ctx context.Context, tx pgx.Tx, id auth.Identity) error {
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category='cooking_timer' FOR SHARE`,
		id.HouseholdID, id.UserID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func enqueueCookingNotification(
	ctx context.Context, tx pgx.Tx, id auth.Identity, generation int64, timer CookingTimer, eventAt time.Time,
) error {
	if timer.State != "running" || timer.Deadline == nil {
		return ErrConflict
	}
	i := notification.Intent{
		Schema: notification.Schema, HouseholdID: id.HouseholdID, UserID: id.UserID,
		Module: "recipes", Generation: generation, Category: "cooking_timer",
		ResourceType: "cooking_timers", ResourceID: timer.ID, Revision: timer.Version,
		Occurrence: cookingNotificationOccurrence(*timer.Deadline), EventAt: eventAt,
		NotBefore: *timer.Deadline, ExpiresAt: timer.Deadline.Add(time.Minute),
	}
	_, err := EnqueueNotificationIntent(ctx, tx, i)
	return err
}

func cancelCookingNotification(ctx context.Context, tx pgx.Tx, household, timerID string) error {
	return CancelNotificationSource(ctx, tx, household, "recipes", "cooking_timers", timerID, 0)
}

// Source validation deliberately takes no cooking row locks: delivery workers
// can already own job rows. The mutation path cancels old revisions atomically,
// and the sender repeats this check immediately before contacting the provider.
func readCookingNotification(
	ctx context.Context, tx pgx.Tx, i notification.Intent,
) (CookingNotificationContext, error) {
	var result CookingNotificationContext
	if i.Validate() != nil || i.Category != "cooking_timer" || i.ResourceType != "cooking_timers" ||
		i.Occurrence != cookingNotificationOccurrence(i.NotBefore) ||
		!i.ExpiresAt.Equal(i.NotBefore.Add(time.Minute)) {
		return result, ErrNotFound
	}
	err := tx.QueryRow(ctx, `SELECT t.id::text,s.id::text,t.version,t.deadline
 FROM recipes.cooking_timers t JOIN recipes.cooking_sessions s
 ON s.household_id=t.household_id AND s.id=t.session_id
 WHERE t.household_id=$1 AND t.id=$2 AND s.user_id=$3 AND s.state='active'
 AND t.state='running' AND t.version=$4 AND t.deadline=$5 AND $6::timestamptz<=clock_timestamp()`,
		i.HouseholdID, i.ResourceID, i.UserID, i.Revision, i.NotBefore, i.EventAt).
		Scan(&result.TimerID, &result.SessionID, &result.Revision, &result.Deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	return result, err
}

func cookingNotificationAudience(ctx context.Context, tx pgx.Tx, i notification.Intent) (bool, error) {
	_, err := readCookingNotification(ctx, tx, i)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CookingNotificationDetails is the explicit cooking-to-push context contract.
// Callers authenticate the requesting user and bind the delivery to that user
// before supplying its stored intent. Current domain authority is revalidated.
func (s *Store) CookingNotificationDetails(
	ctx context.Context, tx pgx.Tx, i notification.Intent,
) (CookingNotificationContext, error) {
	var result CookingNotificationContext
	if i.Category != "cooking_timer" {
		return result, ErrNotFound
	}
	allowed, err := s.NotificationAudience(ctx, tx, i)
	if err != nil {
		return result, err
	}
	if !allowed {
		return result, ErrNotFound
	}
	return readCookingNotification(ctx, tx, i)
}

// CookingTimerSession resolves a timer to its owner's session. It never changes
// timer state, and a guessed UUID does not disclose another person's session.
func (s *Store) CookingTimerSession(ctx context.Context, id auth.Identity, timerID string) (string, error) {
	if !value.ValidUUID(timerID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.HouseholdID) ||
		!value.ValidUUID(id.SessionID) || !value.ValidUUID(id.MembershipID) || id.AuthVersion < 1 {
		return "", ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var sessionID string
	err := s.Pool.QueryRow(ctx, `SELECT cooking.id::text FROM recipes.cooking_timers timer
 JOIN recipes.cooking_sessions cooking ON cooking.household_id=timer.household_id AND cooking.id=timer.session_id
 JOIN core.users u ON u.id=cooking.user_id
 JOIN core.memberships m ON m.household_id=cooking.household_id AND m.user_id=u.id
 JOIN core.sessions login ON login.user_id=u.id
 JOIN core.module_states module ON module.household_id=cooking.household_id AND module.module_id='recipes'
 WHERE timer.id=$1 AND cooking.household_id=$2 AND cooking.user_id=$3 AND m.id=$4 AND m.role=$5
 AND login.id=$6 AND u.auth_version=$7 AND login.auth_version=u.auth_version
 AND login.revoked_at IS NULL AND login.idle_expires_at>clock_timestamp() AND login.absolute_expires_at>clock_timestamp()
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password AND module.state='enabled'`,
		timerID, id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.SessionID, id.AuthVersion).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return sessionID, err
}
