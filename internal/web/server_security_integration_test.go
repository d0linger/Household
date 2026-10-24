//go:build integration

package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/database"
	"household/internal/value"
)

func TestRequestSecurityContract(t *testing.T) {
	f := newIntegrationFixture(t)
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, tc := range []struct {
		name, token, csrf string
		headers           map[string]string
		want              int
	}{
		{"missing_session", "", csrf, nil, 401},
		{"missing_csrf", f.token, "", nil, 403},
		{"another_session_csrf", f.token, f.app.Auth.CSRF(auth.RandomToken(), "session"), nil, 403},
		{"login_csrf_on_session", f.token, f.app.Auth.CSRF(f.token, "login"), nil, 403},
		{"cross_origin", f.token, csrf, map[string]string{"Origin": "https://untrusted.example"}, 403},
		{"cross_site", f.token, csrf, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same_origin", f.token, csrf, map[string]string{"Origin": f.server.URL, "Sec-Fetch-Site": "same-origin"}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"generation":%d,"data":{"name":"Boundary %s"}}`, f.app.Store.States()["shopping"].Generation, tc.name)
			headers := map[string]string{"Idempotency-Key": value.UUID()}
			for key, value := range tc.headers {
				headers[key] = value
			}
			response, data, err := f.request(http.MethodPost, "/api/v1/m/shopping/lists", tc.token, tc.csrf, "application/json", strings.NewReader(body), headers)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want {
				t.Fatalf("request status = %d, want %d: %s", response.StatusCode, tc.want, data)
			}
			var count int
			if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM shopping.lists WHERE name=$1", "Boundary "+tc.name).Scan(&count); err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.want == 200 {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("persisted records = %d, want %d", count, wantCount)
			}
		})
	}
	response, _, err := f.request(http.MethodGet, "/api/v1/m/shopping/lists", "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read: response=%v error=%v", response, err)
	}
}

func TestSessionInvalidationContract(t *testing.T) {
	for _, tc := range []struct {
		name, update string
		user         bool
	}{
		{"idle_expired", "UPDATE core.sessions SET idle_expires_at=now()-interval '1 second' WHERE id=$1", false},
		{"absolute_expired", "UPDATE core.sessions SET idle_expires_at=now()-interval '2 seconds',absolute_expires_at=now()-interval '1 second' WHERE id=$1", false},
		{"revoked", "UPDATE core.sessions SET revoked_at=now() WHERE id=$1", false},
		{"credential_version_changed", "UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1", true},
		{"account_disabled", "UPDATE core.users SET disabled_at=now() WHERE id=$1", true},
		{"membership_inactive", "UPDATE core.memberships SET inactive_at=now() WHERE user_id=$1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			id := f.identity.SessionID
			if tc.user {
				id = f.identity.UserID
			}
			if _, err := f.owner.Exec(f.ctx, tc.update, id); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.Auth.Resolve(f.ctx, f.token, true); !errors.Is(err, auth.ErrSession) {
				t.Fatalf("invalid session resolved: %v", err)
			}
			response, _, err := f.request(http.MethodGet, "/api/v1/core/bootstrap", f.token, "", "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("invalid session HTTP status = %d, want 401", response.StatusCode)
			}
		})
	}
}

func TestSessionRenewalContract(t *testing.T) {
	f := newIntegrationFixture(t)
	var before, absolute time.Time
	if err := f.owner.QueryRow(f.ctx, "UPDATE core.sessions SET idle_expires_at=now()+interval '5 minutes',absolute_expires_at=now()+interval '10 minutes' WHERE id=$1 RETURNING idle_expires_at,absolute_expires_at", f.identity.SessionID).Scan(&before, &absolute); err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodGet, "/api/v1/core/bootstrap", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("API read: response=%v error=%v", response, err)
	}
	var after time.Time
	if err = f.owner.QueryRow(f.ctx, "SELECT idle_expires_at FROM core.sessions WHERE id=$1", f.identity.SessionID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) || len(response.Cookies()) != 0 {
		t.Fatal("background API polling extended the session")
	}
	response, _, err = f.request(http.MethodGet, "/settings", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("interactive read: response=%v error=%v", response, err)
	}
	if err = f.owner.QueryRow(f.ctx, "SELECT idle_expires_at FROM core.sessions WHERE id=$1", f.identity.SessionID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(absolute) {
		t.Fatalf("sliding expiration = %v, expected absolute cap %v", after, absolute)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == f.app.cookieName() {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" || sessionCookie.Expires.After(absolute) {
		t.Fatal("renewed session cookie lacks required bounds or flags")
	}
}

func TestForcedPasswordAndRevocationContract(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.users SET must_change_password=true WHERE id=$1", f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/api/v1/core/bootstrap", 403}, {"/settings", 303}, {"/change-password", 200}} {
		t.Run(tc.path, func(t *testing.T) {
			response, _, err := f.request(http.MethodGet, tc.path, f.token, "", "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.want)
			}
		})
	}
	secondToken, _, err := f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.1", "Second browser")
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "Changed integration 1234"
	if err = f.app.Auth.ChangePassword(f.ctx, f.identity, integrationPassword, replacement); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{f.token, secondToken} {
		if _, err = f.app.Auth.Resolve(f.ctx, token, false); !errors.Is(err, auth.ErrSession) {
			t.Fatalf("password change left an old session active: %v", err)
		}
	}
	if _, _, err = f.app.Auth.Login(f.ctx, f.identity.Username, integrationPassword, "", "127.0.0.1", "Old password"); !errors.Is(err, auth.ErrCredentials) {
		t.Fatalf("previous password accepted: %v", err)
	}
	_, id, err := f.app.Auth.Login(f.ctx, f.identity.Username, replacement, "", "127.0.0.1", "New password")
	if err != nil || id.MustChange {
		t.Fatalf("replacement login or forced-change release failed: %v", err)
	}
}

func TestRateLimitPersistenceContract(t *testing.T) {
	f := newIntegrationFixture(t)
	const attempts, allowed = 24, 7
	// A broad fixed window avoids a wall-clock rollover during the concurrency test.
	const window = 100 * 365 * 24 * time.Hour
	subject := "fixture-" + value.UUID()
	results := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- f.app.Auth.Limit(f.ctx, "integration-limit", subject, allowed, window)
		}()
	}
	wait.Wait()
	close(results)
	accepted, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, auth.ErrLimited):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if accepted != allowed || limited != attempts-allowed {
		t.Fatalf("concurrent limit accepted=%d limited=%d, want %d/%d", accepted, limited, allowed, attempts-allowed)
	}
	var count int
	var digest []byte
	if err := f.owner.QueryRow(f.ctx, "SELECT attempt_count,subject_hash FROM core.rate_limit_buckets WHERE scope='integration-limit'").Scan(&count, &digest); err != nil {
		t.Fatal(err)
	}
	if count != attempts || len(digest) != 32 || bytes.Equal(digest, []byte(subject)) {
		t.Fatal("persistent rate bucket lost attempts or retained a raw subject")
	}
	reconnected, err := database.Open(f.ctx, f.app.Cfg.DatabaseURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	restarted, err := auth.New(reconnected, f.app.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Limit(f.ctx, "integration-limit", subject, allowed, window); !errors.Is(err, auth.ErrLimited) {
		t.Fatalf("service restart reset the durable rate limit: %v", err)
	}
	for _, tc := range []struct{ name, scope, subject string }{
		{"different_subject", "integration-limit", subject + "-other"},
		{"different_scope", "another-limit", subject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := restarted.Limit(f.ctx, tc.scope, tc.subject, allowed, window); err != nil {
				t.Fatalf("independent bucket rejected: %v", err)
			}
		})
	}
}

func TestLastAdministratorContract(t *testing.T) {
	for _, tc := range []struct {
		name, role, disabled string
	}{
		{"demotion", "reader", "false"},
		{"disable", "admin", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntegrationFixture(t)
			form := url.Values{"role": {tc.role}, "disabled": {tc.disabled}}
			response, data, err := f.request(http.MethodPost, "/settings/users/"+f.identity.UserID, f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("last admin removal returned %d: %s", response.StatusCode, data)
			}
			id, err := f.app.Auth.Resolve(f.ctx, f.token, false)
			if err != nil || id.Role != "admin" {
				t.Fatalf("rejected admin update changed access: %v", err)
			}
		})
	}
}
