package web

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"household/internal/config"
	"household/internal/modules"
	"household/internal/store"
	"household/internal/value"
)

func TestTrustedProxyBoundary(t *testing.T) {
	t.Parallel()
	_, trusted, err := net.ParseCIDR("10.20.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: config.Config{TrustedProxies: []*net.IPNet{trusted}}}
	for _, tc := range []struct {
		name, peer, forwarded, proto, wantIP string
		tls, wantHTTPS                       bool
	}{
		{name: "untrusted cannot spoof", peer: "192.0.2.5:1234", forwarded: "198.51.100.1", proto: "https", wantIP: "192.0.2.5"},
		{name: "trusted edge", peer: "10.20.0.3:1234", forwarded: "198.51.100.1", proto: "https", wantIP: "198.51.100.1", wantHTTPS: true},
		{name: "rightmost untrusted hop", peer: "10.20.0.3:1234", forwarded: "203.0.113.8, 198.51.100.1, 10.20.0.2", wantIP: "198.51.100.1"},
		{name: "invalid forwarded address", peer: "10.20.0.3:1234", forwarded: "bogus", wantIP: "10.20.0.3"},
		{name: "IPv6 direct", peer: "[2001:db8::1]:443", forwarded: "203.0.113.8", tls: true, wantIP: "2001:db8::1", wantHTTPS: true},
		{name: "missing port", peer: "invalid", proto: "https", wantIP: "unknown"},
		{name: "ambiguous proto rejected", peer: "10.20.0.3:1234", proto: "https,http", wantIP: "10.20.0.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := s.clientIP(r); got != tc.wantIP {
				t.Errorf("client IP = %q, want %q", got, tc.wantIP)
			}
			if got := s.isHTTPS(r); got != tc.wantHTTPS {
				t.Errorf("HTTPS = %v, want %v", got, tc.wantHTTPS)
			}
		})
	}
}

func TestSessionCookiePolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, origin, cookieName string
		production, secure       bool
	}{
		{"production", "https://household.example", "__Host-household", true, true},
		{"local development", "http://localhost:8101", "household_dev", false, false},
		{"HTTPS development", "https://localhost:8101", "household_dev", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{Cfg: config.Config{Production: tc.production, Origin: tc.origin}}
			cookie := s.cookie(s.cookieName(), "opaque", 60, time.Now().Add(time.Minute))
			if cookie.Name != tc.cookieName || cookie.Secure != tc.secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatalf("invalid cookie policy: %+v", cookie)
			}
			deleted := s.cookie(s.cookieName(), "", -1, time.Unix(0, 0))
			if deleted.MaxAge != -1 || deleted.Secure != cookie.Secure || deleted.Path != cookie.Path {
				t.Fatal("logout must delete the exact protected cookie")
			}
		})
	}
}

func TestMiddlewareBoundaries(t *testing.T) {
	t.Parallel()
	s := &Server{Cfg: config.Config{Origin: "https://household.example", Production: true}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, host, origin, site string
		tls                      bool
		status                   int
	}{
		{"valid", "household.example", "https://household.example", "same-origin", true, 204},
		{"foreign host", "evil.example", "", "", true, 400},
		{"plain transport", "household.example", "", "", false, 400},
		{"foreign origin", "household.example", "https://evil.example", "", true, 403},
		{"opaque origin", "household.example", "null", "", true, 403},
		{"cross site metadata", "household.example", "", "cross-site", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/mutation", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("X-Request-ID", "untrusted\nvalue")
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if !value.ValidUUID(w.Header().Get("X-Request-ID")) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Strict-Transport-Security") == "" {
				t.Fatal("private response tracing/cache/transport policy missing")
			}
			csp := w.Header().Get("Content-Security-Policy")
			for _, rule := range []string{"frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "script-src 'nonce-"} {
				if !strings.Contains(csp, rule) {
					t.Errorf("missing CSP directive %q", rule)
				}
			}
		})
	}
}

func TestPanicDoesNotDiscloseRequestData(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	s := &Server{Cfg: config.Config{Origin: "http://localhost"}, Log: slog.New(slog.NewJSONHandler(&logs, nil))}
	h := s.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private-password-and-document") }))
	r := httptest.NewRequest(http.MethodGet, "http://localhost/private?token=private-token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || strings.Contains(w.Body.String()+logs.String(), "private-") {
		t.Fatal("panic response or log disclosed request data")
	}
	if !strings.Contains(logs.String(), w.Header().Get("X-Request-ID")) {
		t.Fatal("panic is not correlated with request ID")
	}
	if !strings.Contains(logs.String(), `"panic_type":"string"`) || !strings.Contains(logs.String(), `"call_path":"household/internal/web.TestPanicDoesNotDiscloseRequestData.`) {
		t.Fatalf("panic log lacks its type or originating frame: %s", logs.String())
	}
}

func TestPanicAfterResponseStartAndAbort(t *testing.T) {
	t.Parallel()
	s := &Server{Cfg: config.Config{Origin: "http://localhost"}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Run("partial response is not overwritten", func(t *testing.T) {
		t.Parallel()
		h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("partial"))
			panic("late failure")
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if w.Code != http.StatusOK || w.Body.String() != "partial" {
			t.Fatalf("started response was altered: %d %q", w.Code, w.Body.String())
		}
	})
	t.Run("abort handler propagates", func(t *testing.T) {
		t.Parallel()
		h := s.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Fatalf("ErrAbortHandler was swallowed: %v", recovered)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	})
}

func TestUnauthenticatedPrivateRoutes(t *testing.T) {
	t.Parallel()
	s := &Server{Cfg: config.Config{Origin: "http://localhost"}, Store: store.New(nil, modules.Registry()), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.Handler()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/v1/core/bootstrap", 401}, {"/api/v1/m/shopping/lists", 401}, {"/attachments/abc", 303}, {"/settings/users", 303}, {"/metrics", 404}} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost"+tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("private route: status=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestReadJSONBodyLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		size int
	}{{"small body", 128}, {"over limit", 1 << 20}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]string{"name": strings.Repeat("x", tc.size)})
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]string
			err = readJSON(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), &result)
			if (err == nil) != (tc.size < 1<<20) {
				t.Fatalf("body size %d: %v", len(body), err)
			}
		})
	}
}
