package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"household/internal/store"
)

func (s *Server) recipeWorkspacePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("recipes")
	e, _ := m.Entity("recipes")
	p := s.page(r, "Rezept bearbeiten")
	p.View, p.Module, p.Entity = "recipe-workspace", m, e
	p.Record = store.Record{"id": r.PathValue("id")}
	if r.PathValue("id") != "" {
		record, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.Record = record
	}
	s.render(w, r, p)
}

func (s *Server) recipeWorkspace(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if r.Method == http.MethodGet {
		result, err := s.Store.Recipe(ctx, identity(r), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	var in store.RecipeInput
	if err := readJSON(w, r, &in); err != nil {
		s.fail(w, r, store.Invalid{Message: "Rezeptdaten sind ungültig"})
		return
	}
	result, err := s.Store.SaveRecipe(ctx, identity(r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), trace(r), in)
	var fields store.RecipeErrors
	if errors.As(err, &fields) {
		writeJSON(w, 422, map[string]any{"error": fields.Error(), "fields": fields, "request_id": trace(r)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) recipeVersions(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("recipes"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if vid := r.PathValue("version"); vid != "" {
		result, err := s.Store.RecipeVersion(r.Context(), identity(r), r.PathValue("id"), vid)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if r.URL.Query().Get("before") == "" {
		before = 0
		err = nil
	}
	if err != nil || before < 0 {
		s.fail(w, r, store.Invalid{Message: "Ungültiger Verlaufszeiger"})
		return
	}
	result, err := s.Store.RecipeVersions(r.Context(), identity(r), r.PathValue("id"), before)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := ""
	if len(result) > 20 {
		result = result[:20]
		next = store.String(result[19]["revision"])
	}
	writeJSON(w, 200, map[string]any{"items": result, "next": next})
}
