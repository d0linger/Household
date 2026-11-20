package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"household/internal/i18n"
	"household/internal/metrics"

	"github.com/jackc/pgx/v5"
)

// maintenanceState is the web process's part of a live restore: it stops
// serving, lets running requests finish, confirms the pause to the backup
// service and resumes when the restore ended either way.
type maintenanceState struct {
	active atomic.Bool
	// admitted counts work that passed the gate and may still write: requests
	// and worker ticks. Work increments it before checking active, so once a
	// pause is visible, admitted reaching zero means nothing can still write.
	admitted atomic.Int64
	job      atomic.Value // string
	since    atomic.Int64
}

// Paused reports whether a live restore holds the app.
func (s *Server) Paused() bool { return s.maintenance.active.Load() }

// admit registers one unit of writing work unless a live restore pauses the
// app. The caller must call the returned release exactly once when admitted.
func (s *Server) admit() (func(), bool) {
	s.maintenance.admitted.Add(1)
	if s.maintenance.active.Load() {
		s.maintenance.admitted.Add(-1)
		return nil, false
	}
	return func() { s.maintenance.admitted.Add(-1) }, true
}

// AdmitWork lets background work take part in the restore drain: a worker
// tick runs only when admitted, and the pause is confirmed only after it ended.
func (s *Server) AdmitWork() (func(), bool) { return s.admit() }

// maintenanceExempt routes keep working while paused: liveness, static
// assets and the status the maintenance page polls.
func maintenanceExempt(path string) bool {
	return path == "/health/live" || strings.HasPrefix(path, "/assets/") || path == "/maintenance/status" || path == "/sw.js" || path == "/manifest.webmanifest"
}

// maintenanceGate answers every other request with a localized 503 while a
// live restore runs, and counts requests so the pause can wait for them.
func (s *Server) maintenanceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maintenanceExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		release, ok := s.admit()
		if !ok {
			metrics.Default.Inc("household_maintenance_rejections_total")
			s.maintenancePage(w, r)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

var maintenancePageTemplate = template.Must(template.New("maintenance").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="refresh" content="15"><title>{{.Title}}</title><link rel="stylesheet" href="{{.Stylesheet}}"></head>
<body><main id="main" class="public"><section class="panel narrow maintenance-page" role="status" aria-live="polite">
<h1>{{.Title}}</h1><p>{{.Body}}</p><p class="fine">{{.Hint}}</p></section></main></body></html>`))

func (s *Server) maintenancePage(w http.ResponseWriter, r *http.Request) {
	locale := requestLocale(r)
	w.Header().Set("Retry-After", "15")
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": i18n.Text(locale, "Household wird gerade aus einer Sicherung wiederhergestellt."), "code": "maintenance"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = maintenancePageTemplate.Execute(w, map[string]string{
		"Lang": i18n.LanguageTag(locale), "Stylesheet": s.assets["app.css"],
		"Title": i18n.Text(locale, "Wiederherstellung läuft"),
		"Body":  i18n.Text(locale, "Household wird gerade aus einer Sicherung wiederhergestellt. Die Seite lädt sich von selbst neu."),
		"Hint":  i18n.Text(locale, "Nach der Wiederherstellung meldest du dich erneut an."),
	})
}

// maintenanceStatus is polled by the maintenance page and the backup page.
func (s *Server) maintenanceStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"maintenance": s.maintenance.active.Load()})
}

// CheckRestoreOnce runs before the listener starts, so a web process that
// starts during a live restore never serves restored metadata before the
// restore finished its uploads. It returns only after the restore state was
// read successfully (an unreachable database during the switch is retried),
// pausing the app first when a restore is active.
func (s *Server) CheckRestoreOnce(ctx context.Context) {
	for ctx.Err() == nil {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var id string
		err := s.Pool.QueryRow(checkCtx, `SELECT id::text FROM core.backup_jobs WHERE kind='restore' AND state IN('awaiting_drain','swapping')
 ORDER BY created_at DESC LIMIT 1`).Scan(&id)
		cancel()
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return
		case err == nil:
			s.maintenance.job.Store(id)
			s.maintenance.since.Store(time.Now().Unix())
			s.maintenance.active.Store(true)
			s.Log.Warn("live restore: starting paused", "job", id)
			s.drainForRestore(ctx, id)
			return
		}
		s.Log.Warn("live restore: start-up state check failed; retrying", "error", err)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// WatchRestores runs until ctx ends and drives the pause for live restores.
func (s *Server) WatchRestores(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		s.checkRestore(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) checkRestore(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !s.maintenance.active.Load() {
		var id string
		err := s.Pool.QueryRow(checkCtx, `SELECT id::text FROM core.backup_jobs WHERE kind='restore' AND state IN('awaiting_drain','swapping')
 ORDER BY created_at DESC LIMIT 1`).Scan(&id)
		if err != nil {
			return
		}
		s.maintenance.job.Store(id)
		s.maintenance.since.Store(time.Now().Unix())
		s.maintenance.active.Store(true)
		s.Log.Warn("live restore: pausing the app", "job", id)
		s.drainForRestore(ctx, id)
		return
	}
	id, _ := s.maintenance.job.Load().(string)
	var state string
	var stale bool
	err := s.Pool.QueryRow(checkCtx, "SELECT state,coalesce(heartbeat_at,created_at)<now()-interval '10 minutes' FROM core.backup_jobs WHERE id=$1", id).Scan(&state, &stale)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The restored database does not know the job yet: keep waiting.
	case err != nil:
		// The database is being switched; its connections were ended. Drop
		// them all, so the next check and the resumed app use fresh ones.
		s.Pool.Reset()
	case state == "complete" || state == "failed" || state == "cancelled" || state == "awaiting_drain" && stale:
		// Connections opened before the switch may point at the replaced
		// database's ended sessions; start the resumed app with fresh ones.
		s.Pool.Reset()
		s.maintenance.active.Store(false)
		s.Log.Warn("live restore: resuming the app", "job", id, "state", state)
	case state == "awaiting_drain":
		// Not confirmed yet, or confirmed before a restart of this process:
		// confirm only while no admitted work is running.
		if s.maintenance.admitted.Load() == 0 {
			s.acknowledgeDrain(ctx, id)
		}
	}
}

// drainForRestore waits until admitted requests and worker ticks finished,
// then confirms the pause. Work still running after a minute is not cut
// short: the watcher confirms on a later check once it ended, and without a
// confirmation the backup service gives up and the app resumes.
func (s *Server) drainForRestore(ctx context.Context, id string) {
	deadline := time.Now().Add(60 * time.Second)
	for s.maintenance.admitted.Load() > 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if s.maintenance.admitted.Load() > 0 {
		s.Log.Warn("live restore: work still running; not confirming the pause yet", "job", id)
		return
	}
	s.acknowledgeDrain(ctx, id)
}

// HoldPresence keeps one database session open for as long as the web
// process runs, outside the request pool. The backup service treats a live
// database without any app session as a stopped web process; this session
// makes that inference sound while the app runs but has no idle connections.
func (s *Server) HoldPresence(ctx context.Context) {
	for ctx.Err() == nil {
		cfg := s.Pool.Config().ConnConfig.Copy()
		cfg.RuntimeParams["application_name"] = "household-presence"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			for ctx.Err() == nil {
				pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = conn.Ping(pingCtx)
				cancel()
				if err != nil {
					break
				}
				select {
				case <-ctx.Done():
				case <-time.After(2 * time.Second):
				}
			}
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			_ = conn.Close(closeCtx)
			cancel()
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) acknowledgeDrain(ctx context.Context, id string) {
	ackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var acknowledged bool
	if err := s.Pool.QueryRow(ackCtx, "SELECT core.acknowledge_backup_drain($1)", id).Scan(&acknowledged); err != nil {
		s.Log.Warn("live restore: pause confirmation failed", "job", id, "error", err)
	}
}
