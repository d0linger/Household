package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
)

func pantryReviewRequest(id auth.Identity, key string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/pantry/leftovers/execute", nil)
	r.Header.Set("Idempotency-Key", key)
	return r.WithContext(context.WithValue(r.Context(), identityKey, id))
}

func TestPantryReviewBindsCurrentIdentityAndIntent(t *testing.T) {
	s := &Server{Cfg: config.Config{CSRFSecret: strings.Repeat("synthetic-review-key", 3)}}
	id := auth.Identity{UserID: "00000000-0000-4000-8000-000000000001",
		HouseholdID:  "00000000-0000-4000-8000-000000000002",
		MembershipID: "00000000-0000-4000-8000-000000000003",
		SessionID:    "00000000-0000-4000-8000-000000000004", AuthVersion: 4, Role: "editor"}
	key := "00000000-0000-4000-8000-000000000005"
	r := pantryReviewRequest(id, key)
	input := map[string]any{"generation": 3, "quantity": "1.250001"}
	token, expires, err := s.issuePantryReview(r, "leftovers", key, 3, input, strings.Repeat("a", 64))
	if err != nil || expires <= time.Now().Unix() {
		t.Fatalf("issue review: %v", err)
	}
	got, err := s.parsePantryReview(r, token, "leftovers")
	if err != nil || got.Generation != 3 || got.Key != key ||
		!strings.Contains(string(got.Input), `"quantity":"1.250001"`) {
		t.Fatalf("valid exact review: %v", err)
	}
	mutations := []struct {
		name string
		edit func(*auth.Identity)
	}{
		{name: "different user", edit: func(v *auth.Identity) { v.UserID = key }},
		{name: "different household", edit: func(v *auth.Identity) { v.HouseholdID = key }},
		{name: "different membership", edit: func(v *auth.Identity) { v.MembershipID = key }},
		{name: "different session", edit: func(v *auth.Identity) { v.SessionID = key }},
		{name: "changed authentication", edit: func(v *auth.Identity) { v.AuthVersion++ }},
		{name: "changed role", edit: func(v *auth.Identity) { v.Role = "reader" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			other := id
			tc.edit(&other)
			if _, err := s.parsePantryReview(pantryReviewRequest(other, key), token, "leftovers"); err == nil {
				t.Fatal("review crossed its current identity scope")
			}
		})
	}
	for _, candidate := range []struct{ name, token, key, kind string }{
		{"changed intent key", token, id.SessionID, "leftovers"},
		{"cross action", token, key, "food-facts"},
		{"extra signature part", token + ".extra", key, "leftovers"},
		{"corrupt payload", "x" + token, key, "leftovers"},
		{"oversized", strings.Repeat("x", 2*pantryReviewLimit+1), key, "leftovers"},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			if _, err := s.parsePantryReview(pantryReviewRequest(id, candidate.key), candidate.token, candidate.kind); err == nil {
				t.Fatal("unbound or malformed review accepted")
			}
		})
	}
}

func TestPantryReviewRejectsExpiredAndMalformedServerEnvelope(t *testing.T) {
	s := &Server{Cfg: config.Config{CSRFSecret: strings.Repeat("synthetic-expiry-key", 3)}}
	key := "00000000-0000-4000-8000-000000000010"
	r := pantryReviewRequest(auth.Identity{UserID: key, HouseholdID: key, SessionID: key, Role: "editor"}, key)
	base := pantryExtensionReview{Version: 1, Kind: "leftovers", Key: key, Generation: 1,
		Expires: time.Now().Add(time.Minute).Unix(), Fingerprint: strings.Repeat("b", 64),
		Input: json.RawMessage(`{"generation":1}`)}
	cases := []struct {
		name string
		edit func(*pantryExtensionReview)
	}{
		{name: "expired", edit: func(v *pantryExtensionReview) { v.Expires = time.Now().Add(-time.Second).Unix() }},
		{name: "unversioned", edit: func(v *pantryExtensionReview) { v.Version = 0 }},
		{name: "missing generation", edit: func(v *pantryExtensionReview) { v.Generation = 0 }},
		{name: "short fingerprint", edit: func(v *pantryExtensionReview) { v.Fingerprint = "abc" }},
		{name: "null input", edit: func(v *pantryExtensionReview) { v.Input = json.RawMessage("null") }},
		{name: "array input", edit: func(v *pantryExtensionReview) { v.Input = json.RawMessage("[]") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope := base
			tc.edit(&envelope)
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			payload := base64.RawURLEncoding.EncodeToString(raw)
			token := payload + "." + base64.RawURLEncoding.EncodeToString(s.pantryReviewSignature(r, payload))
			if _, err := s.parsePantryReview(r, token, "leftovers"); err == nil {
				t.Fatal("malformed signed server envelope accepted")
			}
		})
	}
}
