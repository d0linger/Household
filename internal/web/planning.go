package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
)

func (s *Server) planningPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("mealplan"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("mealplan")
	e, _ := m.Entity("plans")
	plan, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("plan"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	week := r.URL.Query().Get("week")
	if week == "" {
		week = store.String(plan["starts_on"])
	}
	p := s.page(r, "Wochenplanung")
	p.View = "planning"
	p.Module = m
	p.Record = store.Record{"id": plan["id"], "week": week, "meal": r.URL.Query().Get("meal")}
	s.render(w, r, p)
}

func (s *Server) planningAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.Store.State("mealplan"); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	pid, mid, cid := r.PathValue("plan"), r.PathValue("meal"), r.PathValue("component")
	switch {
	case r.Method == http.MethodPost:
		var in store.PlanningInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.MutatePlanning(ctx, identity(r), pid, r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case r.PathValue("source") != "":
		result, err = s.Store.PlanningSource(ctx, identity(r), r.PathValue("source"))
	case cid != "" && strings.HasSuffix(r.Pattern, "/source"):
		result, err = s.Store.PlanningSourceStatus(ctx, identity(r), pid, mid, cid)
	case cid != "":
		result, err = s.Store.PlanningComponent(ctx, identity(r), pid, mid, cid)
	case mid != "":
		result, err = s.Store.PlanningMeal(ctx, identity(r), pid, mid)
	default:
		week := r.URL.Query().Get("week")
		if week == "" {
			m, _ := s.Store.Module("mealplan")
			e, _ := m.Entity("plans")
			var plan store.Record
			plan, err = s.Store.Get(ctx, identity(r), m, e, pid)
			week = store.String(plan["starts_on"])
		}
		if err == nil {
			result, err = s.Store.PlanningWeek(ctx, identity(r), pid, week)
		}
	}
	var conflict store.PlanningConflict
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
