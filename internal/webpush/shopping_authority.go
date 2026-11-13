package webpush

import (
	"context"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

// authorizeLegacyShoppingSend runs after acquiring a transport slot. Its
// transaction ends before network I/O; an accepted request cannot be recalled.
func (s *Service) authorizeLegacyShoppingSend(ctx context.Context, claim sendClaim) error {
	tx, err := audit.Begin(audit.WithSystem(ctx, "worker"), s.pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	// The shared advisory lock also fences an absent preference row against a
	// first central-settings save. Acquire it before any membership/session lock.
	managed, err := store.LockShoppingNotificationManagement(ctx, tx, claim.ref.HouseholdID, claim.ref.RecipientID)
	if err != nil {
		return err
	}
	if managed {
		return store.ErrForbidden
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", claim.ref.HouseholdID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT module_id FROM core.module_states
 WHERE household_id=$1 AND module_id='shopping' FOR SHARE`, claim.ref.HouseholdID); err != nil {
		return err
	}
	var raw []byte
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=4096 THEN payload END,clock_timestamp()
 FROM core.jobs WHERE id=$1 AND household_id=$2 AND module_id='shopping' AND module_generation=$3
 AND kind=$4 AND payload_version=1 AND state='running' AND lease_token=$5 AND lease_until>clock_timestamp()
 FOR UPDATE`, claim.job, claim.ref.HouseholdID, claim.ref.Generation, store.ShoppingPushJobKind, claim.lease).Scan(&raw, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrConflict
	}
	if err != nil {
		return err
	}
	var ref store.ShoppingNotificationReference
	if len(raw) == 0 || strictJSON(raw, &ref) != nil || ref != claim.ref ||
		!validSendReference(ref, ref.HouseholdID, ref.Generation, now) || !claim.data.ExpiresAt.After(now) {
		return store.ErrForbidden
	}
	audience, err := s.store.ShoppingNotificationAudience(ctx, tx, ref)
	if err != nil {
		return err
	}
	if !audience.Eligible || !audience.PushEligible {
		return store.ErrForbidden
	}
	id := auth.Identity{UserID: claim.data.UserID, HouseholdID: claim.data.HouseholdID,
		SessionID: claim.data.SessionID, AuthVersion: claim.data.AuthVersion, Role: claim.data.Role}
	if err = auth.LockSession(ctx, tx, id); err != nil {
		return err
	}
	var unchanged bool
	err = tx.QueryRow(ctx, `SELECT true FROM core.push_subscriptions WHERE id=$1 AND household_id=$2
 AND user_id=$3 AND endpoint_hash=$4 AND encrypted_subscription=$5 AND encryption_key_id=$6 AND encryption_format=$7
 AND disabled_at IS NULL FOR SHARE`, claim.row.id, claim.row.household, claim.row.user,
		claim.row.hash, claim.row.cipher, claim.row.keyID, claim.row.format).Scan(&unchanged)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrForbidden
	}
	if err != nil {
		return err
	}
	// Recheck wall-clock expiry after the last possibly contended row lock.
	current, err := currentEnvelope(ctx, tx, claim.data)
	if err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	if !current || !claim.data.ExpiresAt.After(now) || !claim.ref.ExpiresAt.After(now) {
		return store.ErrForbidden
	}
	return tx.Commit(ctx)
}
