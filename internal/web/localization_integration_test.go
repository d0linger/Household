//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLanguagePreferencesAndSettingsNavigation(t *testing.T) {
	f := newIntegrationFixture(t)
	csrf := f.app.Auth.CSRF(f.token, "session")
	save := func(body string, status int) {
		t.Helper()
		r, raw, err := f.request(http.MethodPost, "/api/v1/core/preferences", f.token, csrf, "application/json", strings.NewReader(body), nil)
		if err != nil || r.StatusCode != status {
			t.Fatalf("save %s: %v %v %s", body, r, err, raw)
		}
	}
	save(`{"theme":"pink-system","offline":true,"locale":"en"}`, 200)
	save(`{"theme":"dark"}`, 200)
	var theme, locale string
	var offline bool
	err := f.pool.QueryRow(f.ctx, "SELECT theme,locale,offline_storage_enabled FROM core.user_preferences WHERE user_id=$1", f.identity.UserID).Scan(&theme, &locale, &offline)
	if err != nil || theme != "dark" || locale != "en" || !offline {
		t.Fatal("partial appearance changed unrelated preferences", theme, locale, offline, err)
	}
	for _, input := range []string{`{}`, `{"locale":"fr"}`, `{"locale":"../../en"}`, `{"theme":"pink-auto"}`, `{"locale":null}`} {
		save(input, 422)
	}
	for _, path := range []string{"/settings", "/settings/modules", "/settings/security", "/settings/users", "/settings/audit"} {
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, map[string]string{"Accept": "text/html"})
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("settings %s: %v %v %s", path, r, err, raw)
		}
		html := string(raw)
		for _, text := range []string{`lang="en-GB"`, `class="settings-nav"`, `href="` + path + `" aria-current="page"`, `class="tray-session"`, `action="/logout"`, `Sign out`, `Audit log`} {
			if !strings.Contains(html, text) {
				t.Errorf("%s missing %s", path, text)
			}
		}
		if r.Header.Get("Content-Language") != "en-GB" {
			t.Error("missing response language")
		}
	}
	r, raw, err := f.request(http.MethodGet, "/api/v1/core/preferences", f.token, "", "", nil, nil)
	var result struct {
		Theme, Locale string
		Offline       bool
	}
	if err != nil || r.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || result.Theme != "dark" || result.Locale != "en" || !result.Offline {
		t.Fatalf("preference read %v %v %s", r, err, raw)
	}
	form := url.Values{"locale": {"de"}, "return_to": {"/settings/audit?q=tea"}}
	r, raw, err = f.request(http.MethodPost, "/settings/language", f.token, csrf, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
	if err != nil || r.StatusCode != 303 || r.Header.Get("Location") != "/settings/audit?q=tea" {
		t.Fatalf("language return %v %v %s", r, err, raw)
	}
	var cookie *http.Cookie
	for _, value := range r.Cookies() {
		if value.Name == "household_locale" {
			cookie = value
		}
	}
	if cookie == nil || cookie.Value != "de" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe or missing locale cookie", cookie)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT theme,locale,offline_storage_enabled FROM core.user_preferences WHERE user_id=$1", f.identity.UserID).Scan(&theme, &locale, &offline); err != nil || theme != "dark" || locale != "de" || !offline {
		t.Fatal("language switch changed appearance or device permission", err)
	}
	form.Set("locale", "en")
	r, _, err = f.request(http.MethodPost, "/settings/language", f.token, "invalid", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
	if err != nil || r.StatusCode != 403 {
		t.Fatal("language mutation bypassed CSRF", r, err)
	}
	// Account language wins over a stale device cookie, and another household
	// member retains their independent language and administration boundary.
	r, raw, err = f.request(http.MethodGet, "/settings", f.token, "", "", nil, map[string]string{"Cookie": f.app.cookieName() + "=" + f.token + "; household_locale=en"})
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), `lang="de-AT"`) {
		t.Fatal("cookie overrode saved account language", err)
	}
	reader := shoppingTestMember(t, f, "localereader", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.9", "Locale check")
	if err != nil {
		t.Fatal(err)
	}
	r, raw, err = f.request(http.MethodGet, "/settings", token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), `lang="de-AT"`) || strings.Contains(string(raw), `href="/settings/users"`) || strings.Contains(string(raw), `href="/settings/audit"`) {
		t.Fatal("reader navigation or locale scope", err)
	}
}

func TestPublicLanguageRequiresLoginCSRF(t *testing.T) {
	f := newIntegrationFixture(t)
	r, _, err := f.request(http.MethodGet, "/login", "", "", "", nil, nil)
	if err != nil || r.StatusCode != 200 {
		t.Fatal("login", err)
	}
	var challenge string
	for _, cookie := range r.Cookies() {
		if cookie.Name == "household_login" {
			challenge = cookie.Value
		}
	}
	if challenge == "" {
		t.Fatal("missing login challenge")
	}
	form := url.Values{"locale": {"en"}, "csrf": {f.app.Auth.CSRF(challenge, "login")}, "return_to": {"https://untrusted.invalid"}}
	r, raw, err := f.request(http.MethodPost, "/language", "", "", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), map[string]string{"Cookie": "household_login=" + challenge})
	if err != nil || r.StatusCode != 303 || r.Header.Get("Location") != "/login" {
		t.Fatalf("public switch %v %v %s", r, err, raw)
	}
	form.Set("csrf", "wrong")
	r, _, err = f.request(http.MethodPost, "/language", "", "", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), map[string]string{"Cookie": "household_login=" + challenge})
	if err != nil || r.StatusCode != 403 {
		t.Fatal("public language CSRF bypass", r, err)
	}
}
