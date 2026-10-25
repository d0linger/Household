//go:build integration

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func cookingStart(t *testing.T, f *integrationFixture, rid string) store.CookingSession {
	t.Helper()
	source, err := f.app.Store.CookingSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	in := store.CookingInput{Action: "start", Generation: f.app.Store.States()["recipes"].Generation, SourceVersionID: store.String(source["source_version_id"]), Servings: "2.500001"}
	key := value.UUID()
	result, err := f.app.Store.MutateCooking(f.ctx, f.identity, "", key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.MutateCooking(f.ctx, f.identity, "", key, value.UUID(), in)
	if err != nil || replay["id"] != result["id"] {
		t.Fatal("start replay", replay, err)
	}
	return cookingRead(t, f, store.String(result["id"]))
}

func cookingRead(t *testing.T, f *integrationFixture, sid string) store.CookingSession {
	t.Helper()
	s, err := f.app.Store.CookingSession(f.ctx, f.identity, sid)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cookingChange(t *testing.T, f *integrationFixture, s store.CookingSession, in store.CookingInput) store.CookingSession {
	t.Helper()
	in.Generation, in.Version = f.app.Store.States()["recipes"].Generation, s.Version
	if _, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	return cookingRead(t, f, s.ID)
}

func TestCookingFrozenSourceTimersAndDurableFinish(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	s := cookingStart(t, f, rid)
	if s.Quantities[0].Display != "9007199254.740993" || s.Quantities[0].Approximate || s.Quantities[1] != nil {
		t.Fatal("exact and unknown amounts", s.Quantities)
	}
	s = cookingChange(t, f, s, store.CookingInput{Action: "progress", CurrentStep: 1, CheckedIDs: []string{store.String(s.Source.Ingredients[0]["id"])}})
	s = cookingChange(t, f, s, store.CookingInput{Action: "portions", Servings: "5.000002"})
	if s.Quantities[0].Display != "18014398509.481986" || s.CurrentStep != 1 || len(s.CheckedIDs) != 1 {
		t.Fatal("scaled progress", s)
	}
	for _, name := range []string{"Ofen", "Teig"} {
		s = cookingChange(t, f, s, store.CookingInput{Action: "timer_add", TimerName: name, Seconds: 120})
	}
	s = cookingChange(t, f, s, store.CookingInput{Action: "pause", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version})
	paused := *s.Timers[0].RemainingMillis
	if paused <= 0 || paused > 120000 || s.Timers[1].Deadline == nil {
		t.Fatal("independent timers", s.Timers)
	}
	s = cookingChange(t, f, s, store.CookingInput{Action: "resume", TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version})
	if s.Timers[0].Deadline == nil || s.Timers[0].RemainingMillis != nil {
		t.Fatal("resume", s.Timers)
	}
	edit, original := recipeEdit(t, f, rid)
	if store.String(original.Recipe["base_servings"]) != "2.500001" {
		t.Fatal("cooking altered source servings")
	}
	edit.Data["title"] = json.RawMessage(`"Changed live recipe"`)
	recipeSave(t, f, rid, edit)
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM recipes.recipes WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	s = cookingRead(t, f, s.ID)
	if store.String(s.Source.Recipe["title"]) != "Precise recipe" || len(s.Source.Steps) != 2 {
		t.Fatal("frozen source did not survive delete")
	}
	finish := store.CookingInput{Action: "finish", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version, ActualServings: "3.000001", CookedOn: s.Today, Notes: "Private finish note sentinel"}
	result, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), finish)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), finish)
	if err != nil || result["record_id"] != replay["record_id"] {
		t.Fatal("durable completion replay", replay, err)
	}
	finish.Notes = "Conflicting finish"
	if _, err = f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), finish); !errors.Is(err, store.ErrConflict) {
		t.Fatal("different finish accepted", err)
	}
	s = cookingRead(t, f, s.ID)
	if s.State != "completed" || s.ResultID != store.String(result["record_id"]) {
		t.Fatal("terminal state", s)
	}
	for _, timer := range s.Timers {
		if timer.State != "done" || timer.Deadline != nil || timer.RemainingMillis != nil {
			t.Fatal("terminal timer still active", timer)
		}
	}
	history, err := f.app.Store.CookingRecord(f.ctx, f.identity, s.ResultID)
	if err != nil || history["rating"] != nil || store.String(history["actual_servings"]) != "3.000001" || history["notes"] != "Private finish note sentinel" {
		t.Fatal("immutable completion", history, err)
	}
	for _, query := range []string{"UPDATE recipes.cooking_records SET notes='changed' WHERE id=$1", "DELETE FROM recipes.cooking_records WHERE id=$1"} {
		if _, err = f.pool.Exec(f.ctx, query, s.ResultID); err == nil {
			t.Fatal("runtime changed history", query)
		}
	}
	var count, leaked int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM recipes.cooking_records WHERE session_id=$1", s.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate history", count, err)
	}
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE row_to_json(audit_log)::text LIKE '%Private finish note sentinel%'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("notes leaked into audit", leaked, err)
	}
}

func TestCookingPrivacyHTTPAndRollback(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	s := cookingStart(t, f, rid)
	other := shoppingTestMember(t, f, "other-cook", "editor")
	reader := shoppingTestMember(t, f, "cook-reader", "reader")
	if _, err := f.app.Store.CookingSession(f.ctx, other, s.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("private session disclosed", err)
	}
	if sessions, err := f.app.Store.CookingSessions(f.ctx, other); err != nil || len(sessions) != 0 {
		t.Fatal("private list disclosed", sessions, err)
	}
	valid := store.CookingInput{Action: "progress", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version}
	if _, err := f.app.Store.MutateCooking(f.ctx, other, s.ID, value.UUID(), value.UUID(), valid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another member changed session", err)
	}
	if _, err := f.app.Store.MutateCooking(f.ctx, reader, s.ID, value.UUID(), value.UUID(), valid); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader write", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err := f.app.Store.CookingSession(f.ctx, foreign, s.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross-household read", err)
	}
	for _, tc := range []struct {
		name, token, csrf, body string
		status                  int
	}{
		{"anonymous", "", "", `{}`, 401}, {"csrf", f.token, "", `{}`, 403},
		{"wrong-type", f.token, f.app.Auth.CSRF(f.token, "session"), `{"version":"1"}`, 422},
		{"unknown-field", f.token, f.app.Auth.CSRF(f.token, "session"), `{"administrator":true}`, 422},
		{"trailing-json", f.token, f.app.Auth.CSRF(f.token, "session"), `{} {}`, 422},
		{"body-limit", f.token, f.app.Auth.CSRF(f.token, "session"), `{"notes":"` + strings.Repeat("a", (1<<20)+1) + `"}`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, body, err := f.request(http.MethodPost, "/api/v1/cooking/"+s.ID, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || r.StatusCode != tc.status {
				t.Fatal(r, string(body), err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		input store.CookingInput
	}{
		{"foreign-ingredient", store.CookingInput{Action: "progress", CheckedIDs: []string{value.UUID()}}},
		{"duplicate-ingredient", store.CookingInput{Action: "progress", CheckedIDs: []string{store.String(s.Source.Ingredients[0]["id"]), store.String(s.Source.Ingredients[0]["id"])}}},
		{"step-range", store.CookingInput{Action: "progress", CurrentStep: 2}},
		{"zero-servings", store.CookingInput{Action: "portions", Servings: "0"}},
		{"timer-zero", store.CookingInput{Action: "timer_add", TimerName: "x", Seconds: 0}},
		{"timer-long", store.CookingInput{Action: "timer_add", TimerName: "x", Seconds: 86401}},
		{"timer-empty-name", store.CookingInput{Action: "timer_add", Seconds: 60}},
		{"foreign-timer", store.CookingInput{Action: "pause", TimerID: value.UUID()}},
		{"future-finish", store.CookingInput{Action: "finish", ActualServings: "1", CookedOn: "2199-01-01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input
			in.Version = s.Version
			in.Generation = valid.Generation
			request := value.UUID()
			if _, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), request, in); err == nil {
				t.Fatal("invalid operation accepted")
			}
			after := cookingRead(t, f, s.ID)
			if after.Version != s.Version || after.State != "active" || len(after.Timers) != 0 {
				t.Fatal("failed operation left changes")
			}
			var n int
			if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE request_id=$1", request).Scan(&n); err != nil || n != 0 {
				t.Fatal("failed operation left audit", n, err)
			}
		})
	}
	valid.Generation++
	if _, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), valid); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale generation", err)
	}
	m, _ := f.app.Store.Module("recipes")
	if _, ok := m.Entity("cooking_sessions"); ok {
		t.Fatal("private sessions registered for generic CRUD")
	}
	var indexed bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.search_documents WHERE resource_id=$1)", s.ID).Scan(&indexed); err != nil || indexed {
		t.Fatal("private session searchable", err)
	}
	s = cookingChange(t, f, s, store.CookingInput{Action: "finish", ActualServings: "1", CookedOn: s.Today, Notes: "Shared only on confirmation"})
	if record, err := f.app.Store.CookingRecord(f.ctx, reader, s.ResultID); err != nil || record["notes"] != "Shared only on confirmation" {
		t.Fatal("reader cannot see confirmed household history", record, err)
	}
}

func TestCookingConcurrentBoundsAndTerminalStates(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	s := cookingStart(t, f, rid)
	in := store.CookingInput{Action: "progress", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version, CurrentStep: 1}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), in)
			results <- err
		}()
	}
	success, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("concurrent version protection", success, conflicts)
	}
	s = cookingRead(t, f, s.ID)
	for i := range 20 {
		s = cookingChange(t, f, s, store.CookingInput{Action: "timer_add", TimerName: fmt.Sprint("Timer ", i), Seconds: 60})
	}
	invalid := store.CookingInput{Action: "timer_add", Generation: in.Generation, Version: s.Version, TimerName: "Overflow", Seconds: 60}
	if _, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), invalid); err == nil {
		t.Fatal("21st timer accepted")
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE recipes.cooking_timers SET state='paused',deadline=NULL,remaining_millis=NULL WHERE id=$1", s.Timers[0].ID); err == nil {
		t.Fatal("paused timer without remaining duration accepted")
	}
	invalid = store.CookingInput{Action: "pause", Generation: in.Generation, Version: s.Version, TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version + 1}
	if _, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), invalid); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale timer accepted", err)
	}
	for range 8 {
		cookingStart(t, f, rid)
	}
	start := store.CookingInput{Action: "start", Generation: in.Generation, SourceVersionID: s.SourceVersionID, Servings: "1"}
	for range 2 {
		go func() {
			_, err := f.app.Store.MutateCooking(f.ctx, f.identity, "", value.UUID(), value.UUID(), start)
			results <- err
		}()
	}
	success = 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else {
			var invalid store.Invalid
			if !errors.As(err, &invalid) {
				t.Fatal(err)
			}
		}
	}
	if sessions, err := f.app.Store.CookingSessions(f.ctx, f.identity); err != nil || len(sessions) != 10 || success != 1 {
		t.Fatal("concurrent session bound", len(sessions), success, err)
	}
	s = cookingChange(t, f, s, store.CookingInput{Action: "abandon"})
	s = cookingChange(t, f, s, store.CookingInput{Action: "abandon"})
	if s.State != "abandoned" || s.ResultID != "" {
		t.Fatal("abandon created cooking history")
	}
	for _, timer := range s.Timers {
		if timer.State != "done" {
			t.Fatal("abandon left timer running")
		}
	}
	s = cookingStart(t, f, rid)
	finish := store.CookingInput{Action: "finish", Generation: in.Generation, Version: s.Version, ActualServings: "1", CookedOn: s.Today}
	for range 2 {
		go func() {
			_, err := f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), finish)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent matching completion", err)
		}
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM recipes.cooking_records WHERE session_id=$1", s.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent completion duplicated", count, err)
	}
}

func TestCookingServerClockAfterLockAndExpiredTimer(t *testing.T) {
	f := newIntegrationFixture(t)
	s := cookingStart(t, f, recipeSave(t, f, "", recipeInput(t, f)))
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM recipes.cooking_sessions WHERE id=$1 FOR UPDATE", s.ID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, e := f.app.Store.MutateCooking(ctx, f.identity, s.ID, value.UUID(), value.UUID(), store.CookingInput{Action: "timer_add", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version, TimerName: "After lock", Seconds: 30})
		finished <- e
	}()
	blocked := false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM recipes.cooking_sessions WHERE household_id%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("mutation did not reach held session lock")
	}
	var release time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&release); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	s = cookingRead(t, f, s.ID)
	if s.Timers[0].Deadline.Before(release.Add(30 * time.Second)) {
		t.Fatal("deadline used transaction-start time before lock")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE recipes.cooking_timers SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", s.Timers[0].ID); err != nil {
		t.Fatal(err)
	}
	s = cookingRead(t, f, s.ID)
	in := store.CookingInput{Action: "pause", Generation: f.app.Store.States()["recipes"].Generation, Version: s.Version, TimerID: s.Timers[0].ID, TimerVersion: s.Timers[0].Version}
	if _, err = f.app.Store.MutateCooking(f.ctx, f.identity, s.ID, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("elapsed timer resurrected by pause")
	}
	in.Action = "acknowledge"
	s = cookingChange(t, f, s, in)
	if s.Timers[0].State != "done" {
		t.Fatal("elapsed acknowledgement")
	}
	in.Action = "restart"
	in.TimerVersion = s.Timers[0].Version
	in.Seconds = 86400
	s = cookingChange(t, f, s, in)
	if s.Timers[0].State != "running" || s.Timers[0].DurationSeconds != 86400 {
		t.Fatal("explicit restart")
	}
	in.Action = "timer_remove"
	in.TimerVersion = s.Timers[0].Version
	s = cookingChange(t, f, s, in)
	if len(s.Timers) != 0 {
		t.Fatal("remove timer")
	}
}

func TestCookingHistoryCursorAndPopulatedUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	for range 22 {
		s := cookingStart(t, f, rid)
		cookingChange(t, f, s, store.CookingInput{Action: "finish", ActualServings: "1", CookedOn: s.Today})
	}
	first, err := f.app.Store.CookingHistory(f.ctx, f.identity, rid, "")
	if err != nil || len(first) != 21 {
		t.Fatal("history sentinel", len(first), err)
	}
	second, err := f.app.Store.CookingHistory(f.ctx, f.identity, rid, store.String(first[19]["id"]))
	if err != nil || len(second) != 2 || second[0]["id"] != first[20]["id"] {
		t.Fatal("stable history cursor", second, err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/cooking/history", f.token, "", "", nil, nil)
	var page struct {
		Items []store.Record `json:"items"`
		Next  string         `json:"next"`
	}
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response, err)
	}
	if err = json.Unmarshal(raw, &page); err != nil || len(page.Items) != 20 || page.Next != store.String(first[19]["id"]) {
		t.Fatal("HTTP bounded cursor", page, err)
	}
	// Upgrade the actual pre-cooking schema, including already populated recipe versions.
	if err = resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 11, "recipes": 2, "shopping": 4, "pantry": 2}
	for _, schema := range database.Schemas {
		if version, ok := prior[schema]; ok {
			migratePriorAuditSchema(t, f, schema, version)
		} else {
			migratePriorAuditSchema(t, f, schema)
		}
	}
	hid, uid, recipe := value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Cooking upgrade')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'cooking-upgrade','Prior','fixture-no-login')", []any{uid}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hid, uid}},
		{"INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES($1,$2,'Before cooking',2.500001,$3)", []any{recipe, hid, uid}},
		{"INSERT INTO recipes.ingredients(household_id,recipe_id,name,quantity,created_by) VALUES($1,$2,'Precise',9007199254.740993,$3)", []any{hid, recipe, uid}},
	} {
		if _, err = f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err = f.owner.QueryRow(f.ctx, "SELECT md5(string_agg(snapshot::text,',' ORDER BY revision)) FROM recipes.versions WHERE recipe_id=$1", recipe).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	var after string
	var sessions int
	if err = f.owner.QueryRow(f.ctx, "SELECT md5(string_agg(snapshot::text,',' ORDER BY revision)) FROM recipes.versions WHERE recipe_id=$1", recipe).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade altered recipe history", before, after, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM recipes.cooking_sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("upgrade fabricated cooking history", sessions, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
