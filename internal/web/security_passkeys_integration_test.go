//go:build integration

package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"

	"household/internal/auth"

	"github.com/fxamacker/cbor/v2"
)

type securityAuthenticator struct {
	key  *ecdsa.PrivateKey
	id   []byte
	user string
}

func securityJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func securityBase64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
func securityChallenge(t *testing.T, begin map[string]any) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(securityJSON(t, begin["options"]), &fields); err != nil {
		t.Fatal(err)
	}
	var challenge string
	if err := json.Unmarshal(fields["challenge"], &challenge); err != nil {
		t.Fatal(err)
	}
	return challenge
}
func securityClientData(t *testing.T, kind, challenge, origin string) []byte {
	return securityJSON(t, map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
}
func securityAuthData(flags byte, counter uint32) []byte {
	hash := sha256.Sum256([]byte("localhost"))
	data := append([]byte{}, hash[:]...)
	data = append(data, flags, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(data[33:], counter)
	return data
}
func newSecurityAuthenticator(t *testing.T, user string) *securityAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &securityAuthenticator{key: key, id: id, user: user}
}
func (a *securityAuthenticator) register(t *testing.T, challenge string) []byte {
	t.Helper()
	public, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: a.key.X.FillBytes(make([]byte, 32)), -3: a.key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	data := securityAuthData(0x45, 0)
	data = append(data, make([]byte, 16)...)
	data = append(data, 0, byte(len(a.id)))
	data = append(data, a.id...)
	data = append(data, public...)
	attestation, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": data})
	if err != nil {
		t.Fatal(err)
	}
	return securityJSON(t, map[string]any{"id": securityBase64(a.id), "rawId": securityBase64(a.id), "type": "public-key", "authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": securityBase64(securityClientData(t, "webauthn.create", challenge, "http://localhost:8100")), "attestationObject": securityBase64(attestation), "transports": []string{"internal"}}})
}
func (a *securityAuthenticator) assert(t *testing.T, challenge, origin string, flags byte, counter uint32) []byte {
	t.Helper()
	client := securityClientData(t, "webauthn.get", challenge, origin)
	hash := sha256.Sum256(client)
	data := securityAuthData(flags, counter)
	message := append(append([]byte{}, data...), hash[:]...)
	digest := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return securityJSON(t, map[string]any{"id": securityBase64(a.id), "rawId": securityBase64(a.id), "type": "public-key", "authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": securityBase64(client), "authenticatorData": securityBase64(data), "signature": securityBase64(signature), "userHandle": securityBase64([]byte(a.user))}})
}

func TestSecurityPasskeyCryptographicLifecycle(t *testing.T) {
	f := newIntegrationFixture(t)
	device := newSecurityAuthenticator(t, f.identity.UserID)
	begin, err := f.app.Auth.BeginPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "Test device")
	if err != nil {
		t.Fatal(err)
	}
	registration := device.register(t, securityChallenge(t, begin))
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "test", begin["challenge_id"].(string), registration); err != nil {
		t.Fatal("registration", err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "test", begin["challenge_id"].(string), registration); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("registration replay accepted", err)
	}
	keys, err := f.app.Auth.ListPasskeys(f.ctx, f.identity)
	if err != nil || len(keys) != 1 || keys[0].Name != "Test device" {
		t.Fatal("registration list", keys, err)
	}
	var raw []byte
	if err = f.owner.QueryRow(f.ctx, "SELECT encrypted_credential FROM core.passkey_credentials WHERE user_id=$1", f.identity.UserID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Valid(raw) {
		t.Fatal("credential stored without encryption")
	}
	binding := auth.RandomToken()
	login, err := f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	assertion := device.assert(t, securityChallenge(t, login), "http://localhost:8100", 0x05, 1)
	token, resolved, err := f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "Virtual passkey", login["challenge_id"].(string), assertion)
	if err != nil || resolved.UserID != f.identity.UserID || token == "" {
		t.Fatal("passkey login", resolved, err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "test", login["challenge_id"].(string), assertion); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("login replay accepted", err)
	}
	for _, tc := range []struct {
		name, origin string
		flags        byte
		counter      uint32
		otherBinding bool
	}{
		{"wrong origin", "https://attacker.example", 0x05, 2, false},
		{"missing user verification", "http://localhost:8100", 0x01, 2, false},
		{"counter regression", "http://localhost:8100", 0x05, 1, false},
		{"wrong browser", "http://localhost:8100", 0x05, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, e := f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "")
			if e != nil {
				t.Fatal(e)
			}
			proof := device.assert(t, securityChallenge(t, start), tc.origin, tc.flags, tc.counter)
			bound := binding
			if tc.otherBinding {
				bound = auth.RandomToken()
			}
			if _, _, e = f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", bound, "127.0.0.1", "test", start["challenge_id"].(string), proof); e == nil {
				t.Fatal("unsafe assertion accepted")
			}
		})
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.sessions SET reauthenticated_at=now()-interval '1 hour' WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	stale, err := f.app.Auth.Resolve(f.ctx, f.token, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Auth.BeginPasskey(f.ctx, stale, "register", f.token, "127.0.0.1", "Another"); !errors.Is(err, auth.ErrRecent) {
		t.Fatal("stale registration allowed", err)
	}
	reauth, err := f.app.Auth.BeginPasskey(f.ctx, stale, "reauth", f.token, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, stale, "reauth", f.token, "127.0.0.1", "test", reauth["challenge_id"].(string), device.assert(t, securityChallenge(t, reauth), "http://localhost:8100", 0x05, 2)); err != nil {
		t.Fatal("passkey confirmation", err)
	}
	current, err := f.app.Auth.Resolve(f.ctx, f.token, false)
	if err != nil || !current.Recent() {
		t.Fatal("passkey did not refresh authentication", err)
	}
	if err = f.app.Auth.ChangePasskey(f.ctx, current, keys[0].ID, "Renamed device", false); err != nil {
		t.Fatal(err)
	}
	foreign := shoppingTestMember(t, f, "passkeyforeign", "editor")
	if err = f.app.Auth.ChangePasskey(f.ctx, foreign, keys[0].ID, "Stolen", false); err == nil {
		t.Fatal("foreign credential rename accepted")
	}
	if err = f.app.Auth.ChangePasskey(f.ctx, current, keys[0].ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Auth.Resolve(f.ctx, token, false); !errors.Is(err, auth.ErrSession) {
		t.Fatal("credential removal retained another session", err)
	}
	if keys, err = f.app.Auth.ListPasskeys(f.ctx, current); err != nil || len(keys) != 0 {
		t.Fatal("deleted credential remains", keys, err)
	}
}

func TestSecurityPasskeyChallengeInvalidation(t *testing.T) {
	f := newIntegrationFixture(t)
	device := newSecurityAuthenticator(t, f.identity.UserID)
	begin, err := f.app.Auth.BeginPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "Test device")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.auth_challenges SET expires_at=now()-interval '1 second' WHERE token_hash=$1", auth.HashToken(begin["challenge_id"].(string))); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "test", begin["challenge_id"].(string), device.register(t, securityChallenge(t, begin))); !errors.Is(err, auth.ErrCredentials) {
		t.Fatal("expired ceremony accepted", err)
	}
	begin, err = f.app.Auth.BeginPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "Test device")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.users SET disabled_at=now(),auth_version=auth_version+1 WHERE id=$1", f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "test", begin["challenge_id"].(string), device.register(t, securityChallenge(t, begin))); err == nil {
		t.Fatal("disabled account registered credential")
	}
	binding := auth.RandomToken()
	for range 20 {
		if _, err = f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "198.51.100.2", ""); err != nil {
			t.Fatal("begin limit fired early", err)
		}
	}
	if _, err = f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "198.51.100.2", ""); !errors.Is(err, auth.ErrLimited) {
		t.Fatal("begin requests are not limited", err)
	}
	for range 20 {
		if _, _, err = f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "198.51.100.2", "test", "invalid", []byte(`{}`)); !errors.Is(err, auth.ErrCredentials) {
			t.Fatal("finish limit fired early", err)
		}
	}
	if _, _, err = f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "198.51.100.2", "test", "invalid", []byte(`{}`)); !errors.Is(err, auth.ErrLimited) {
		t.Fatal("finish requests are not limited", err)
	}
}

func TestSecurityAnonymousPasskeyGenerationInvalidation(t *testing.T) {
	f := newIntegrationFixture(t)
	device := newSecurityAuthenticator(t, f.identity.UserID)
	start, err := f.app.Auth.BeginPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "Generation device")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.app.Auth.FinishPasskey(f.ctx, f.identity, "register", f.token, "127.0.0.1", "test", start["challenge_id"].(string), device.register(t, securityChallenge(t, start))); err != nil {
		t.Fatal(err)
	}
	binding := auth.RandomToken()
	start, err = f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	// Disable and re-enable leaves the account available, but the earlier
	// anonymous ceremony must not regain authority under its old generation.
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.users SET disabled_at=now(),auth_version=auth_version+1 WHERE id=$1`, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.users SET disabled_at=NULL,auth_version=auth_version+1 WHERE id=$1`, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	proof := device.assert(t, securityChallenge(t, start), "http://localhost:8100", 0x05, 1)
	if _, _, err := f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "test", start["challenge_id"].(string), proof); !errors.Is(err, auth.ErrCredentials) {
		t.Fatalf("anonymous ceremony survived account generation change: %v", err)
	}
	start, err = f.app.Auth.BeginPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	proof = device.assert(t, securityChallenge(t, start), "http://localhost:8100", 0x05, 1)
	if token, _, err := f.app.Auth.FinishPasskey(f.ctx, auth.Identity{}, "login", binding, "127.0.0.1", "test", start["challenge_id"].(string), proof); err != nil || token == "" {
		t.Fatalf("new ceremony did not accept the current account generation: %v", err)
	}
}
