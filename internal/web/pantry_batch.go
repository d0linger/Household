package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) pantryBatchPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if r.PathValue("receipt") == "" && !identity(r).CanWrite() {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	p := s.page(r, "Einlagern und Inventur")
	p.Module, _ = s.Store.Module("pantry")
	p.View = "pantry-batches"
	p.Record = store.Record{"draft": r.URL.Query().Get("draft"), "receipt": r.PathValue("receipt")}
	s.render(w, r, p)
}

func (s *Server) pantryBatchAPI(w http.ResponseWriter, r *http.Request) {
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
	draft := r.PathValue("draft")
	switch {
	case r.Pattern == "GET /api/v1/pantry/batches/intake-state":
		result, err = s.Store.PantryIntakeState(ctx, identity(r), r.URL.Query().Get("product"), r.URL.Query().Get("location"))
	case r.PathValue("receipt") != "":
		result, err = s.Store.PantryBatchReceipt(ctx, identity(r), r.PathValue("receipt"))
	case r.Pattern == "POST /api/v1/pantry/batches/drafts/{draft}/post":
		var in store.PantryBatchPostInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.PostPantryBatch(ctx, identity(r), draft, r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case r.Method == http.MethodPut:
		var in store.PantryBatchDraftInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.SavePantryBatchDraft(ctx, identity(r), draft, r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case r.Method == http.MethodPost:
		var in store.PantryBatchDiscardInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.DiscardPantryBatchDraft(ctx, identity(r), draft, r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	case draft != "":
		result, err = s.Store.PantryBatchDraft(ctx, identity(r), draft)
	default:
		result, err = s.Store.PantryBatchDrafts(ctx, identity(r))
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
