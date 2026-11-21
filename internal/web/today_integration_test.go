//go:build integration

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"household/internal/chores"
	"household/internal/store"
	"household/internal/value"
)

func TestTodayPersonalAuthorityAndReadOnlyHome(t *testing.T) {
	f := newIntegrationFixture(t)
	reader := shoppingTestMember(t, f, "todayreader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.9", "today")
	if err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["chores"]
	if state.State != "enabled" {
		if err = f.app.Store.SetModule(f.ctx, f.identity, "chores", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["chores"]
	}
	var day string
	if err = f.owner.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	series := store.ChoreSeriesInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation}, Title: "Personal due task", Description: "PRIVATE-INSTRUCTIONS-EXCLUDED", Schedule: chores.Schedule{Kind: "once", StartsOn: day, Timezone: "Europe/Vienna"}, AssignmentKind: "fixed", AssigneeID: f.identity.MembershipID}
	preview, err := f.app.Store.PreviewChoreSeries(f.ctx, f.identity, series)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := json.Marshal(preview["normalized"])
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(normalized, &series); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SaveChoreSeries(f.ctx, f.identity, value.UUID(), value.UUID(), series); err != nil {
		t.Fatal(err)
	}
	// The owner captures two historical completions with opposite due and completion days.
	templateID, completedToday, completedEarlier := value.UUID(), value.UUID(), value.UUID()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.task_templates(id,household_id,title,created_by)
 VALUES($1,$2,'Completion dates',$3)`, templateID, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.task_occurrences(id,household_id,template_id,occurrence_key,due_at,completed_at,state,assignee_id,created_by)
 SELECT dates.id,h.id,$2,dates.id::text,(dates.due+time '12:00') AT TIME ZONE h.timezone,
 (dates.done+time '12:00') AT TIME ZONE h.timezone,'completed',$3,$4
 FROM core.households h CROSS JOIN (VALUES ($5::uuid,$7::date-1,$7::date),($6::uuid,$7::date,$7::date-1)) AS dates(id,due,done)
 WHERE h.id=$1`, f.identity.HouseholdID, templateID, f.identity.MembershipID, f.identity.UserID, completedToday, completedEarlier, day); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.completions(household_id,occurrence_id,template_id,template_version_id,completed_at,snapshot,legacy_capture,created_by)
 SELECT o.household_id,o.id,o.template_id,d.template_version_id,o.completed_at,'{}',true,o.created_by
 FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id WHERE o.template_id=$1`, templateID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	due, err := f.app.Store.ChoreTasks(f.ctx, f.identity, store.ChoreQuery{State: "completed", From: day, Through: day})
	if err != nil {
		t.Fatal(err)
	}
	if items := todayTasks(due, "en").Items; len(items) != 1 || items[0].ID != completedEarlier {
		t.Fatal("the task workspace must retain its due-date filter", items)
	}
	for _, row := range []struct{ user, module, title, key string }{
		{f.identity.UserID, "", "Personal today notice", "today-admin"},
		{reader.UserID, "", "Other account notice", "today-reader"},
		{f.identity.UserID, "expenses", "Private finance title", "today-finance"},
	} {
		if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 VALUES($1,$2,nullif($3,''),'legacy_notice',$4,'/notifications',$5)`, f.identity.HouseholdID, row.user, row.module, row.title, row.key); err != nil {
			t.Fatal(err)
		}
	}
	// Newer Shopping candidates without a current authorized source must not
	// consume Today's visible-result limit and hide an older valid own notice.
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.notifications SET created_at=clock_timestamp()-interval '1 hour' WHERE household_id=$1 AND dedupe_key='today-admin'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 VALUES($1,$2,'shopping','shopping_list_changed_v1','INACCESSIBLE-SHOPPING-TITLE','/notifications',$3)`, f.identity.HouseholdID, f.identity.UserID, "today-invalid-"+value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ token, want, forbidden string }{{f.token, "Personal today notice", "Other account notice"}, {readerToken, "Other account notice", "Personal today notice"}} {
		r, raw, requestErr := f.request(http.MethodGet, "/api/v1/core/today", tc.token, "", "", nil, nil)
		var data todayResponse
		if requestErr != nil || r.StatusCode != 200 || json.Unmarshal(raw, &data) != nil {
			t.Fatalf("Today read: %v %v %s", r, requestErr, raw)
		}
		if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") || data.Day == "" || data.Timezone == "" {
			t.Fatal("missing private cache or civil-day contract", r.Header, data)
		}
		if !strings.Contains(string(raw), tc.want) || strings.Contains(string(raw), tc.forbidden) || strings.Contains(string(raw), "Private finance title") || strings.Contains(string(raw), "INACCESSIBLE-SHOPPING-TITLE") {
			t.Fatal("personal/finance boundary", string(raw))
		}
		if data.Tasks.State != "ready" || strings.Contains(string(raw), "PRIVATE-INSTRUCTIONS-EXCLUDED") {
			t.Fatal("task summary state/privacy", string(raw))
		}
		if tc.token == f.token && (len(data.Tasks.Items) != 1 || data.Tasks.Items[0].Title != "Personal due task") {
			t.Fatal("personal due task missing", data.Tasks)
		}
		if tc.token == readerToken && len(data.Tasks.Items) != 0 {
			t.Fatal("another member task in mine", data.Tasks)
		}
		if tc.token == f.token && (len(data.Completed.Items) != 1 || data.Completed.Items[0].ID != completedToday || !data.Completed.Items[0].Read) {
			t.Fatal("Today must select the actual completion day", data.Completed)
		}
		if tc.token == readerToken && len(data.Completed.Items) != 0 {
			t.Fatal("another member's completion in Today", data.Completed)
		}
		for module, section := range map[string]todaySection{"shopping": data.Shopping, "expenses": data.Finances} {
			_, enabled := f.app.Store.State(module)
			if !enabled && (section.State != "disabled" || len(section.Items) != 0) || enabled && section.State != "ready" {
				t.Fatalf("unexpected %s section state for %s: %s", module, tc.want, section.State)
			}
		}
		if _, enabled := f.app.Store.State("mealplan"); !enabled && data.Meals.State != "disabled" {
			t.Fatal("disabled planning queried", data.Meals)
		}
		r, raw, requestErr = f.request(http.MethodGet, "/", tc.token, "", "", nil, nil)
		if requestErr != nil || r.StatusCode != 200 || !strings.Contains(string(raw), `id="today"`) {
			t.Fatal("Today page unavailable to role", r, requestErr)
		}
	}
	var mutated int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.notifications WHERE dedupe_key IN('today-admin','today-reader') AND (read_at IS NOT NULL OR dismissed_at IS NOT NULL)`).Scan(&mutated); err != nil || mutated != 0 {
		t.Fatal("Today read mutated notices", mutated, err)
	}
	r, _, err := f.request(http.MethodGet, "/api/v1/core/today", "", "", "", nil, nil)
	if err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous Today API accessible", r, err)
	}
}
