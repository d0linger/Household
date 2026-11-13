package web

import (
	"net/http"
	"time"

	"household/internal/notification"
	"household/internal/store"
	"household/internal/webpush"
)

func (s *Server) registerNotificationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/notifications", s.require(s.notificationSettingsPage))
	mux.Handle("GET /api/v1/core/notification-preferences", s.require(s.notificationPreferencesAPI))
	mux.Handle("POST /api/v1/core/notification-preferences", s.require(s.notificationPreferencesAPI))
	mux.Handle("GET /api/v1/core/push/device-preferences", s.require(s.notificationDevicesAPI))
	mux.Handle("POST /api/v1/core/push/subscriptions/{id}/preferences", s.require(s.notificationDevicePreferenceAPI))
	mux.Handle("POST /api/v1/core/push/subscriptions/{id}/test", s.require(s.notificationTestAPI))
	mux.Handle("GET /api/v1/core/push/deliveries/{id}", s.require(s.notificationDeliveryAPI))
	mux.Handle("GET /api/v1/core/push/deliveries/{id}/context", s.require(s.notificationDeliveryContextAPI))
	mux.Handle("GET /api/v1/core/push/deliveries/{id}/cooking-context", s.require(s.notificationCookingContextAPI))
	mux.Handle("POST /api/v1/core/push/deliveries/{id}/displayed", s.require(s.notificationDisplayedAPI))
	mux.Handle("POST /api/v1/core/push/deliveries/{id}/seen", s.require(s.notificationSeenAPI))
	mux.Handle("GET /notifications/device/{id}", s.require(s.notificationOpen))
	mux.Handle("GET /m/recipes/cooking/timer/{id}", s.require(s.notificationCookingOpen))
}

func (s *Server) notificationCookingContextAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	view, err := s.Push.CookingDelivery(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) notificationCookingOpen(w http.ResponseWriter, r *http.Request) {
	session, err := s.Store.CookingTimerSession(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/m/recipes/cooking/"+session, http.StatusSeeOther)
}

func (s *Server) notificationPreferencesAPI(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if r.Method == http.MethodGet {
		prefs, err := s.Store.NotificationPreferences(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		type category struct {
			Category  string `json:"category"`
			Module    string `json:"module"`
			Available bool   `json:"available"`
		}
		categories := []category{}
		for _, p := range notification.Categories() {
			categories = append(categories, category{p.Category, p.Module, store.NotificationCategoryAvailable(p.Category)})
		}
		writeJSON(w, 200, map[string]any{"preferences": prefs, "categories": categories})
		return
	}
	if err := s.Auth.Limit(r.Context(), "notification-preferences-v1", id.UserID, 30, time.Minute); err != nil {
		s.fail(w, r, err)
		return
	}
	var in notificationPreferenceInput
	if err := readNotificationPreferenceJSON(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := store.NotificationPreference{Category: in.Category, Enabled: in.Enabled, Version: in.Version, QuietHours: notification.QuietHours{Timezone: in.Timezone, Start: in.Start, End: in.End}}
	if p.Enabled && !store.NotificationCategoryAvailable(p.Category) {
		s.fail(w, r, store.Invalid{Message: "Diese Benachrichtigungskategorie ist noch nicht verfügbar"})
		return
	}
	if len(p.Category) >= 7 && p.Category[:7] == "chores_" {
		p.Start = nil
		p.End = nil
	}
	view, err := s.Store.SaveNotificationPreference(r.Context(), id, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) notificationDevicesAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	view, err := s.Push.DevicePreferences(r.Context(), identity(r))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": view})
}

func (s *Server) notificationDevicePreferenceAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	var in struct {
		Category string `json:"category"`
		Enabled  bool   `json:"enabled"`
		Paused   bool   `json:"paused"`
		Name     string `json:"name"`
		Version  int64  `json:"version"`
	}
	if err := readShoppingExtensionJSON(w, r, &in, 2048); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Enabled && !store.NotificationCategoryAvailable(in.Category) {
		s.fail(w, r, store.Invalid{Message: "Diese Benachrichtigungskategorie ist noch nicht verfügbar"})
		return
	}
	view, err := s.Push.SaveDevicePreference(r.Context(), identity(r), r.PathValue("id"), webpush.DevicePreference{Category: in.Category, Enabled: in.Enabled, Paused: in.Paused, Name: in.Name, Version: in.Version})
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) notificationTestAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	var in struct{}
	if err := readShoppingExtensionJSON(w, r, &in, 128); err != nil {
		s.fail(w, r, err)
		return
	}
	view, err := s.Push.QueueTest(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (s *Server) notificationDeliveryAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	view, err := s.Push.Delivery(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) notificationDeliveryContextAPI(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	view, err := s.Push.Delivery(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	scope, err := s.Push.Context(r.Context(), identity(r))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	csrf, _ := r.Context().Value(csrfKey).(string)
	writeJSON(w, 200, map[string]string{"id": view.ID, "auth_scope": scope.Context.AuthScope, "csrf": csrf})
}

func (s *Server) notificationDisplayedAPI(w http.ResponseWriter, r *http.Request) {
	s.notificationAcknowledge(w, r, false)
}
func (s *Server) notificationSeenAPI(w http.ResponseWriter, r *http.Request) {
	s.notificationAcknowledge(w, r, true)
}
func (s *Server) notificationAcknowledge(w http.ResponseWriter, r *http.Request, seen bool) {
	if s.Push == nil {
		s.pushAPIError(w, r, webpush.ErrUnavailable)
		return
	}
	var in struct{}
	if err := readShoppingExtensionJSON(w, r, &in, 128); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Push.AcknowledgeDelivery(r.Context(), identity(r), r.PathValue("id"), seen); err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	view, err := s.Push.Delivery(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.pushAPIError(w, r, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) notificationOpen(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		http.Redirect(w, r, "/notifications", http.StatusSeeOther)
		return
	}
	target, err := s.Push.DeliveryTarget(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
