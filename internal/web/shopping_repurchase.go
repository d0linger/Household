package web

import (
	"net/http"

	"household/internal/store"
)

func (s *Server) shoppingRepurchase(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("shopping"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if r.Method == http.MethodGet {
		page, err := s.Store.ShoppingRepurchase(r.Context(), identity(r), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	var input store.RepurchaseInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, store.Invalid{Message: "Vorschlag und Menge erneut prüfen"})
		return
	}
	item, err := s.Store.AddRepurchase(r.Context(), identity(r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
