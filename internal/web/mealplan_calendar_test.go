package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"household/internal/config"
	"household/internal/modules"
	"household/internal/store"
)

func TestPlanningCalendarInvalidCapabilityNeverEchoesOrLogsSecret(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{Cfg: config.Config{Origin: "http://localhost:8100"},
		Log: slog.New(slog.NewJSONHandler(&logs, nil)), Store: store.New(nil, modules.Registry())}
	handler := s.Handler()
	for _, path := range []string{
		"/calendar/mealplan/invalid-private-capability.ics",
		"/calendar/mealplan/invalid-private-capability",
		"/calendar/mealplan/invalid-private-capability.ics?unexpected=1",
	} {
		r := httptest.NewRequest(http.MethodGet, "http://localhost:8100"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "private-capability") ||
			response.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("invalid capability disclosed input or lost privacy headers")
		}
	}
	if strings.Contains(logs.String(), "private-capability") || !strings.Contains(logs.String(), "GET /calendar/mealplan/{token}") {
		t.Fatal("HTTP logger retained a capability path instead of its closed route")
	}
}
