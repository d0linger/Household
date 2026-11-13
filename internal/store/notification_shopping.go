package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

// LockShoppingNotificationManagement fences even the first central preference
// insert. Call before member, session or job locks: the preference writer takes
// the exclusive advisory counterpart before acquiring authentication locks.
// A present row owns this channel even when disabled; absence preserves legacy
// list consent. Callers retain the transaction until their authority decision.
func LockShoppingNotificationManagement(
	ctx context.Context, tx pgx.Tx, household, user string,
) (bool, error) {
	if !shoppingCanonicalUUID(household) || !shoppingCanonicalUUID(user) {
		return false, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(
 hashtextextended('household/notification-preferences/'||$1::text||'/'||$2::text,0))`, household, user); err != nil {
		return false, err
	}
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category='shopping' FOR SHARE`, household, user).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func shoppingDigestKey(ref ShoppingNotificationReference) string {
	return "shopping-digest-v1/" + strings.TrimPrefix(shoppingNoticeKey(ref), "shopping-notice-v1/")
}

func shoppingDigestNotBefore(ref ShoppingNotificationReference) time.Time {
	return time.Unix((ref.Bucket+1)*60, 0).UTC()
}

// The completed digest is one immutable logical episode. Its closed payload
// retains the full decimal uint64 watch revision; that value must not be narrowed
// into the core contract's safe-integer source revision.
func enqueueShoppingNotification(
	ctx context.Context, tx pgx.Tx, jobID, inboxID string, ref ShoppingNotificationReference,
) error {
	i := notification.Intent{
		Schema: notification.Schema, HouseholdID: ref.HouseholdID, UserID: ref.RecipientID,
		Module: "shopping", Generation: ref.Generation, ResourceType: "shopping_digest",
		ResourceID: jobID, Revision: 1, Occurrence: "notice/" + inboxID, Category: "shopping",
		EventAt: ref.EventAt, NotBefore: shoppingDigestNotBefore(ref), ExpiresAt: ref.ExpiresAt,
	}
	_, err := EnqueueNotificationIntent(ctx, tx, i)
	return err
}

// The source read takes no job lock. Senders may already own their delivery job;
// the finalized digest and current list/watch authority are read independently.
func (s *Store) shoppingNotificationAudience(ctx context.Context, tx pgx.Tx, i notification.Intent) (bool, error) {
	if i.Validate() != nil || i.Category != "shopping" || i.ResourceType != "shopping_digest" || i.Revision != 1 ||
		!strings.HasPrefix(i.Occurrence, "notice/") {
		return false, nil
	}
	inboxID := strings.TrimPrefix(i.Occurrence, "notice/")
	if !shoppingCanonicalUUID(inboxID) {
		return false, nil
	}
	var raw []byte
	var key string
	err := tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=4096 THEN payload END,dedupe_key
 FROM core.jobs WHERE id=$1 AND household_id=$2 AND module_id='shopping' AND module_generation=$3
 AND kind=$4 AND payload_version=1 AND state='done' AND finished_at IS NOT NULL AND last_error IS NULL`,
		i.ResourceID, i.HouseholdID, i.Generation, ShoppingDigestJobKind).Scan(&raw, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var ref ShoppingNotificationReference
	if len(raw) == 0 || shoppingDecodeClosed(raw, &ref) != nil || !shoppingValidReference(ref) ||
		ref.HouseholdID != i.HouseholdID || ref.RecipientID != i.UserID || ref.Generation != i.Generation ||
		ref.NotificationID != "" || ref.SubscriptionID != "" || key != shoppingDigestKey(ref) ||
		!ref.EventAt.Equal(i.EventAt) || !shoppingDigestNotBefore(ref).Equal(i.NotBefore) || !ref.ExpiresAt.Equal(i.ExpiresAt) {
		return false, nil
	}
	ref.NotificationID = inboxID
	audience, err := s.ShoppingNotificationAudience(ctx, tx, ref)
	return audience.Eligible && audience.PushEligible, err
}
