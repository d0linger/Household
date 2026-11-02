// Package auth provides PostgreSQL-backed authentication and session management.
package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrCredentials = errors.New("ungültige Anmeldedaten oder Sicherheitscode")
	ErrSession     = errors.New("sitzung abgelaufen")
	ErrLimited     = errors.New("zu viele Versuche; bitte später erneut versuchen")
)

type Identity struct {
	UserID       string    `json:"id"`
	HouseholdID  string    `json:"household_id"`
	MembershipID string    `json:"membership_id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	MustChange   bool      `json:"must_change_password"`
	AuthVersion  int64     `json:"-"`
	SessionID    string    `json:"session_id"`
	CreatedAt    time.Time `json:"-"`
	ExpiresAt    time.Time `json:"-"`
	Renewed      bool      `json:"-"`
}

func (i Identity) CanWrite() bool { return i.Role == "admin" || i.Role == "editor" }
func (i Identity) Recent() bool   { return time.Since(i.CreatedAt) < 10*time.Minute }

type Service struct {
	pool        *pgxpool.Pool
	cfg         config.Config
	dummy       []byte
	bcryptSlots chan struct{}
	aead        cipher.AEAD
}

func New(pool *pgxpool.Pool, cfg config.Config) (*Service, error) {
	dummy, err := bcrypt.GenerateFromPassword([]byte(RandomToken()), 12)
	if err != nil {
		return nil, fmt.Errorf("create timing equalizer: %w", err)
	}
	key := sha256.Sum256([]byte(cfg.EncryptionKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	return &Service{pool: pool, cfg: cfg, dummy: dummy, bcryptSlots: make(chan struct{}, 4), aead: aead}, nil
}

func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("cryptographic random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func HashToken(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }

func PasswordValid(password string) bool { return len([]rune(password)) >= 14 && len(password) <= 72 }

func (s *Service) Bootstrap(ctx context.Context, modules []string, enabled map[string]bool) error {
	ctx = audit.WithSystem(ctx, "bootstrap")
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return fmt.Errorf("begin bootstrap: %w", err)
	}
	defer rollback(ctx, tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(726241906)"); err != nil {
		return fmt.Errorf("lock bootstrap: %w", err)
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM core.users").Scan(&count); err != nil {
		return fmt.Errorf("inspect bootstrap: %w", err)
	}
	if count == 0 {
		if !PasswordValid(s.cfg.AdminPassword) {
			return errors.New("initial admin password requires 14 characters and at most 72 bytes")
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), 12)
		if e != nil {
			return fmt.Errorf("hash bootstrap password: %w", e)
		}
		var hh, uid string
		if err = tx.QueryRow(ctx, "INSERT INTO core.households(name) VALUES($1) RETURNING id", s.cfg.HouseholdName).Scan(&hh); err != nil {
			return fmt.Errorf("create household: %w", err)
		}
		if err = tx.QueryRow(ctx, "INSERT INTO core.users(username,display_name,password_hash) VALUES($1,$2,$3) RETURNING id", s.cfg.AdminUsername, "Administrator", string(hash)).Scan(&uid); err != nil {
			return fmt.Errorf("create bootstrap admin: %w", err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", hh, uid); err != nil {
			return fmt.Errorf("create administrator membership: %w", err)
		}
	}
	for _, id := range modules {
		if _, err = tx.Exec(ctx, "INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES($1,$1,'0.1.0') ON CONFLICT(module_id) DO UPDATE SET last_seen_build=excluded.last_seen_build", id); err != nil {
			return fmt.Errorf("register module: %w", err)
		}
		state := "disabled"
		if enabled[id] {
			state = "enabled"
		}
		if _, err = tx.Exec(ctx, "INSERT INTO core.module_states(household_id,module_id,state) SELECT id,$1,$2 FROM core.households ON CONFLICT DO NOTHING", id, state); err != nil {
			return fmt.Errorf("initialize module: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) Limit(ctx context.Context, scope, subject string, limit int, window time.Duration) error {
	mac := hmac.New(sha256.New, []byte(s.cfg.RateSecret))
	mac.Write([]byte(scope + "\x00" + subject))
	var count int
	err := s.pool.QueryRow(ctx, `WITH w AS (SELECT to_timestamp(floor(extract(epoch FROM clock_timestamp())/$3)*$3) AS start_at)
	 INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end) SELECT $1,$2,start_at,start_at+make_interval(secs=>$3) FROM w
	 ON CONFLICT(scope,subject_hash,window_start) DO UPDATE SET attempt_count=core.rate_limit_buckets.attempt_count+1,updated_at=now()
	 RETURNING attempt_count`, scope, mac.Sum(nil), window.Seconds()).Scan(&count)
	if err != nil {
		return fmt.Errorf("enforce persistent rate limit: %w", err)
	}
	if count > limit {
		return ErrLimited
	}
	return nil
}

func (s *Service) Login(ctx context.Context, username, password, code, ip, ua string) (token string, result Identity, loginErr error) {
	if err := s.Limit(ctx, "login-network", ip, 100, 15*time.Minute); err != nil {
		return "", Identity{}, err
	}
	// The tight budget is per account and client network, so one network cannot
	// lock the owner out from another; the account-wide cap is a distributed backstop.
	account := strings.ToLower(username)
	if err := s.Limit(ctx, "login-account-network", account+"\x00"+networkPrefix(ip), 20, time.Hour); err != nil {
		return "", Identity{}, err
	}
	if err := s.Limit(ctx, "login-account", account, 500, time.Hour); err != nil {
		return "", Identity{}, err
	}
	// Only credential attempts admitted by both durable limits create an event.
	// Submitted credentials, account names and network addresses never enter it.
	defer func() {
		if errors.Is(loginErr, ErrCredentials) {
			if err := s.securityEvent(audit.WithSystem(ctx, "system"), "", "users", "login_failed", nil); err != nil {
				loginErr = fmt.Errorf("record rejected login: %w", err)
			}
		}
	}()
	select {
	case s.bcryptSlots <- struct{}{}:
		defer func() { <-s.bcryptSlots }()
	case <-ctx.Done():
		return "", Identity{}, ctx.Err()
	}
	var uid, hash string
	var disabled *time.Time
	err := s.pool.QueryRow(ctx, "SELECT id,password_hash,disabled_at FROM core.users WHERE username_key=lower($1)", username).Scan(&uid, &hash, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummy, []byte(password))
		return "", Identity{}, ErrCredentials
	}
	if err != nil {
		return "", Identity{}, fmt.Errorf("load login account: %w", err)
	}
	if len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || disabled != nil {
		return "", Identity{}, ErrCredentials
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return "", Identity{}, err
	}
	defer rollback(ctx, tx)
	var household string
	if err = tx.QueryRow(ctx, "SELECT h.id FROM core.households h JOIN core.memberships m ON m.household_id=h.id WHERE m.user_id=$1 AND m.inactive_at IS NULL FOR SHARE OF h", uid).Scan(&household); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", Identity{}, ErrCredentials
		}
		return "", Identity{}, fmt.Errorf("load login household: %w", err)
	}
	ctx = audit.WithActor(ctx, uid, household)
	if err = audit.Bind(ctx, tx); err != nil {
		return "", Identity{}, err
	}
	var currentHash string
	if err = tx.QueryRow(ctx, "SELECT password_hash FROM core.users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE", uid).Scan(&currentHash); err != nil {
		return "", Identity{}, ErrCredentials
	}
	if !hmac.Equal([]byte(hash), []byte(currentHash)) {
		return "", Identity{}, ErrCredentials
	}
	if err = s.verifyFactor(ctx, tx, uid, code); err != nil {
		return "", Identity{}, err
	}
	raw, err := s.createSession(ctx, tx, uid, ip, ua)
	if err != nil {
		return "", Identity{}, err
	}
	if err = audit.Event(ctx, tx, "core", "users", uid, "login_success", nil); err != nil {
		return "", Identity{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Identity{}, err
	}
	identity, err := s.Resolve(ctx, raw, false)
	return raw, identity, err
}

// networkPrefix groups a client address with its /24 (IPv4) or /56 (IPv6)
// neighbours, the typical span one household or attacker controls.
func networkPrefix(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	bits := 56
	if addr.Unmap().Is4() {
		addr, bits = addr.Unmap(), 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ip
	}
	return prefix.String()
}

func (s *Service) createSession(ctx context.Context, tx pgx.Tx, uid, ip, ua string) (string, error) {
	if len(ua) > 512 {
		ua = ua[:512]
	}
	raw := RandomToken()
	var id string
	_, network, _ := net.ParseCIDR(ip + "/24")
	prefix := ""
	if network != nil {
		prefix = network.String()
	}
	err := tx.QueryRow(ctx, `INSERT INTO core.sessions(user_id,token_hash,auth_version,idle_expires_at,absolute_expires_at,user_agent,ip_prefix)
	 SELECT id,$2,auth_version,now()+make_interval(secs=>$3),now()+make_interval(secs=>$4),$5,nullif($6,'')::inet FROM core.users WHERE id=$1 AND disabled_at IS NULL RETURNING id`, uid, HashToken(raw), s.cfg.SessionIdle.Seconds(), s.cfg.SessionAbsolute.Seconds(), ua, prefix).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return raw, nil
}

func (s *Service) Resolve(ctx context.Context, raw string, renew bool) (Identity, error) {
	if len(raw) != 43 {
		return Identity{}, ErrSession
	}
	var i Identity
	err := s.pool.QueryRow(ctx, `SELECT u.id,m.household_id,m.id,u.username,u.display_name,m.role,u.must_change_password,u.auth_version,s.id,s.created_at,s.idle_expires_at
	 FROM core.sessions s JOIN core.users u ON u.id=s.user_id JOIN core.memberships m ON m.user_id=u.id
	 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.idle_expires_at>now() AND s.absolute_expires_at>now()
	 AND s.auth_version=u.auth_version AND u.disabled_at IS NULL AND m.inactive_at IS NULL`, HashToken(raw)).Scan(&i.UserID, &i.HouseholdID, &i.MembershipID, &i.Username, &i.DisplayName, &i.Role, &i.MustChange, &i.AuthVersion, &i.SessionID, &i.CreatedAt, &i.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrSession
	}
	if err != nil {
		return Identity{}, fmt.Errorf("resolve session: %w", err)
	}
	if renew && time.Until(i.ExpiresAt) < s.cfg.SessionIdle/2 {
		err = s.pool.QueryRow(ctx, "UPDATE core.sessions SET idle_expires_at=least(now()+make_interval(secs=>$2),absolute_expires_at),last_seen_at=now() WHERE id=$1 AND revoked_at IS NULL AND idle_expires_at>now() RETURNING idle_expires_at", i.SessionID, s.cfg.SessionIdle.Seconds()).Scan(&i.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return Identity{}, ErrSession
		}
		if err != nil {
			return Identity{}, fmt.Errorf("renew session: %w", err)
		}
		i.Renewed = true
	}
	return i, nil
}

func (s *Service) CSRF(raw, kind string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.CSRFSecret))
	mac.Write([]byte(kind + "\x00" + raw))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Service) ValidCSRF(raw, kind, token string) bool {
	return len(token) == 43 && hmac.Equal([]byte(s.CSRF(raw, kind)), []byte(token))
}

func (s *Service) Logout(ctx context.Context, id Identity) error {
	return s.RevokeSession(ctx, id, id.SessionID, "logout")
}

func (s *Service) ChangePassword(ctx context.Context, id Identity, current, next string) error {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if err := s.Limit(ctx, "password-change", id.UserID, 10, time.Hour); err != nil {
		return err
	}
	select {
	case s.bcryptSlots <- struct{}{}:
		defer func() { <-s.bcryptSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if !PasswordValid(next) || next == current {
		return errors.New("neues Passwort: mindestens 14 Zeichen, höchstens 72 Bytes, vom bisherigen verschieden")
	}
	var old string
	if err := s.pool.QueryRow(ctx, "SELECT password_hash FROM core.users WHERE id=$1", id.UserID).Scan(&old); err != nil {
		return fmt.Errorf("load password: %w", err)
	}
	if len(current) > 72 || bcrypt.CompareHashAndPassword([]byte(old), []byte(current)) != nil {
		return ErrCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), 12)
	if err != nil {
		return fmt.Errorf("hash replacement password: %w", err)
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = LockSession(ctx, tx, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE core.users SET password_hash=$2,must_change_password=false,auth_version=auth_version+1,password_changed_at=now(),updated_at=now() WHERE id=$1 AND password_hash=$3", id.UserID, string(hash), old)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentials
	}
	_, err = tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1", id.UserID)
	if err != nil {
		return err
	}
	if err = audit.Event(ctx, tx, "core", "users", id.UserID, "password_changed", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Seal(data []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("cryptographic random source unavailable")
	}
	return s.aead.Seal(nonce, nonce, data, []byte("household-v1"))
}
func (s *Service) Open(data []byte) ([]byte, error) {
	if len(data) < s.aead.NonceSize() {
		return nil, errors.New("invalid encrypted data")
	}
	return s.aead.Open(nil, data[:s.aead.NonceSize()], data[s.aead.NonceSize():], []byte("household-v1"))
}

func (s *Service) SetupTOTP(ctx context.Context, id Identity) (string, string, error) {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if !id.Recent() {
		return "", "", errors.New("für diese Aktion erneut anmelden")
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Household", AccountName: id.Username})
	if err != nil {
		return "", "", err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return "", "", err
	}
	defer rollback(ctx, tx)
	if err = LockSession(ctx, tx, id); err != nil {
		return "", "", err
	}
	tag, err := tx.Exec(ctx, "INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_key_id) VALUES($1,$2,'v1') ON CONFLICT(user_id) DO UPDATE SET encrypted_secret=excluded.encrypted_secret WHERE core.totp_credentials.enabled_at IS NULL", id.UserID, s.Seal([]byte(key.Secret())))
	if err != nil {
		return "", "", err
	}
	if tag.RowsAffected() != 1 {
		return "", "", errors.New("zweiter Faktor ist bereits aktiv")
	}
	if err = audit.Event(ctx, tx, "core", "users", id.UserID, "totp_setup", nil); err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func (s *Service) EnableTOTP(ctx context.Context, id Identity, code string) ([]string, error) {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if !id.Recent() {
		return nil, errors.New("für diese Aktion erneut anmelden")
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	if err = LockSession(ctx, tx, id); err != nil {
		return nil, err
	}
	var encrypted []byte
	var enabled *time.Time
	if err = tx.QueryRow(ctx, "SELECT encrypted_secret,enabled_at FROM core.totp_credentials WHERE user_id=$1 FOR UPDATE", id.UserID).Scan(&encrypted, &enabled); err != nil {
		return nil, err
	}
	if enabled != nil {
		return nil, errors.New("zweiter Faktor ist bereits aktiv")
	}
	secret, err := s.Open(encrypted)
	if err != nil {
		return nil, err
	}
	counter, valid := factorCounter(string(secret), code, time.Now())
	if !valid {
		return nil, ErrCredentials
	}
	if _, err = tx.Exec(ctx, "UPDATE core.totp_credentials SET enabled_at=now(),last_accepted_counter=$2 WHERE user_id=$1", id.UserID, counter); err != nil {
		return nil, err
	}
	codes := []string{}
	for range 8 {
		code := hex.EncodeToString(HashToken(RandomToken())[:10])
		if _, err = tx.Exec(ctx, "INSERT INTO core.recovery_codes(user_id,code_hash) VALUES($1,$2)", id.UserID, HashToken(code)); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if _, err = tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2", id.UserID, id.SessionID); err != nil {
		return nil, err
	}
	if err = audit.Event(ctx, tx, "core", "users", id.UserID, "totp_enabled", nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

func factorCounter(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	center := now.Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		counter := center + offset
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(counter*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && hmac.Equal([]byte(code), []byte(expected)) {
			return counter, true
		}
	}
	return 0, false
}

func (s *Service) verifyFactor(ctx context.Context, tx pgx.Tx, user, code string) error {
	var encrypted []byte
	var last *int64
	err := tx.QueryRow(ctx, "SELECT encrypted_secret,last_accepted_counter FROM core.totp_credentials WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE", user).Scan(&encrypted, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	secret, err := s.Open(encrypted)
	if err != nil {
		return fmt.Errorf("decrypt second factor: %w", err)
	}
	counter, valid := factorCounter(string(secret), code, time.Now())
	if valid && (last == nil || counter > *last) {
		_, err = tx.Exec(ctx, "UPDATE core.totp_credentials SET last_accepted_counter=$2 WHERE user_id=$1", user, counter)
	} else {
		var used string
		err = tx.QueryRow(ctx, "UPDATE core.recovery_codes SET consumed_at=now() WHERE user_id=$1 AND code_hash=$2 AND consumed_at IS NULL RETURNING id", user, HashToken(strings.ToLower(strings.TrimSpace(code)))).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCredentials
		}
		if err == nil {
			err = audit.Event(ctx, tx, "core", "users", user, "recovery_code_used", nil)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

// LockSession linearizes account, membership, and session changes with mutations.
// Every caller takes the household barrier before authentication row locks.
func LockSession(ctx context.Context, tx pgx.Tx, id Identity) error {
	if _, err := tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); err != nil {
		return err
	}
	var valid bool
	err := tx.QueryRow(ctx, `SELECT true FROM core.sessions s JOIN core.users u ON u.id=s.user_id JOIN core.memberships m ON m.user_id=u.id WHERE s.id=$1 AND s.user_id=$2 AND m.household_id=$3 AND s.revoked_at IS NULL AND s.idle_expires_at>now() AND s.absolute_expires_at>now() AND s.auth_version=u.auth_version AND u.auth_version=$5 AND u.disabled_at IS NULL AND m.inactive_at IS NULL AND m.role=$4 FOR SHARE OF s,u,m`, id.SessionID, id.UserID, id.HouseholdID, id.Role, id.AuthVersion).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSession
	}
	return err
}

func (s *Service) HashPassword(ctx context.Context, user, password string) ([]byte, error) {
	if !PasswordValid(password) {
		return nil, errors.New("invalid password length")
	}
	if err := s.Limit(ctx, "password-hash", user, 10, time.Hour); err != nil {
		return nil, err
	}
	select {
	case s.bcryptSlots <- struct{}{}:
		defer func() { <-s.bcryptSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return bcrypt.GenerateFromPassword([]byte(password), 12)
}

func rollback(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}

func (s *Service) RecoverPassword(ctx context.Context, username, password string, resetFactor ...bool) error {
	ctx = audit.WithSystem(ctx, "operator")
	var role string
	if err := s.pool.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil {
		return err
	}
	if role != "household_owner" {
		return errors.New("account recovery requires the maintenance role")
	}
	if !PasswordValid(password) {
		return errors.New("invalid replacement password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return err
	}
	var uid string
	err = tx.QueryRow(ctx, "UPDATE core.users SET password_hash=$2,must_change_password=true,auth_version=auth_version+1,disabled_at=NULL WHERE username_key=lower($1) RETURNING id", username, string(hash)).Scan(&uid)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1", uid); err != nil {
		return err
	}
	reset := len(resetFactor) > 0 && resetFactor[0]
	if reset {
		if _, err = tx.Exec(ctx, "DELETE FROM core.totp_credentials WHERE user_id=$1", uid); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM core.recovery_codes WHERE user_id=$1", uid); err != nil {
			return err
		}
	}
	if err = audit.Event(ctx, tx, "core", "users", uid, "operator_password_recovery", map[string]any{"mfa_reset": reset}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
