package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"household/internal/i18n"
)

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	expected, ok := s.assets[name]
	if !ok || expected != r.URL.Path || path.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	data, err := files.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	}
	s.writeBytes(w, r, data)
}
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	locale := r.URL.Query().Get("lang")
	if !validLocale(locale) {
		locale = "de"
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public,max-age=3600")
	if err := json.NewEncoder(w).Encode(map[string]any{"id": "/", "name": "Household", "short_name": "Household", "lang": i18n.LanguageTag(locale), "start_url": "/", "scope": "/", "display": "standalone", "background_color": "#f4f8fe", "theme_color": "#075ca8", "icons": []map[string]string{{"src": s.assets["icon-192.png"], "sizes": "192x192", "type": "image/png", "purpose": "any maskable"}, {"src": s.assets["icon-512.png"], "sizes": "512x512", "type": "image/png", "purpose": "any maskable"}}}); err != nil {
		s.Log.Debug("manifest response interrupted", "request_id", trace(r))
	}
}
func (s *Server) offline(w http.ResponseWriter, r *http.Request) {
	s.offlinePage(w, r, "Offline", "offline")
}

// Only the explicit language selects a public cache variant. No session,
// account preferences, CSRF token, private record, or module state is loaded.
func (s *Server) offlinePage(w http.ResponseWriter, r *http.Request, title, view string) {
	locale := r.URL.Query().Get("lang")
	if validLocale(locale) {
		w.Header().Set("Cache-Control", "public,max-age=0,must-revalidate")
	} else {
		locale = requestLocale(r)
		w.Header().Set("Cache-Control", "private,no-store")
	}
	r = r.WithContext(context.WithValue(r.Context(), localeKey, locale))
	nonce, _ := r.Context().Value(nonceKey).(string)
	p := Page{Title: title, View: view, Nonce: nonce, Asset: s.assets, OfflineShell: true, ReturnTo: r.URL.Path}
	s.render(w, r, p)
}

// offlinePantryScans serves the same public, data-free shell to every caller.
// Device drafts are opened only by local explicit consent, never embedded here.
func (s *Server) offlinePantryScans(w http.ResponseWriter, r *http.Request) {
	pantryScanCameraHeaders(w, r)
	s.offlinePage(w, r, "Offline-Scans", "pantry-scans")
}

func (s *Server) offlineShopping(w http.ResponseWriter, r *http.Request) {
	s.offlinePage(w, r, "Offline-Einkaufslisten", "shopping-offline")
}

func (s *Server) offlineRecipes(w http.ResponseWriter, r *http.Request) {
	s.offlinePage(w, r, "Offline-Rezepte", "recipe-offline")
}

// Installation needs complete public fallback screens, not every module editor.
// Rendered-shell tests keep this list aligned with template dependencies.
var offlineShellAssets = [...]string{
	"i18n-catalog.js", "i18n.js", "theme.js", "app.css", "settings-shell.css", "settings-shell.js", "pantry-device.js", "shopping-extension-ui.js",
	"shopping-device.js", "push-device.js", "recipe-offline.js", "report-values.js", "app.js",
	"pantry-extensions.css", "pantry-extension-ui.js", "pantry-scans.js",
	"shopping-extensions.css", "shopping-offline.js", "recipe-offline.css",
	"icon.svg", "icons.svg", "icon-192.png", "icon-512.png",
}

const serviceWorkerRuntime = `const STATIC_ASSETS=new Set(ASSETS.filter(path=>path.startsWith('/assets/')));
const PREFERENCE_CACHE='household-public-preferences-v1',LOCALE_KEY='/__household_public_locale';
self.householdLocale=async()=>{try{const response=await(await caches.open(PREFERENCE_CACHE)).match(LOCALE_KEY);const value=response?await response.text():'';return value==='en'?'en':'de';}catch{return 'de';}};
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(PRECACHE))));
self.addEventListener('message',event=>{if(event.data?.type==='ACTIVATE'){self.skipWaiting();return;}if(event.data?.type!=='SET_LOCALE'||!['de','en'].includes(event.data.locale))return;try{if(!event.source?.url||new URL(event.source.url).origin!==self.location.origin)return;}catch{return;}event.waitUntil(caches.open(PREFERENCE_CACHE).then(cache=>cache.put(LOCALE_KEY,new Response(event.data.locale,{headers:{'Content-Type':'text/plain'}}))).catch(()=>{}));});
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('household-')&&key!==CACHE&&key!==PREFERENCE_CACHE).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',event=>{const url=new URL(event.request.url);if(event.request.method!=='GET'||url.origin!==self.location.origin)return;
if(url.pathname.startsWith('/calendar/')||url.pathname.endsWith('/calendar.ics'))return;
if(STATIC_ASSETS.has(url.pathname)&&!url.search){event.respondWith((async()=>{
const cache=await caches.open(CACHE).catch(()=>null),hit=cache?await cache.match(url.pathname).catch(()=>null):null;if(hit)return hit;
const response=await fetch(event.request);
if(cache&&response.status===200&&response.type==='basic'&&!response.redirected)await cache.put(url.pathname,response.clone()).catch(()=>{});
return response;
})());return;}
if(event.request.mode==='navigate'){const fallback=['/offline/scans','/m/pantry/scans'].includes(url.pathname)?'/offline/scans':url.pathname==='/offline/shopping'||url.pathname.startsWith('/m/shopping/')?'/offline/shopping':url.pathname==='/offline/recipes'||url.pathname.startsWith('/m/recipes/')?'/offline/recipes':'/offline';event.respondWith(fetch(event.request).catch(async()=>{const chosen=url.searchParams.get('lang'),locale=['de','en'].includes(chosen)?chosen:await self.householdLocale();return(await caches.open(CACHE)).match(fallback+'?lang='+locale);}));}});
`

// pushCategoryMessages contains only authored privacy-safe text. No record data
// or message payload can supply the displayed text or a navigation destination.
func pushCategoryMessages(locale string) map[string]string {
	return map[string]string{
		"chores_due":        i18n.Text(locale, "Eine Aufgabe ist fällig. Household öffnen."),
		"chores_digest":     i18n.Text(locale, "Deine Aufgabenübersicht ist verfügbar. Household öffnen."),
		"chores_escalation": i18n.Text(locale, "Eine Aufgabenerinnerung ist verfügbar. Household öffnen."),
		"cooking_timer":     i18n.Text(locale, "Ein Kochtimer ist abgelaufen. Household öffnen."),
		"shopping":          i18n.Text(locale, "Änderungen im Einkauf. Household öffnen."),
		"pantry":            i18n.Text(locale, "Ein Vorratshinweis ist verfügbar. Household öffnen."),
		"fuel":              i18n.Text(locale, "Ein Fahrzeughinweis ist verfügbar. Household öffnen."),
		"expenses":          i18n.Text(locale, "Ein Finanzhinweis ist verfügbar. Household öffnen."),
		"meters":            i18n.Text(locale, "Ein Zählerhinweis ist verfügbar. Household öffnen."),
		"documents":         i18n.Text(locale, "Ein Dokumenthinweis ist verfügbar. Household öffnen."),
		"mealplan":          i18n.Text(locale, "Ein Hinweis zum Essensplan ist verfügbar. Household öffnen."),
		"security":          i18n.Text(locale, "Ein Sicherheitshinweis für dein Konto ist verfügbar. Household öffnen."),
		"test":              i18n.Text(locale, "Testnachricht von Household. Bitte bestätige in der App, wenn du sie siehst."),
	}
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	precache := []string{}
	for _, shell := range []string{"/offline", "/offline/scans", "/offline/shopping", "/offline/recipes"} {
		for _, locale := range []string{"de", "en"} {
			precache = append(precache, shell+"?lang="+locale)
		}
	}
	list := append([]string(nil), precache...)
	for _, url := range s.assets {
		list = append(list, url)
	}
	for _, name := range offlineShellAssets {
		url, ok := s.assets[name]
		if !ok {
			http.Error(w, "Service Worker unavailable", http.StatusInternalServerError)
			return
		}
		precache = append(precache, url)
	}
	pushSource, err := files.ReadFile("static/push-worker.js")
	if err != nil {
		http.Error(w, "Service Worker unavailable", http.StatusInternalServerError)
		return
	}
	sort.Strings(list)
	sort.Strings(precache)
	raw, _ := json.Marshal(list)
	initial, _ := json.Marshal(precache)
	pushMessages, _ := json.Marshal(map[string]string{"de": i18n.Text("de", "Änderungen im Einkauf. Household öffnen."), "en": i18n.Text("en", "Änderungen im Einkauf. Household öffnen.")})
	categoryMessages, _ := json.Marshal(map[string]map[string]string{"de": pushCategoryMessages("de"), "en": pushCategoryMessages("en")})
	body := fmt.Sprintf("const ASSETS=%s;\nconst PRECACHE=%s;\nconst PUSH_ICON=%q;\nconst PUSH_MESSAGES=%s;\nconst PUSH_CATEGORY_MESSAGES=%s;\n", raw, initial, s.assets["icon-192.png"], pushMessages, categoryMessages) + serviceWorkerRuntime + string(pushSource)
	// Runtime and push changes must rotate caches even if asset URLs stay stable.
	sum := sha256.Sum256([]byte(body))
	version := "household-" + hex.EncodeToString(sum[:8])
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	s.writeBytes(w, r, []byte(fmt.Sprintf("'use strict';const CACHE=%q;\n", version)+body))
}
