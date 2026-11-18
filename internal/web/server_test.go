package web

import (
	"bytes"
	"context"
	"fmt"
	"household/internal/auth"
	"household/internal/config"
	"household/internal/modules"
	"household/internal/store"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
func TestFail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, message, retry, presetRetry string
		err                                     error
		status                                  int
	}{
		{"busy module gate", "/settings/modules/pantry", "erneut versuchen", "5", "", fmt.Errorf("toggle module: %w", store.ErrBusy), http.StatusServiceUnavailable},
		{"rate limit default interval", "/login", "", "900", "", auth.ErrLimited, http.StatusTooManyRequests},
		{"rate limit keeps the caller's interval", "/api/v1/m/notes/notes/x/attachments", "", "30", "30", auth.ErrLimited, http.StatusTooManyRequests},
		{"correctable auth refusal", "/change-password", "mindestens 14 Zeichen", "", "", fmt.Errorf("change: %w", auth.Rejection("neues Passwort: mindestens 14 Zeichen")), http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			s := &Server{Log: slog.New(slog.NewTextHandler(&logs, nil))}
			request := httptest.NewRequest(http.MethodPost, "http://localhost:8100"+tc.path, nil)
			request.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			if tc.presetRetry != "" {
				response.Header().Set("Retry-After", tc.presetRetry)
			}
			s.fail(response, request, tc.err)
			if response.Code != tc.status || response.Header().Get("Retry-After") != tc.retry || !strings.Contains(response.Body.String(), tc.message) {
				t.Fatalf("fail = %d %q %s", response.Code, response.Header().Get("Retry-After"), response.Body)
			}
			if logs.Len() != 0 {
				t.Fatalf("an expected refusal was logged as a failure: %s", logs.String())
			}
		})
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

func TestUploadJobsAddedCountsEveryQueuedJob(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind    string
		capture bool
		want    int
	}{
		{"application/pdf", false, 0},
		{"application/pdf", true, 1},
		{"image/png", false, 1},
		{"image/jpeg", true, 2},
	} {
		if got := uploadJobsAdded(tc.kind, tc.capture); got != tc.want {
			t.Errorf("uploadJobsAdded(%s, %v) = %d, want %d", tc.kind, tc.capture, got, tc.want)
		}
	}
	// 19 outstanding plus one job is still admitted; plus two is not.
	if 19+uploadJobsAdded("image/png", false) > maxOutstandingUploadJobs || 19+uploadJobsAdded("image/png", true) <= maxOutstandingUploadJobs {
		t.Fatal("admission must count the jobs this upload adds")
	}
}

type unlockCounter struct{ unlocked int }

func (u *unlockCounter) RUnlock() { u.unlocked++ }

func TestGatedLookupReleasesTheGateOnPanic(t *testing.T) {
	t.Parallel()
	gate := &unlockCounter{}
	func() {
		defer func() { _ = recover() }()
		_, _ = gatedLookup(context.Background(), gate, func(context.Context) (int, error) { panic("lookup failed") })
	}()
	if gate.unlocked != 1 {
		t.Fatal("a panicking feed lookup must release the module gate", gate.unlocked)
	}
	if got, err := gatedLookup(context.Background(), gate, func(ctx context.Context) (int, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("lookup must run with the feed budget")
		}
		return 7, nil
	}); got != 7 || err != nil || gate.unlocked != 2 {
		t.Fatal("lookup result or release", got, err, gate.unlocked)
	}
}

func TestReadinessProbeStopsWaitingWhenCanceled(t *testing.T) {
	t.Parallel()
	s := &Server{}
	s.readiness.once.Do(func() { s.readiness.slot = make(chan struct{}, 1) })
	s.readiness.slot <- struct{}{} // another probe's inspection is running
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan bool, 1)
	go func() { done <- s.databaseReady(ctx) }()
	select {
	case ready := <-done:
		if ready {
			t.Fatal("a canceled probe must not report ready")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a canceled probe kept waiting for the running inspection")
	}
	<-s.readiness.slot
	s.readiness.checked, s.readiness.ready = time.Now(), true
	if !s.databaseReady(context.Background()) {
		t.Fatal("a fresh cached result must be shared")
	}
}
