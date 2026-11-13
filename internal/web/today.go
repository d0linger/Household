package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"household/internal/i18n"
	"household/internal/store"
	"household/internal/value"
)

type todayItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Meta  string `json:"meta,omitempty"`
	Read  bool   `json:"read,omitempty"`
}

type todaySection struct {
	State string      `json:"state"`
	Items []todayItem `json:"items"`
	More  bool        `json:"more"`
}

type todayResponse struct {
	Day           string       `json:"day"`
	Timezone      string       `json:"timezone"`
	Notifications todaySection `json:"notifications"`
	Cooking       todaySection `json:"cooking"`
	Meals         todaySection `json:"meals"`
	Tasks         todaySection `json:"tasks"`
}

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Heute")
	p.View = "today"
	s.render(w, r, p)
}

func newTodaySection(state string) todaySection {
	return todaySection{State: state, Items: []todayItem{}}
}

// todayAPI is a read-only, network-only overview. Each independently bounded
// section may fail without hiding the other sections or retaining old values.
func (s *Server) todayAPI(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	w.Header().Set("Cache-Control", "private, no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id, locale := identity(r), requestLocale(r)
	out := todayResponse{Notifications: newTodaySection("error"), Cooking: newTodaySection("disabled"), Meals: newTodaySection("disabled"), Tasks: newTodaySection("disabled")}
	clock, stopClock := context.WithTimeout(ctx, 2*time.Second)
	err := s.Store.Pool.QueryRow(clock, `SELECT timezone,(CURRENT_TIMESTAMP AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1`, id.HouseholdID).Scan(&out.Timezone, &out.Day)
	stopClock()
	if err != nil {
		out.Day, out.Timezone = "", ""
	}
	noticesCtx, stopNotices := context.WithTimeout(ctx, 2*time.Second)
	notices, err := s.notificationRecords(noticesCtx, id, locale, 7)
	stopNotices()
	if err == nil {
		out.Notifications = todayNotices(notices)
	}
	if _, enabled := s.Store.State("chores"); enabled {
		out.Tasks.State = "error"
		sectionCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		tasks, readErr := s.Store.ChoreTasks(sectionCtx, id, store.ChoreQuery{Scope: "mine", State: "open", Limit: 6})
		stop()
		if readErr == nil {
			out.Tasks = todayTasks(tasks, locale)
		}
	}
	if _, enabled := s.Store.State("recipes"); enabled {
		out.Cooking.State = "error"
		sectionCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		sessions, readErr := s.Store.CookingSessions(sectionCtx, id)
		stop()
		if readErr == nil {
			out.Cooking = todayCooking(sessions)
		}
	}
	if _, enabled := s.Store.State("mealplan"); enabled {
		out.Meals.State = "error"
		if out.Day != "" {
			sectionCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			meals, readErr := s.Store.TodayMeals(sectionCtx, id, out.Day, 7)
			stop()
			if readErr == nil {
				out.Meals = todayMeals(meals, out.Day, locale)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func todaySafeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.ContainsFunc(raw, unicode.IsControl) || parsed.Host != "" || parsed.Scheme != "" {
		return "/notifications"
	}
	return raw
}

func todayNotices(records []store.Record) todaySection {
	out := newTodaySection("ready")
	for _, record := range records {
		if len(out.Items) == 6 {
			out.More = true
			break
		}
		read, _ := record["read"].(bool)
		out.Items = append(out.Items, todayItem{ID: store.String(record["id"]), Title: store.String(record["title"]), URL: todaySafeURL(store.String(record["url"])), Read: read})
	}
	return out
}

func todayCooking(records []store.Record) todaySection {
	out := newTodaySection("ready")
	for _, record := range records {
		id := store.String(record["id"])
		if !value.ValidUUID(id) {
			continue
		}
		if len(out.Items) == 3 {
			out.More = true
			break
		}
		out.Items = append(out.Items, todayItem{ID: id, Title: store.String(record["title"]), URL: "/m/recipes/cooking/" + id})
	}
	return out
}

func todayMeals(meals []store.PlanningTodayMeal, day, locale string) todaySection {
	out := newTodaySection("ready")
	for _, meal := range meals {
		if !value.ValidUUID(meal.ID) || !value.ValidUUID(meal.PlanID) {
			continue
		}
		if len(out.Items) == 6 {
			out.More = true
			break
		}
		slot := map[string]string{"breakfast": "Frühstück", "lunch": "Mittagessen", "dinner": "Abendessen", "snack": "Zwischenmahlzeit"}[meal.Slot]
		meta := i18n.Text(locale, slot)
		if meal.Status == "cooked" {
			meta += " · " + i18n.Text(locale, "Gekocht")
		}
		query := url.Values{"week": {day}, "meal": {meal.ID}}
		out.Items = append(out.Items, todayItem{ID: meal.ID, Title: meal.Title, URL: "/m/mealplan/workspace/" + meal.PlanID + "?" + query.Encode(), Meta: meta})
	}
	return out
}

func todayTasks(data store.Record, locale string) todaySection {
	out := newTodaySection("ready")
	items, ok := data["items"].([]store.Record)
	location, err := time.LoadLocation(store.String(data["timezone"]))
	if !ok || err != nil {
		return newTodaySection("error")
	}
	for _, task := range items {
		occurrence, ok := task["occurrence"].(map[string]any)
		if !ok {
			continue
		}
		id := store.String(occurrence["id"])
		due, err := time.Parse(time.RFC3339Nano, store.String(occurrence["due_at"]))
		if !value.ValidUUID(id) || err != nil {
			continue
		}
		if len(out.Items) == 6 {
			out.More = true
			break
		}
		meta := i18n.Text(locale, "Heute fällig")
		if day := due.In(location).Format(time.DateOnly); day < store.String(data["today"]) {
			meta = i18n.Text(locale, "Überfällig") + " · " + day
		}
		if ready, ok := task["prerequisites_met"].(bool); ok && !ready {
			meta += " · " + i18n.Text(locale, "Voraussetzung offen")
		}
		out.Items = append(out.Items, todayItem{ID: id, Title: store.String(task["title"]), URL: "/m/chores/workspace/work/" + id, Meta: meta})
	}
	out.More = out.More || store.String(data["next_cursor"]) != ""
	return out
}
