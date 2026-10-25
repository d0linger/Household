package web

import (
	"context"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) pantryReplenishmentPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p := s.page(r, "Nachkauf prüfen")
	p.Module, _ = s.Store.Module("pantry")
	entity, _ := p.Module.Entity("products")
	product, err := s.Store.Get(ctx, identity(r), p.Module, entity, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.Record, p.View = product, "pantry-replenishment"
	s.render(w, r, p)
}

func (s *Server) pantryReplenishmentAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	view, err := s.Store.PantryReplenishment(ctx, identity(r), r.PathValue("id"), r.URL.Query().Get("pack"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
