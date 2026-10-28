package web

import (
	"net/http"

	"household/internal/store"
)

func (s *Server) shoppingBarcodePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("shopping"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	view, err := s.Store.ShoppingBarcode(r.Context(), identity(r), r.PathValue("id"), "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Barcode prüfen")
	p.View = "shopping-barcode"
	p.Module, _ = s.Store.Module("shopping")
	p.Record, p.Extra = store.Record{"id": r.PathValue("id")}, view
	// Only this successfully authorized open-list document can request a camera.
	// The browser still requires an explicit click and permission.
	w.Header().Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=(), payment=()")
	w.Header().Set("Content-Security-Policy", w.Header().Get("Content-Security-Policy")+"; media-src 'self' blob:")
	s.render(w, r, p)
}

func (s *Server) shoppingBarcodeAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("shopping"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if r.Method == http.MethodGet {
		view, err := s.Store.ShoppingBarcode(r.Context(), identity(r), r.PathValue("id"), r.URL.Query().Get("code"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	var input store.ShoppingBarcodeInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, store.Invalid{Message: "Barcode, Produkt und Menge erneut prüfen"})
		return
	}
	result, err := s.Store.MutateShoppingBarcode(r.Context(), identity(r), r.PathValue("id"), r.PathValue("intent"), r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
