package web

import (
	"encoding/json"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) fuelOverviewPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("fuel"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, err := s.Store.FuelOverviewContext(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Fahrzeugübersicht")
	p.Module, _ = s.Store.Module("fuel")
	p.View = "fuel-overview"
	s.render(w, r, p)
}

func (s *Server) fuelOverviewAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("fuel"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	id := identity(r)
	scope, err := s.Store.FuelOverviewContext(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := store.FuelOverviewQuery{Search: r.URL.Query().Get("q"), Archived: r.URL.Query().Get("archived") == "1"}
	binding, err := json.Marshal([]any{"fuel-overview-v1", id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.Role, id.AuthVersion, scope.Generation, scope.Timezone, scope.Today, q.Search, q.Archived})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, err := s.readReferenceCursor(r.URL.Query().Get("cursor"), string(binding))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q.After, q.AfterID = cursor.Created, cursor.ID
	out, err := s.Store.FuelOverview(r.Context(), id, scope, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := ""
	if out.More {
		last := out.Vehicles[len(out.Vehicles)-1]
		expires := cursor.Expires
		if expires == 0 {
			expires = time.Now().Add(30 * time.Minute).Unix()
		}
		next, err = s.makeReferenceCursor(referenceCursor{Created: last.Created, ID: last.ID, Expires: expires}, string(binding))
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		store.FuelOverviewPage
		Next string `json:"next_cursor"`
	}{out, next})
}
