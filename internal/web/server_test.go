package web

import (
	"bytes"
	"fmt"
	"household/internal/config"
	"household/internal/modules"
	"household/internal/store"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRoutesAndHeaders(t *testing.T) {
	t.Parallel()
	s := &Server{Cfg: config.Config{Origin: "http://localhost:8100"}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: store.New(nil, modules.Registry())}
	handler := s.Handler()
	for _, tc := range []struct {
		name, host, origin, method string
		status                     int
	}{{"liveness", "localhost:8100", "", "GET", 200}, {"cross origin", "localhost:8100", "https://evil.invalid", "POST", 403}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(tc.method, "http://localhost:8100/health/live", nil)
			request.Host = tc.host
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("got status %d", response.Code)
			}
			if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers missing")
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "http://evil.invalid/login", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("unrecognized Host accepted")
	}
}
func TestFailBusyModuleGate(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	s := &Server{Log: slog.New(slog.NewTextHandler(&logs, nil))}
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8100/settings/modules/pantry", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	s.fail(response, request, fmt.Errorf("toggle module: %w", store.ErrBusy))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" || !strings.Contains(response.Body.String(), "erneut versuchen") {
		t.Fatalf("busy gate = %d %q %s", response.Code, response.Header().Get("Retry-After"), response.Body)
	}
	if logs.Len() != 0 {
		t.Fatalf("an expected retryable refusal was logged as a failure: %s", logs.String())
	}
}
func TestReadJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{{"single", `{"name":"x"}`, true}, {"trailing whitespace", "{\"name\":\"x\"}\n", true}, {"trailing garbage", `{"name":"x"}garbage`, false}, {"two documents", `{"name":"x"}{}`, false}, {"unknown", `{"other":true}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("POST", "/", strings.NewReader(tc.input))
			var v struct {
				Name string `json:"name"`
			}
			err := readJSON(httptest.NewRecorder(), request, &v)
			if (err == nil) != tc.valid {
				t.Fatalf("got %v", err)
			}
		})
	}
}
