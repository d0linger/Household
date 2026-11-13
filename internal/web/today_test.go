package web

import (
	"net/url"
	"strings"
	"testing"

	"household/internal/store"
)

func TestTodayProjectionBoundsAndSafeLinks(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"https://evil.invalid", "//evil.invalid/x", "/\\evil.invalid", "javascript:alert(1)", "/path\nLocation: evil", "", "/%zz"} {
		if got := todaySafeURL(raw); got != "/notifications" {
			t.Errorf("unsafe path %q accepted as %q", raw, got)
		}
	}
	const good = "/m/mealplan/workspace/00000000-0000-4000-8000-000000000001?week=2026-11-11"
	if todaySafeURL(good) != good {
		t.Fatal("internal module destination lost")
	}
	records := make([]store.Record, 20)
	for i := range records {
		records[i] = store.Record{"id": "00000000-0000-4000-8000-000000000001", "title": "<script>not markup</script>", "url": "//evil.invalid", "read": true}
	}
	notices := todayNotices(records)
	if notices.State != "ready" || len(notices.Items) != 6 || !notices.More || !notices.Items[0].Read || notices.Items[0].URL != "/notifications" {
		t.Fatalf("notification projection: %#v", notices)
	}
	if notices.Items[0].Title != "<script>not markup</script>" {
		t.Fatal("title must remain plain data for textContent, not pre-escaped markup")
	}
	if cooking := todayCooking(records); len(cooking.Items) != 3 || !cooking.More {
		t.Fatalf("cooking bound: %#v", cooking)
	}
	if cooking := todayCooking([]store.Record{{"id": "../../settings", "title": "invalid"}}); len(cooking.Items) != 0 || cooking.Items == nil {
		t.Fatal("invalid session identifier exposed or empty JSON is null")
	}
}

func TestTodayMealProjectionUsesSavedIdentityAndDay(t *testing.T) {
	t.Parallel()
	meal := store.PlanningTodayMeal{ID: "00000000-0000-4000-8000-000000000001", PlanID: "00000000-0000-4000-8000-000000000002", Title: "Dinner", Slot: "dinner", Status: "cooked"}
	items := make([]store.PlanningTodayMeal, 9)
	for i := range items {
		items[i] = meal
	}
	out := todayMeals(items, "2026-10-25", "de")
	if len(out.Items) != 6 || !out.More || out.Items[0].Meta != "Abendessen · Gekocht" {
		t.Fatalf("meal projection: %#v", out)
	}
	path, err := url.Parse(out.Items[0].URL)
	if err != nil || path.Query().Get("week") != "2026-10-25" || path.Query().Get("meal") != meal.ID || !strings.HasSuffix(path.Path, meal.PlanID) {
		t.Fatal("meal route lost saved plan, meal, or household day", path, err)
	}
	meal.PlanID = "../other"
	if out := todayMeals([]store.PlanningTodayMeal{meal}, "2026-10-25", "de"); len(out.Items) != 0 {
		t.Fatal("invalid plan identity exposed")
	}
}

func TestTodayTasksUsesHouseholdCivilDayAndBoundedOwnedContract(t *testing.T) {
	t.Parallel()
	const taskID = "00000000-0000-4000-8000-000000000001"
	data := store.Record{"today": "2026-03-29", "timezone": "Europe/Vienna", "next_cursor": "more", "items": []store.Record{
		{"title": "Late yesterday", "occurrence": map[string]any{"id": taskID, "due_at": "2026-03-28T21:00:00Z"}, "prerequisites_met": false},
		{"title": "Today across UTC midnight", "occurrence": map[string]any{"id": taskID, "due_at": "2026-03-28T23:30:00Z"}, "prerequisites_met": true},
	}}
	out := todayTasks(data, "de")
	if len(out.Items) != 2 || !out.More || out.Items[0].Meta != "Überfällig · 2026-03-28 · Voraussetzung offen" || out.Items[1].Meta != "Heute fällig" {
		t.Fatalf("household due-date projection: %#v", out)
	}
	if out.Items[0].URL != "/m/chores/workspace/work/"+taskID {
		t.Fatal("task did not retain its authorized work identity")
	}
	if todayTasks(store.Record{}, "de").State != "error" {
		t.Fatal("invalid task response presented as empty success")
	}
	items := make([]store.Record, 9)
	for i := range items {
		items[i] = data["items"].([]store.Record)[0]
	}
	data["items"] = items
	if out = todayTasks(data, "de"); len(out.Items) != 6 || !out.More {
		t.Fatal("unbounded task preview", out)
	}
}
