//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"household/internal/value"
)

func TestPantryExtensionsPublicShellAndCurrentSessionContext(t *testing.T) {
	f := newIntegrationFixture(t)
	response, body, err := f.request(http.MethodGet, "/offline/scans?lang=de", "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("public data-free shell: response=%v error=%v", response, err)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "public") || len(response.Cookies()) != 0 ||
		strings.Contains(string(body), f.identity.UserID) || strings.Contains(string(body), f.identity.SessionID) ||
		strings.Contains(string(body), f.app.Auth.CSRF(f.token, "session")) {
		t.Fatal("public shell included private identity/credential or set a cookie")
	}
	if !strings.Contains(response.Header.Get("Permissions-Policy"), "camera=(self)") ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "connect-src 'self'") ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "media-src 'self' blob:") {
		t.Fatal("dedicated camera or same-origin policy differs")
	}
	response, body, err = f.request(http.MethodGet, "/offline/scans", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private,no-store" ||
		len(response.Cookies()) != 0 || strings.Contains(string(body), f.identity.UserID) ||
		strings.Contains(string(body), f.identity.SessionID) || strings.Contains(string(body), f.app.Auth.CSRF(f.token, "session")) {
		t.Fatal("unqualified locale-dependent shell became shared or contained private identity")
	}
	response, _, err = f.request(http.MethodGet, "/login", "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK ||
		!strings.Contains(response.Header.Get("Permissions-Policy"), "camera=()") {
		t.Fatal("camera permission escaped its dedicated document")
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/pantry/extensions/context", "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous private context was exposed")
	}
	read := func(token string, write bool) string {
		t.Helper()
		response, raw, requestErr := f.request(http.MethodGet, "/api/v1/pantry/extensions/context", token, "", "", nil, nil)
		if requestErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("current context unavailable: status=%v error=%v", response, requestErr)
		}
		var out struct {
			Context struct {
				HouseholdID string `json:"household_id"`
				UserID      string `json:"user_id"`
				SessionID   string `json:"session_id"`
				AuthScope   string `json:"auth_scope"`
				Generation  int64  `json:"generation"`
				CanWrite    bool   `json:"can_write"`
			} `json:"context"`
		}
		if decodeErr := json.Unmarshal(raw, &out); decodeErr != nil || out.Context.HouseholdID != f.identity.HouseholdID ||
			!value.ValidUUID(out.Context.UserID) || !value.ValidUUID(out.Context.SessionID) ||
			out.Context.Generation != f.app.Store.States()["pantry"].Generation ||
			len(out.Context.AuthScope) != 64 || out.Context.CanWrite != write {
			t.Fatal("context is not scoped to current identity, generation and authority")
		}
		return out.Context.AuthScope
	}
	adminScope := read(f.token, true)
	shoppingTestMember(t, f, "pantry-extension-reader", "reader")
	readerToken, reader, err := f.app.Auth.Login(f.ctx, "pantry-extension-reader", integrationPassword, "", "127.0.0.1", "pantry-extension-context")
	if err != nil {
		t.Fatal(err)
	}
	if read(readerToken, false) == adminScope {
		t.Fatal("different actor/session reused the same scope marker")
	}
	for _, path := range []string{"/m/pantry/leftovers", "/m/pantry/food-facts", "/m/pantry/scans"} {
		response, _, err = f.request(http.MethodGet, path, readerToken, "", "", nil, nil)
		if err != nil || response.StatusCode != http.StatusForbidden {
			t.Fatal("reader entered a mutation wizard", path, err)
		}
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/pantry/lots/"+value.UUID()+"/prepared", readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatal("reader-safe prepared detail did not hide nonexistent facts")
	}
	if err = f.app.Auth.RevokeSession(f.ctx, reader, reader.SessionID, "session_revoked"); err != nil {
		t.Fatal(err)
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/pantry/extensions/context", readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("revoked session retained a current scan context")
	}
}

func TestPantryExtensionsServiceWorkerCachesOnlyNeutralScanShell(t *testing.T) {
	f := newIntegrationFixture(t)
	response, body, err := f.request(http.MethodGet, "/sw.js", "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("service worker response unavailable", err)
	}
	match := regexp.MustCompile(`ASSETS=(\[[^\n]*?\]);`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("versioned public cache inventory missing")
	}
	var paths []string
	if err = json.Unmarshal(match[1], &paths); err != nil {
		t.Fatal(err)
	}
	shells := []struct{ path, marker string }{
		{"/offline", ""}, {"/offline/scans", ""},
		{"/offline/shopping", "data-shopping-offline"},
		{"/offline/recipes", `<section id="recipe-offline"`},
	}
	expected := make(map[string]bool, 8)
	for _, shell := range shells {
		for _, locale := range []string{"de", "en"} {
			expected[shell.path+"?lang="+locale] = false
		}
	}
	assetPath := regexp.MustCompile(`^/assets/[a-f0-9]{12}/[a-z0-9][a-z0-9.-]*\.(js|css|svg|png)$`)
	for _, path := range paths {
		if seen, exists := expected[path]; exists {
			if seen {
				t.Fatal("duplicate locale-qualified public shell", path)
			}
			expected[path] = true
		} else if !assetPath.MatchString(path) {
			t.Fatal("private data entered public asset cache", path)
		}
	}
	for path, seen := range expected {
		if !seen {
			t.Fatal("offline restart lacks an exact locale-qualified neutral document", path)
		}
	}
	for _, caller := range []struct {
		name, token string
	}{{"anonymous", ""}, {"current administrator", f.token}} {
		t.Run(caller.name, func(t *testing.T) {
			for _, shell := range shells {
				for _, locale := range []string{"de", "en"} {
					qualified := shell.path + "?lang=" + locale
					response, raw, requestErr := f.request(http.MethodGet, qualified, caller.token, "", "", nil, nil)
					if requestErr != nil || response.StatusCode != http.StatusOK {
						t.Fatal("neutral document unavailable", shell.path, requestErr)
					}
					if !strings.Contains(response.Header.Get("Cache-Control"), "public") || len(response.Cookies()) != 0 {
						t.Fatal("neutral document set private cache state or a cookie", shell.path)
					}
					text := string(raw)
					language := "de-AT"
					if locale == "en" {
						language = "en-GB"
					}
					if !strings.Contains(text, `lang="`+language+`"`) || !strings.Contains(text, `data-locale="`+locale+`"`) {
						t.Fatal("public shell language differs from its cache key", qualified)
					}
					for _, required := range []string{`data-user=""`, `data-household=""`, `<meta name="csrf-token" content="">`, shell.marker} {
						if !strings.Contains(text, required) {
							t.Fatal("shell did not retain empty identity/CSRF fields and its neutral view", shell.path)
						}
					}
					for _, private := range []string{f.identity.UserID, f.identity.HouseholdID, f.identity.SessionID, f.identity.MembershipID, f.identity.Username, f.token, f.app.Auth.CSRF(f.token, "session")} {
						if private != "" && strings.Contains(text, private) {
							t.Fatal("shell included fixture identity or credentials", shell.path)
						}
					}
				}
			}
		})
	}
}
