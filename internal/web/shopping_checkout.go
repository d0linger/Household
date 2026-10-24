package web

import (
	"context"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) shoppingCheckoutPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, _ := s.Store.Module("shopping")
	e, _ := m.Entity("lists")
	if _, enabled := s.Store.State("shopping"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	list, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Einkauf abschließen")
	p.View, p.Module, p.Record = "shopping-checkout", m, list
	s.render(w, r, p)
}

func (s *Server) shoppingCheckout(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("shopping"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if r.Method == http.MethodGet {
		result, err := s.Store.ShoppingCheckoutPreview(ctx, identity(r), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	var input store.CheckoutInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, store.Invalid{Message: "Abschlussdaten sind ungültig"})
		return
	}
	result, err := s.Store.CheckoutShopping(ctx, identity(r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
