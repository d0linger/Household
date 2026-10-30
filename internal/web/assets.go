package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
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
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public,max-age=3600")
	if err := json.NewEncoder(w).Encode(map[string]any{"id": "/", "name": "Household", "short_name": "Household", "lang": "de-AT", "start_url": "/", "scope": "/", "display": "standalone", "background_color": "#f2f4f1", "theme_color": "#0e6b57", "icons": []map[string]string{{"src": s.assets["icon-192.png"], "sizes": "192x192", "type": "image/png", "purpose": "any maskable"}, {"src": s.assets["icon-512.png"], "sizes": "512x512", "type": "image/png", "purpose": "any maskable"}}}); err != nil {
		s.Log.Debug("manifest response interrupted", "request_id", trace(r))
	}
}
func (s *Server) offline(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Offline")
	p.View = "offline"
	s.render(w, r, p)
}

// offlinePantryScans serves the same public, data-free shell to every caller.
// Device drafts are opened only by local explicit consent, never embedded here.
func (s *Server) offlinePantryScans(w http.ResponseWriter, r *http.Request) {
	nonce, _ := r.Context().Value(nonceKey).(string)
	p := Page{Title: "Offline-Scans", View: "pantry-scans", Nonce: nonce, Asset: s.assets}
	w.Header().Set("Cache-Control", "public,max-age=0,must-revalidate")
	pantryScanCameraHeaders(w, r)
	s.render(w, r, p)
}

func (s *Server) offlineShopping(w http.ResponseWriter, r *http.Request) {
	nonce, _ := r.Context().Value(nonceKey).(string)
	p := Page{Title: "Offline-Einkaufslisten", View: "shopping-offline", Nonce: nonce, Asset: s.assets}
	w.Header().Set("Cache-Control", "public,max-age=0,must-revalidate")
	s.render(w, r, p)
}

func (s *Server) offlineRecipes(w http.ResponseWriter, r *http.Request) {
	nonce, _ := r.Context().Value(nonceKey).(string)
	p := Page{Title: "Offline-Rezepte", View: "recipe-offline", Nonce: nonce, Asset: s.assets}
	w.Header().Set("Cache-Control", "public,max-age=0,must-revalidate")
	s.render(w, r, p)
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	list := []string{"/offline", "/offline/scans", "/offline/shopping", "/offline/recipes"}
	for _, url := range s.assets {
		list = append(list, url)
	}
	sort.Strings(list)
	raw, _ := json.Marshal(list)
	sum := sha256.Sum256(raw)
	version := "household-" + hex.EncodeToString(sum[:8])
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	body := fmt.Sprintf(`'use strict';const CACHE=%q;const ASSETS=%s;
self.addEventListener('install',event=>event.waitUntil(caches.open(CACHE).then(cache=>cache.addAll(ASSETS))));
self.addEventListener('message',event=>{if(event.data?.type==='ACTIVATE')self.skipWaiting();});
self.addEventListener('activate',event=>event.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(key=>key.startsWith('household-')&&key!==CACHE).map(key=>caches.delete(key)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',event=>{const url=new URL(event.request.url);if(event.request.method!=='GET'||url.origin!==self.location.origin)return;
if(ASSETS.includes(url.pathname)&&!['/offline','/offline/scans','/offline/shopping','/offline/recipes'].includes(url.pathname)){event.respondWith(caches.open(CACHE).then(cache=>cache.match(event.request).then(hit=>hit||fetch(event.request))));return;}
if(event.request.mode==='navigate'){const fallback=['/offline/scans','/m/pantry/scans'].includes(url.pathname)?'/offline/scans':url.pathname==='/offline/shopping'||url.pathname.startsWith('/m/shopping/')?'/offline/shopping':url.pathname==='/offline/recipes'||url.pathname.startsWith('/m/recipes/')?'/offline/recipes':'/offline';event.respondWith(fetch(event.request).catch(()=>caches.open(CACHE).then(cache=>cache.match(fallback))));}});
`, version, raw)
	pushSource, err := files.ReadFile("static/push-worker.js")
	if err != nil {
		http.Error(w, "Service Worker unavailable", http.StatusInternalServerError)
		return
	}
	body += fmt.Sprintf("\nconst PUSH_ICON=%q;\n", s.assets["icon-192.png"]) + string(pushSource)
	s.writeBytes(w, r, []byte(body))
}
