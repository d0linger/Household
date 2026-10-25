package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) cookingPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Kochen")
	p.View = "cooking"
	p.Module, _ = s.Store.Module("recipes")
	p.Record = store.Record{"kind": "overview", "id": ""}
	for _, kind := range []string{"source", "session", "history"} {
		if rid := r.PathValue(kind); rid != "" {
			p.Record["kind"], p.Record["id"] = kind, rid
		}
	}
	s.render(w, r, p)
}

func (s *Server) cookingAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	if r.Method == http.MethodPost {
		var in store.CookingInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.MutateCooking(ctx, identity(r), r.PathValue("session"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else if rid := r.PathValue("source"); rid != "" {
		result, err = s.Store.CookingSource(ctx, identity(r), rid)
	} else if sid := r.PathValue("session"); sid != "" {
		result, err = s.Store.CookingSession(ctx, identity(r), sid)
	} else if rid := r.PathValue("history"); rid != "" {
		result, err = s.Store.CookingRecord(ctx, identity(r), rid)
	} else if r.URL.Path == "/api/v1/cooking/history" {
		var items []store.Record
		items, err = s.Store.CookingHistory(ctx, identity(r), r.URL.Query().Get("recipe"), r.URL.Query().Get("before"))
		next := ""
		if len(items) > 20 {
			items, next = items[:20], store.String(items[19]["id"])
		}
		result = store.Record{"items": items, "next": next}
	} else {
		result, err = s.Store.CookingSessions(ctx, identity(r))
	}
	var conflict store.CookingConflict
	if errors.As(err, &conflict) {
		writeJSON(w, 409, store.Record{"error": conflict.Error(), "request_id": trace(r)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
