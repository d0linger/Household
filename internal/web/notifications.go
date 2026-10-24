package web

import (
	"household/internal/store"
	"household/internal/value"
	"net/http"
)

func (s *Server) dismissNotification(w http.ResponseWriter, r *http.Request) {
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if _, err := s.Pool.Exec(r.Context(), "UPDATE core.notifications SET dismissed_at=now() WHERE id=$1 AND user_id=$2", r.PathValue("id"), identity(r).UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}
