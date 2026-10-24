package web

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/store"
)

// shoppingItems pages one household-owned list, with totals over the entire list.
// Reading never changes item versions; mutations use the existing audited store.
func (s *Server) shoppingItems(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	m, _ := s.Store.Module("shopping")
	e, _ := m.Entity("lists")
	st, enabled := s.Store.State("shopping")
	if !enabled {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	list, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 300 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		s.fail(w, r, store.Invalid{Message: "Suchbegriff ist ungültig oder zu lang"})
		return
	}
	scope := "shopping-items-v2:" + identity(r).HouseholdID + ":" + identity(r).UserID + ":" + r.PathValue("id") + ":" + store.String(st.Generation) + ":" + store.String(list["store_id"]) + ":" + query
	cursor, err := s.readReferenceCursor(r.URL.Query().Get("cursor"), scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var rid any
	if cursor.ID != "" {
		rid = cursor.ID
	}
	context, err := s.shoppingContext(r, list)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Pool.Query(r.Context(), shoppingBoardQuery, identity(r).HouseholdID, list["id"], rid, query, cursor.Rank, cursor.Group, cursor.Sort, cursor.Name, st.Generation, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	records := []store.Record{}
	next := ""
	var last referenceCursor
	for rows.Next() {
		var raw []byte
		var created time.Time
		var rank int64
		var group, name string
		if err = rows.Scan(&raw, &created, &rank, &group, &name); err != nil {
			s.fail(w, r, err)
			return
		}
		if len(records) == 100 {
			next, err = s.makeReferenceCursor(last, scope)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			break
		}
		record, decodeErr := store.DecodeRecord(raw)
		if decodeErr != nil {
			s.fail(w, r, decodeErr)
			return
		}
		records = append(records, record)
		last = referenceCursor{Created: created, ID: store.String(record["id"]), Expires: time.Now().Add(30 * time.Minute).Unix(), Rank: rank, Group: group, Sort: store.Int(record["sort_order"]), Name: name}
	}
	if err = rows.Err(); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "next_cursor": next, "total": context["total"], "completed": context["completed"], "context": context, "list": list, "generation": st.Generation})
}
