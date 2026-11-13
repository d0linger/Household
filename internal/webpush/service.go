package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/config"
	"household/internal/secretbox"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool                                     *pgxpool.Pool
	auth                                     *auth.Service
	store                                    *store.Store
	log                                      *slog.Logger
	state, private, public, contact, vapidID string
	keys                                     *secretbox.Keyring
	transport                                *sender
}

// New validates supplied keys even when the optional transport is disabled.
func New(pool *pgxpool.Pool, authentication *auth.Service, records *store.Store, cfg config.Config, log *slog.Logger) (*Service, error) {
	s := &Service{pool: pool, auth: authentication, store: records, log: log, state: "disabled"}
	keys, err := cfg.Keyring()
	if err != nil {
		return nil, err
	}
	s.keys = keys
	if cfg.WebPushPrivateKey != "" {
		private, err := decodeKey(cfg.WebPushPrivateKey, 32)
		if err != nil {
			return nil, errors.New("invalid Web Push private key")
		}
		key, err := ecdh.P256().NewPrivateKey(private)
		if err != nil {
			return nil, errors.New("invalid Web Push private key")
		}
		s.private = cfg.WebPushPrivateKey
		s.public = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
		s.vapidID = digest(s.public)
	}
	if cfg.WebPushContact != "" && !validContact(cfg.WebPushContact) {
		return nil, errors.New("invalid Web Push contact URI")
	}
	s.contact = cfg.WebPushContact
	if cfg.WebPushEnabled {
		s.state = "not_configured"
		if s.private != "" && s.contact != "" {
			s.state = "ready"
		}
	}
	s.transport = newSender(s.public, s.private, s.contact)
	return s, nil
}

// GenerateKeys returns a private scalar followed by its uncompressed public key.
// It does not read configuration, connect to a database, or create files.
func GenerateKeys() (string, string, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", errors.New("generate Web Push key failed")
	}
	return base64.RawURLEncoding.EncodeToString(key.Bytes()), base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func validContact(raw string) bool {
	if len(raw) > 512 || strings.TrimSpace(raw) != raw {
		return false
	}
	for _, r := range raw {
		if r < 33 || r > 126 {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.RawQuery != "" || u.User != nil {
		return false
	}
	if u.Scheme == "mailto" {
		address, err := mail.ParseAddress(u.Opaque)
		return err == nil && address.Address == u.Opaque && address.Name == "" && strings.Contains(address.Address, "@")
	}
	return u.Scheme == "https" && u.Hostname() != "" && u.Opaque == "" && (u.Port() == "" || u.Port() == "443")
}

func digest(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func canonicalUUID(raw string) bool { return value.ValidUUID(raw) && strings.ToLower(raw) == raw }
func hexDigest(raw string) bool {
	if len(raw) != 64 {
		return false
	}
	for _, r := range raw {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func personal(id auth.Identity) PersonalContext {
	session := digest("household-push-session-v1\x00" + id.HouseholdID + "\x00" + id.UserID + "\x00" + id.SessionID)
	scope := digest("household-push-authority-v1\x00" + session + "\x00" + strconv.FormatInt(id.AuthVersion, 10) + "\x00" + id.Role)
	return PersonalContext{UserID: id.UserID, HouseholdID: id.HouseholdID, SessionScope: session, AuthScope: scope, Role: id.Role}
}

func (s *Service) begin(ctx context.Context, id auth.Identity) (pgx.Tx, error) {
	if s == nil || s.pool == nil || s.auth == nil || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.SessionID) || id.MustChange {
		return nil, store.ErrForbidden
	}
	tx, err := audit.Begin(audit.WithActor(ctx, id.UserID, id.HouseholdID), s.pool)
	if err != nil {
		return nil, err
	}
	if err = auth.LockSession(ctx, tx, id); err != nil {
		rollback(tx)
		return nil, store.ErrForbidden
	}
	var ready bool
	err = tx.QueryRow(ctx, `SELECT NOT u.must_change_password FROM core.users u WHERE u.id=$1`, id.UserID).Scan(&ready)
	if err != nil || !ready {
		rollback(tx)
		if err != nil {
			return nil, err
		}
		return nil, store.ErrForbidden
	}
	return tx, nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Service) Context(ctx context.Context, id auth.Identity) (Context, error) {
	tx, err := s.begin(ctx, id)
	if err != nil {
		return Context{}, err
	}
	defer rollback(tx)
	result := Context{Context: personal(id), State: s.state, DeviceLimit: DeviceLimit}
	if s.state == "ready" {
		result.ApplicationServerKey = &s.public
		result.VAPIDKeyID = &s.vapidID
	}
	if err = tx.Commit(ctx); err != nil {
		return Context{}, err
	}
	return result, nil
}

// Configuration exposes public setup facts without requiring an enabled module.
func (s *Service) Configuration() Configuration {
	result := Configuration{State: "disabled", DeviceLimit: DeviceLimit}
	if s == nil {
		return result
	}
	result.State = s.state
	if s.state == "ready" {
		result.ApplicationServerKey = &s.public
		result.VAPIDKeyID = &s.vapidID
	}
	return result
}

func (s *Service) Close() {
	if s != nil && s.transport != nil {
		s.transport.close()
	}
}

func decodeKey(raw string, size int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != size || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nil, errors.New("invalid receiver key")
	}
	return decoded, nil
}

func validateReceiver(receiver Receiver) error {
	if _, err := validateEndpoint(receiver.Endpoint); err != nil {
		return store.Invalid{Message: "Push-Ziel ist nicht zulässig"}
	}
	point, err := decodeKey(receiver.Keys.P256DH, 65)
	if err != nil {
		return store.Invalid{Message: "Ungültiger Push-Empfängerschlüssel"}
	}
	if _, err = ecdh.P256().NewPublicKey(point); err != nil {
		return store.Invalid{Message: "Ungültiger Push-Empfängerschlüssel"}
	}
	if _, err = decodeKey(receiver.Keys.Auth, 16); err != nil {
		return store.Invalid{Message: "Ungültiges Push-Empfängergeheimnis"}
	}
	return nil
}

func strictJSON(raw []byte, target any) error {
	if len(raw) > maxCipher {
		return errors.New("push data exceeds limit")
	}
	probe := json.NewDecoder(bytes.NewReader(raw))
	probe.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 8 {
			return errors.New("push data nesting exceeds limit")
		}
		token, err := probe.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for probe.More() {
				key, err := probe.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate push data field")
				}
				seen[name] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := probe.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid push object")
			}
		case json.Delim('['):
			for probe.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := probe.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid push array")
			}
		case json.Delim('}'), json.Delim(']'):
			return errors.New("invalid push delimiter")
		}
		return nil
	}
	if err := walk(0); err != nil {
		return errors.New("invalid closed push data")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid closed push data")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing push data")
	}
	return nil
}
