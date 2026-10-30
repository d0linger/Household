package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/ical"
	"household/internal/store"
	"household/internal/value"
)

func (s *Server) registerPlanningExtensionRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{
		"GET /api/v1/planning/{plan}/{extension}",
		"POST /api/v1/planning/{plan}/leftovers", "POST /api/v1/planning/{plan}/budget",
		"POST /api/v1/planning/{plan}/autoplan/preview", "POST /api/v1/planning/{plan}/autoplan/apply",
		"POST /api/v1/planning/{plan}/calendar-feeds",
		"POST /api/v1/planning/{plan}/calendar-feeds/{feed}/revoke",
	} {
		mux.Handle(pattern, s.require(s.planningExtensionsAPI))
	}
	// net/http requires a wildcard to occupy a whole segment. The handler
	// validates the literal .ics suffix without logging the secret segment.
	mux.HandleFunc("GET /calendar/mealplan/{token}", s.planningCalendarFeed)
}

func planningExtensionWeek(r *http.Request) (string, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["week"]) != 1 || !value.Date(values.Get("week")) {
		return "", store.Invalid{Message: "Genau einen gültigen Sieben-Tage-Beginn wählen"}
	}
	return values.Get("week"), nil
}

func (s *Server) planningExtensionsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	pid, key := r.PathValue("plan"), r.Header.Get("Idempotency-Key")
	var result any
	var err error
	if r.Method == http.MethodGet {
		var week string
		week, err = planningExtensionWeek(r)
		if err == nil {
			// A closed dispatch below one broad GET pattern preserves the
			// more-specific existing /planning/source/{source} route. Separate
			// suffix-literal GET patterns overlap it and make ServeMux panic.
			switch r.PathValue("extension") {
			case "leftovers":
				result, err = s.Store.PlanningLeftovers(ctx, identity(r), pid, week)
			case "budget":
				result, err = s.Store.PlanningBudget(ctx, identity(r), pid, week)
			case "calendar-feeds":
				result, err = s.Store.PlanningCalendarFeeds(ctx, identity(r), pid, week)
			case "calendar.ics":
				var calendar store.PlanningCalendar
				calendar, err = s.Store.PlanningCalendarDownload(ctx, identity(r), pid, week)
				if err == nil {
					s.writePlanningCalendar(w, r, calendar, true)
					return
				}
			default:
				err = store.ErrNotFound
			}
		}
	} else if r.URL.RawQuery != "" {
		err = store.Invalid{Message: "Planungsbefehle verwenden ausschließlich geprüfte JSON-Daten"}
	} else {
		switch r.Pattern {
		case "POST /api/v1/planning/{plan}/leftovers":
			var in store.PlanningLeftoverInput
			if err = readPlanningExtensionJSON(w, r, &in, "action"); err == nil {
				result, err = s.Store.MutatePlanningLeftovers(ctx, identity(r), pid, key, trace(r), in)
			}
		case "POST /api/v1/planning/{plan}/budget":
			var in store.PlanningBudgetInput
			if err = readPlanningExtensionJSON(w, r, &in, "settings_version", "budget_minor", "manual_estimates", "rules"); err == nil {
				result, err = s.Store.MutatePlanningBudget(ctx, identity(r), pid, key, trace(r), in)
			}
		case "POST /api/v1/planning/{plan}/autoplan/preview", "POST /api/v1/planning/{plan}/autoplan/apply":
			result, err = s.planningAutoplanCommand(ctx, w, r)
		case "POST /api/v1/planning/{plan}/calendar-feeds", "POST /api/v1/planning/{plan}/calendar-feeds/{feed}/revoke":
			var in store.PlanningCalendarInput
			if err = readPlanningExtensionJSON(w, r, &in, "action"); err == nil {
				if r.PathValue("feed") == "" && in.Action != "create" || r.PathValue("feed") != "" && in.Action != "revoke" {
					err = store.Invalid{Message: "Kalenderaktion stimmt nicht mit dem gewählten Ablauf überein"}
				} else {
					result, err = s.Store.MutatePlanningCalendarFeed(ctx, identity(r), pid, r.PathValue("feed"), key, trace(r), in)
				}
			}
		default:
			err = store.ErrNotFound
		}
	}
	var conflict store.PlanningConflict
	if errors.As(err, &conflict) {
		writeJSON(w, http.StatusConflict, store.Record{"error": conflict.Error(), "request_id": trace(r)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writePlanningCalendar(w http.ResponseWriter, r *http.Request, calendar store.PlanningCalendar, download bool) {
	raw, err := ical.Marshal(calendar.Events)
	if err != nil {
		s.Log.Error("calendar encoding failed", "request_id", trace(r))
		http.Error(w, "Kalender vorübergehend nicht verfügbar", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="household-essensplanung.ics"`)
	}
	s.writeBytes(w, r, raw)
}

func (s *Server) planningCalendarFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	token := r.PathValue("token")
	if r.URL.RawQuery != "" || !strings.HasSuffix(token, ".ics") || len(strings.TrimSuffix(token, ".ics")) != 43 {
		http.Error(w, "Nicht gefunden", http.StatusNotFound)
		return
	}
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.Auth.Limit(ctx, "mealplan-calendar-feed", s.clientIP(r), 120, time.Minute); err != nil {
		if errors.Is(err, auth.ErrLimited) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Kalenderabruf später erneut versuchen", http.StatusTooManyRequests)
		} else {
			http.Error(w, "Kalender vorübergehend nicht verfügbar", http.StatusServiceUnavailable)
		}
		return
	}
	calendar, err := s.Store.PlanningCalendarFeed(ctx, strings.TrimSuffix(token, ".ics"))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		http.Error(w, "Nicht gefunden", http.StatusNotFound)
		return
	}
	if err != nil {
		// No error rendering, URL echo or token-bearing diagnostic on this
		// public capability route, including failed conditional requests.
		s.Log.Error("calendar feed unavailable", "request_id", trace(r))
		http.Error(w, "Kalender vorübergehend nicht verfügbar", http.StatusServiceUnavailable)
		return
	}
	s.writePlanningCalendar(w, r, calendar, false)
}
