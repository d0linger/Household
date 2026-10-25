package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
)

func (s *Server) pantryInventoryAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	state, enabled := s.Store.State("pantry")
	if !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	location, query := r.URL.Query().Get("location"), strings.TrimSpace(r.URL.Query().Get("q"))
	archived := r.URL.Query().Get("archived") == "1"
	scope, err := json.Marshal([]any{"pantry-inventory-v1", identity(r).HouseholdID, identity(r).UserID, state.Generation, location, query, archived})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, err := s.readReferenceCursor(r.URL.Query().Get("cursor"), string(scope))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Store.PantryInventoryLots(ctx, identity(r), location, query, archived, cursor.Created, cursor.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := ""
	if len(rows) > pantryPageSize {
		last := rows[pantryPageSize-1]
		next, err = s.makeReferenceCursor(referenceCursor{Created: last.CreatedAt, ID: store.String(last.Lot["id"]), Expires: time.Now().Add(30 * time.Minute).Unix()}, string(scope))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		rows = rows[:pantryPageSize]
	}
	records := make([]store.Record, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.Lot)
	}
	writeJSON(w, http.StatusOK, store.Record{"records": records, "next_cursor": next, "partial": true})
}
