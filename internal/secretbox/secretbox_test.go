package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testSecretA = "synthetic encryption fixture alpha 0123456789"
const testSecretB = "synthetic encryption fixture bravo 0123456789"
const testUser = "11111111-1111-4111-8111-111111111111"
const testOther = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const testRecord = "22222222-2222-4222-8222-222222222222"
const testHousehold = "33333333-3333-4333-8333-333333333333"

func testRing(t *testing.T, active, legacy string, secrets ...string) *Keyring {
	t.Helper()
	cfg := ringConfig{Version: 1, Active: fingerprint(active)}
	if legacy != "" {
		cfg.Legacy = fingerprint(legacy)
	}
	for _, secret := range secrets {
		cfg.Keys = append(cfg.Keys, configEntry{ID: fingerprint(secret), Secret: secret})
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func oldCipher(t *testing.T, secret string, plaintext []byte) []byte {
	t.Helper()
	master := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(master[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x7c}, aead.NonceSize())
	return aead.Seal(nonce, nonce, plaintext, []byte("household-v1"))
}

func TestSecretboxBoundPurposeIdentityAndFreshNonces(t *testing.T) {
	ring := testRing(t, testSecretA, testSecretA, testSecretA)
	bindings := []Binding{
		{Purpose: "totp", UserID: testUser, RecordID: testUser},
		{Purpose: "passkey-credential", UserID: testUser, RecordID: testRecord},
		{Purpose: "passkey-register", UserID: testUser, RecordID: testRecord},
		{Purpose: "passkey-login", RecordID: testRecord},
		{Purpose: "passkey-login", UserID: testUser, RecordID: testRecord},
		{Purpose: "passkey-reauth", UserID: testUser, RecordID: testRecord},
		{Purpose: "push-subscription", HouseholdID: testHousehold, UserID: testUser, RecordID: testRecord},
	}
	for _, binding := range bindings {
		t.Run(binding.Purpose+"/"+binding.UserID, func(t *testing.T) {
			plaintext := []byte("synthetic protected fixture")
			first, err := ring.Seal(binding, plaintext)
			if err != nil {
				t.Fatal(err)
			}
			second, err := ring.Seal(binding, plaintext)
			if err != nil {
				t.Fatal(err)
			}
			if first.Format != 2 || first.KeyID != fingerprint(testSecretA) || len(first.Ciphertext) != len(plaintext)+28 || bytes.Equal(first.Ciphertext[:12], second.Ciphertext[:12]) {
				t.Fatal("fresh nonce or authenticated metadata contract changed")
			}
			opened, err := ring.Open(binding, first)
			if err != nil || !bytes.Equal(opened, plaintext) {
				t.Fatal("actual bound round trip failed", err)
			}
			changed := binding
			changed.RecordID = testOther
			if changed.Purpose == "totp" {
				changed.UserID = testOther
			}
			if _, err := ring.Open(changed, first); !errors.Is(err, ErrInvalidCiphertext) {
				t.Fatal("cross-record ciphertext accepted", err)
			}
			if binding.Purpose != "totp" {
				changed = binding
				changed.UserID = testOther
				if _, err := ring.Open(changed, first); !errors.Is(err, ErrInvalidCiphertext) {
					t.Fatal("cross-account ciphertext accepted", err)
				}
			}
			if binding.Purpose == "push-subscription" {
				changed = binding
				changed.HouseholdID = testOther
				if _, err := ring.Open(changed, first); !errors.Is(err, ErrInvalidCiphertext) {
					t.Fatal("cross-household ciphertext accepted", err)
				}
			}
		})
	}
	credential := Binding{Purpose: "passkey-credential", UserID: testUser, RecordID: testRecord}
	stored, err := ring.Seal(credential, []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	credential.Purpose = "passkey-register"
	if _, err := ring.Open(credential, stored); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("cross-purpose ciphertext accepted", err)
	}
}

func TestSecretboxExactLegacyReaderAndExplicitRetirement(t *testing.T) {
	secret := "  " + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32)) + " \t"
	ring, err := New(secret)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{Purpose: "totp", UserID: testUser, RecordID: testUser}
	legacy := Stored{Format: 1, KeyID: "v1", Ciphertext: oldCipher(t, secret, []byte("original TOTP bytes"))}
	got, err := ring.Open(binding, legacy)
	if err != nil || string(got) != "original TOTP bytes" {
		t.Fatal("exact historical string was changed", err)
	}
	trimmed, err := New(strings.TrimSpace(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trimmed.Open(binding, legacy); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("legacy secret whitespace was ignored", err)
	}
	push := Binding{Purpose: "push-subscription", HouseholdID: testHousehold, UserID: testUser, RecordID: testRecord}
	if _, err := ring.Open(push, legacy); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("push accepted auth v1 alias", err)
	}
	legacy.KeyID = ring.LegacyID()
	if _, err := ring.Open(push, legacy); err != nil {
		t.Fatal("selected legacy push fingerprint rejected", err)
	}
	retired := testRing(t, secret, "", secret)
	if _, err := retired.Open(push, legacy); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("legacy read succeeded without explicit legacy selection", err)
	}
	retained := testRing(t, testSecretB, testSecretB, testSecretB, secret)
	if _, err := retained.Open(push, legacy); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("reader tried nonselected retained legacy key", err)
	}
	legacy.KeyID = "v1"
	if _, err := retained.Open(binding, legacy); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("v1 alias tried every retained key", err)
	}
}

func TestSecretboxRotationTamperAndNoDowngrade(t *testing.T) {
	old := testRing(t, testSecretA, testSecretA, testSecretA)
	rotated := testRing(t, testSecretB, testSecretA, testSecretA, testSecretB)
	binding := Binding{Purpose: "passkey-reauth", UserID: testUser, RecordID: testRecord}
	stored, err := old.Seal(binding, []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Open(binding, stored); err != nil {
		t.Fatal("retained read key was not used", err)
	}
	newStored, err := rotated.Seal(binding, []byte("fixture"))
	if err != nil || newStored.KeyID != fingerprint(testSecretB) {
		t.Fatal("write did not use explicit active key", err)
	}
	if _, err := old.Open(binding, newStored); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatal("unknown key was not operationally distinct", err)
	}
	for _, change := range []struct {
		name  string
		apply func(*Stored)
	}{
		{"format downgrade", func(s *Stored) { s.Format = 1 }},
		{"unsupported format", func(s *Stored) { s.Format = 3 }},
		{"retained wrong key", func(s *Stored) { s.KeyID = fingerprint(testSecretB) }},
		{"nonce", func(s *Stored) { s.Ciphertext[0] ^= 1 }},
		{"tag", func(s *Stored) { s.Ciphertext[len(s.Ciphertext)-1] ^= 1 }},
		{"truncated", func(s *Stored) { s.Ciphertext = s.Ciphertext[:27] }},
	} {
		t.Run(change.name, func(t *testing.T) {
			altered := stored
			altered.Ciphertext = bytes.Clone(stored.Ciphertext)
			change.apply(&altered)
			if _, err := rotated.Open(binding, altered); !errors.Is(err, ErrInvalidCiphertext) {
				t.Fatal("tampered envelope accepted or fell back", err)
			}
		})
	}
}

func TestSecretboxBindingAndSizeBounds(t *testing.T) {
	ring := testRing(t, testSecretA, "", testSecretA)
	for _, binding := range []Binding{
		{}, {Purpose: "unknown", UserID: testUser, RecordID: testRecord},
		{Purpose: "totp", UserID: testUser, RecordID: testRecord},
		{Purpose: "totp", HouseholdID: testHousehold, UserID: testUser, RecordID: testUser},
		{Purpose: "passkey-register", RecordID: testRecord},
		{Purpose: "passkey-reauth", RecordID: testRecord},
		{Purpose: "passkey-login", HouseholdID: testHousehold, RecordID: testRecord},
		{Purpose: "passkey-login", RecordID: strings.ToUpper(testOther)},
		{Purpose: "passkey-login", RecordID: "00000000-0000-0000-0000-000000000000"},
		{Purpose: "push-subscription", UserID: testUser, RecordID: testRecord},
	} {
		if _, err := ring.Seal(binding, nil); !errors.Is(err, ErrInvalidBinding) {
			t.Fatal("invalid binding accepted", err)
		}
	}
	binding := Binding{Purpose: "passkey-login", RecordID: testRecord}
	stored, err := ring.Seal(binding, make([]byte, MaxPlaintextSize))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := ring.Open(binding, stored); err != nil || len(plain) != MaxPlaintextSize {
		t.Fatal("bounded maximum rejected", err)
	}
	if _, err := ring.Seal(binding, make([]byte, MaxPlaintextSize+1)); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("oversized plaintext accepted", err)
	}
	stored.Ciphertext = make([]byte, MaxPlaintextSize+29)
	if _, err := ring.Open(binding, stored); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("oversized ciphertext accepted", err)
	}
}

func TestSecretboxConcurrentImmutableEscrowAndRedaction(t *testing.T) {
	ring := testRing(t, testSecretB, testSecretA, testSecretA, testSecretB)
	ids := ring.ReadableIDs()
	ids[0] = "mutated"
	if ring.ReadableIDs()[0] == "mutated" {
		t.Fatal("key inventory aliases internal state")
	}
	escrow, err := ring.ExportConfig()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseConfig(escrow)
	if err != nil || restored.ActiveID() != ring.ActiveID() || restored.LegacyID() != ring.LegacyID() {
		t.Fatal("private escrow round trip failed", err)
	}
	clear(escrow)
	public, err := json.Marshal(ring)
	if err != nil {
		t.Fatal(err)
	}
	for _, printed := range []string{fmt.Sprintf("%v %+v %#v %s %x", ring, ring, *ring, ring, ring), string(public), ring.LogValue().String()} {
		if strings.Contains(printed, testSecretA) || strings.Contains(printed, testSecretB) {
			t.Fatal("keyring formatting disclosed secret")
		}
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			binding := Binding{Purpose: "passkey-login", RecordID: testRecord}
			for range 8 {
				stored, err := ring.Seal(binding, []byte("concurrent"))
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := restored.Open(binding, stored)
				if err != nil || string(plain) != "concurrent" {
					t.Error("concurrent retained read failed", err)
					return
				}
			}
		})
	}
	group.Wait()
	newID, generated, err := GenerateKey()
	if err != nil || newID != fingerprint(generated) {
		t.Fatal("generated key ID invalid", err)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(generated)
	if err != nil || len(raw) != 32 || len(generated) != 43 {
		t.Fatal("generated secret is not canonical256bit material", err)
	}
}

func TestSecretboxIndependentMaterial(t *testing.T) {
	raw := bytes.Repeat([]byte{0x91}, 32)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	ring, err := New(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{encoded, base64.StdEncoding.EncodeToString(raw), hex.EncodeToString(raw)} {
		if err := ring.ValidateIndependent(duplicate); err == nil {
			t.Fatal("equivalent cross-purpose material accepted")
		}
	}
	if err := ring.ValidateIndependent(testSecretA, testSecretA); err == nil {
		t.Fatal("duplicate non-encryption secret accepted")
	}
	if err := ring.ValidateIndependent(testSecretA, testSecretB, ""); err != nil {
		t.Fatal("independent secrets rejected", err)
	}
}
