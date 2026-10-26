package web

import (
	"net/http"

	"household/internal/store"
)

func (s *Server) pantrySuggestionsPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Rezeptvorschläge aus dem Vorrat")
	p.Module, _ = s.Store.Module("pantry")
	p.View = "pantry-suggestions"
	s.render(w, r, p)
}
