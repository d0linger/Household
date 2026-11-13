package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/securityevent"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const ErrRecent Rejection = "Bitte bestätige deine Anmeldung erneut, bevor du diese Änderung speicherst."

// LockRecentSession rechecks freshness in the same transaction as the mutation.
func LockRecentSession(ctx context.Context, tx pgx.Tx, id Identity) error {
	if err := LockSession(ctx, tx, id); err != nil {
		return err
	}
	var recent bool
	if err := tx.QueryRow(ctx, "SELECT reauthenticated_at>clock_timestamp()-interval '10 minutes' AND reauthenticated_at<=clock_timestamp() FROM core.sessions WHERE id=$1", id.SessionID).Scan(&recent); err != nil {
		return err
	}
	if !recent {
		return ErrRecent
	}
	return nil
}

// Reauthenticate verifies the current password and, when enabled, the second factor.
// It updates only authentication freshness, never the session lifetime.
func (s *Service) Reauthenticate(ctx context.Context, id Identity, password, code string) error {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if err := s.Limit(ctx, "reauthenticate", id.UserID, 10, 15*time.Minute); err != nil {
		return err
	}
	select {
	case s.bcryptSlots <- struct{}{}:
		defer func() { <-s.bcryptSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var hash string
	if err := s.pool.QueryRow(ctx, "SELECT password_hash FROM core.users WHERE id=$1 AND disabled_at IS NULL", id.UserID).Scan(&hash); err != nil {
		return ErrCredentials
	}
	if len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrCredentials
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = LockSession(ctx, tx, id); err != nil {
		return err
	}
	var current string
	if err = tx.QueryRow(ctx, "SELECT password_hash FROM core.users WHERE id=$1", id.UserID).Scan(&current); err != nil {
		return err
	}
	if current != hash {
		return ErrCredentials
	}
	if err = s.verifyFactor(ctx, tx, id.UserID, code); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.sessions SET reauthenticated_at=clock_timestamp() WHERE id=$1", id.SessionID); err != nil {
		return err
	}
	if err = audit.Event(ctx, tx, "core", "reauthentication", id.UserID, "login_success", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type SecurityStatus struct {
	TOTPEnabled       bool
	RecoveryRemaining int
}

func (s *Service) SecurityStatus(ctx context.Context, id Identity) (SecurityStatus, error) {
	var status SecurityStatus
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.totp_credentials WHERE user_id=$1 AND enabled_at IS NOT NULL),
 (SELECT count(*) FROM core.recovery_codes WHERE user_id=$1 AND consumed_at IS NULL)`, id.UserID).Scan(&status.TOTPEnabled, &status.RecoveryRemaining)
	return status, err
}

func (s *Service) DisableTOTP(ctx context.Context, id Identity) error {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if err := s.Limit(ctx, "totp-change", id.UserID, 10, time.Hour); err != nil {
		return err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = LockRecentSession(ctx, tx, id); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT enabled_at IS NOT NULL FROM core.totp_credentials WHERE user_id=$1 FOR UPDATE", id.UserID).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return Rejection("Zwei-Faktor-Anmeldung ist nicht aktiviert.")
	} else if err != nil {
		return err
	}
	if !active {
		return Rejection("Zwei-Faktor-Anmeldung ist nicht aktiviert.")
	}
	if _, err = tx.Exec(ctx, "DELETE FROM core.recovery_codes WHERE user_id=$1", id.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM core.totp_credentials WHERE user_id=$1", id.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL", id.UserID, id.SessionID); err != nil {
		return err
	}
	if err = audit.Event(ctx, tx, "core", "totp_credentials", id.UserID, "record_delete", nil); err != nil {
		return err
	}
	if err = securityevent.Record(ctx, tx, id.HouseholdID, id.UserID, "totp_disabled"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, id Identity) ([]string, error) {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if err := s.Limit(ctx, "recovery-renew", id.UserID, 5, time.Hour); err != nil {
		return nil, err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	if err = LockRecentSession(ctx, tx, id); err != nil {
		return nil, err
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT enabled_at IS NOT NULL FROM core.totp_credentials WHERE user_id=$1 FOR UPDATE", id.UserID).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return nil, Rejection("Aktiviere zuerst die Zwei-Faktor-Anmeldung.")
	} else if err != nil {
		return nil, err
	}
	if !active {
		return nil, Rejection("Aktiviere zuerst die Zwei-Faktor-Anmeldung.")
	}
	if _, err = tx.Exec(ctx, "DELETE FROM core.recovery_codes WHERE user_id=$1", id.UserID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, 8)
	for range 8 {
		code := hex.EncodeToString(HashToken(RandomToken())[:10])
		if _, err = tx.Exec(ctx, "INSERT INTO core.recovery_codes(user_id,code_hash) VALUES($1,$2)", id.UserID, HashToken(code)); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err = audit.Event(ctx, tx, "core", "recovery_codes", id.UserID, "record_write", nil); err != nil {
		return nil, err
	}
	if err = securityevent.Record(ctx, tx, id.HouseholdID, id.UserID, "recovery_codes_regenerated"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, id Identity) error {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = LockRecentSession(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL", id.UserID, id.SessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if err = securityevent.Record(ctx, tx, id.HouseholdID, id.UserID, "sessions_revoked"); err != nil {
			return err
		}
	}
	if err = audit.Event(ctx, tx, "core", "sessions", id.UserID, "session_revoked", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
