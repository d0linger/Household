package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
)

type planningAutoplanProof struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	InputHash   string `json:"input_hash"`
	Expires     int64  `json:"expires"`
}

func planningAutoplanScope(r *http.Request) string {
	id := identity(r)
	raw, _ := json.Marshal([]any{id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.Role, id.AuthVersion, r.PathValue("plan")})
	return string(raw)
}

func (s *Server) planningAutoplanCommand(ctx context.Context, w http.ResponseWriter, r *http.Request) (any, error) {
	if !identity(r).CanWrite() {
		return nil, store.ErrForbidden
	}
	var in store.PlanningAutoplanInput
	if err := readPlanningExtensionJSON(w, r, &in, "recipes_generation", "candidates", "slots", "rules"); err != nil {
		return nil, err
	}
	if strings.HasSuffix(r.Pattern, "/preview") {
		if err := s.Auth.Limit(ctx, "mealplan-autoplan-preview", identity(r).HouseholdID+identity(r).UserID, 20, time.Minute); err != nil {
			return nil, err
		}
		in.ReviewFingerprint, in.ReviewProof, in.ReviewExpiresAt = "", "", 0
		result, err := s.Store.PlanningAutoplanPreview(ctx, identity(r), r.PathValue("plan"), in)
		if err != nil {
			return nil, err
		}
		proof := planningAutoplanProof{Version: 1, Fingerprint: store.String(result["review_fingerprint"]),
			InputHash: store.PlanningAutoplanReviewHash(in), Expires: time.Now().Add(5 * time.Minute).Unix()}
		raw, err := json.Marshal(proof)
		if err != nil {
			return nil, err
		}
		payload := base64.RawURLEncoding.EncodeToString(raw)
		result["review_proof"] = payload + "." + s.Auth.CSRF(payload, "mealplan-autoplan:"+planningAutoplanScope(r))
		result["review_expires_at"] = proof.Expires
		return result, nil
	}
	proof, err := s.readPlanningAutoplanProof(in.ReviewProof, planningAutoplanScope(r))
	if err != nil || proof.Fingerprint != in.ReviewFingerprint || proof.InputHash != store.PlanningAutoplanReviewHash(in) {
		return nil, store.PlanningConflict{Message: "Autoplan-Vorschau gehört nicht mehr zu dieser Auswahl oder Sitzung. Erneut prüfen."}
	}
	// Expiry is evaluated inside the mutation callback, after a matching
	// committed idempotency receipt can be returned. The client cannot set it.
	in.ReviewExpiresAt = proof.Expires
	return s.Store.ApplyPlanningAutoplan(ctx, identity(r), r.PathValue("plan"), r.Header.Get("Idempotency-Key"), trace(r), in)
}

func (s *Server) readPlanningAutoplanProof(token, scope string) (planningAutoplanProof, error) {
	var proof planningAutoplanProof
	invalid := store.PlanningConflict{Message: "Autoplan-Vorschau ist ungültig. Erneut prüfen."}
	if len(token) > 2048 {
		return proof, invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !s.Auth.ValidCSRF(payload, "mealplan-autoplan:"+scope, signature) {
		return proof, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return proof, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proof) != nil || decoder.Decode(new(any)) != io.EOF || proof.Version != 1 ||
		len(proof.Fingerprint) != 64 || len(proof.InputHash) != 64 || proof.Expires <= 0 {
		return planningAutoplanProof{}, invalid
	}
	return proof, nil
}
