package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/store"
	"household/internal/value"
)

type recipeDiscoverCursor struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
	Expires   int64     `json:"expires"`
}

func (s *Server) recipeFindPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Rezepte finden")
	p.View = "recipe-find"
	s.render(w, r, p)
}

func (s *Server) recipeDeviceContextAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	context, err := s.Store.RecipeDeviceContext(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, context)
}

func recipeDiscoverFilter(r *http.Request) (store.RecipeDiscoverFilter, error) {
	var filter store.RecipeDiscoverFilter
	q := r.URL.Query()
	filter.Query = strings.TrimSpace(q.Get("q"))
	if utf8.RuneCountInString(filter.Query) > 100 || !utf8.ValidString(filter.Query) ||
		strings.IndexFunc(filter.Query, unicode.IsControl) >= 0 {
		return filter, store.Invalid{Message: "Rezeptname ist ungültig oder zu lang"}
	}
	filter.Tags, filter.Catalog = q["tags"], q["catalog"]
	if len(filter.Tags) > 5 || len(filter.Catalog) > 5 {
		return filter, store.Invalid{Message: "Höchstens fünf Tags und fünf Katalogzutaten auswählen"}
	}
	if raw := q.Get("max_minutes"); raw != "" {
		minutes, err := strconv.Atoi(raw)
		if err != nil || minutes < 1 || minutes > 10080 {
			return filter, store.Invalid{Message: "Dauerfilter muss zwischen 1 und 10080 Minuten liegen"}
		}
		filter.MaxMinutes = minutes
	}
	for _, entry := range []struct {
		name string
		into *bool
	}{{"include_unknown_duration", &filter.IncludeUnknownDuration}, {"never_cooked", &filter.NeverCooked}} {
		value := q.Get(entry.name)
		if value != "" && value != "0" && value != "1" {
			return filter, store.Invalid{Message: "Rezeptfilter ist ungültig"}
		}
		*entry.into = value == "1"
	}
	if raw := q.Get("cooked_after"); raw != "" {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil || day.Format("2006-01-02") != raw || day.After(time.Now().UTC().Add(24*time.Hour)) {
			return filter, store.Invalid{Message: "Kochdatum ist ungültig"}
		}
		filter.CookedAfter = &day
	}
	if filter.NeverCooked && filter.CookedAfter != nil {
		return filter, store.Invalid{Message: "Kochdatum und niemals gekocht können nicht kombiniert werden"}
	}
	return filter, nil
}

func (s *Server) recipeDiscoverAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	filter, err := recipeDiscoverFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	current, err := s.Store.RecipeDeviceContext(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	filterKey, err := json.Marshal([]any{current.OwnerScope, current.SessionScope, current.Generation,
		filter.Query, filter.Tags, filter.Catalog, filter.MaxMinutes, filter.IncludeUnknownDuration,
		filter.CookedAfter, filter.NeverCooked})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	scope := "recipe-discover-v1:" + string(filterKey)
	if token := r.URL.Query().Get("cursor"); token != "" {
		invalid := store.Invalid{Message: "Rezeptseite ist abgelaufen. Suche erneut starten."}
		if len(token) > 1024 {
			s.fail(w, r, invalid)
			return
		}
		payload, signature, ok := strings.Cut(token, ".")
		if !ok || !s.Auth.ValidCSRF(payload, scope, signature) {
			s.fail(w, r, invalid)
			return
		}
		raw, decodeErr := base64.RawURLEncoding.DecodeString(payload)
		var cursor recipeDiscoverCursor
		if decodeErr != nil || len(raw) > 512 || json.Unmarshal(raw, &cursor) != nil ||
			!value.ValidUUID(cursor.ID) || cursor.UpdatedAt.IsZero() || cursor.Expires <= time.Now().Unix() {
			s.fail(w, r, invalid)
			return
		}
		filter.BeforeUpdatedAt, filter.BeforeID = &cursor.UpdatedAt, cursor.ID
	}
	page, err := s.Store.DiscoverRecipes(r.Context(), identity(r), filter)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if page.Context.SessionScope != current.SessionScope || page.Context.AuthScope != current.AuthScope {
		s.fail(w, r, store.ErrConflict)
		return
	}
	if page.HasMore && len(page.Items) == 50 {
		last := page.Items[49]
		payload, encodeErr := json.Marshal(recipeDiscoverCursor{UpdatedAt: last.UpdatedAt, ID: last.ID, Expires: time.Now().Add(15 * time.Minute).Unix()})
		if encodeErr != nil {
			s.fail(w, r, encodeErr)
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(payload)
		page.Next = encoded + "." + s.Auth.CSRF(encoded, scope)
	}
	writeJSON(w, http.StatusOK, page)
}
