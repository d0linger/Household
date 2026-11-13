package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"household/internal/modules"
	"household/internal/store"

	"golang.org/x/net/html"
)

func serviceWorkerTestServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{assets: map[string]string{}, Store: store.New(nil, modules.Registry()), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	entries, err := files.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, readErr := files.ReadFile("static/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		sum := sha256.Sum256(body)
		s.assets[entry.Name()] = "/assets/" + hex.EncodeToString(sum[:6]) + "/" + entry.Name()
	}
	// Helpers outside the public shells only need to exist while parsing.
	funcs := template.FuncMap{"dict": dict, "icon": func(assets map[string]string, name string) string { return assets["icons.svg"] + "#" + name }}
	for _, name := range []string{"text", "qty", "choice", "prev", "next", "integer", "money", "formValue", "isTrue", "selected", "enabled", "moduleURL", "recordURL"} {
		funcs[name] = func(...any) any { return nil }
	}
	s.templates, err = template.New("household").Funcs(funcs).Funcs(localizedTemplateFuncs("de")).ParseFS(files, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	s.templatesEN, err = template.New("household").Funcs(funcs).Funcs(localizedTemplateFuncs("en")).ParseFS(files, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func renderedServiceWorker(t *testing.T, s *Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.serviceWorker(w, httptest.NewRequest(http.MethodGet, "http://localhost/sw.js", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("service worker: %d %s", w.Code, w.Body)
	}
	return w.Body.String()
}

func workerInventory(t *testing.T, body, name string) []string {
	t.Helper()
	match := regexp.MustCompile(`const ` + name + `=(\[[^\n]*?\]);`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("missing %s inventory", name)
	}
	var inventory []string
	if err := json.Unmarshal([]byte(match[1]), &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func TestServiceWorkerPrecacheCoversRenderedOfflineShells(t *testing.T) {
	t.Parallel()
	s := serviceWorkerTestServer(t)
	body := renderedServiceWorker(t, s)
	precache, all := workerInventory(t, body, "PRECACHE"), workerInventory(t, body, "ASSETS")
	needed := map[string]bool{}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{{"/offline", s.offline}, {"/offline/scans", s.offlinePantryScans}, {"/offline/shopping", s.offlineShopping}, {"/offline/recipes", s.offlineRecipes}} {
		for _, locale := range []string{"de", "en"} {
			name := tc.name + "?lang=" + locale
			t.Run(name, func(t *testing.T) {
				w := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "http://localhost"+name, nil)
				request.AddCookie(&http.Cookie{Name: "household_locale", Value: map[string]string{"de": "en", "en": "de"}[locale]})
				tc.handler(w, request)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-user=""`) || !strings.Contains(w.Body.String(), "</html>") {
					t.Fatalf("public shell failed to render completely: %d", w.Code)
				}
				if !strings.Contains(w.Body.String(), `data-locale="`+locale+`"`) || !strings.Contains(w.Body.String(), `name="csrf-token" content=""`) || strings.Contains(w.Body.String(), `action="/language"`) {
					t.Fatal("public locale variant included private mutation state or ignored its explicit language")
				}
				if w.Header().Get("Cache-Control") != "public,max-age=0,must-revalidate" {
					t.Fatal("qualified public shell is not cacheable")
				}
				needed[name] = true
				document, err := html.Parse(strings.NewReader(w.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				for node := range document.Descendants() {
					for _, attribute := range node.Attr {
						if attribute.Key != "src" && attribute.Key != "href" {
							continue
						}
						asset := strings.SplitN(attribute.Val, "#", 2)[0]
						if strings.HasPrefix(asset, "/assets/") {
							needed[asset] = true
						}
					}
				}
			})
		}
	}
	manifest := httptest.NewRecorder()
	s.manifest(manifest, httptest.NewRequest(http.MethodGet, "http://localhost/manifest.webmanifest", nil))
	var appManifest struct{ Icons []struct{ Src string } }
	if err := json.Unmarshal(manifest.Body.Bytes(), &appManifest); err != nil {
		t.Fatal(err)
	}
	for _, icon := range appManifest.Icons {
		needed[icon.Src] = true
	}
	// Follow local CSS imports/URLs too, so a later font or image dependency cannot silently disappear offline.
	pending := make([]string, 0, len(needed))
	for asset := range needed {
		pending = append(pending, asset)
	}
	cssReference := regexp.MustCompile(`(?:url\(\s*["']?|@import\s+["'])([^"'\s)]+)`)
	for len(pending) > 0 {
		asset := pending[0]
		pending = pending[1:]
		if !strings.HasSuffix(asset, ".css") {
			continue
		}
		css, err := files.ReadFile("static/" + path.Base(asset))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range cssReference.FindAllStringSubmatch(string(css), -1) {
			ref := strings.SplitN(match[1], "#", 2)[0]
			if ref == "" || strings.HasPrefix(ref, "data:") {
				continue
			}
			if !strings.HasPrefix(ref, "/assets/") {
				t.Fatalf("offline CSS dependency needs a versioned local asset URL: %q", ref)
			}
			if !needed[ref] {
				needed[ref] = true
				pending = append(pending, ref)
			}
		}
	}
	for asset := range needed {
		if !slices.Contains(precache, asset) {
			t.Errorf("rendered offline dependency missing from precache: %s", asset)
		}
	}
	for _, asset := range precache {
		if !needed[asset] {
			t.Errorf("unneeded install-time fetch: %s", asset)
		}
	}
	if len(all) != len(s.assets)+8 || len(precache) >= len(all) {
		t.Fatal("full immutable allowlist must remain larger than the offline precache")
	}
	for _, asset := range all {
		if !strings.HasPrefix(asset, "/assets/") && !needed[asset] {
			t.Errorf("private route in cache inventory: %s", asset)
		}
	}
	for _, asset := range s.assets {
		if !slices.Contains(all, asset) {
			t.Errorf("immutable asset omitted from runtime allowlist: %s", asset)
		}
	}
}

func TestUnqualifiedOfflineLanguageAndManifestVariants(t *testing.T) {
	t.Parallel()
	s := serviceWorkerTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://localhost/offline", nil)
	r.AddCookie(&http.Cookie{Name: "household_locale", Value: "en"})
	s.offline(w, r)
	if w.Header().Get("Cache-Control") != "private,no-store" || !strings.Contains(w.Body.String(), `data-locale="en"`) {
		t.Fatal("unqualified device-language response must not enter shared caches")
	}
	for _, locale := range []string{"de", "en", "invalid"} {
		w := httptest.NewRecorder()
		s.manifest(w, httptest.NewRequest(http.MethodGet, "http://localhost/manifest.webmanifest?lang="+locale, nil))
		var data struct{ Lang string }
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		want := "de-AT"
		if locale == "en" {
			want = "en-GB"
		}
		if data.Lang != want {
			t.Fatalf("manifest language %q, want %q", data.Lang, want)
		}
	}
}

func TestServiceWorkerVersionIncludesManifestAndRuntime(t *testing.T) {
	t.Parallel()
	s := serviceWorkerTestServer(t)
	body := renderedServiceWorker(t, s)
	prefix, source, ok := strings.Cut(body, "\n")
	if !ok {
		t.Fatal("worker has no version declaration")
	}
	sum := sha256.Sum256([]byte(source))
	if prefix != fmt.Sprintf("'use strict';const CACHE=%q;", "household-"+hex.EncodeToString(sum[:8])) {
		t.Fatal("worker version does not fingerprint the complete runtime and manifests")
	}
	if !strings.Contains(source, serviceWorkerRuntime) || !strings.Contains(source, "notificationclick") {
		t.Fatal("cache strategy or push behavior omitted from fingerprint")
	}
	s.assets["finance-workspace.js"] = "/assets/changed/finance-workspace.js"
	changed := renderedServiceWorker(t, s)
	if strings.SplitN(changed, "\n", 2)[0] == prefix {
		t.Fatal("changing an on-demand asset did not rotate the worker cache")
	}
	delete(s.assets, "app.css")
	w := httptest.NewRecorder()
	s.serviceWorker(w, httptest.NewRequest(http.MethodGet, "http://localhost/sw.js", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatal("missing offline dependency must fail worker generation")
	}
}
