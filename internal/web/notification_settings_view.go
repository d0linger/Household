package web

import "net/http"

func (s *Server) notificationSettingsPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Benachrichtigungen")
	p.View = "notification-settings"
	s.render(w, r, p)
}
