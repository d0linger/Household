package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) cookingStockPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	mid := "recipes"
	if r.PathValue("posting") != "" {
		mid = "pantry"
	}
	if _, ok := s.Store.State(mid); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Verbrauch nach Kochen")
	p.View = "cooking-stock"
	p.Module, _ = s.Store.Module(mid)
	p.Record = store.Record{"history": r.PathValue("history"), "posting": r.PathValue("posting")}
	s.render(w, r, p)
}

func (s *Server) cookingStockAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	module := "recipes"
	if r.PathValue("posting") != "" {
		module = "pantry"
	}
	if _, ok := s.Store.State(module); !ok {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result store.Record
	var err error
	if r.Method == http.MethodPost {
		var in store.CookingStockInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.PostCookingStock(ctx, identity(r), r.PathValue("history"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else if posting := r.PathValue("posting"); posting != "" {
		result, err = s.Store.CookingStockReceipt(ctx, identity(r), posting)
	} else {
		result, err = s.Store.CookingStockPreview(ctx, identity(r), r.PathValue("history"))
	}
	var conflict store.PantryStockConflict
	if errors.As(err, &conflict) {
		writeJSON(w, 409, store.Record{"error": conflict.Error(), "request_id": trace(r), "current": conflict.Current})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
