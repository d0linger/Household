package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"household/internal/store"
)

func (s *Server) pantryPriorityAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	state, enabled := s.Store.State("pantry")
	if !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	query, product := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("product")
	var today, zone string
	if err := s.Pool.QueryRow(ctx, "SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text,timezone FROM core.households WHERE id=$1", identity(r).HouseholdID).Scan(&today, &zone); err != nil {
		s.fail(w, r, err)
		return
	}
	scope, err := json.Marshal([]any{"pantry-priority-v1", identity(r).HouseholdID, identity(r).UserID, state.Generation, query, product, zone, today})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, err := s.readReferenceCursor(r.URL.Query().Get("cursor"), string(scope))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cursor.Rank != 0 && cursor.Rank != 1 || cursor.Rank == 1 && cursor.Group != "" {
		s.fail(w, r, store.Invalid{Message: "Ungültige Verbrauchsreihenfolge; Auswahl erneut starten"})
		return
	}
	rows, err := s.Store.PantryPriority(ctx, identity(r), query, product, cursor.ID, cursor.Group, cursor.Rank == 1)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := ""
	if len(rows) > pantryPageSize {
		last := rows[pantryPageSize-1]
		rank := int64(0)
		if store.String(last.Lot["priority_date"]) == "" {
			rank = 1
		}
		next, err = s.makeReferenceCursor(referenceCursor{Created: last.CreatedAt, ID: store.String(last.Lot["id"]), Group: store.String(last.Lot["priority_date"]), Rank: rank, Expires: time.Now().Add(30 * time.Minute).Unix()}, string(scope))
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
	writeJSON(w, http.StatusOK, store.Record{"records": records, "next_cursor": next, "today": today, "timezone": zone})
}
