// Package secretbox protects record-bound secrets with a bounded, immutable keyring.
// It has no deployment, database or authentication-service dependencies.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxConfigSize          = 32 << 10
	MaxKeys                = 8
	MaxPlaintextSize       = 128 << 10
	FormatLegacy     int16 = 1
	FormatBound      int16 = 2
)

var (
	ErrKeyUnavailable    = errors.New("encryption key unavailable")
	ErrInvalidBinding    = errors.New("invalid encryption binding")
	ErrInvalidCiphertext = errors.New("invalid encrypted data")
	ErrInvalidConfig     = errors.New("invalid encryption keyring configuration")
)

var purposes = [...]string{"totp", "passkey-credential", "passkey-register", "passkey-login", "passkey-reauth", "push-subscription"}

// Binding contains immutable row identities, never display names or current membership.
// Auth records omit HouseholdID; discoverable login may also omit UserID.
type Binding struct {
	Purpose     string
	HouseholdID string
	UserID      string
	RecordID    string
}

// Stored includes the authenticated format and key selection persisted with a row.
type Stored struct {
	Format     int16
	KeyID      string
	Ciphertext []byte
}

type material struct {
	secret     string
	master     [32]byte
	readLegacy cipher.AEAD
	bound      [6]cipher.AEAD
}

// Keyring is immutable after construction and safe for concurrent use. Only
// ExportConfig exposes secrets, for explicit private encrypted-backup escrow.
type Keyring struct {
	active string
	legacy string
	keys   map[string]material
}

type configEntry struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

type ringConfig struct {
	Version int           `json:"version"`
	Active  string        `json:"active"`
	Legacy  string        `json:"legacy,omitempty"`
	Keys    []configEntry `json:"keys"`
}

func (Keyring) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted encryption keyring]")
}
func (Keyring) String() string               { return "[redacted encryption keyring]" }
func (Keyring) GoString() string             { return "[redacted encryption keyring]" }
func (Keyring) LogValue() slog.Value         { return slog.StringValue("[redacted encryption keyring]") }
func (Keyring) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

// New preserves the exact original configured string, including environment
// whitespace. Its SHA-256 legacy AES material must never be base64-decoded first.
func New(legacy string) (*Keyring, error) {
	id := fingerprint(legacy)
	return build(ringConfig{Version: 1, Active: id, Legacy: id, Keys: []configEntry{{ID: id, Secret: legacy}}})
}

// GenerateKey returns a deterministic identifier and a fresh 256-bit random secret.
// Callers must write the secret only to explicitly requested private key storage.
func GenerateKey() (id, secret string, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", errors.New("generate encryption key failed")
	}
	secret = base64.RawURLEncoding.EncodeToString(raw[:])
	return fingerprint(secret), secret, nil
}

func fingerprint(secret string) string {
	digest := sha256.Sum256([]byte("household-encryption-v1\x00" + secret))
	return hex.EncodeToString(digest[:])
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func build(cfg ringConfig) (*Keyring, error) {
	if cfg.Version != 1 || !validID(cfg.Active) || cfg.Legacy != "" && !validID(cfg.Legacy) || len(cfg.Keys) < 1 || len(cfg.Keys) > MaxKeys {
		return nil, ErrInvalidConfig
	}
	ring := &Keyring{active: cfg.Active, legacy: cfg.Legacy, keys: make(map[string]material, len(cfg.Keys))}
	for _, entry := range cfg.Keys {
		if !utf8.ValidString(entry.Secret) || strings.ContainsRune(entry.Secret, utf8.RuneError) || len(strings.TrimSpace(entry.Secret)) < 32 || len(entry.Secret) > 4096 || strings.Contains(strings.ToLower(entry.Secret), "change-me") || entry.ID != fingerprint(entry.Secret) {
			return nil, ErrInvalidConfig
		}
		if _, exists := ring.keys[entry.ID]; exists {
			return nil, ErrInvalidConfig
		}
		if len(ring.keys) > 0 && ring.ValidateIndependent(entry.Secret) != nil {
			return nil, ErrInvalidConfig
		}
		key := material{secret: entry.Secret, master: sha256.Sum256([]byte(entry.Secret))}
		legacyBlock, err := aes.NewCipher(key.master[:])
		if err != nil {
			return nil, ErrInvalidConfig
		}
		// Random-nonce GCM's reader accepts the same nonce||ciphertext||tag bytes
		// used by historical Seal; it is never used to write legacy records.
		key.readLegacy, err = cipher.NewGCMWithRandomNonce(legacyBlock)
		if err != nil {
			return nil, ErrInvalidConfig
		}
		for i, purpose := range purposes {
			derived, err := hkdf.Key(sha256.New, key.master[:], []byte("household/secretbox/hkdf/v2"), "household/secretbox/aes256gcm/v2/"+purpose, 32)
			if err != nil {
				return nil, ErrInvalidConfig
			}
			block, err := aes.NewCipher(derived)
			clear(derived)
			if err != nil {
				return nil, ErrInvalidConfig
			}
			key.bound[i], err = cipher.NewGCMWithRandomNonce(block)
			if err != nil {
				return nil, ErrInvalidConfig
			}
		}
		ring.keys[entry.ID] = key
	}
	if _, exists := ring.keys[ring.active]; !exists {
		return nil, ErrInvalidConfig
	}
	if ring.legacy != "" {
		if _, exists := ring.keys[ring.legacy]; !exists {
			return nil, ErrInvalidConfig
		}
	}
	return ring, nil
}

func (r *Keyring) ActiveID() string {
	if r == nil {
		return ""
	}
	return r.active
}
func (r *Keyring) LegacyID() string {
	if r == nil {
		return ""
	}
	return r.legacy
}

// ReadableIDs returns a sorted defensive copy of the retained key identifiers.
func (r *Keyring) ReadableIDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.keys))
	for id := range r.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ExportConfig returns actual secret material for private encrypted escrow only.
func (r *Keyring) ExportConfig() ([]byte, error) {
	if r == nil || len(r.keys) == 0 {
		return nil, ErrInvalidConfig
	}
	cfg := ringConfig{Version: 1, Active: r.active, Legacy: r.legacy}
	for _, id := range r.ReadableIDs() {
		cfg.Keys = append(cfg.Keys, configEntry{ID: id, Secret: r.keys[id].secret})
	}
	data, err := json.Marshal(cfg)
	if err != nil || len(data) > MaxConfigSize {
		return nil, ErrInvalidConfig
	}
	return data, nil
}

// ValidateIndependent rejects identical or equivalent material across encryption
// and separately configured CSRF, rate-limit and VAPID secrets. Empty inputs are
// optional absent keys. It never exports or includes key material in errors.
func (r *Keyring) ValidateIndependent(secrets ...string) error {
	if r == nil || len(r.keys) == 0 || len(secrets) > 16 {
		return ErrInvalidConfig
	}
	all := make([]string, 0, len(r.keys)+len(secrets))
	for _, key := range r.keys {
		all = append(all, key.secret)
	}
	for _, secret := range secrets {
		if secret != "" {
			if len(secret) > MaxConfigSize {
				return ErrInvalidConfig
			}
			all = append(all, secret)
		}
	}
	for i, leftSecret := range all {
		for _, rightSecret := range all[i+1:] {
			for _, left := range equivalents(leftSecret) {
				for _, right := range equivalents(rightSecret) {
					if subtle.ConstantTimeCompare(left[:], right[:]) == 1 {
						return errors.New("security secrets must be independent")
					}
				}
			}
		}
	}
	return nil
}

func equivalents(secret string) [][32]byte {
	out := [][32]byte{sha256.Sum256([]byte(secret))}
	appendBytes := func(raw []byte) {
		if len(raw) == 32 {
			var value [32]byte
			copy(value[:], raw)
			out = append(out, value)
		}
	}
	appendBytes([]byte(secret))
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding.Strict(), base64.URLEncoding.Strict(), base64.RawStdEncoding.Strict(), base64.StdEncoding.Strict()} {
		if len(secret) <= 44 {
			if raw, err := encoding.DecodeString(secret); err == nil && encoding.EncodeToString(raw) == secret {
				appendBytes(raw)
			}
		}
	}
	if len(secret) == 64 {
		if raw, err := hex.DecodeString(secret); err == nil {
			appendBytes(raw)
		}
	}
	return out
}

func canonicalUUID(raw string) bool {
	if len(raw) != 36 || raw == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range raw {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func bindingPurpose(binding Binding) (int, error) {
	index := -1
	for i, purpose := range purposes {
		if binding.Purpose == purpose {
			index = i
			break
		}
	}
	if index < 0 || !canonicalUUID(binding.RecordID) {
		return 0, ErrInvalidBinding
	}
	if binding.Purpose == "push-subscription" {
		if !canonicalUUID(binding.HouseholdID) || !canonicalUUID(binding.UserID) {
			return 0, ErrInvalidBinding
		}
	} else {
		if binding.HouseholdID != "" || !canonicalUUID(binding.UserID) && (binding.Purpose != "passkey-login" || binding.UserID != "") {
			return 0, ErrInvalidBinding
		}
		if binding.Purpose == "totp" && binding.RecordID != binding.UserID {
			return 0, ErrInvalidBinding
		}
	}
	return index, nil
}

func aad(binding Binding, keyID string) []byte {
	fields := [...]string{"household", "secretbox", "2", keyID, binding.Purpose, binding.HouseholdID, binding.UserID, binding.RecordID}
	data := make([]byte, 0, 256)
	for _, field := range fields {
		data = binary.BigEndian.AppendUint32(data, uint32(len(field))) // #nosec G115 -- validated UUIDs, key IDs and closed purpose/constants are at most64 bytes.
		data = append(data, field...)
	}
	return data
}

// Seal always writes format2 using a fresh standard-library random nonce. Rotate
// a purpose key before 2^32 encryptions, as required by random-nonce AES-GCM.
func (r *Keyring) Seal(binding Binding, plaintext []byte) (Stored, error) {
	index, err := bindingPurpose(binding)
	if err != nil {
		return Stored{}, err
	}
	if len(plaintext) > MaxPlaintextSize {
		return Stored{}, ErrInvalidCiphertext
	}
	if r == nil {
		return Stored{}, ErrKeyUnavailable
	}
	key, exists := r.keys[r.active]
	if !exists {
		return Stored{}, ErrKeyUnavailable
	}
	return Stored{Format: FormatBound, KeyID: r.active, Ciphertext: key.bound[index].Seal(nil, nil, plaintext, aad(binding, r.active))}, nil // #nosec G407 -- bound AEADs are exclusively NewGCMWithRandomNonce; nil is its required nonce argument and Seal prepends a fresh random nonce.
}

// Open dispatches strictly from stored metadata. Legacy reads use exactly one
// explicitly selected key; a failed bound-format authentication never falls back.
func (r *Keyring) Open(binding Binding, stored Stored) ([]byte, error) {
	index, err := bindingPurpose(binding)
	if err != nil {
		return nil, err
	}
	if stored.Format != FormatLegacy && stored.Format != FormatBound || len(stored.Ciphertext) < 28 || len(stored.Ciphertext) > MaxPlaintextSize+28 {
		return nil, ErrInvalidCiphertext
	}
	if r == nil {
		return nil, ErrKeyUnavailable
	}
	var aead cipher.AEAD
	var associated []byte
	if stored.Format == FormatLegacy {
		if stored.KeyID != "v1" && !validID(stored.KeyID) || stored.KeyID == "v1" && binding.Purpose == "push-subscription" {
			return nil, ErrInvalidCiphertext
		}
		if r.legacy == "" || stored.KeyID != "v1" && stored.KeyID != r.legacy {
			return nil, ErrKeyUnavailable
		}
		key, exists := r.keys[r.legacy]
		if !exists {
			return nil, ErrKeyUnavailable
		}
		aead, associated = key.readLegacy, []byte("household-v1")
	} else {
		if !validID(stored.KeyID) {
			return nil, ErrInvalidCiphertext
		}
		key, exists := r.keys[stored.KeyID]
		if !exists {
			return nil, ErrKeyUnavailable
		}
		aead, associated = key.bound[index], aad(binding, stored.KeyID)
	}
	plaintext, err := aead.Open(nil, nil, stored.Ciphertext, associated)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}
