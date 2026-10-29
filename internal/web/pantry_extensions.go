package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Server) pantryExtensionPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	lot := r.PathValue("lot")
	if lot != "" && !value.ValidUUID(lot) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if lot == "" && !identity(r).CanWrite() {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	view, title := "pantry-leftovers", "Gekochte Reste"
	switch r.URL.Path {
	case "/m/pantry/scans":
		view, title = "pantry-scans", "Scans prüfen"
		pantryScanCameraHeaders(w, r)
	case "/m/pantry/food-facts":
		view, title = "pantry-food-facts", "Produktinformationen prüfen"
	case "/m/pantry/leftovers":
	default:
		if lot == "" {
			s.fail(w, r, store.ErrNotFound)
			return
		}
	}
	p := s.page(r, title)
	p.Module, _ = s.Store.Module("pantry")
	p.Entity, _ = p.Module.Entity("products")
	p.View = view
	if lot != "" {
		p.Record = store.Record{"lot": lot}
		p.Title = "Restcharge ansehen"
	}
	s.render(w, r, p)
}

func (s *Server) pantryPreparedLotAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.Store.PantryPreparedLot(ctx, identity(r), r.PathValue("lot"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func pantryScanCameraHeaders(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/m/pantry/scans" && r.URL.Path != "/offline/scans" {
		return
	}
	w.Header().Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=(), payment=()")
	// Add only the local media source needed by a user-started camera stream.
	if nonce, ok := r.Context().Value(nonceKey).(string); ok && nonce != "" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'self'; img-src 'self' blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	}
}

func (s *Server) pantryExtensionsContextAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	id := identity(r)
	if err = auth.LockSession(ctx, tx, id); err != nil {
		if errors.Is(err, auth.ErrSession) {
			err = store.ErrForbidden
		}
		s.fail(w, r, err)
		return
	}
	var generation int64
	var state, today string
	var serverTime time.Time
	err = tx.QueryRow(ctx, `SELECT m.state,m.data_generation,
 (clock_timestamp() AT TIME ZONE h.timezone)::date::text,clock_timestamp()
 FROM core.module_states m JOIN core.households h ON h.id=m.household_id
 WHERE m.household_id=$1 AND m.module_id='pantry' FOR SHARE OF m,h`, id.HouseholdID).
		Scan(&state, &generation, &today, &serverTime)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "enabled" {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, store.Record{
		"context": store.Record{"household_id": id.HouseholdID, "user_id": id.UserID,
			"session_id": id.SessionID, "role": id.Role, "generation": generation,
			"auth_scope": s.pantryExtensionAuthScope(r), "can_write": id.CanWrite(),
			"server_time": serverTime.UTC().Format(time.RFC3339), "today": today},
		"limits": store.Record{"scan_lines": 100, "scan_bytes": 256 << 10,
			"scan_days": 7, "leftover_containers": 20},
	})
}

func readPantryReviewBody(w http.ResponseWriter, r *http.Request) (string, error) {
	var body struct {
		Review string `json:"review"`
	}
	if err := readPantryExtensionJSON(w, r, &body, 3*pantryReviewLimit); err != nil {
		return "", err
	}
	return body.Review, nil
}

func readPantryExtensionJSON(w http.ResponseWriter, r *http.Request, into any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil {
		return store.Invalid{Message: "Vorratsdaten sind ungültig oder überschreiten die erlaubte Größe"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return store.Invalid{Message: "Nur ein abgeschlossenes JSON-Dokument senden"}
	}
	return nil
}

func decodePantryReviewedInput(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return pantryReviewInvalid()
	}
	return nil
}

func pantryOperationGeneration(r *http.Request) (int64, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["generation"]) != 1 {
		return 0, store.Invalid{Message: "Den Datenstand des bestehenden Vorgangs erneut prüfen"}
	}
	generation, err := strconv.ParseInt(values.Get("generation"), 10, 64)
	if err != nil || generation < 1 {
		return 0, store.Invalid{Message: "Gültiger Datenstand des Vorgangs erforderlich"}
	}
	return generation, nil
}

func (s *Server) pantryLeftoversAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	key := r.Header.Get("Idempotency-Key")
	switch r.Pattern {
	case "GET /api/v1/pantry/leftovers/context":
		result, err = s.Store.PantryLeftoversContext(ctx, identity(r), r.URL.Query().Get("source_kind"), r.URL.Query().Get("source_id"))
	case "POST /api/v1/pantry/leftovers/preview":
		var input store.PantryLeftoversInput
		if !value.ValidUUID(key) {
			err = store.Invalid{Message: "Gültigen Vorgangsschlüssel vor der Prüfung festlegen"}
		} else if err = readPantryExtensionJSON(w, r, &input, pantryReviewLimit); err == nil {
			var out store.Record
			out, err = s.Store.ReviewPantryLeftovers(ctx, identity(r), input)
			if err == nil {
				var review string
				var expires int64
				review, expires, err = s.issuePantryReview(r, "leftovers", key, input.Generation,
					out["normalized"], store.String(out["fingerprint"]))
				if err == nil {
					out["review"], out["expires"] = review, expires
					result = out
				}
			}
		}
	case "POST /api/v1/pantry/leftovers/execute":
		var token string
		token, err = readPantryReviewBody(w, r)
		if err == nil {
			var review pantryExtensionReview
			review, err = s.parsePantryReview(r, token, "leftovers")
			if err == nil {
				var input store.PantryLeftoversInput
				if err = decodePantryReviewedInput(review.Input, &input); err == nil {
					if input.Generation != review.Generation || input.Fingerprint != review.Fingerprint {
						err = pantryReviewInvalid()
					} else {
						result, err = s.Store.PostPantryLeftovers(ctx, identity(r), review.Key, trace(r), input)
					}
				}
			}
		}
	case "GET /api/v1/pantry/leftovers/operations/{key}":
		var generation int64
		generation, err = pantryOperationGeneration(r)
		if err == nil {
			result, err = s.Store.PantryLeftoverOperation(ctx, identity(r), r.PathValue("key"), generation)
		}
	case "POST /api/v1/pantry/leftovers/operations/{key}/abandon":
		var input struct {
			Generation int64 `json:"generation"`
		}
		if err = readJSON(w, r, &input); err == nil {
			result, err = s.Store.AbandonPantryLeftoverOperation(ctx, identity(r), r.PathValue("key"), input.Generation, trace(r))
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
