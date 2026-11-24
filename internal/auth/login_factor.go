package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"household/internal/audit"

	"github.com/jackc/pgx/v5"
)

const loginFactorPurpose = "password-totp"

// LoginFactorTTL is the lifetime of a verified password awaiting its second factor.
const LoginFactorTTL = 5 * time.Minute

var ErrLoginChallenge = errors.New("login challenge expired or consumed")

type SecondFactorRequired struct {
	Token string
}

func (SecondFactorRequired) Error() string { return "second factor required" }

func loginFactorHash(binding, token string) []byte {
	return HashToken(loginFactorPurpose + "\x00" + binding + "\x00" + token)
}

func startLoginFactor(ctx context.Context, tx pgx.Tx, uid string, version int64, binding string) (string, error) {
	token := RandomToken()
	_, err := tx.Exec(ctx, `INSERT INTO core.auth_challenges(user_id,purpose,token_hash,payload,expires_at)
 VALUES($1,$2,$3,jsonb_build_object('auth_version',$4::bigint),clock_timestamp()+$5::bigint*interval '1 second')`,
		uid, loginFactorPurpose, loginFactorHash(binding, token), version, int64(LoginFactorTTL/time.Second))
	return token, err
}

func lockLoginAccount(ctx context.Context, tx pgx.Tx, uid string) (context.Context, string, int64, error) {
	var household, hash string
	var version int64
	if err := tx.QueryRow(ctx, "SELECT h.id FROM core.households h JOIN core.memberships m ON m.household_id=h.id WHERE m.user_id=$1 AND m.inactive_at IS NULL FOR SHARE OF h", uid).Scan(&household); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ctx, "", 0, ErrCredentials
		}
		return ctx, "", 0, fmt.Errorf("load login household: %w", err)
	}
	ctx = audit.WithActor(ctx, uid, household)
	if err := audit.Bind(ctx, tx); err != nil {
		return ctx, "", 0, err
	}
	err := tx.QueryRow(ctx, "SELECT password_hash,auth_version FROM core.users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE", uid).Scan(&hash, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrCredentials
	}
	return ctx, hash, version, err
}

// FinishLoginFactor exchanges a cookie-bound password proof and a current
// authenticator or recovery code for a session. Only success consumes the proof.
func (s *Service) FinishLoginFactor(ctx context.Context, binding, token, code, ip, ua string) (string, Identity, error) {
	if len(binding) != 43 || len(token) != 43 {
		return "", Identity{}, ErrLoginChallenge
	}
	if err := s.Limit(ctx, "login-factor-network", networkPrefix(ip), 100, 15*time.Minute); err != nil {
		return "", Identity{}, err
	}
	hash := loginFactorHash(binding, token)
	var uid string
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM core.auth_challenges
 WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, hash, loginFactorPurpose).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Identity{}, ErrLoginChallenge
	}
	if err != nil {
		return "", Identity{}, err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return "", Identity{}, err
	}
	defer rollback(ctx, tx)
	ctx, _, version, err := lockLoginAccount(ctx, tx, uid)
	if errors.Is(err, ErrCredentials) {
		return "", Identity{}, ErrLoginChallenge
	}
	if err != nil {
		return "", Identity{}, err
	}
	var proofID string
	err = tx.QueryRow(ctx, `SELECT id FROM core.auth_challenges WHERE token_hash=$1 AND purpose=$2 AND user_id=$3
 AND payload->'auth_version'=to_jsonb($4::bigint) AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`,
		hash, loginFactorPurpose, uid, version).Scan(&proofID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Identity{}, ErrLoginChallenge
	}
	if err != nil {
		return "", Identity{}, err
	}
	if err = s.factorBudget(ctx, tx, uid); err != nil {
		return "", Identity{}, err
	}
	factored, err := s.verifyFactor(ctx, tx, uid, code)
	if errors.Is(err, ErrCredentials) {
		s.failFactor(ctx, tx, uid)
		eventErr := s.securityEvent(audit.WithSystem(ctx, "system"), "", "users", "login_failed", nil)
		if eventErr != nil {
			return "", Identity{}, fmt.Errorf("record rejected second factor: %w", eventErr)
		}
	}
	if err != nil {
		return "", Identity{}, err
	}
	if !factored {
		return "", Identity{}, ErrLoginChallenge
	}
	if _, err = tx.Exec(ctx, "UPDATE core.auth_challenges SET consumed_at=clock_timestamp(),attempts=attempts+1 WHERE id=$1", proofID); err != nil {
		return "", Identity{}, err
	}
	return s.commitPasswordLogin(ctx, tx, uid, ip, ua, true)
}
