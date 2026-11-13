package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/secretbox"
	"household/internal/securityevent"
	"household/internal/value"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
)

const maxPasskeys = 10

type passkeyUser struct {
	Identity
	Credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return []byte(u.UserID) }
func (u passkeyUser) WebAuthnName() string                       { return u.Username }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.DisplayName }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

type Passkey struct {
	ID, Name   string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}
type passkeyEnvelope struct {
	UserID     string
	Credential webauthn.Credential
}
type passkeyChallenge struct {
	Session       webauthn.SessionData
	Identity      Identity
	AuthVersion   int64
	LoginVersions map[string]int64
	Binding       []byte
	Name          string
	StartedAt     time.Time
}

func passkeyEngine(origin string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return nil, Rejection("Passkeys benötigen HTTPS oder die lokale Adresse localhost.")
	}
	secureOrigin := u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "localhost")
	if u.Hostname() == "" || net.ParseIP(u.Hostname()) != nil || !secureOrigin {
		return nil, Rejection("Passkeys benötigen HTTPS oder die lokale Adresse localhost.")
	}
	return webauthn.New(&webauthn.Config{RPID: u.Hostname(), RPDisplayName: "Household", RPOrigins: []string{origin},
		AttestationPreference:  protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired},
		Timeouts:               webauthn.TimeoutsConfig{Login: webauthn.TimeoutConfig{Enforce: true, Timeout: 3 * time.Minute}, Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 3 * time.Minute}}})
}

func (s *Service) PasskeysAvailable() bool { _, err := passkeyEngine(s.cfg.Origin); return err == nil }

func (s *Service) ListPasskeys(ctx context.Context, id Identity) ([]Passkey, error) {
	rows, err := s.pool.Query(ctx, "SELECT id,name,created_at,last_used_at FROM core.passkey_credentials WHERE user_id=$1 ORDER BY created_at LIMIT 11", id.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Passkey{}
	for rows.Next() {
		var key Passkey
		if err = rows.Scan(&key.ID, &key.Name, &key.CreatedAt, &key.LastUsedAt); err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, rows.Err()
}

func validPasskeyName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 100 && !strings.ContainsAny(name, "\x00\r\n")
}

func (s *Service) ChangePasskey(ctx context.Context, id Identity, key, name string, remove bool) error {
	if !value.ValidUUID(key) {
		return Rejection("Passkey nicht gefunden.")
	}
	name = strings.TrimSpace(name)
	if !remove && !validPasskeyName(name) {
		return Rejection("Der Passkey-Name muss 1 bis 100 Zeichen lang sein.")
	}
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if err := s.Limit(ctx, "passkey-manage", id.UserID, 30, time.Hour); err != nil {
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
	query := "UPDATE core.passkey_credentials SET name=$3 WHERE id=$1 AND user_id=$2"
	args := []any{key, id.UserID, name}
	action := "record_write"
	if remove {
		query = "DELETE FROM core.passkey_credentials WHERE id=$1 AND user_id=$2"
		args = args[:2]
		action = "record_delete"
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return Rejection("Passkey nicht gefunden.")
	}
	if remove {
		if _, err = tx.Exec(ctx, "UPDATE core.sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL", id.UserID, id.SessionID); err != nil {
			return err
		}
	}
	if err = audit.Event(ctx, tx, "core", "passkeys", key, action, nil); err != nil {
		return err
	}
	if remove {
		if err = securityevent.Record(ctx, tx, id.HouseholdID, id.UserID, "passkey_removed"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) loadPasskeyUser(ctx context.Context, tx pgx.Tx, uid string) (passkeyUser, error) {
	var user passkeyUser
	err := tx.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.auth_version,u.must_change_password,m.household_id,m.id,m.role
 FROM core.users u JOIN core.memberships m ON m.user_id=u.id WHERE u.id=$1 AND u.disabled_at IS NULL AND m.inactive_at IS NULL FOR UPDATE OF u`, uid).
		Scan(&user.UserID, &user.Username, &user.DisplayName, &user.AuthVersion, &user.MustChange, &user.HouseholdID, &user.MembershipID, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return user, ErrCredentials
	}
	if err != nil {
		return user, err
	}
	rows, err := tx.Query(ctx, "SELECT id,credential_id,encrypted_credential,encryption_format,encryption_key_id FROM core.passkey_credentials WHERE user_id=$1 ORDER BY id LIMIT 11 FOR UPDATE", uid)
	if err != nil {
		return user, err
	}
	defer rows.Close()
	for rows.Next() {
		var rowID string
		var rawID []byte
		var sealed secretbox.Stored
		if err = rows.Scan(&rowID, &rawID, &sealed.Ciphertext, &sealed.Format, &sealed.KeyID); err != nil {
			return user, err
		}
		raw, openErr := s.OpenRecord(secretbox.Binding{Purpose: "passkey-credential", UserID: uid, RecordID: rowID}, sealed)
		if openErr != nil {
			return user, fmt.Errorf("open stored passkey: %w", openErr)
		}
		var envelope passkeyEnvelope
		if json.Unmarshal(raw, &envelope) != nil || envelope.UserID != uid || !bytes.Equal(rawID, envelope.Credential.ID) {
			return user, errors.New("invalid stored passkey binding")
		}
		user.Credentials = append(user.Credentials, envelope.Credential)
	}
	if err = rows.Err(); err != nil {
		return user, err
	}
	if len(user.Credentials) > maxPasskeys {
		return user, errors.New("passkey limit exceeded")
	}
	return user, nil
}

func (s *Service) passkeyLimit(ctx context.Context, id Identity, binding, ip, step string) error {
	if len(binding) != 43 {
		return ErrCredentials
	}
	for _, limit := range []struct {
		scope, subject string
		count          int
		window         time.Duration
	}{
		{"passkey-global-" + step, "installation", 2000, time.Hour},
		{"passkey-network-" + step, networkPrefix(ip), 100, 15 * time.Minute},
		{"passkey-browser-" + step, binding, 20, 15 * time.Minute},
	} {
		if err := s.Limit(ctx, limit.scope, limit.subject, limit.count, limit.window); err != nil {
			return err
		}
	}
	if id.UserID != "" {
		return s.Limit(ctx, "passkey-user-"+step, id.UserID, 20, 15*time.Minute)
	}
	return nil
}

// BeginPasskey stores the complete library session encrypted and bound to the
// browser credential, ceremony purpose and current account generation.
func (s *Service) BeginPasskey(ctx context.Context, id Identity, purpose, binding, ip, name string) (map[string]any, error) {
	if purpose != "login" && purpose != "register" && purpose != "reauth" {
		return nil, ErrCredentials
	}
	if err := s.passkeyLimit(ctx, id, binding, ip, "begin"); err != nil {
		return nil, err
	}
	engine, err := passkeyEngine(s.cfg.Origin)
	if err != nil {
		return nil, err
	}
	var options any
	var session *webauthn.SessionData
	var loginVersions map[string]int64
	if purpose == "login" {
		// Discovery does not identify the account until finish. Keep a bounded,
		// encrypted generation snapshot so account changes invalidate a pending
		// anonymous ceremony without exposing an account list to the browser.
		rows, e := s.pool.Query(ctx, `SELECT id::text,auth_version FROM core.users
WHERE disabled_at IS NULL AND NOT must_change_password ORDER BY id LIMIT 101`)
		if e != nil {
			return nil, e
		}
		loginVersions = make(map[string]int64)
		for rows.Next() {
			var uid string
			var version int64
			if e = rows.Scan(&uid, &version); e != nil {
				rows.Close()
				return nil, e
			}
			loginVersions[uid] = version
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(loginVersions) > 100 {
			return nil, errors.New("passkey account snapshot limit exceeded")
		}
		creation, state, e := engine.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired), webauthn.WithLoginOrigin(s.cfg.Origin))
		if e != nil {
			return nil, e
		}
		options = creation.Response
		session = state
	} else {
		ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
		tx, e := audit.Begin(ctx, s.pool)
		if e != nil {
			return nil, e
		}
		defer rollback(ctx, tx)
		// The household barrier serializes account mutation and credential ceremonies.
		if _, e = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); e != nil {
			return nil, e
		}
		user, e := s.loadPasskeyUser(ctx, tx, id.UserID)
		if e != nil {
			return nil, e
		}
		if purpose == "register" {
			e = LockRecentSession(ctx, tx, id)
		} else {
			e = LockSession(ctx, tx, id)
		}
		if e != nil {
			return nil, e
		}
		if user.MustChange {
			return nil, Rejection("Ändere zuerst dein Startpasswort.")
		}
		if purpose == "register" {
			name = strings.TrimSpace(name)
			if !validPasskeyName(name) {
				return nil, Rejection("Der Passkey-Name muss 1 bis 100 Zeichen lang sein.")
			}
			if len(user.Credentials) >= maxPasskeys {
				return nil, Rejection("Pro Konto sind höchstens zehn Passkeys möglich.")
			}
			excluded := make([]protocol.CredentialDescriptor, 0, len(user.Credentials))
			for _, key := range user.Credentials {
				excluded = append(excluded, key.Descriptor())
			}
			creation, state, e := engine.BeginRegistration(user, webauthn.WithExclusions(excluded), webauthn.WithRegistrationOrigin(s.cfg.Origin))
			if e != nil {
				return nil, e
			}
			options = creation.Response
			session = state
		} else {
			creation, state, e := engine.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired), webauthn.WithLoginOrigin(s.cfg.Origin))
			if e != nil {
				return nil, Rejection("Für dieses Konto ist kein verwendbarer Passkey vorhanden.")
			}
			options = creation.Response
			session = state
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
	}
	state := passkeyChallenge{Session: *session, Identity: id, AuthVersion: id.AuthVersion, LoginVersions: loginVersions, Binding: HashToken(binding), Name: name, StartedAt: time.Now()}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	challengeID := value.UUID()
	protected, err := s.SealRecord(secretbox.Binding{Purpose: "passkey-" + purpose, UserID: id.UserID, RecordID: challengeID}, raw)
	if err != nil {
		return nil, err
	}
	sealed, err := json.Marshal(map[string][]byte{"sealed": protected.Ciphertext})
	if err != nil {
		return nil, err
	}
	token := RandomToken()
	_, err = s.pool.Exec(ctx, `INSERT INTO core.auth_challenges(id,user_id,purpose,token_hash,payload,expires_at,encryption_format,encryption_key_id)
 VALUES($1,nullif($2,'')::uuid,$3,$4,$5,now()+interval '3 minutes',$6,$7)`, challengeID, id.UserID, "passkey-"+purpose, HashToken(token), sealed, protected.Format, protected.KeyID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"challenge_id": token, "options": options}, nil
}

func (s *Service) consumePasskeyChallenge(ctx context.Context, id Identity, purpose, binding, token string) (passkeyChallenge, error) {
	var state passkeyChallenge
	if len(token) != 43 {
		return state, ErrCredentials
	}
	var payload []byte
	var rowID, userID string
	var protected secretbox.Stored
	// Consume even rejected assertions so a failed ceremony can never be replayed.
	err := s.pool.QueryRow(ctx, `UPDATE core.auth_challenges SET consumed_at=clock_timestamp(),attempts=attempts+1
 WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp()
 RETURNING id,coalesce(user_id::text,''),CASE WHEN octet_length(payload::text)<=180000 THEN payload END,encryption_format,encryption_key_id`, HashToken(token), "passkey-"+purpose).Scan(&rowID, &userID, &payload, &protected.Format, &protected.KeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrCredentials
	}
	if err != nil {
		return state, err
	}
	var envelope map[string][]byte
	if json.Unmarshal(payload, &envelope) != nil {
		return state, ErrCredentials
	}
	protected.Ciphertext = envelope["sealed"]
	raw, err := s.OpenRecord(secretbox.Binding{Purpose: "passkey-" + purpose, UserID: userID, RecordID: rowID}, protected)
	if errors.Is(err, secretbox.ErrKeyUnavailable) {
		return state, err
	}
	if err != nil || json.Unmarshal(raw, &state) != nil {
		return state, ErrCredentials
	}
	if !hmac.Equal(state.Binding, HashToken(binding)) || state.Identity.UserID != id.UserID || state.Identity.SessionID != id.SessionID || state.AuthVersion != id.AuthVersion {
		return state, ErrCredentials
	}
	return state, nil
}

// FinishPasskey validates cryptographic proofs while holding the account and
// credential locks; persisted counters and backup flags are always updated.
func (s *Service) FinishPasskey(ctx context.Context, id Identity, purpose, binding, ip, ua, token string, response []byte) (resultToken string, resultIdentity Identity, resultErr error) {
	if purpose != "login" && purpose != "register" && purpose != "reauth" {
		return "", Identity{}, ErrCredentials
	}
	if len(response) > 65536 {
		return "", Identity{}, ErrCredentials
	}
	if err := s.passkeyLimit(ctx, id, binding, ip, "finish"); err != nil {
		return "", Identity{}, err
	}
	// Admitted failed proofs have a sanitized event, even when the anonymous
	// assertion cannot be associated with an account. Never record its payload.
	defer func() {
		var rejection Rejection
		if !errors.Is(resultErr, ErrCredentials) && !errors.As(resultErr, &rejection) {
			return
		}
		eventContext, entity := audit.WithSystem(ctx, "system"), "passkeys"
		if id.UserID != "" {
			eventContext = audit.WithActor(ctx, id.UserID, id.HouseholdID)
		}
		if purpose == "reauth" {
			entity = "reauthentication"
		}
		if err := s.securityEvent(eventContext, "", entity, "login_failed", nil); err != nil {
			resultErr = err
		}
	}()
	state, err := s.consumePasskeyChallenge(ctx, id, purpose, binding, token)
	if err != nil {
		return "", Identity{}, err
	}
	engine, err := passkeyEngine(s.cfg.Origin)
	if err != nil {
		return "", Identity{}, err
	}
	tx, err := audit.Begin(ctx, s.pool)
	if err != nil {
		return "", Identity{}, err
	}
	defer rollback(ctx, tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR SHARE"); err != nil {
		return "", Identity{}, err
	}
	uid := id.UserID
	var assertion *protocol.ParsedCredentialAssertionData
	if purpose != "register" {
		assertion, err = protocol.ParseCredentialRequestResponseBytes(response)
		if err != nil {
			return "", Identity{}, ErrCredentials
		}
		if purpose == "login" {
			if len(assertion.Response.UserHandle) == 0 || len(assertion.RawID) == 0 {
				return "", Identity{}, ErrCredentials
			}
			err = tx.QueryRow(ctx, "SELECT user_id FROM core.passkey_credentials WHERE credential_id=$1", []byte(assertion.RawID)).Scan(&uid)
			if errors.Is(err, pgx.ErrNoRows) {
				return "", Identity{}, ErrCredentials
			}
			if err != nil {
				return "", Identity{}, err
			}
			if !bytes.Equal(assertion.Response.UserHandle, []byte(uid)) {
				return "", Identity{}, ErrCredentials
			}
		}
	}
	user, err := s.loadPasskeyUser(ctx, tx, uid)
	if err != nil {
		return "", Identity{}, err
	}
	if user.MustChange {
		return "", Identity{}, ErrCredentials
	}
	if purpose == "login" {
		if version, found := state.LoginVersions[uid]; !found || version != user.AuthVersion {
			return "", Identity{}, ErrCredentials
		}
	}
	ctx = audit.WithActor(ctx, user.UserID, user.HouseholdID)
	if err = audit.Bind(ctx, tx); err != nil {
		return "", Identity{}, err
	}
	switch purpose {
	case "register":
		err = LockRecentSession(ctx, tx, id)
	case "reauth":
		err = LockSession(ctx, tx, id)
	}
	if err != nil {
		return "", Identity{}, err
	}
	var credential *webauthn.Credential
	switch purpose {
	case "register":
		if len(user.Credentials) >= maxPasskeys {
			return "", Identity{}, Rejection("Pro Konto sind höchstens zehn Passkeys möglich.")
		}
		parsed, e := protocol.ParseCredentialCreationResponseBytes(response)
		if e != nil {
			return "", Identity{}, ErrCredentials
		}
		credential, err = engine.CreateCredential(user, state.Session, parsed)
	case "reauth":
		credential, err = engine.ValidateLogin(user, state.Session, assertion)
	default:
		var changed time.Time
		if err = tx.QueryRow(ctx, "SELECT password_changed_at FROM core.users WHERE id=$1", uid).Scan(&changed); err != nil {
			return "", Identity{}, err
		}
		if changed.After(state.StartedAt) {
			return "", Identity{}, ErrCredentials
		}
		credential, err = engine.ValidateDiscoverableLogin(func(rawID, handle []byte) (webauthn.User, error) {
			if !bytes.Equal(handle, user.WebAuthnID()) {
				return nil, ErrCredentials
			}
			return user, nil
		}, state.Session, assertion)
	}
	if err != nil || credential == nil {
		return "", Identity{}, ErrCredentials
	}
	if credential.Authenticator.CloneWarning {
		return "", Identity{}, Rejection("Dieser Passkey konnte nicht sicher bestätigt werden. Verwende dein Passwort und richte den Passkey erneut ein.")
	}
	if !credential.Flags.UserVerified {
		return "", Identity{}, ErrCredentials
	}
	raw, err := json.Marshal(passkeyEnvelope{UserID: uid, Credential: *credential})
	if err != nil {
		return "", Identity{}, err
	}
	keyID := value.UUID()
	if purpose != "register" {
		if err = tx.QueryRow(ctx, "SELECT id FROM core.passkey_credentials WHERE user_id=$1 AND credential_id=$2 FOR UPDATE", uid, credential.ID).Scan(&keyID); err != nil {
			return "", Identity{}, err
		}
	}
	sealed, err := s.SealRecord(secretbox.Binding{Purpose: "passkey-credential", UserID: uid, RecordID: keyID}, raw)
	if err != nil {
		return "", Identity{}, err
	}
	if purpose == "register" {
		err = tx.QueryRow(ctx, `INSERT INTO core.passkey_credentials(id,user_id,credential_id,encrypted_credential,name,encryption_format,encryption_key_id)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(credential_id) DO NOTHING RETURNING id`, keyID, uid, credential.ID, sealed.Ciphertext, state.Name, sealed.Format, sealed.KeyID).Scan(&keyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", Identity{}, Rejection("Dieser Passkey ist bereits registriert.")
		}
	} else {
		err = tx.QueryRow(ctx, `UPDATE core.passkey_credentials SET encrypted_credential=$3,last_used_at=clock_timestamp(),
 encryption_format=$4,encryption_key_id=$5 WHERE user_id=$1 AND credential_id=$2 AND id=$6 RETURNING id`, uid, credential.ID, sealed.Ciphertext, sealed.Format, sealed.KeyID, keyID).Scan(&keyID)
	}
	if err != nil {
		return "", Identity{}, err
	}
	rawToken := ""
	switch purpose {
	case "login":
		rawToken, err = s.createSession(ctx, tx, uid, ip, ua)
	case "reauth":
		_, err = tx.Exec(ctx, "UPDATE core.sessions SET reauthenticated_at=clock_timestamp() WHERE id=$1", id.SessionID)
	}
	if err != nil {
		return "", Identity{}, err
	}
	action, entity := "login_success", "passkeys"
	switch purpose {
	case "register":
		action = "record_write"
	case "reauth":
		entity = "reauthentication"
	}
	if err = audit.Event(ctx, tx, "core", entity, keyID, action, nil); err != nil {
		return "", Identity{}, err
	}
	if purpose == "register" {
		if err = securityevent.Record(ctx, tx, user.HouseholdID, uid, "passkey_added"); err != nil {
			return "", Identity{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Identity{}, err
	}
	if purpose == "login" {
		resolved, e := s.Resolve(ctx, rawToken, false)
		return rawToken, resolved, e
	}
	return "", id, nil
}
