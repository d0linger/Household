package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
	"household/internal/value"
)

const pantryReviewLimit = 64 << 10

type pantryExtensionReview struct {
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	Key         string          `json:"key"`
	Generation  int64           `json:"generation"`
	Expires     int64           `json:"expires"`
	Fingerprint string          `json:"fingerprint"`
	Input       json.RawMessage `json:"input"`
}

func pantryExtensionIdentityScope(r *http.Request) []byte {
	id := identity(r)
	raw, _ := json.Marshal([]any{"pantry-extension-identity-v1", id.HouseholdID, id.UserID,
		id.MembershipID, id.SessionID, id.AuthVersion, id.Role})
	return raw
}

func (s *Server) pantryExtensionAuthScope(r *http.Request) string {
	mac := hmac.New(sha256.New, []byte(s.Cfg.CSRFSecret))
	_, _ = mac.Write(pantryExtensionIdentityScope(r))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) pantryReviewSignature(r *http.Request, payload string) []byte {
	mac := hmac.New(sha256.New, []byte(s.Cfg.CSRFSecret))
	_, _ = mac.Write([]byte("pantry-explicit-review-v1\x00"))
	_, _ = mac.Write(pantryExtensionIdentityScope(r))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func pantryReviewInvalid() error {
	return store.Invalid{Message: "Prüfung ist ungültig oder abgelaufen. Bei ungewissem Ausgang zuerst den bestehenden Vorgang klären."}
}

func (s *Server) issuePantryReview(r *http.Request, kind, key string, generation int64, input any, fingerprint string) (string, int64, error) {
	if (kind != "leftovers" && kind != "food-facts") || !value.ValidUUID(key) || generation < 1 {
		return "", 0, pantryReviewInvalid()
	}
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size {
		return "", 0, pantryReviewInvalid()
	}
	rawInput, err := json.Marshal(input)
	if err != nil || len(rawInput) == 0 || rawInput[0] != '{' || len(rawInput) > pantryReviewLimit {
		return "", 0, pantryReviewInvalid()
	}
	expires := time.Now().Add(5 * time.Minute).Unix()
	raw, err := json.Marshal(pantryExtensionReview{
		Version: 1, Kind: kind, Key: key, Generation: generation, Expires: expires,
		Fingerprint: fingerprint, Input: rawInput,
	})
	if err != nil || len(raw) > pantryReviewLimit {
		return "", 0, pantryReviewInvalid()
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	signature := base64.RawURLEncoding.EncodeToString(s.pantryReviewSignature(r, payload))
	return payload + "." + signature, expires, nil
}

func (s *Server) parsePantryReview(r *http.Request, token, kind string) (pantryExtensionReview, error) {
	var out pantryExtensionReview
	if len(token) == 0 || len(token) > 2*pantryReviewLimit {
		return out, pantryReviewInvalid()
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok {
		return out, pantryReviewInvalid()
	}
	actual, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(actual, s.pantryReviewSignature(r, payload)) {
		return out, pantryReviewInvalid()
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(raw) > pantryReviewLimit {
		return out, pantryReviewInvalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.Version != 1 ||
		out.Kind != kind || (kind != "leftovers" && kind != "food-facts") ||
		!value.ValidUUID(out.Key) || out.Key != r.Header.Get("Idempotency-Key") || out.Generation < 1 ||
		out.Expires <= time.Now().Unix() || len(out.Input) == 0 || out.Input[0] != '{' {
		return pantryExtensionReview{}, pantryReviewInvalid()
	}
	fingerprint, err := hex.DecodeString(out.Fingerprint)
	if err != nil || len(fingerprint) != sha256.Size {
		return pantryExtensionReview{}, pantryReviewInvalid()
	}
	return out, nil
}
