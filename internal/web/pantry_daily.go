package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) pantryDailyPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Verbrauchsreihenfolge")
	p.Module, _ = s.Store.Module("pantry")
	p.View = "pantry-priority"
	p.Record = store.Record{"product": r.URL.Query().Get("product")}
	if product := r.URL.Query().Get("product"); product != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		entity, _ := p.Module.Entity("products")
		record, err := s.Store.Get(ctx, identity(r), p.Module, entity, product)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.Record["product_name"] = record["name"]
	}
	if lot := r.PathValue("lot"); lot != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		record, err := s.Store.PantryLotState(ctx, identity(r), lot)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.Record, p.Title, p.View = record, "Verbrauch prüfen", "pantry-consume"
	}
	s.render(w, r, p)
}

func (s *Server) pantryConsumptionAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result any
	var err error
	switch {
	case r.Method == http.MethodPost:
		var in store.PantryStockInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.MutatePantryStock(ctx, identity(r), r.PathValue("lot"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case r.PathValue("operation") != "":
		result, err = s.Store.PantryStockOperation(ctx, identity(r), r.PathValue("operation"))
	case r.Pattern == "GET /api/v1/pantry/lookup":
		result, err = s.Store.PantryBarcode(ctx, identity(r), r.URL.Query().Get("code"))
	default:
		result, err = s.Store.PantryConsumptionView(ctx, identity(r), r.PathValue("lot"))
	}
	var conflict store.PantryStockConflict
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
