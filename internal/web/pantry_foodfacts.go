package web

import (
	"context"
	"net/http"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func (s *Server) pantryFoodFactsAPI(w http.ResponseWriter, r *http.Request) {
	// A provider lookup may wait on network IO. Its Store contract authorizes
	// before and after that call without holding the module lifecycle gate.
	if r.Pattern != "POST /api/v1/pantry/extensions/food-facts/lookup" {
		s.Store.Gate.RLock()
		defer s.Store.Gate.RUnlock()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	key := r.Header.Get("Idempotency-Key")
	switch r.Pattern {
	case "POST /api/v1/pantry/extensions/food-facts/lookup":
		var input store.PantryFoodFactsLookupInput
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.LookupPantryFoodFacts(ctx, identity(r), input)
		}
	case "POST /api/v1/pantry/extensions/food-facts/preview":
		var input store.PantryFoodFactsInput
		if !value.ValidUUID(key) {
			err = store.Invalid{Message: "Gültigen Vorgangsschlüssel vor der Prüfung festlegen"}
		} else if err = readPantryExtensionJSON(w, r, &input, pantryReviewLimit); err == nil {
			var out store.PantryFoodFactsReview
			out, err = s.Store.ReviewPantryFoodFacts(ctx, identity(r), key, input)
			if err == nil {
				var review string
				var expires int64
				review, expires, err = s.issuePantryReview(r, "food-facts", key,
					out.Input.Generation, out.Input, out.Fingerprint)
				if err == nil {
					result = struct {
						store.PantryFoodFactsReview
						Review  string `json:"review"`
						Expires int64  `json:"expires"`
					}{out, review, expires}
				}
			}
		}
	case "POST /api/v1/pantry/extensions/food-facts/execute":
		var token string
		token, err = readPantryReviewBody(w, r)
		if err == nil {
			var review pantryExtensionReview
			review, err = s.parsePantryReview(r, token, "food-facts")
			if err == nil {
				var input store.PantryFoodFactsInput
				if err = decodePantryReviewedInput(review.Input, &input); err == nil {
					if input.Generation != review.Generation {
						err = pantryReviewInvalid()
					} else {
						result, err = s.Store.PostPantryFoodFacts(ctx, identity(r), review.Key, trace(r), input, review.Fingerprint)
					}
				}
			}
		}
	case "GET /api/v1/pantry/extensions/food-facts/operations/{key}":
		var generation int64
		generation, err = pantryOperationGeneration(r)
		if err == nil {
			result, err = s.Store.PantryFoodFactsOperationStatus(ctx, identity(r), r.PathValue("key"), generation)
		}
	case "POST /api/v1/pantry/extensions/food-facts/operations/{key}/abandon":
		var input struct {
			Generation int64 `json:"generation"`
		}
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.AbandonPantryFoodFacts(ctx, identity(r), r.PathValue("key"), input.Generation, trace(r))
		}
	case "GET /api/v1/pantry/extensions/food-facts/provenance/{product}":
		var generation int64
		generation, err = pantryOperationGeneration(r)
		if err == nil {
			result, err = s.Store.PantryFoodFactsProvenance(ctx, identity(r), r.PathValue("product"), generation)
		}
	default:
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
