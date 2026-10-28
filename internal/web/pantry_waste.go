package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"household/internal/store"
)

func (s *Server) pantryWastePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	p := s.page(r, "Abfallanalyse")
	p.Module, _ = s.Store.Module("pantry")
	p.View = "pantry-waste"
	p.Record = store.Record{"lot": r.PathValue("lot"), "movement": r.URL.Query().Get("movement")}
	if lot := r.PathValue("lot"); lot != "" {
		view, err := s.Store.PantryWasteLot(r.Context(), identity(r), lot, r.URL.Query().Get("movement"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		p.Record["lot"] = store.String(view["lot"].(store.Record)["id"])
		p.Title = "Verlust prüfen"
	}
	s.render(w, r, p)
}

func (s *Server) pantryWasteAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, enabled := s.Store.State("pantry"); !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var result any
	var err error
	if r.Method == http.MethodPost {
		var in store.PantryWasteInput
		if err = readJSON(w, r, &in); err == nil {
			result, err = s.Store.MutatePantryWaste(r.Context(), identity(r), r.PathValue("lot"), r.Header.Get("Idempotency-Key"), trace(r), in)
		}
	} else if r.PathValue("lot") != "" {
		result, err = s.Store.PantryWasteLot(r.Context(), identity(r), r.PathValue("lot"), r.URL.Query().Get("movement"))
	} else {
		result, err = s.pantryWasteReport(r)
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

func (s *Server) pantryWasteReport(r *http.Request) (any, error) {
	id := identity(r)
	scope, err := s.Store.PantryWasteContext(r.Context(), id)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	filter, err := store.NormalizePantryWasteFilter(store.PantryWasteFilter{From: q.Get("from"), To: q.Get("to"), ProductID: q.Get("product"), LocationID: q.Get("location"), Reason: q.Get("reason")}, scope.Today)
	if err != nil {
		return nil, err
	}
	binding, err := json.Marshal([]any{"pantry-waste-v1", id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.SessionID, id.AuthVersion, scope.Generation, scope.Timezone, scope.Today, filter})
	if err != nil {
		return nil, err
	}
	cursor, err := s.readReferenceCursor(q.Get("cursor"), string(binding))
	if err != nil {
		return nil, err
	}
	var point store.PantryWastePoint
	if cursor.ID != "" {
		if json.Unmarshal([]byte(cursor.Group), &point) != nil || point.ID != cursor.ID || !point.Created.Equal(cursor.Created) {
			return nil, store.Invalid{Message: "Auswertung erneut beginnen"}
		}
	}
	report, err := s.Store.PantryWasteReport(r.Context(), id, scope, filter, point)
	if err != nil {
		return nil, err
	}
	next := ""
	if report.Next != nil {
		encoded, marshalErr := json.Marshal(report.Next)
		if marshalErr != nil {
			return nil, marshalErr
		}
		expires := cursor.Expires
		if expires == 0 {
			expires = time.Now().Add(30 * time.Minute).Unix()
		}
		next, err = s.makeReferenceCursor(referenceCursor{Created: report.Next.Created, ID: report.Next.ID, Expires: expires, Group: string(encoded)}, string(binding))
		if err != nil {
			return nil, err
		}
	}
	return struct {
		store.PantryWasteReport
		Next string `json:"next_cursor"`
	}{report, next}, nil
}
