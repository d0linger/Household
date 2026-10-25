//go:build integration

package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func planningFixture(t *testing.T) (*integrationFixture, string) {
	t.Helper()
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["mealplan"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Week with reviewed components", "starts_on": "2026-10-22", "ends_on": "2026-11-04"})
	return f, store.String(plan["id"])
}

func TestPlanningBoundedLegacyLocksAndComponentContention(t *testing.T) {
	f, pid := planningFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	in := planningInput(t, f, pid)
	in.Action = "save"
	in.Meal = store.PlanningMealInput{Title: "Ten independently bounded components", PlannedOn: "2026-10-25", Slot: "dinner", Kind: "recipe", Status: "planned", Servings: "1"}
	for range 10 {
		in.Components = append(in.Components, store.PlanningComponentInput{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "1"})
	}
	in.Components = append(in.Components, in.Components[0])
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err == nil || !strings.Contains(err.Error(), "zehn") {
		t.Fatal("eleventh component accepted", err)
	}
	in.Components = in.Components[:10]
	result := planningMutation(t, f, pid, in)
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, store.String(result["meal_id"]))
	if err != nil || len(meal.Components) != 10 {
		t.Fatal("ten-component boundary", meal, err)
	}
	blocked, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocked.Rollback(f.ctx) }()
	if _, err = blocked.Exec(f.ctx, "SELECT id FROM mealplan.components WHERE id=$1 FOR UPDATE", meal.Components[9].ID); err != nil {
		t.Fatal(err)
	}
	copyInput := planningInput(t, f, pid)
	copyInput.Action, copyInput.Name, copyInput.Week, copyInput.TargetDate = "copy", "Bounded copy", "2026-10-22", "2026-11-05"
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), copyInput); !errors.Is(err, store.ErrConflict) {
		t.Fatal("locked component was omitted from copy", err)
	}
	move := planningInput(t, f, pid)
	move.Action, move.MealID, move.Version, move.TargetDate, move.TargetSlot = "move", meal.ID, meal.Version, "2026-10-25", "lunch"
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), move); !errors.Is(err, store.ErrConflict) {
		t.Fatal("single-meal lock ignored component contention", err)
	}
	if err = blocked.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	planningMutation(t, f, pid, copyInput)

	// Model a valid pre-workspace import that exceeds the new weekly limit.
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.meals DISABLE TRIGGER planning_window_count"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,created_by)
 SELECT ('10000000-0000-4000-8000-'||lpad(g::text,12,'0'))::uuid,$1,$2,'2026-10-25','Legacy meal '||g,$3 FROM generate_series(1,100) g`, f.identity.HouseholdID, pid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.meals ENABLE TRIGGER planning_window_count"); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT version FROM mealplan.plans WHERE id=$1", pid).Scan(&copyInput.PlanVersion); err != nil {
		t.Fatal(err)
	}
	last, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = last.Rollback(f.ctx) }()
	if _, err = last.Exec(f.ctx, "SELECT id FROM mealplan.meals WHERE id='10000000-0000-4000-8000-000000000100' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	_, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), copyInput)
	var invalid store.Invalid
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "42") {
		t.Fatal("legacy overflow locked beyond the bounded selection", err)
	}
	if _, err = f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22"); !errors.As(err, &invalid) {
		t.Fatal("oversized week was silently truncated", err)
	}
}

func TestPlanningPopulatedUpgradePreservesUnreviewedLegacy(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	prior := map[string]uint{"core": 12, "recipes": 3, "shopping": 4, "pantry": 2}
	for _, schema := range database.Schemas {
		if version, ok := prior[schema]; ok {
			migratePriorAuditSchema(t, f, schema, version)
		} else {
			migratePriorAuditSchema(t, f, schema)
		}
	}
	hid, uid, pid, mid, rid := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Planning upgrade')", []any{hid}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'planning-upgrade','Prior','fixture-no-login')", []any{uid}},
		{"INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,'Existing long plan','2026-10-22','2026-11-04',$3)", []any{pid, hid, uid}},
		{"INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,servings,notes,created_by) VALUES($1,$2,$3,'2026-10-25','Preserved legacy title',2.500001,'Preserved legacy notes',$4)", []any{mid, hid, pid, uid}},
		{"INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation) VALUES($1,'mealplan','meals',$2,'recipes','recipes',$3,'meal_recipe')", []any{hid, mid, rid}},
		{"INSERT INTO mealplan.meals(household_id,plan_id,planned_on,title,created_by) SELECT $1,$2,'2026-10-29','Legacy large window '||g,$3 FROM generate_series(1,50) g", []any{hid, pid, uid}},
	} {
		if _, err := f.owner.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	id := f.identity
	id.HouseholdID = hid
	meal, err := f.app.Store.PlanningMeal(f.ctx, id, pid, mid)
	if err != nil || meal.Kind != "recipe" || !meal.RequiresReview || len(meal.Components) != 0 || meal.Servings != "2.500001" || meal.Notes != "Preserved legacy notes" || meal.Version != 1 {
		t.Fatal("upgrade fabricated or rewrote legacy selection", meal, err)
	}
	var retained int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM mealplan.meals WHERE plan_id=$1", pid).Scan(&retained); err != nil || retained != 51 {
		t.Fatal("upgrade dropped oversized legacy window", retained, err)
	}
	var invalid store.Invalid
	if _, err = f.app.Store.PlanningWeek(f.ctx, id, pid, "2026-10-29"); !errors.As(err, &invalid) {
		t.Fatal("large legacy window silently truncated", err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningSnapshotShoppingAndHTTPGuards(t *testing.T) {
	f, pid := planningFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"recipe", "out"} {
		in := planningInput(t, f, pid)
		in.Action = "save"
		in.Meal = store.PlanningMealInput{Title: "Eligible " + kind, PlannedOn: "2026-10-25", Slot: "dinner", Kind: kind, Status: "planned", Servings: "2.500001"}
		if kind == "recipe" {
			in.Components = []store.PlanningComponentInput{{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "2.500001"}}
		}
		planningMutation(t, f, pid, in)
	}
	legacy, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": pid, "planned_on": "2026-10-25", "title": "Needs source review", "servings": "1", "kind": "recipe"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Snapshot ingredients"})
	m, _ := f.app.Store.Module("mealplan")
	e, _ := m.Entity("plans")
	action := func() (store.Record, error) {
		plan, readErr := f.app.Store.Get(f.ctx, f.identity, m, e, pid)
		if readErr != nil {
			return nil, readErr
		}
		input := auditMutation(t, f, "mealplan", store.Record{"list_id": list["id"], "week_start": "2026-10-22"}, store.Int(plan["version"]))
		input.TargetGenerations = map[string]int64{"shopping": f.app.Store.States()["shopping"].Generation}
		return f.app.Store.Action(f.ctx, f.identity, m, e, pid, "shopping", value.UUID(), value.UUID(), input)
	}
	if _, err = action(); err == nil || !strings.Contains(err.Error(), "Needs source review") {
		t.Fatal("unreviewed draft not named", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM shopping.items WHERE list_id=$1", list["id"]).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed preview wrote items", count, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET status='cancelled' WHERE id=$1", legacy["id"]); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM recipes.recipes WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if _, err = action(); err != nil {
		t.Fatal("snapshot transfer still needs recipe module", err)
	}
	var amount string
	var unknown int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*),max(quantity::text),count(*) FILTER(WHERE quantity IS NULL AND unit_code IS NULL) FROM shopping.items WHERE list_id=$1", list["id"]).Scan(&count, &amount, &unknown); err != nil || count != 2 || amount != "9007199254.740993" || unknown != 1 {
		t.Fatal("snapshot quantities or meal-kind filtering", count, amount, unknown, err)
	}
	for _, tc := range []struct {
		name, method, path, token, csrf, body string
		want                                  int
	}{
		{"anonymous", http.MethodGet, "/api/v1/planning/" + pid, "", "", "", 401},
		{"csrf", http.MethodPost, "/api/v1/planning/" + pid, f.token, "", "{}", 403},
		{"wrong-type", http.MethodPost, "/api/v1/planning/" + pid, f.token, f.app.Auth.CSRF(f.token, "session"), `{"plan_version":"1"}`, 422},
		{"unknown-field", http.MethodPost, "/api/v1/planning/" + pid, f.token, f.app.Auth.CSRF(f.token, "session"), `{"admin":true}`, 422},
		{"week", http.MethodGet, "/api/v1/planning/" + pid + "?week=2026-10-22", f.token, "", "", 200},
		{"disabled-source", http.MethodGet, "/api/v1/planning/source/" + rid, f.token, "", "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, raw, requestErr := f.request(tc.method, tc.path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if requestErr != nil || response.StatusCode != tc.want {
				t.Fatal(response, string(raw), requestErr)
			}
		})
	}
	reader := shoppingTestMember(t, f, "planning-reader", "reader")
	if _, err = f.app.Store.PlanningWeek(f.ctx, reader, pid, "2026-10-22"); err != nil {
		t.Fatal("reader cannot inspect plan", err)
	}
	if _, err = f.app.Store.MutatePlanning(f.ctx, reader, pid, value.UUID(), value.UUID(), planningInput(t, f, pid)); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader mutation", err)
	}
	state = f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	response, raw, err := f.request(http.MethodGet, "/api/v1/planning/"+pid+"?week=2026-10-22", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("disabled planning route", response, string(raw), err)
	}
}

func planningInput(t *testing.T, f *integrationFixture, pid string) store.PlanningInput {
	t.Helper()
	week, err := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil {
		t.Fatal(err)
	}
	return store.PlanningInput{Generation: f.app.Store.States()["mealplan"].Generation, PlanVersion: store.Int(week.Plan["version"])}
}

func planningMutation(t *testing.T, f *integrationFixture, pid string, in store.PlanningInput) store.Record {
	t.Helper()
	result, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPlanningAtomicComponentsReplayAndCopyWithoutSource(t *testing.T) {
	f, pid := planningFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	prep, cook := int64(5), int64(15)
	in := planningInput(t, f, pid)
	in.Action = "save"
	in.Meal = store.PlanningMealInput{Title: "Recipe with a side", PlannedOn: "2026-10-25", Slot: "dinner", Kind: "recipe", Status: "planned", Servings: "3.000001", Notes: "Own meal note"}
	for i := range 2 {
		in.Components = append(in.Components, store.PlanningComponentInput{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: fmt.Sprint(i + 1), PrepMinutes: &prep, CookMinutes: &cook})
	}
	key := value.UUID()
	first, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in)
	if err != nil || replayed["meal_id"] != first["meal_id"] {
		t.Fatal("save replay", replayed, err)
	}
	mid := store.String(first["meal_id"])
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil || len(meal.Components) != 2 || meal.Components[0].ID == meal.Components[1].ID || meal.RequiresReview {
		t.Fatal("independent components", meal, err)
	}
	week, err := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil || week.Effort["2026-10-25"].PrepMinutes != 10 || week.Effort["2026-10-25"].CookMinutes != 30 {
		t.Fatal("effort counted once per component", week.Effort, err)
	}
	edit, _ := recipeEdit(t, f, rid)
	edit.Ingredients[0].Data["quantity"] = []byte(`"1"`)
	recipeSave(t, f, rid, edit)
	if status, statusErr := f.app.Store.PlanningSourceStatus(f.ctx, f.identity, pid, mid, meal.Components[0].ID); statusErr != nil || status["available"] != true || status["changed"] != true {
		t.Fatal("changed source not offered explicitly", status, statusErr)
	}
	detail, err := f.app.Store.PlanningComponent(f.ctx, f.identity, pid, mid, meal.Components[0].ID)
	if err != nil || detail["source"] == nil {
		t.Fatal(detail, err)
	}
	st := f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, st.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM recipes.recipes WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE target_generations ? 'recipes'"); err != nil {
		t.Fatal(err)
	}
	if again, replayErr := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in); replayErr != nil || again["meal_id"] != mid {
		t.Fatal("committed copy replay depends on removed read source", again, replayErr)
	}
	if status, statusErr := f.app.Store.PlanningSourceStatus(f.ctx, f.identity, pid, mid, meal.Components[0].ID); statusErr != nil || status["available"] != false {
		t.Fatal("deleted source status", status, statusErr)
	}
	update := planningInput(t, f, pid)
	update.Action = "save"
	update.MealID = mid
	update.Version = meal.Version
	update.Meal = in.Meal
	update.Meal.Servings = "4"
	update.Meal.Notes = "Edited while source disabled"
	for _, c := range meal.Components {
		update.Components = append(update.Components, store.PlanningComponentInput{ID: c.ID, Version: c.Version, Servings: c.Servings, PrepMinutes: c.PrepMinutes, CookMinutes: c.CookMinutes})
	}
	planningMutation(t, f, pid, update)
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil || meal.Components[0].Servings != "1.000000" || meal.Servings != "4.000000" {
		t.Fatal("meal portions silently scaled components", meal, err)
	}
	copyInput := planningInput(t, f, pid)
	copyInput.Action = "copy"
	copyInput.Week = "2026-10-22"
	copyInput.TargetDate = "2026-10-29"
	copyInput.Name = "Following week"
	copyInput.IsTemplate = true
	copyKey := value.UUID()
	copied, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, copyKey, value.UUID(), copyInput)
	if err != nil {
		t.Fatal(err)
	}
	copyReplay, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, copyKey, value.UUID(), copyInput)
	if err != nil || copyReplay["plan_id"] != copied["plan_id"] {
		t.Fatal("copy replay", copyReplay, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE key=$1", copyKey); err != nil {
		t.Fatal(err)
	}
	if durable, replayErr := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, copyKey, value.UUID(), copyInput); replayErr != nil || durable["plan_id"] != copied["plan_id"] {
		t.Fatal("copy duplicated after receipt expiry", durable, replayErr)
	}
	changedCopy := copyInput
	changedCopy.Name = "Different copy request"
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, copyKey, value.UUID(), changedCopy); !errors.Is(err, store.ErrConflict) {
		t.Fatal("durable copy key accepted different payload", err)
	}
	newPlan := store.String(copied["plan_id"])
	newWeek, err := f.app.Store.PlanningWeek(f.ctx, f.identity, newPlan, "2026-10-29")
	if err != nil || len(newWeek.Meals) != 1 {
		t.Fatal(newWeek, err)
	}
	newMeal := newWeek.Meals[0]
	if newPlan == pid || newMeal.ID == mid || newMeal.Status != "open" || newMeal.PlannedOn != "2026-11-01" || len(newMeal.Components) != 2 || newMeal.Components[0].ID == meal.Components[0].ID || newMeal.Components[0].SourceVersionID != meal.Components[0].SourceVersionID {
		t.Fatal("copy lost provenance or inherited state", newMeal)
	}
	var links int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.resource_links WHERE source_module='mealplan' AND source_id=$1", newMeal.ID).Scan(&links); err != nil || links != 0 {
		t.Fatal("copied mutable source links", links, err)
	}
	if detail, err = f.app.Store.PlanningComponent(f.ctx, f.identity, newPlan, newMeal.ID, newMeal.Components[0].ID); err != nil || detail["source"] == nil {
		t.Fatal("copy lost frozen content", detail, err)
	}
	backup, purge := value.UUID(), value.UUID()
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,'recipes',$3,$4,$5)", purge, f.identity.HouseholdID, st.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.owner, purge); err != nil {
		t.Fatal(err)
	}
	if detail, err = f.app.Store.PlanningComponent(f.ctx, f.identity, newPlan, newMeal.ID, newMeal.Components[0].ID); err != nil || detail["source"] == nil {
		t.Fatal("real recipe purge erased copied planning source", detail, err)
	}
	if replay, replayErr := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, copyKey, value.UUID(), copyInput); replayErr != nil || replay["plan_id"] != newPlan {
		t.Fatal("durable copy replay depended on purged recipe generation", replay, replayErr)
	}
}

func TestPlanningSwapFullWindowsAndConcurrentVersion(t *testing.T) {
	f, pid := planningFixture(t)
	for _, day := range []string{"2026-10-22", "2026-10-29"} {
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO mealplan.meals(household_id,plan_id,planned_on,title,servings,kind,status,created_by) SELECT $1,$2,$3::date,'Filled slot '||g,1,'free','planned',$4 FROM generate_series(1,42) g`, f.identity.HouseholdID, pid, day, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil || len(first.Meals) != 42 {
		t.Fatal(first, err)
	}
	second, err := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-29")
	if err != nil || len(second.Meals) != 42 {
		t.Fatal(second, err)
	}
	in := planningInput(t, f, pid)
	in.Action = "swap"
	in.MealID = first.Meals[0].ID
	in.Version = first.Meals[0].Version
	in.OtherID = second.Meals[0].ID
	in.OtherVersion = second.Meals[0].Version
	planningMutation(t, f, pid, in)
	for _, day := range []string{"2026-10-22", "2026-10-29"} {
		week, readErr := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, day)
		if readErr != nil || len(week.Meals) != 42 {
			t.Fatal("swap changed count", len(week.Meals), readErr)
		}
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO mealplan.meals(household_id,plan_id,planned_on,title,servings,created_by) VALUES($1,$2,'2026-10-22','Overflow',1,$3)`, f.identity.HouseholdID, pid, f.identity.UserID); err == nil {
		t.Fatal("43rd meal accepted")
	}
	moved, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, in.MealID)
	if err != nil || moved.PlannedOn != "2026-10-29" {
		t.Fatal("swap did not exchange dates", moved, err)
	}
	in = planningInput(t, f, pid)
	in.Action = "move"
	in.MealID = moved.ID
	in.Version = moved.Version
	in.TargetDate = moved.PlannedOn
	in.TargetSlot = "lunch"
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in)
			results <- e
		}()
	}
	success, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if errors.Is(e, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("concurrent planner versions", success, conflicts)
	}
}

func TestPlanningSchemaVersionsFrozenSourceAndReadBounds(t *testing.T) {
	f, pid := planningFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	meal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": pid, "planned_on": "2026-10-25", "title": "Private planning title sentinel", "servings": "2.500001", "kind": "recipe", "status": "planned"})
	mid := store.String(meal["id"])
	week, err := f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil || len(week.Meals) != 1 || !week.Meals[0].RequiresReview || store.Int(week.Plan["version"]) < 2 {
		t.Fatal("legacy draft and parent invalidation", week, err)
	}
	beforePlan, beforeMeal := store.Int(week.Plan["version"]), week.Meals[0].Version
	var vid string
	var revision int64
	var raw []byte
	if err = f.pool.QueryRow(f.ctx, "SELECT id,revision,snapshot FROM recipes.versions WHERE recipe_id=$1 ORDER BY revision DESC LIMIT 1", rid).Scan(&vid, &revision, &raw); err != nil {
		t.Fatal(err)
	}
	cid := value.UUID()
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO mealplan.components(id,household_id,meal_id,position,servings,prep_minutes,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by) VALUES($1,$2,$3,0,2.500001,0,$4,$5,$6,$7,$8,$9)`, cid, f.identity.HouseholdID, mid, rid, vid, f.app.Store.States()["recipes"].Generation, revision, raw, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	week, err = f.app.Store.PlanningWeek(f.ctx, f.identity, pid, "2026-10-22")
	if err != nil || len(week.Meals[0].Components) != 1 || week.Meals[0].RequiresReview || store.Int(week.Plan["version"]) <= beforePlan || week.Meals[0].Version <= beforeMeal {
		t.Fatal("component invalidation", week, err)
	}
	component := week.Meals[0].Components[0]
	if component.Servings != "2.500001" || component.PrepMinutes == nil || *component.PrepMinutes != 0 || component.CookMinutes != nil || week.Effort["2026-10-25"].UnknownCook != 1 {
		t.Fatal("exact amounts and unknown effort", component, week.Effort)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.components SET source_version_id=$2 WHERE id=$1", cid, value.UUID()); err == nil {
		t.Fatal("source provenance mutable")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.components SET snapshot='{}'::jsonb WHERE id=$1", cid); err == nil {
		t.Fatal("source snapshot mutable")
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO mealplan.components(household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by) VALUES($1,$2,1,1,$3,$4,1,1,'{}',$5)`, f.identity.HouseholdID, mid, rid, vid, f.identity.UserID); err == nil {
		t.Fatal("missing snapshot keys accepted")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET kind='out' WHERE id=$1", mid); err == nil {
		t.Fatal("components attached to non-recipe kind")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET planned_on='2026-11-05' WHERE id=$1", mid); err == nil {
		t.Fatal("meal moved outside plan")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.plans SET ends_on='2026-10-24' WHERE id=$1", pid); err == nil {
		t.Fatal("plan excluded existing meal")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET status='cooked' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET planned_on='2026-10-26' WHERE id=$1", mid); err == nil {
		t.Fatal("terminal meal moved without reopening")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET status='open' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET planned_on='2026-10-26' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["recipes"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "recipes", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PlanningSource(f.ctx, f.identity, rid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled source disclosed", err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM recipes.recipes WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if detail, readErr := f.app.Store.PlanningComponent(f.ctx, f.identity, pid, mid, cid); readErr != nil || detail["source"] == nil {
		t.Fatal("stored snapshot depends on live recipe", detail, readErr)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningWeek(f.ctx, foreign, pid, "2026-10-22"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross-household week disclosed", err)
	}
	var leaked int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE row_to_json(audit_log)::text LIKE '%Private planning title sentinel%'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("human title leaked in new audit policy", leaked, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
