package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"household/internal/ical"
	"household/internal/store"
)

var chorePanes = []string{"today", "areas", "series", "plan", "history", "settings"}

func (s *Server) registerChoreRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{"GET /m/chores/workspace/{pane}", "GET /m/chores/workspace/work/{id}", "GET /m/chores/workspace/series/{id}"} {
		mux.Handle(pattern, s.require(s.choreWorkspacePage))
	}
	for _, pattern := range []string{"GET /api/v1/chores/{view}", "GET /api/v1/chores/series/{id}", "GET /api/v1/chores/occurrences/{id}",
		"POST /api/v1/chores/series/preview", "POST /api/v1/chores/series", "POST /api/v1/chores/series/{id}/control",
		"POST /api/v1/chores/occurrences/{id}", "POST /api/v1/chores/occurrences/{id}/preview", "POST /api/v1/chores/areas", "POST /api/v1/chores/areas/{id}",
		"POST /api/v1/chores/absences", "POST /api/v1/chores/absences/{id}", "POST /api/v1/chores/preferences",
		"POST /api/v1/chores/calendar-feeds", "POST /api/v1/chores/calendar-feeds/{id}/revoke"} {
		mux.Handle(pattern, s.require(s.choreWorkspaceAPI))
	}
	mux.HandleFunc("GET /calendar/chores/{token}", s.choreCalendarFeed)
}

func (s *Server) choreWorkspacePage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	pane := r.PathValue("pane")
	if r.Pattern == "GET /m/chores/workspace/work/{id}" {
		pane = "work"
	}
	if r.Pattern == "GET /m/chores/workspace/series/{id}" {
		pane = "series-edit"
	}
	if !slices.Contains(chorePanes, pane) && pane != "work" && pane != "series-edit" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	initial, err := s.Store.ChorePreferences(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Aufgaben")
	p.Module, _ = s.Store.Module("chores")
	p.View = "chores-workspace"
	p.Extra = store.Record{"pane": pane, "id": r.PathValue("id"), "generation": initial["generation"]}
	s.render(w, r, p)
}

func readChoreJSON(w http.ResponseWriter, r *http.Request, into any) error {
	invalid := store.Invalid{Message: "Aufgabenentscheidung ist unvollständig, ungültig oder zu groß"}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return invalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return invalid
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 4000
	if planningJSONValue(decoder, reflect.TypeOf(into), 0, &remaining) != nil {
		return invalid
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return invalid
	}
	for _, field := range []string{"generation", "version"} {
		if _, exists := members[field]; !exists {
			return invalid
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}

func choreQuery(r *http.Request) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 10000 {
		return nil, store.Invalid{Message: "Aufgabenfilter ist zu groß oder ungültig"}
	}
	allowed := []string{"from", "through", "scope", "area", "template", "state", "search", "cursor", "limit", "include_paused", "include_archived", "day", "budget", "person", "days_back", "days_ahead"}
	for key, entries := range values {
		if !slices.Contains(allowed, key) || len(entries) != 1 {
			return nil, store.Invalid{Message: "Unbekannte oder doppelte Aufgabenfilter"}
		}
	}
	for _, key := range []string{"include_paused", "include_archived"} {
		if v := values.Get(key); v != "" && v != "true" && v != "false" {
			return nil, store.Invalid{Message: "Booleschen Aufgabenfilter prüfen"}
		}
	}
	return values, nil
}

func choreQueryInt(q url.Values, key string, fallback int) (int, error) {
	if q.Get(key) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(q.Get(key))
	if err != nil {
		return 0, store.Invalid{Message: "Zahlenfilter prüfen"}
	}
	return n, nil
}

func (s *Server) choreWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	var out store.Record
	var err error
	id, ctx := identity(r), r.Context()
	resource, key := r.PathValue("id"), r.Header.Get("Idempotency-Key")
	if r.Method == http.MethodGet {
		q, parseErr := choreQuery(r)
		if parseErr != nil {
			s.fail(w, r, parseErr)
			return
		}
		switch r.Pattern {
		case "GET /api/v1/chores/series/{id}":
			out, err = s.Store.ChoreSeries(ctx, id, resource)
		case "GET /api/v1/chores/occurrences/{id}":
			out, err = s.Store.ChoreWork(ctx, id, resource)
		default:
			switch view := r.PathValue("view"); view {
			case "tasks":
				var limit int
				limit, err = choreQueryInt(q, "limit", 50)
				if err == nil {
					out, err = s.Store.ChoreTasks(ctx, id, store.ChoreQuery{From: q.Get("from"), Through: q.Get("through"), Scope: q.Get("scope"), AreaID: q.Get("area"), TemplateID: q.Get("template"), State: q.Get("state"), Search: q.Get("search"), Cursor: q.Get("cursor"), Limit: limit, IncludePaused: q.Get("include_paused") == "true", IncludeArchived: q.Get("include_archived") == "true"})
				}
			case "areas", "series", "absences":
				out, err = s.Store.ChoreManagement(ctx, id, view, q.Get("cursor"), q.Get("search"), q.Get("include_archived") == "true")
			case "preferences":
				out, err = s.Store.ChorePreferences(ctx, id)
			case "calendar-feeds":
				out, err = s.Store.ChoreCalendarFeeds(ctx, id)
			case "plan":
				var budget *int
				if q.Get("budget") != "" {
					var n int
					n, err = choreQueryInt(q, "budget", 60)
					budget = &n
				}
				if err == nil {
					out, err = s.Store.ChoreDayPlan(ctx, id, q.Get("day"), budget)
				}
			case "history":
				out, err = s.Store.ChoreHistory(ctx, id, q.Get("from"), q.Get("through"), q.Get("person"), q.Get("area"), q.Get("cursor"))
			case "calendar.ics":
				var back, ahead int
				back, err = choreQueryInt(q, "days_back", 7)
				if err == nil {
					ahead, err = choreQueryInt(q, "days_ahead", 90)
				}
				if err == nil {
					var events []ical.TimedEvent
					selection := q.Get("scope")
					if selection == "" {
						selection = "mine"
					}
					events, err = s.Store.ChoreCalendarDownload(ctx, id, selection, back, ahead)
					if err == nil {
						s.writeChoreCalendar(w, r, events, true)
						return
					}
				}
			default:
				err = store.ErrNotFound
			}
		}
	} else {
		switch r.Pattern {
		case "POST /api/v1/chores/series/preview", "POST /api/v1/chores/series":
			var in store.ChoreSeriesInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				if strings.HasSuffix(r.Pattern, "/preview") {
					out, err = s.Store.PreviewChoreSeries(ctx, id, in)
				} else {
					out, err = s.Store.SaveChoreSeries(ctx, id, key, trace(r), in)
				}
			}
		case "POST /api/v1/chores/series/{id}/control", "POST /api/v1/chores/occurrences/{id}", "POST /api/v1/chores/occurrences/{id}/preview":
			var in store.ChoreActionInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				if strings.HasSuffix(r.Pattern, "/preview") {
					out, err = s.Store.PreviewChoreOccurrence(ctx, id, resource, in)
				} else if strings.Contains(r.Pattern, "/control") {
					out, err = s.Store.ControlChoreSeries(ctx, id, resource, key, trace(r), in)
				} else {
					out, err = s.Store.MutateChoreOccurrence(ctx, id, resource, key, trace(r), in)
				}
			}
		case "POST /api/v1/chores/areas", "POST /api/v1/chores/areas/{id}":
			var in store.ChoreAreaInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				out, err = s.Store.SaveChoreArea(ctx, id, resource, key, trace(r), in)
			}
		case "POST /api/v1/chores/absences", "POST /api/v1/chores/absences/{id}":
			var in store.ChoreAbsenceInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				out, err = s.Store.SaveChoreAbsence(ctx, id, resource, key, trace(r), in)
			}
		case "POST /api/v1/chores/preferences":
			var in store.ChorePreferencesInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				out, err = s.Store.SaveChorePreferences(ctx, id, key, trace(r), in)
			}
		case "POST /api/v1/chores/calendar-feeds", "POST /api/v1/chores/calendar-feeds/{id}/revoke":
			var in store.ChoreFeedInput
			err = readChoreJSON(w, r, &in)
			if err == nil {
				out, err = s.Store.MutateChoreCalendarFeed(ctx, id, resource, key, trace(r), in)
			}
		default:
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) choreCalendarFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	token := r.PathValue("token")
	if !strings.HasSuffix(token, ".ics") || r.URL.RawQuery != "" {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	events, err := s.Store.ChoreCalendarFeed(r.Context(), strings.TrimSuffix(token, ".ics"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeChoreCalendar(w, r, events, false)
}

func (s *Server) writeChoreCalendar(w http.ResponseWriter, r *http.Request, events []ical.TimedEvent, download bool) {
	raw, err := ical.MarshalTimed(events)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="household-aufgaben.ics"`)
	}
	_, _ = w.Write(raw)
}
