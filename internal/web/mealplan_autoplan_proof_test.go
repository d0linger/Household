package web

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
)

func TestPlanningAutoplanProofRejectsForgeryAndDifferentAuthority(t *testing.T) {
	service, err := auth.New(nil, config.Config{CSRFSecret: strings.Repeat("s", 32), EncryptionKey: strings.Repeat("e", 32)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Auth: service}
	proof := planningAutoplanProof{Version: 1, Fingerprint: strings.Repeat("a", 64), InputHash: strings.Repeat("b", 64), Expires: time.Now().Add(5 * time.Minute).Unix()}
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	token := payload + "." + service.CSRF(payload, "mealplan-autoplan:owned-session-plan")
	got, err := s.readPlanningAutoplanProof(token, "owned-session-plan")
	if err != nil || got != proof {
		t.Fatal("signed review changed", err)
	}
	for _, scope := range []string{"other-session-plan", "owned-session-other-plan", "other-role"} {
		if _, err = s.readPlanningAutoplanProof(token, scope); err == nil {
			t.Fatal("review accepted different authority")
		}
	}
	for _, bad := range []string{"", "unsigned.signature", token + "a", strings.Repeat("a", 2049)} {
		if _, err = s.readPlanningAutoplanProof(bad, "owned-session-plan"); err == nil {
			t.Fatal("malformed review accepted")
		}
	}
	// Cryptographic validity survives expiry so the Store can return an
	// already committed receipt; fresh apply checks expiry inside its callback.
	proof.Expires = time.Now().Add(-time.Minute).Unix()
	raw, err = json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	payload = base64.RawURLEncoding.EncodeToString(raw)
	token = payload + "." + service.CSRF(payload, "mealplan-autoplan:owned-session-plan")
	if _, err = s.readPlanningAutoplanProof(token, "owned-session-plan"); err != nil {
		t.Fatal("expired authentic receipt review blocked before replay lookup", err)
	}
}
