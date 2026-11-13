package auth

import (
	"context"
	"time"

	"household/internal/audit"
	"household/internal/securityevent"
	"household/internal/value"
)

// RevokeSession records only the opaque session identifier, never its credential.
func (s *Service) RevokeSession(ctx context.Context, id Identity, session, action string) error {
	if !value.ValidUUID(session) || action != "logout" && action != "session_revoked" {
		return ErrSession
	}
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = LockSession(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL", session, id.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if action == "session_revoked" {
			if err = securityevent.Record(ctx, tx, id.HouseholdID, id.UserID, "sessions_revoked"); err != nil {
				return err
			}
		}
		if err = audit.Event(ctx, tx, "core", "sessions", session, action, nil); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) securityEvent(ctx context.Context, resource, entity, action string, details map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = audit.Event(ctx, tx, "core", entity, resource, action, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
