// Package web serves the embedded PWA and its authenticated API.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/config"
	"household/internal/database"
	"household/internal/module"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates/*.html static/*
var files embed.FS

type requestKey int

const identityKey requestKey = 0
const nonceKey requestKey = 1
const csrfKey requestKey = 2
const traceKey requestKey = 3

type Server struct {
	Cfg        config.Config
	Pool       *pgxpool.Pool
	Auth       *auth.Service
	Store      *store.Store
	Log        *slog.Logger
	templates  *template.Template
	assets     map[string]string
	shutting   atomic.Bool
	imageSlots chan struct{}
}
type Page struct {
	Title      string
	User       auth.Identity
	CSRF       string
	Nonce      string
	Asset      map[string]string
	Modules    []module.Descriptor
	States     map[string]store.State
	Module     module.Descriptor
	Entity     module.Entity
	Records    []store.Record
	Record     store.Record
	References map[string][]store.Record
	Error      string
	Success    string
	View       string
	Key        string
	Page       int
	Query      string
	Extra      any
	Related    []Related
}
type Related struct {
	Entity  module.Entity
	Field   string
	Records []store.Record
}

func New(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, mods []module.Descriptor, log *slog.Logger) (*Server, error) {
	if err := module.ValidateRegistry(mods); err != nil {
		return nil, err
	}
	a, err := auth.New(pool, cfg)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	enabled := map[string]bool{}
	for _, m := range mods {
		ids = append(ids, m.ID)
		enabled[m.ID] = m.DefaultEnabled
	}
	if err = a.Bootstrap(ctx, ids, enabled); err != nil {
		return nil, err
	}
	s := &Server{Cfg: cfg, Pool: pool, Auth: a, Store: store.New(pool, mods), Log: log, assets: map[string]string{}, imageSlots: make(chan struct{}, 2)}
	if err = s.Store.LoadStates(ctx); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(files, "static")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		b, err := files.ReadFile("static/" + entry.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		s.assets[entry.Name()] = "/assets/" + hex.EncodeToString(sum[:6]) + "/" + entry.Name()
	}
	s.templates, err = template.New("household").Funcs(template.FuncMap{"text": store.String, "qty": formatQuantity, "choice": formatChoice, "prev": func(v any) int64 { return store.Int(v) - 1 }, "next": func(v any) int64 { return store.Int(v) + 1 }, "integer": store.Int, "money": formatMoney, "formValue": formValue, "isTrue": func(v any) bool { b, _ := v.(bool); return b }, "selected": func(a, b any) bool { return store.String(a) == store.String(b) }, "enabled": func(states map[string]store.State, id string) bool { return states[id].State == "enabled" }, "moduleURL": func(m module.Descriptor) string {
		if m.ID == "pantry" {
			return "/m/pantry/stock"
		}
		return "/m/" + m.ID + "/" + m.Entities[0].ID
	}, "dict": dict, "icon": func(asset map[string]string, name string) string { return asset["icons.svg"] + "#" + name }}).ParseFS(files, "templates/*.html")
	return s, err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"alive": true}) })
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /assets/{hash}/{name}", s.asset)
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	mux.HandleFunc("GET /offline", s.offline)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.Handle("GET /{$}", s.require(s.home))
	mux.Handle("GET /change-password", s.require(s.passwordPage))
	mux.Handle("POST /change-password", s.require(s.password))
	mux.Handle("POST /logout", s.require(s.logout))
	mux.Handle("GET /settings", s.require(s.settings))
	mux.Handle("GET /settings/modules", s.require(s.settings))
	mux.Handle("POST /settings/modules/{module}", s.require(s.setModule))
	mux.Handle("GET /settings/security", s.require(s.securityPage))
	mux.Handle("POST /settings/security/totp/setup", s.require(s.setupTOTP))
	mux.Handle("POST /settings/security/totp/enable", s.require(s.enableTOTP))
	mux.Handle("POST /settings/security/sessions/{id}/revoke", s.require(s.revokeSession))
	mux.Handle("GET /settings/users", s.require(s.usersPage))
	mux.Handle("GET /settings/audit", s.require(s.auditPage))
	mux.Handle("GET /api/v1/core/audit", s.require(s.auditAPI))
	mux.Handle("POST /settings/users", s.require(s.createUser))
	mux.Handle("POST /settings/users/{id}", s.require(s.updateUser))
	mux.Handle("POST /settings/modules/{module}/purge", s.require(s.requestPurge))
	mux.Handle("GET /search", s.require(s.searchPage))
	mux.Handle("GET /notifications", s.require(s.notifications))
	mux.Handle("POST /notifications/{id}/read", s.require(s.readNotification))
	mux.Handle("POST /notifications/{id}/dismiss", s.require(s.dismissNotification))
	mux.Handle("GET /api/v1/core/bootstrap", s.require(s.bootstrap))
	mux.Handle("GET /m/recipes/new", s.require(s.recipeWorkspacePage))
	mux.Handle("GET /m/recipes/recipes/{id}/workspace", s.require(s.recipeWorkspacePage))
	mux.Handle("POST /api/v1/recipes/workspace", s.require(s.recipeWorkspace))
	mux.Handle("GET /api/v1/recipes/{id}/workspace", s.require(s.recipeWorkspace))
	mux.Handle("POST /api/v1/recipes/{id}/workspace", s.require(s.recipeWorkspace))
	mux.Handle("GET /api/v1/recipes/{id}/versions", s.require(s.recipeVersions))
	mux.Handle("GET /api/v1/recipes/{id}/versions/{version}", s.require(s.recipeVersions))
	mux.Handle("GET /m/recipes/cooking", s.require(s.cookingPage))
	mux.Handle("GET /m/recipes/cook/{source}", s.require(s.cookingPage))
	mux.Handle("GET /m/recipes/cooking/{session}", s.require(s.cookingPage))
	mux.Handle("GET /m/recipes/cooking/history/{history}", s.require(s.cookingPage))
	mux.Handle("GET /api/v1/cooking", s.require(s.cookingAPI))
	mux.Handle("POST /api/v1/cooking", s.require(s.cookingAPI))
	mux.Handle("GET /api/v1/cooking/source/{source}", s.require(s.cookingAPI))
	mux.Handle("GET /api/v1/cooking/history", s.require(s.cookingAPI))
	mux.Handle("GET /api/v1/cooking/history/{history}", s.require(s.cookingAPI))
	mux.Handle("GET /api/v1/cooking/{session}", s.require(s.cookingAPI))
	mux.Handle("POST /api/v1/cooking/{session}", s.require(s.cookingAPI))
	mux.Handle("GET /m/mealplan/workspace/{plan}", s.require(s.planningPage))
	mux.Handle("GET /api/v1/planning/source/{source}", s.require(s.planningAPI))
	mux.Handle("GET /api/v1/planning/{plan}", s.require(s.planningAPI))
	mux.Handle("POST /api/v1/planning/{plan}", s.require(s.planningAPI))
	mux.Handle("GET /api/v1/planning/{plan}/meals/{meal}", s.require(s.planningAPI))
	mux.Handle("GET /api/v1/planning/{plan}/meals/{meal}/components/{component}", s.require(s.planningAPI))
	mux.Handle("GET /api/v1/planning/{plan}/meals/{meal}/components/{component}/source", s.require(s.planningAPI))
	mux.Handle("GET /api/v1/core/search", s.require(s.searchAPI))
	mux.Handle("GET /api/v1/core/references/{module}/{entity}", s.require(s.referencesAPI))
	mux.Handle("GET /api/v1/core/reference-options/{module}/{entity}", s.require(s.referenceOptions))
	mux.Handle("GET /api/v1/core/reference-options/{module}/{entity}/{id}", s.require(s.referenceOptions))
	mux.Handle("GET /api/v1/shopping/lists/{id}/items", s.require(s.shoppingItems))
	mux.Handle("GET /m/shopping/lists/{id}/checkout", s.require(s.shoppingCheckoutPage))
	mux.Handle("GET /api/v1/shopping/lists/{id}/checkout", s.require(s.shoppingCheckout))
	mux.Handle("POST /api/v1/shopping/lists/{id}/checkout", s.require(s.shoppingCheckout))
	mux.Handle("GET /api/v1/shopping/items/{id}", s.require(s.shoppingItem))
	mux.Handle("GET /api/v1/shopping/items/{id}/merge-preview", s.require(s.shoppingMergePreview))
	mux.Handle("GET /m/pantry/stock", s.require(s.pantryPage))
	mux.Handle("GET /api/v1/pantry/stock", s.require(s.pantryStock))
	mux.Handle("GET /api/v1/pantry/products/{id}/lots", s.require(s.pantryLots))
	mux.Handle("POST /api/v1/core/preferences", s.require(s.preferences))
	mux.Handle("GET /m/{module}/{entity}", s.require(s.modulePage))
	mux.Handle("GET /m/{module}/reports", s.require(s.reportPage))
	mux.Handle("GET /m/{module}/{entity}/{id}", s.require(s.detailPage))
	mux.Handle("GET /api/v1/m/{module}/{entity}", s.require(s.listRecords))
	mux.Handle("POST /api/v1/m/{module}/{entity}", s.require(s.createRecord))
	mux.Handle("GET /api/v1/m/{module}/{entity}/{id}", s.require(s.getRecord))
	mux.Handle("PATCH /api/v1/m/{module}/{entity}/{id}", s.require(s.updateRecord))
	mux.Handle("DELETE /api/v1/m/{module}/{entity}/{id}", s.require(s.deleteRecord))
	mux.Handle("POST /api/v1/m/{module}/{entity}/{id}/actions/{action}", s.require(s.actionRecord))
	mux.Handle("GET /api/v1/m/{module}/reports/summary", s.require(s.report))
	mux.Handle("GET /api/v1/m/{module}/{entity}/export.csv", s.require(s.exportCSV))
	mux.Handle("POST /api/v1/m/{module}/{entity}/import", s.require(s.importCSV))
	mux.Handle("POST /api/v1/core/imports/{module}/{entity}/{id}/confirm", s.require(s.confirmImport))
	mux.Handle("GET /api/v1/m/{module}/{entity}/{id}/attachments", s.require(s.attachmentList))
	mux.Handle("POST /api/v1/m/{module}/{entity}/{id}/attachments", s.require(s.upload))
	mux.Handle("GET /attachments/{id}", s.require(s.download))
	mux.Handle("GET /attachments/{id}/preview", s.require(s.download))
	mux.Handle("GET /capture/{id}", s.require(s.capturePage))
	mux.Handle("POST /capture/{id}/confirm", s.require(s.confirmCapture))
	return s.middleware(mux)
}

func (s *Server) Stop() { s.shutting.Store(true) }
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.shutting.Load() || database.Ready(ctx, s.Pool) != nil {
		writeJSON(w, 503, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready", "version": "0.1.0"})
}
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.MetricsToken == "" || !hmac.Equal([]byte(s.Cfg.MetricsToken), []byte(r.Header.Get("X-Metrics-Token"))) {
		http.NotFound(w, r)
		return
	}
	stats := s.Pool.Stat()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.writeBytes(w, r, []byte(fmt.Sprintf("household_db_acquired_connections %d\nhousehold_db_total_connections %d\n", stats.AcquiredConns(), stats.TotalConns())))
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := value.UUID()
		nonce := auth.RandomToken()
		ctx := context.WithValue(r.Context(), nonceKey, nonce)
		ctx = context.WithValue(ctx, traceKey, requestID)
		ctx = audit.WithRequest(ctx, requestID)
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=(), payment=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'self'; img-src 'self' blob:; font-src 'self'; connect-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if s.Cfg.Production {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		recorder := &response{ResponseWriter: w, status: 200}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.Log.Error("request panic", "request_id", requestID)
				http.Error(recorder, "Interner Fehler", 500)
			}
			s.Log.Info("http request", "request_id", requestID, "method", r.Method, "route", r.Pattern, "status", recorder.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		if !strings.HasPrefix(r.URL.Path, "/health/") {
			origin, _ := url.Parse(s.Cfg.Origin)
			if r.Host != origin.Host {
				http.Error(recorder, "Unbekannter Host", http.StatusBadRequest)
				return
			}
			if s.Cfg.Production && !s.isHTTPS(r) {
				http.Error(recorder, "HTTPS erforderlich", http.StatusBadRequest)
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != s.Cfg.Origin {
				http.Error(recorder, "Ungültiger Ursprung", http.StatusForbidden)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
				http.Error(recorder, "Ungültige Anfrage", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(recorder, r)
	})
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(status int) { w.status = status; w.ResponseWriter.WriteHeader(status) }

func (s *Server) trusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, network := range s.Cfg.TrustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || s.trusted(r) && r.Header.Get("X-Forwarded-Proto") == "https"
}
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	if !s.trusted(r) {
		return host
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for j := len(parts) - 1; j >= 0; j-- {
		ip := net.ParseIP(strings.TrimSpace(parts[j]))
		if ip == nil {
			continue
		}
		trusted := false
		for _, n := range s.Cfg.TrustedProxies {
			if n.Contains(ip) {
				trusted = true
				break
			}
		}
		if !trusted {
			return ip.String()
		}
	}
	return host
}
func (s *Server) cookieName() string {
	if s.Cfg.Production {
		return "__Host-household"
	}
	return "household_dev"
}
func (s *Server) setCookie(w http.ResponseWriter, raw string, id auth.Identity) {
	http.SetCookie(w, s.cookie(s.cookieName(), raw, max(1, int(time.Until(id.ExpiresAt).Seconds())), id.ExpiresAt))
}
func (s *Server) cookie(name, raw string, age int, expires time.Time) *http.Cookie {
	c := &http.Cookie{Name: name, Value: raw, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age, Expires: expires} // #nosec G124 -- production is always Secure; only explicit HTTP development relaxes it below
	// security: HTTP cookies are allowed solely in explicitly selected local development.
	if !s.Cfg.Production && !strings.HasPrefix(s.Cfg.Origin, "https://") {
		c.Secure = false
	}
	return c
}
func (s *Server) writeBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	if _, err := w.Write(data); err != nil {
		s.Log.Debug("response interrupted", "request_id", trace(r))
	}
}
func identity(r *http.Request) auth.Identity {
	id, _ := r.Context().Value(identityKey).(auth.Identity)
	return id
}
func trace(r *http.Request) string { s, _ := r.Context().Value(traceKey).(string); return s }

func (s *Server) require(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cookieName())
		if err != nil {
			s.authRequired(w, r)
			return
		}
		id, err := s.Auth.Resolve(r.Context(), cookie.Value, r.Method == "GET" && !strings.HasPrefix(r.URL.Path, "/api/"))
		if errors.Is(err, auth.ErrSession) {
			s.authRequired(w, r)
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.Store.Gate.RLock()
		stateErr := s.Store.LoadStates(r.Context())
		s.Store.Gate.RUnlock()
		if stateErr != nil {
			s.fail(w, r, stateErr)
			return
		}
		if id.Renewed {
			s.setCookie(w, cookie.Value, id)
		}
		ctx := context.WithValue(r.Context(), identityKey, id)
		ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
		ctx = context.WithValue(ctx, csrfKey, s.Auth.CSRF(cookie.Value, "session"))
		r = r.WithContext(ctx)
		if id.MustChange && r.URL.Path != "/change-password" && r.URL.Path != "/logout" {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, 403, map[string]string{"error": "Passwortwechsel erforderlich"})
			} else {
				http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			}
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			token := r.Header.Get("X-CSRF-Token")
			if token == "" && !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				if err = r.ParseMultipartForm(2 << 20); err != nil && r.MultipartForm == nil {
					if err = r.ParseForm(); err != nil {
						s.fail(w, r, store.Invalid{Message: "ungültiges Formular"})
						return
					}
				}
				token = r.FormValue("csrf")
			}
			if !s.Auth.ValidCSRF(cookie.Value, "session", token) {
				s.fail(w, r, store.ErrForbidden)
				return
			}
		}
		next(w, r)
	})
}

func (s *Server) authRequired(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, 401, map[string]string{"error": "Anmeldung erforderlich"})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (s *Server) page(r *http.Request, title string) Page {
	nonce, _ := r.Context().Value(nonceKey).(string)
	csrf, _ := r.Context().Value(csrfKey).(string)
	return Page{Title: title, User: identity(r), CSRF: csrf, Nonce: nonce, Asset: s.assets, Modules: s.Store.Modules, States: s.Store.States(), Records: []store.Record{}, References: map[string][]store.Record{}, Key: value.UUID()}
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, p Page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "page", p); err != nil {
		s.Log.Error("render response", "request_id", trace(r), "error", err)
	}
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, message := 500, "Vorgang fehlgeschlagen; bitte erneut versuchen"
	var invalid store.Invalid
	var pgerr *pgconn.PgError
	switch {
	case errors.As(err, &invalid):
		status, message = 422, invalid.Message
	case errors.Is(err, store.ErrNotFound):
		status, message = 404, "Nicht gefunden"
	case errors.Is(err, store.ErrConflict):
		status, message = 409, store.ErrConflict.Error()
	case errors.As(err, &pgerr) && (pgerr.Code == "55P03" || pgerr.Code == "40P01"):
		status, message = 409, "Gleichzeitige Änderung; bitte aktuelle Werte laden und prüfen"
	case errors.Is(err, store.ErrForbidden):
		status, message = 403, "Keine Berechtigung"
	case errors.Is(err, auth.ErrLimited):
		status, message = 429, auth.ErrLimited.Error()
		w.Header().Set("Retry-After", "900")
	case errors.Is(err, auth.ErrCredentials):
		status, message = 401, auth.ErrCredentials.Error()
	case errors.As(err, &pgerr) && (pgerr.Code == "23503" || pgerr.Code == "23505" || pgerr.Code == "23514" || pgerr.Code == "23502" || pgerr.Code == "22003" || pgerr.Code == "22P02"):
		status, message = 409, "Daten widersprechen bestehenden Einträgen oder einer Integritätsregel"
	}
	if status == 500 {
		s.Log.Error("request failed", "request_id", trace(r), "error", err)
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, status, map[string]string{"error": message, "request_id": trace(r)})
		return
	}
	p := s.page(r, "Vorgang prüfen")
	p.View = "error"
	p.Error = message
	w.WriteHeader(status)
	s.render(w, r, p)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return store.Invalid{Message: "ungültige JSON-Daten"}
	}
	var tail any
	if err := d.Decode(&tail); !errors.Is(err, io.EOF) {
		return store.Invalid{Message: "mehrere oder ungültige JSON-Dokumente nicht erlaubt"}
	}
	return nil
}
func formatMoney(v any) string {
	n := store.Int(v)
	sign := ""
	if n < 0 {
		sign = "−"
		n = -n
	}
	return fmt.Sprintf("%s%d,%02d €", sign, n/100, n%100)
}
func formValue(f module.Field, v any) string {
	if v == nil {
		return f.Default
	}
	if f.Kind == "money" {
		if raw, ok := v.(string); ok && (strings.Contains(raw, ",") || strings.Contains(raw, ".")) {
			return raw
		}
		n := store.Int(v)
		sign := ""
		if n < 0 {
			sign = "-"
		}
		n = abs(n)
		return sign + strconv.FormatInt(n/100, 10) + "," + fmt.Sprintf("%02d", n%100)
	}
	return store.String(v)
}

func formatChoice(v any) string {
	raw := store.String(v)
	labels := map[string]string{"piece": "Stück", "available": "Verfügbar", "unavailable": "Nicht verfügbar", "substituted": "Ersatz gewählt", "diesel": "Diesel", "petrol": "Benzin", "lpg": "LPG", "cng": "CNG", "receive": "Zugang", "consume": "Verbrauch", "waste": "Entsorgung", "adjust": "Korrektur", "expense": "Ausgabe", "refund": "Erstattung", "dinner": "Abendessen", "lunch": "Mittagessen", "breakfast": "Frühstück", "snack": "Zwischenmahlzeit", "actual": "Abgelesen", "estimated": "Geschätzt"}
	if label, ok := labels[raw]; ok {
		return label
	}
	return raw
}
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
func dict(values ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(values); i += 2 {
		m[fmt.Sprint(values[i])] = values[i+1]
	}
	return m
}

func formatQuantity(v any) string {
	raw := store.String(v)
	r, err := value.Decimal(raw)
	if err != nil {
		return raw
	}
	return strings.ReplaceAll(strings.TrimRight(strings.TrimRight(r.FloatString(6), "0"), "."), ".", ",")
}
