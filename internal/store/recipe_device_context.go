package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// RecipeDeviceContext is a revocable, non-credential description of the
// current recipe authority. The hashes partition private device storage; they
// never grant server access.
type RecipeDeviceContext struct {
	OwnerScope     string    `json:"owner_scope"`
	SessionScope   string    `json:"session_scope"`
	AuthScope      string    `json:"auth_scope"`
	Role           string    `json:"role"`
	Generation     int64     `json:"generation"`
	OfflineAllowed bool      `json:"offline_allowed"`
	CanWrite       bool      `json:"can_write"`
	ServerTime     time.Time `json:"server_time"`
}

const recipeSafeInteger = 1<<53 - 1

func recipeScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (RecipeDeviceContext, error) {
	var out RecipeDeviceContext
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) ||
		!value.ValidUUID(id.SessionID) || !value.ValidUUID(id.MembershipID) || id.AuthVersion <= 0 {
		return out, ErrForbidden
	}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT ms.data_generation,ms.state='enabled',coalesce(p.offline_storage_enabled,false),clock_timestamp()
 FROM core.module_states ms
 JOIN core.memberships m ON m.household_id=ms.household_id AND m.id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.auth_version=$5 AND u.disabled_at IS NULL AND NOT u.must_change_password
 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id AND s.auth_version=u.auth_version AND s.revoked_at IS NULL
 AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 LEFT JOIN core.user_preferences p ON p.household_id=ms.household_id AND p.user_id=u.id
 WHERE ms.household_id=$1 AND ms.module_id='recipes'`, id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).
		Scan(&out.Generation, &enabled, &out.OfflineAllowed, &out.ServerTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrForbidden
	}
	if err != nil {
		return out, err
	}
	if !enabled {
		return out, ErrNotFound
	}
	if out.Generation <= 0 || out.Generation > recipeSafeInteger {
		return out, Invalid{Message: "Rezeptgeneration wird nicht unterstützt"}
	}
	out.Role, out.CanWrite = id.Role, id.CanWrite()
	out.ServerTime = out.ServerTime.UTC()
	for _, scope := range []struct {
		label string
		parts []any
		dest  *string
	}{
		{"recipe-owner-v1", []any{id.HouseholdID, id.UserID}, &out.OwnerScope},
		{"recipe-session-v1", []any{id.HouseholdID, id.UserID, id.SessionID, id.MembershipID, id.Role, id.AuthVersion, out.Generation}, &out.SessionScope},
		{"recipe-authority-v1", []any{id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.AuthVersion, out.Generation}, &out.AuthScope},
	} {
		raw, err := json.Marshal(append([]any{scope.label}, scope.parts...))
		if err != nil {
			return RecipeDeviceContext{}, err
		}
		sum := sha256.Sum256(raw)
		*scope.dest = hex.EncodeToString(sum[:])
	}
	return out, nil
}

func recipeRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Store) RecipeDeviceContext(ctx context.Context, id auth.Identity) (RecipeDeviceContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RecipeDeviceContext{}, err
	}
	defer recipeRollback(tx)
	out, err := recipeScope(ctx, tx, id)
	if err != nil {
		return RecipeDeviceContext{}, err
	}
	return out, tx.Commit(ctx)
}
