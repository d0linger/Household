package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) ingredientCatalogAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("recipes"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	if r.Method == http.MethodGet {
		result, err = s.Store.IngredientCatalogView(ctx, identity(r), r.PathValue("catalog"))
	} else {
		var in store.IngredientBindingInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.SetIngredientBinding(ctx, identity(r), r.PathValue("catalog"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	}
	var conflict store.IngredientBindingConflict
	if errors.As(err, &conflict) {
		writeJSON(w, http.StatusConflict, store.Record{"error": conflict.Error(), "current": conflict.Current, "request_id": trace(r)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipePantryAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("recipes"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := s.Store.RecipePantryComparison(ctx, identity(r), r.PathValue("id"), r.URL.Query().Get("version"), r.URL.Query().Get("servings"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) ingredientPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("recipes"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("recipes")
	p := s.page(r, "Rezept mit Vorrat vergleichen")
	p.Module, p.View = m, "ingredient-comparison"
	var err error
	if catalog := r.PathValue("catalog"); catalog != "" {
		p.Title, p.View = "Katalogzutat und Vorratsprodukt", "ingredient-catalog"
		p.Record, err = s.Store.IngredientCatalogView(r.Context(), identity(r), catalog)
	} else {
		e, _ := m.Entity("recipes")
		p.Record, err = s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, p)
}
