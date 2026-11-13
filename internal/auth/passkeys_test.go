package auth

import (
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func TestPasskeyEnginePolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, origin string
		allowed      bool
	}{
		{"local", "http://localhost:8100", true}, {"secure", "https://household.example", true},
		{"remote insecure", "http://household.example", false}, {"numeric local", "http://127.0.0.1:8100", false},
		{"missing", "", false}, {"opaque", "data:application/json,{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, err := passkeyEngine(tc.origin)
			if (err == nil) != tc.allowed {
				t.Fatalf("availability=%v want %v: %v", err == nil, tc.allowed, err)
			}
			if !tc.allowed {
				return
			}
			user := passkeyUser{Identity: Identity{UserID: "148a506f-b6bc-4ca0-9131-0091e9f8b994", Username: "member", DisplayName: "Member"}}
			created, session, err := engine.BeginRegistration(user, webauthn.WithRegistrationOrigin(tc.origin))
			if err != nil {
				t.Fatal(err)
			}
			if created.Response.AuthenticatorSelection.UserVerification != protocol.VerificationRequired || created.Response.AuthenticatorSelection.ResidentKey != protocol.ResidentKeyRequirementRequired || session.UserVerification != protocol.VerificationRequired || session.Origin != tc.origin || session.Expires.IsZero() {
				t.Fatal("registration lost verification, discoverability, origin or expiry policy")
			}
			login, state, err := engine.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired), webauthn.WithLoginOrigin(tc.origin))
			if err != nil {
				t.Fatal(err)
			}
			if login.Response.UserVerification != protocol.VerificationRequired || state.UserVerification != protocol.VerificationRequired || state.Origin != tc.origin || len(login.Response.AllowedCredentials) != 0 {
				t.Fatal("discoverable login weakened policy or disclosed account credentials")
			}
		})
	}
}

func TestValidPasskeyName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{"device", "Mein Telefon", true}, {"unicode", strings.Repeat("界", 100), true}, {"long", strings.Repeat("x", 101), false}, {"empty", "", false}, {"newline", "key\nname", false}, {"invalid encoding", string([]byte{0xff}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := validPasskeyName(tc.input); got != tc.want {
				t.Fatalf("valid=%v want %v", got, tc.want)
			}
		})
	}
}
