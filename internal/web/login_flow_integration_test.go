//go:build integration

package web

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
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
	if err != nil || len(codes) < 2 || codes[0] == codes[1] {
		t.Fatalf("enable fixture MFA with distinct recovery codes: %v", err)
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
	doSubmit := func(cookie *http.Cookie, form url.Values) (int, bool, string, error) {
		form.Set("csrf", f.app.Auth.CSRF(cookie.Value, "login"))
		request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			return 0, false, "", err
		}
		request.AddCookie(cookie)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", f.app.Cfg.Origin)
		response, err := f.client.Do(request)
		if err != nil {
			return 0, false, "", err
		}
		defer func() { _ = response.Body.Close() }()
		var session, renewed bool
		for _, c := range response.Cookies() {
			session = session || c.Name == f.app.cookieName() && c.MaxAge > 0
			renewed = renewed || c.Name == "household_login" && c.Value == cookie.Value && c.MaxAge == int(auth.LoginFactorTTL/time.Second)
		}
		if response.StatusCode == http.StatusOK && form.Has("password") && !renewed {
			return 0, false, "", errors.New("entering the MFA step must renew the same login cookie for the full factor lifetime")
		}
		body, err := io.ReadAll(response.Body)
		return response.StatusCode, session, string(body), err
	}
	submit := func(cookie *http.Cookie, form url.Values, want int, authenticated bool) string {
		t.Helper()
		status, session, body, err := doSubmit(cookie, form)
		if err != nil || status != want || session != authenticated {
			t.Fatalf("login step: got status %d, session %v, error %v; want status %d, session %v", status, session, err, want, authenticated)
		}
		return body
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
	sessionCount := func() int {
		t.Helper()
		var count int
		if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.sessions WHERE user_id=$1", member.UserID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := sessionCount()
	type submission struct {
		status  int
		session bool
		err     error
	}
	ready, start := make(chan struct{}, 2), make(chan struct{})
	results := make(chan submission, 2)
	for _, code := range codes[:2] {
		go func() {
			ready <- struct{}{}
			<-start
			form := url.Values{"factor": {tokenField[1]}, "code": {code}}
			status, session, _, err := doSubmit(binding, form)
			results <- submission{status: status, session: session, err: err}
		}()
	}
	<-ready
	<-ready
	close(start)
	accepted := 0
	for range 2 {
		result := <-results
		switch {
		case result.err != nil:
			t.Fatal(result.err)
		case result.status == http.StatusSeeOther && result.session:
			accepted++
		case result.status != http.StatusUnauthorized || result.session:
			t.Fatalf("unexpected concurrent MFA result: status %d, session %v", result.status, result.session)
		}
	}
	if after := sessionCount(); accepted != 1 || after != before+1 {
		t.Fatalf("concurrent MFA redemption accepted %d requests and created %d sessions, want one of each", accepted, after-before)
	}
	factor.Set("code", codes[0])
	submit(binding, factor, http.StatusUnauthorized, false)
}
