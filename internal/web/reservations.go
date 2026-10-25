package web

import (
	"context"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) reservationPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("mealplan"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	view, err := s.Store.PlanningReservation(ctx, identity(r), r.PathValue("plan"), r.PathValue("meal"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Vorrat reservieren")
	p.Module, _ = s.Store.Module("mealplan")
	p.View, p.Record = "reservations", view["source"].(store.Record)
	s.render(w, r, p)
}

func (s *Server) reservationAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("mealplan"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	pid, mid := r.PathValue("plan"), r.PathValue("meal")
	var result any
	var err error
	switch {
	case r.Method == http.MethodPost && r.PathValue("reservation") != "":
		var in struct {
			Generation int64 `json:"generation"`
			Version    int64 `json:"version"`
		}
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.ReleasePlanningStock(ctx, identity(r), pid, mid, r.PathValue("reservation"), trace(r), in.Generation, in.Version)
		}
	case r.Method == http.MethodPost:
		var in store.ReservationInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.ReservePlanningStock(ctx, identity(r), pid, mid, r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case r.PathValue("lot") != "":
		result, err = s.Store.PlanningReservationLot(ctx, identity(r), pid, mid, r.PathValue("lot"))
	default:
		result, err = s.Store.PlanningReservation(ctx, identity(r), pid, mid)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
