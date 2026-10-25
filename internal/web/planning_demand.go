package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
)

func (s *Server) planningDemandPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("mealplan"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("mealplan")
	e, _ := m.Entity("plans")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	plan, err := s.Store.Get(ctx, identity(r), m, e, r.PathValue("plan"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	week := r.URL.Query().Get("week")
	if week == "" {
		week = store.String(plan["starts_on"])
	}
	p := s.page(r, "Bedarf und Vorrat prüfen")
	p.Module, p.View = m, "planning-demand"
	p.Record = store.Record{"id": plan["id"], "name": plan["name"], "week": week, "selected": strings.Join(r.URL.Query()["meal"], ",")}
	s.render(w, r, p)
}

func (s *Server) planningDemandAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("mealplan"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	if r.Method == http.MethodPost {
		var in store.PlanningIngredientBindingInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.SetPlanningIngredientBinding(ctx, identity(r), r.PathValue("plan"), r.PathValue("meal"), r.PathValue("component"),
				r.PathValue("ingredient"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else {
		result, err = s.Store.PlanningDemand(ctx, identity(r), r.PathValue("plan"), r.URL.Query()["meal"])
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
