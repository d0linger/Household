package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

type suggestionCursor struct {
	Version int                         `json:"version"`
	Created time.Time                   `json:"created"`
	ID      string                      `json:"id"`
	Expires int64                       `json:"expires"`
	Scope   store.RecipeSuggestionScope `json:"scope"`
}

type suggestionResponse struct {
	store.RecipeSuggestionPage
	Next string `json:"next_cursor"`
}

func (s *Server) planningSuggestionsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	query, token, err := suggestionRequest(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	scope, err := suggestionRequestScope(identity(r), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if token != "" {
		cursor, cursorErr := s.readSuggestionCursor(token, scope)
		if cursorErr != nil {
			s.fail(w, r, cursorErr)
			return
		}
		query.AfterID, query.AfterCreated, query.ExpectedScope = cursor.ID, cursor.Created, &cursor.Scope
		query.CursorExpiresAt = time.Unix(cursor.Expires, 0)
	}
	page, err := s.Store.RecipeSuggestions(r.Context(), identity(r), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response := suggestionResponse{RecipeSuggestionPage: page}
	if page.More {
		cursor := suggestionCursor{Version: 1, Created: page.AfterCreated, ID: page.AfterID,
			Expires: time.Now().Add(30 * time.Minute).Unix(), Scope: page.Scope()}
		response.Next, err = s.makeSuggestionCursor(cursor, scope)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func suggestionRequest(r *http.Request) (store.RecipeSuggestionQuery, string, error) {
	invalid := store.Invalid{Message: "Suchbegriff, Portionen, Planungsdatum und Auswahlseite prüfen"}
	// ParseQuery errors must not silently remove a malformed filter/cursor.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return store.RecipeSuggestionQuery{}, "", invalid
	}
	for key, items := range values {
		if len(items) != 1 || key != "q" && key != "servings" && key != "on" && key != "cursor" {
			return store.RecipeSuggestionQuery{}, "", invalid
		}
	}
	query := store.RecipeSuggestionQuery{Search: strings.TrimSpace(values.Get("q")), Servings: values.Get("servings"), PlannedOn: values.Get("on")}
	if query.Servings == "" {
		query.Servings = "1"
	}
	portions, err := value.Decimal(query.Servings)
	if err != nil || portions.Sign() <= 0 || query.PlannedOn != "" && !value.Date(query.PlannedOn) {
		return query, "", invalid
	}
	query.Servings = portions.FloatString(6)
	return query, values.Get("cursor"), nil
}

func suggestionRequestScope(id auth.Identity, query store.RecipeSuggestionQuery) (string, error) {
	raw, err := json.Marshal(struct {
		Household, User, Member, Session, Role string
		AuthVersion                            int64
		Search, Servings                       string
	}{id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.Role, id.AuthVersion, query.Search, query.Servings})
	return string(raw), err
}

func (s *Server) makeSuggestionCursor(cursor suggestionCursor, scope string) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + s.Auth.CSRF(payload, "recipe-suggestions:"+scope), nil
}

func (s *Server) readSuggestionCursor(token, scope string) (suggestionCursor, error) {
	var cursor suggestionCursor
	invalid := store.Invalid{Message: "Vorschlagsseite ist abgelaufen oder ungültig. Neu bewerten."}
	if len(token) > 4096 {
		return cursor, invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || !s.Auth.ValidCSRF(payload, "recipe-suggestions:"+scope, signature) {
		return cursor, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return cursor, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 ||
		!value.ValidUUID(cursor.ID) || cursor.Created.IsZero() || cursor.Expires <= time.Now().Unix() ||
		cursor.Scope.Generation <= 0 || cursor.Scope.RecipesGeneration < 0 || cursor.Scope.PantryGeneration < 0 ||
		cursor.Scope.RecipesEnabled && cursor.Scope.RecipesGeneration == 0 || cursor.Scope.PantryEnabled && cursor.Scope.PantryGeneration == 0 ||
		cursor.Scope.Timezone == "" || !value.Date(cursor.Scope.Today) || !value.Date(cursor.Scope.PlannedOn) || cursor.Scope.Servings == "" {
		return suggestionCursor{}, invalid
	}
	return cursor, nil
}
