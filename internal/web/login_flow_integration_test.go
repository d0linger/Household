//go:build integration

package web

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"household/internal/database"

	"github.com/pquerna/otp/totp"
)

func TestBrowserEmailLoginWithSecondFactor(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("Core45 database readiness:", err)
	}
	adminForm := url.Values{"username": {"Daniel+home@example.org"}, "display_name": {"Email member"}, "role": {"reader"}, "password": {integrationPassword}}
	create := func(want int) {
		t.Helper()
		response, _, err := f.request(http.MethodPost, "/settings/users", f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(adminForm.Encode()), nil)
		if err != nil || response.StatusCode != want {
			t.Fatalf("create email account: want %d, got %v, error %v", want, response, err)
		}
	}
	create(http.StatusSeeOther)
	adminForm.Set("username", "daniel+home@EXAMPLE.ORG")
	create(http.StatusUnprocessableEntity)

	username := adminForm.Get("username")
	_, member, err := f.app.Auth.Login(f.ctx, username, integrationPassword, "", "127.0.0.3", "Email login")
	if err != nil || !member.MustChange {
		t.Fatalf("case-insensitive email sign-in and starter password policy: %v", err)
	}
	secret, _, err := f.app.Auth.SetupTOTP(f.ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := f.app.Auth.EnableTOTP(f.ctx, member, code)
	if err != nil || len(codes) == 0 {
		t.Fatalf("enable fixture MFA: %v", err)
	}

	browser := func() *http.Cookie {
		t.Helper()
		response, err := f.client.Get(f.server.URL + "/login")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		for _, cookie := range response.Cookies() {
			if cookie.Name == "household_login" {
				return cookie
			}
		}
		t.Fatal("login binding missing")
		return nil
	}
	submit := func(cookie *http.Cookie, form url.Values, want int, authenticated bool) string {
		t.Helper()
		form.Set("csrf", f.app.Auth.CSRF(cookie.Value, "login"))
		request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(cookie)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", f.app.Cfg.Origin)
		response, err := f.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		var session bool
		for _, c := range response.Cookies() {
			session = session || c.Name == f.app.cookieName() && c.MaxAge > 0
		}
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want || session != authenticated {
			t.Fatalf("login step: got status %d, session %v, error %v; want status %d, session %v", response.StatusCode, session, err, want, authenticated)
		}
		return string(body)
	}
	binding := browser()
	first := url.Values{"username": {username}, "password": {"wrong"}}
	if html := submit(binding, first, http.StatusUnauthorized, false); strings.Contains(html, "one-time-code") || !strings.Contains(html, `role="alert"`) {
		t.Fatal("wrong password must show an inline error without revealing MFA")
	}
	first.Set("password", integrationPassword)
	html := submit(binding, first, http.StatusOK, false)
	tokenField := regexp.MustCompile(`name="factor" value="([A-Za-z0-9_-]{43})"`).FindStringSubmatch(html)
	if len(tokenField) != 2 || !strings.Contains(html, `autocomplete="one-time-code"`) || strings.Contains(html, `name="password"`) {
		t.Fatal("verified password did not show the standalone MFA step")
	}
	factor := url.Values{"factor": {tokenField[1]}, "code": {"invalid"}}
	submit(browser(), factor, http.StatusUnauthorized, false)
	if html = submit(binding, factor, http.StatusUnauthorized, false); !strings.Contains(html, `autocomplete="one-time-code"`) {
		t.Fatal("invalid code must allow retry in the same challenge")
	}
	factor.Set("code", codes[0])
	submit(binding, factor, http.StatusSeeOther, true)
	submit(binding, factor, http.StatusUnauthorized, false)
}
