//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgxpool"
)

func choreTestRecord(t *testing.T, item any) Record {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func choreTestInput(t *testing.T, f *recipeExpansionFixture, anchor string) ChoreSeriesInput {
	t.Helper()
	var day string
	if err := f.owner.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Vienna')::date::text`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	return ChoreSeriesInput{ChoreCommand: ChoreCommand{Generation: f.store.States()["chores"].Generation}, Title: "Native chore", Description: "Private native instructions", Priority: 1,
		Schedule: chore.Schedule{Kind: "days", Interval: 7, Anchor: anchor, StartsOn: day, LocalTime: "00:00", Timezone: "Europe/Vienna"}, AssignmentKind: "fixed", AssigneeID: f.identity.MembershipID}
}

func choreTestSave(t *testing.T, f *recipeExpansionFixture, in ChoreSeriesInput) Record {
	t.Helper()
	preview, err := f.store.PreviewChoreSeries(f.ctx, f.identity, in)
	if err != nil {
		t.Fatal("preview", err)
	}
	raw, err := json.Marshal(preview["normalized"])
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.SaveChoreSeries(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("save", err)
	}
	return result
}

func choreTestOccurrence(t *testing.T, f *recipeExpansionFixture, templateID string) Record {
	t.Helper()
	var raw []byte
	if err := f.app.QueryRow(f.ctx, `SELECT to_jsonb(o) FROM chores.task_occurrences o WHERE household_id=$1 AND template_id=$2 AND state='open' ORDER BY due_at,id LIMIT 1`, f.identity.HouseholdID, templateID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	record, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestChoreNativeChecklistConcurrentCompletionAndDurableReplay(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	in.Checklist = []ChoreChecklistInput{{Title: "Required actual step", Required: true}}
	series := choreTestSave(t, f, in)
	template := choreTestRecord(t, series["template"])
	occurrence := choreTestOccurrence(t, f, String(template["id"]))
	oid := String(occurrence["id"])
	command := ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(occurrence["version"])}, Action: "complete"}
	if _, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, value.UUID(), value.UUID(), command); err == nil {
		t.Fatal("mandatory checklist bypassed")
	}
	work, err := f.store.ChoreWork(f.ctx, f.identity, oid)
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := work["checklist"].([]Record)
	if !ok || len(parts) != 1 {
		t.Fatal("captured checklist missing")
	}
	checked, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: command.ChoreCommand, Action: "check", ChecklistKey: String(parts[0]["item_key"]), Done: true})
	if err != nil {
		t.Fatal(err)
	}
	command.Version = Int(choreTestRecord(t, checked["occurrence"])["version"])
	zero := 0
	command.ActualMinutes = &zero
	keys := []string{value.UUID(), value.UUID()}
	results := make([]Record, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Go(func() {
			<-start
			results[i], errs[i] = f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, keys[i], value.UUID(), command)
		})
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, err := range errs {
		if err == nil {
			if winner >= 0 {
				t.Fatal("two independent completions succeeded")
			}
			winner = i
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal("unexpected concurrent failure", err)
		}
	}
	if winner < 0 {
		t.Fatal("no completion succeeded", errs)
	}
	replay, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, keys[winner], value.UUID(), command)
	if err != nil || String(choreTestRecord(t, replay["occurrence"])["id"]) != oid {
		t.Fatal("exact completion replay", err)
	}
	var facts, open int
	var minutes *int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.completions WHERE occurrence_id=$1),
 (SELECT count(*) FROM chores.task_occurrences WHERE template_id=$2 AND state='open'),(SELECT actual_minutes FROM chores.completions WHERE occurrence_id=$1)`, oid, template["id"]).Scan(&facts, &open, &minutes); err != nil || facts != 1 || open != 1 || minutes == nil || *minutes != 0 {
		t.Fatal("completion/followup facts", facts, open, minutes, err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE chores.completions SET actual_minutes=1 WHERE occurrence_id=$1`, oid); err == nil {
		t.Fatal("immutable actual history changed")
	}
	_ = results
}

func TestChoreNativeFrozenVersionsSnoozeSkipPauseAndArchive(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "scheduled")
	series := choreTestSave(t, f, in)
	template := choreTestRecord(t, series["template"])
	tid := String(template["id"])
	occurrence := choreTestOccurrence(t, f, tid)
	oid := String(occurrence["id"])
	start, _ := chore.ParseDay(in.Schedule.StartsOn)
	moved, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(occurrence["version"])}, Action: "snooze", DueOn: start.AddDate(0, 0, 1).Format(time.DateOnly), LocalTime: "10:00", Timezone: "Europe/Vienna"})
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.store.ChoreWork(f.ctx, f.identity, oid)
	if err != nil {
		t.Fatal(err)
	}
	detail := choreTestRecord(t, choreTestRecord(t, work["item"])["detail"])
	if String(detail["original_due_at"]) == String(choreTestRecord(t, moved["occurrence"])["due_at"]) {
		t.Fatal("snooze overwrote original deadline")
	}
	current, err := f.store.ChoreSeries(f.ctx, f.identity, tid)
	if err != nil {
		t.Fatal(err)
	}
	in.ID = tid
	in.Version = Int(choreTestRecord(t, current["profile"])["version"])
	in.BaseVersion = Int(choreTestRecord(t, current["template"])["version"])
	in.Title = "New future title"
	in.EffectiveOn = start.AddDate(0, 0, 2).Format(time.DateOnly)
	choreTestSave(t, f, in)
	work, err = f.store.ChoreWork(f.ctx, f.identity, oid)
	if err != nil || choreTestRecord(t, work["item"])["title"] != "Native chore" {
		t.Fatal("historical snapshot changed", err)
	}
	current, err = f.store.ChoreSeries(f.ctx, f.identity, tid)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := f.store.ControlChoreSeries(f.ctx, f.identity, tid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(choreTestRecord(t, current["profile"])["version"])}, Action: "pause"})
	if err != nil || choreTestRecord(t, paused["profile"])["paused"] != true {
		t.Fatal("pause", err)
	}
	resumed, err := f.store.ControlChoreSeries(f.ctx, f.identity, tid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(choreTestRecord(t, paused["profile"])["version"])}, Action: "resume"})
	if err != nil {
		t.Fatal("resume", err)
	}
	_, err = f.store.ControlChoreSeries(f.ctx, f.identity, tid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(choreTestRecord(t, resumed["profile"])["version"])}, Action: "archive"})
	if err != nil {
		t.Fatal(err)
	}
	currentOccurrence := choreTestRecord(t, moved["occurrence"])
	if _, err = f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(currentOccurrence["version"])}, Action: "complete"}); err == nil {
		t.Fatal("archived series accepted completion")
	}
}

func TestChoreNativeCalendarHashesStableUIDRevocationAndModuleGeneration(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	choreTestSave(t, f, in)
	command := ChoreFeedInput{ChoreCommand: ChoreCommand{Generation: in.Generation}, Action: "create", Scope: "mine", DaysBack: 7, DaysAhead: 90}
	key := value.UUID()
	created, err := f.store.MutateChoreCalendarFeed(f.ctx, f.identity, "", key, value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	path := String(created["url"])
	secret := strings.TrimSuffix(strings.TrimPrefix(path, "/calendar/chores/"), ".ics")
	if len(secret) != 43 {
		t.Fatal("one-time capability missing")
	}
	events, err := f.store.ChoreCalendarFeed(f.ctx, secret)
	if err != nil || len(events) != 1 || strings.Contains(events[0].Summary, "Private native instructions") {
		t.Fatal("calendar projection", len(events), err)
	}
	replay, err := f.store.MutateChoreCalendarFeed(f.ctx, f.identity, "", key, value.UUID(), command)
	if err != nil || replay["secret_unavailable"] != true || replay["url"] != nil {
		t.Fatal("secret leaked from receipt", err)
	}
	var leaked bool
	if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.idempotency_records WHERE household_id=$1 AND response_payload::text LIKE '%'||$2||'%')`, f.identity.HouseholdID, secret).Scan(&leaked); err != nil || leaked {
		t.Fatal("stored bearer secret", err)
	}
	feed := choreTestRecord(t, created["feed"])
	_, err = f.store.MutateChoreCalendarFeed(f.ctx, f.identity, String(feed["id"]), value.UUID(), value.UUID(), ChoreFeedInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(feed["version"])}, Action: "revoke"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ChoreCalendarFeed(f.ctx, secret); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked feed authorized", err)
	}
	second, err := f.store.MutateChoreCalendarFeed(f.ctx, f.identity, "", value.UUID(), value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	secondToken := strings.TrimSuffix(strings.TrimPrefix(String(second["url"]), "/calendar/chores/"), ".ics")
	state := f.store.States()["chores"]
	if err = f.store.SetModule(f.ctx, f.identity, "chores", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ChoreCalendarFeed(f.ctx, secondToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled module feed authorized", err)
	}
}

func TestChoreNativeSuitableSubstituteAndActualDependencyProof(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	uid, member := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.users(id,username,display_name,password_hash,must_change_password)
 SELECT $1,$2,'Explicit substitute',password_hash,false FROM core.users WHERE id=$3`, uid, "chore-substitute-"+uid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.memberships(id,household_id,user_id,role) VALUES($1,$2,$3,'editor')`, member, f.identity.HouseholdID, uid); err != nil {
		t.Fatal(err)
	}
	in := choreTestInput(t, f, "completion")
	if _, err := f.store.SaveChoreAbsence(f.ctx, f.identity, "", value.UUID(), value.UUID(), ChoreAbsenceInput{ChoreCommand: in.ChoreCommand, MembershipID: f.identity.MembershipID, StartsOn: in.Schedule.StartsOn, EndsOn: in.Schedule.StartsOn, SubstituteID: member}); err != nil {
		t.Fatal(err)
	}
	in.Members = []ChoreMemberInput{{MembershipID: f.identity.MembershipID, Eligible: true}, {MembershipID: member, Eligible: true}}
	first := choreTestSave(t, f, in)
	tid := String(choreTestRecord(t, first["template"])["id"])
	prerequisite := choreTestOccurrence(t, f, tid)
	if prerequisite["assignee_id"] != member {
		t.Fatal("explicit suitable substitute not selected")
	}
	childInput := choreTestInput(t, f, "completion")
	childInput.Title = "Requires actual first completion"
	childInput.PrerequisiteIDs = []string{tid}
	child := choreTestSave(t, f, childInput)
	childID := String(choreTestRecord(t, child["template"])["id"])
	occurrence := choreTestOccurrence(t, f, childID)
	complete := ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(occurrence["version"])}, Action: "complete"}
	if _, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, String(occurrence["id"]), value.UUID(), value.UUID(), complete); err == nil {
		t.Fatal("missing actual prerequisite accepted")
	}
	if _, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, String(prerequisite["id"]), value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(prerequisite["version"])}, Action: "complete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MutateChoreOccurrence(f.ctx, f.identity, String(occurrence["id"]), value.UUID(), value.UUID(), complete); err != nil {
		t.Fatal("completed prerequisite refused", err)
	}
	var proved bool
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_array_length(snapshot->'prerequisites')=1
 AND snapshot->'prerequisites'->0->>'occurrence_id'=$2 AND actor_user_id=$3::uuid FROM chores.completions WHERE occurrence_id=$1`, occurrence["id"], String(prerequisite["id"]), f.identity.UserID).Scan(&proved); err != nil || !proved {
		t.Fatal("actual prerequisite/actor evidence missing", err)
	}
}

func choreMigrateFinancePredecessor(t *testing.T, ctx context.Context, ownerURL string, owner *pgxpool.Pool) {
	t.Helper()
	financeMigratePreviousRelease(t, ctx, ownerURL, owner)
	for _, stage := range []struct {
		schema  string
		version uint
	}{{"core", 29}, {"expenses", 5}, {"core", 30}, {"core", 31}, {"fuel", 8}, {"pantry", 14}, {"mealplan", 9}} {
		recipeExpansionMigrateTo(t, ownerURL, stage.schema, stage.version)
		if stage.schema == "core" && stage.version == 31 {
			if _, err := owner.Exec(ctx, `SELECT core.install_audit_triggers()`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := owner.Exec(ctx, `SELECT core.install_audit_triggers()`); err != nil {
		t.Fatal(err)
	}
}

func TestChoreNativePopulatedCore31UpgradeRetainsUTCAndUnknownHistory(t *testing.T) {
	f := newRecipeExpansionFixtureForMigration(t, choreMigrateFinancePredecessor)
	var coreVersion, choresVersion int
	var dirty bool
	if err := f.owner.QueryRow(f.ctx, `SELECT version,dirty,(SELECT version FROM chores.schema_migrations) FROM core.schema_migrations`).Scan(&coreVersion, &dirty, &choresVersion); err != nil || coreVersion != 31 || choresVersion != 1 || dirty {
		t.Fatal("legacy data requires genuine clean Core31/Chores1", coreVersion, choresVersion, dirty, err)
	}
	tid, oid := value.UUID(), value.UUID()
	due := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO chores.task_templates(id,household_id,title,description,repeat_days,timezone,created_by)
 VALUES($1,$2,'Legacy captured task','Known only at upgrade',7,'Europe/Vienna',$3)`, tid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO chores.task_occurrences(id,household_id,template_id,occurrence_key,due_at,state,completed_at,completed_by,created_by)
 VALUES($1,$2,$3,'actual-old-key',$4,'completed',$4,NULL,$5)`, oid, f.identity.HouseholdID, tid, due, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, f.ownerURL); err != nil {
		t.Fatal("forward upgrade", err)
	}
	var original time.Time
	var actor, minutes *string
	var legacy, review bool
	if err := f.owner.QueryRow(f.ctx, `SELECT d.original_due_at,c.actor_user_id::text,c.actual_minutes::text,c.legacy_capture,p.review_required
 FROM chores.occurrence_details d JOIN chores.completions c ON c.occurrence_id=d.occurrence_id JOIN chores.series_profiles p ON p.template_id=d.template_id
 WHERE d.occurrence_id=$1`, oid).Scan(&original, &actor, &minutes, &legacy, &review); err != nil || !original.Equal(due) || actor != nil || minutes != nil || !legacy || !review {
		t.Fatal("invented or changed legacy facts", original, actor, minutes, legacy, review, err)
	}
	if err := database.Ready(f.ctx, f.app); err != nil {
		t.Fatal("composed readiness", err)
	}
}

func TestChoreNativeDayPlanAbsenceCurrentRoleAndDependencyCycle(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	ten := 10
	in.EstimatedMinutes = &ten
	first := choreTestSave(t, f, in)
	tid := String(choreTestRecord(t, first["template"])["id"])
	_, err := f.store.SaveChoreAbsence(f.ctx, f.identity, "", value.UUID(), value.UUID(), ChoreAbsenceInput{ChoreCommand: in.ChoreCommand, MembershipID: f.identity.MembershipID, StartsOn: in.Schedule.StartsOn, EndsOn: in.Schedule.StartsOn})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := f.store.ChoreDayPlan(f.ctx, f.identity, in.Schedule.StartsOn, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := proposal["plan"].(chore.DayPlan)
	if !ok || len(plan.Selected) != 0 || len(plan.Excluded) != 1 || plan.Excluded[0].ReasonCode != "blocked" {
		t.Fatal("absence silently ignored", plan)
	}
	secondInput := choreTestInput(t, f, "completion")
	secondInput.Title = "Dependent"
	secondInput.PrerequisiteIDs = []string{tid}
	second := choreTestSave(t, f, secondInput)
	sid := String(choreTestRecord(t, second["template"])["id"])
	current, err := f.store.ChoreSeries(f.ctx, f.identity, tid)
	if err != nil {
		t.Fatal(err)
	}
	in.ID = tid
	in.Version = Int(choreTestRecord(t, current["profile"])["version"])
	in.BaseVersion = Int(choreTestRecord(t, current["template"])["version"])
	in.PrerequisiteIDs = []string{sid}
	if _, err = f.store.PreviewChoreSeries(f.ctx, f.identity, in); err == nil {
		t.Fatal("dependency cycle accepted")
	}
	stale := f.identity
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.memberships SET role='reader' WHERE id=$1`, stale.MembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ChoreTasks(f.ctx, stale, ChoreQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale role authorized", err)
	}
	reader := auth.Identity{UserID: stale.UserID, HouseholdID: stale.HouseholdID, MembershipID: stale.MembershipID, SessionID: stale.SessionID, AuthVersion: stale.AuthVersion, Role: "reader"}
	if _, err = f.store.ChoreTasks(f.ctx, reader, ChoreQuery{}); err != nil {
		t.Fatal("current reader denied", err)
	}
	secondInput.EffectiveOn = secondInput.Schedule.StartsOn
	if _, err = f.store.SaveChoreSeries(f.ctx, reader, value.UUID(), value.UUID(), secondInput); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader mutated task series", err)
	}
}

func TestChoreNativeOwnerPurgeClearsAllSixteenTablesAndOldCommands(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	choreTestSave(t, f, in)
	feed, err := f.store.MutateChoreCalendarFeed(f.ctx, f.identity, "", value.UUID(), value.UUID(), ChoreFeedInput{ChoreCommand: in.ChoreCommand, Action: "create", Scope: "mine", DaysAhead: 30})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(String(feed["url"]), "/calendar/chores/"), ".ics")
	state := f.store.States()["chores"]
	if err = f.store.SetModule(f.ctx, f.identity, "chores", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, request := value.UUID(), value.UUID()
	// This is a controlled authorization fixture, not evidence of a real
	// backup/restore. The separate release recovery campaign must execute that.
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1::uuid,'complete',clock_timestamp(),clock_timestamp(),'native-chores-boundary','controlled-native-fixture',$1::uuid::text)`, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'chores',$3,$4,$5)`, request, f.identity.HouseholdID, in.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Purge(f.ctx, f.app, request); err == nil {
		t.Fatal("application role performed destructive maintenance")
	}
	if err = maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal("owner purge", err)
	}
	for _, table := range []string{"task_templates", "task_occurrences", "areas", "series_profiles", "template_versions", "version_checklist_items", "version_dependencies", "rotation_members", "absences", "occurrence_details", "occurrence_checklist_items", "completions", "member_preferences", "calendar_feeds", "reminder_deliveries", "schedule_state"} {
		var count int // Table identifiers are this closed compiled fixture list.
		if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM chores."+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("purge retained table", table, count, err)
		}
	}
	if err = f.store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	current := f.store.States()["chores"]
	if current.Generation != in.Generation+1 || current.State != "disabled" {
		t.Fatal("generation boundary missing", current)
	}
	if err = f.store.SetModule(f.ctx, f.identity, "chores", true, current.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	in.EffectiveOn = in.Schedule.StartsOn
	if _, err = f.store.SaveChoreSeries(f.ctx, f.identity, value.UUID(), value.UUID(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("old-generation command accepted", err)
	}
	if _, err = f.store.ChoreCalendarFeed(f.ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged feed resurrected", err)
	}
}

func TestChoreNativeSinglePreviewHasNoEffectsAndFencesChangedVersion(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	series := choreTestSave(t, f, in)
	tid := String(choreTestRecord(t, series["template"])["id"])
	occurrence := choreTestOccurrence(t, f, tid)
	oid := String(occurrence["id"])
	var before, after int64
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE household_id=$1`, f.identity.HouseholdID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	command := ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(occurrence["version"])}, Action: "snooze", DueOn: "2026-03-29", LocalTime: "02:30", Timezone: "Europe/Vienna"}
	preview, err := f.store.PreviewChoreOccurrence(f.ctx, f.identity, oid, command)
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := preview["new_due_at"].(time.Time)
	if !ok || deadline.UTC().Format(time.RFC3339) != "2026-03-29T01:30:00Z" {
		t.Fatal("preview ignored persisted gap policy", deadline)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE household_id=$1`, f.identity.HouseholdID).Scan(&after); err != nil || before != after {
		t.Fatal("read-only preview produced audit effects", before, after, err)
	}
	if _, err = f.store.MutateChoreOccurrence(f.ctx, f.identity, oid, value.UUID(), value.UUID(), command); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.PreviewChoreOccurrence(f.ctx, f.identity, oid, command); !errors.Is(err, ErrConflict) {
		t.Fatal("old preview version remained usable", err)
	}
}

func TestChoreNativeReadinessRejectsCatalogBodyReplacementEvenForSameJSON(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(f.ctx, `DO $test$ DECLARE captured jsonb;BEGIN captured=chores.expansion_catalog();
 EXECUTE format('CREATE OR REPLACE FUNCTION chores.expansion_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
 'SELECT '||quote_literal(captured::text)||'::jsonb');END $test$`); err != nil {
		t.Fatal(err)
	}
	var ready bool
	if err = tx.QueryRow(f.ctx, `SELECT chores.expansion_ready()`).Scan(&ready); err != nil || ready {
		t.Fatal("catalog function replacement went undetected", ready, err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.app); err != nil {
		t.Fatal("rollback failed to restore composed readiness", err)
	}
}

func TestChoreNativeResumeRebasesLongCompletionRuleWithoutInventingCompletion(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	tid, oid := value.UUID(), value.UUID()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.task_templates(id,household_id,title,repeat_days,timezone,created_by)
 VALUES($1,$2,'Historical long interval',366,'Europe/Vienna',$3)`, tid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.task_occurrences(id,household_id,template_id,occurrence_key,due_at,state,completed_at,created_by)
 VALUES($1,$2,$3,'known-historical-completion',clock_timestamp()-interval '400 days','completed',clock_timestamp()-interval '400 days',$4)`, oid, f.identity.HouseholdID, tid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.completions(household_id,occurrence_id,template_id,template_version_id,completed_at,snapshot,legacy_capture,created_by)
 SELECT o.household_id,o.id,o.template_id,d.template_version_id,o.completed_at,'{}',true,o.created_by
 FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id WHERE o.id=$1`, oid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE chores.series_profiles SET paused=true,paused_at=clock_timestamp()-interval '300 days',
 paused_until=(clock_timestamp()-interval '10 days')::date,resume_at=clock_timestamp()-interval '10 days',version=version+1 WHERE template_id=$1`, tid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.ChoreSeries(f.ctx, f.identity, tid)
	if err != nil {
		t.Fatal(err)
	}
	in := choreTestInput(t, f, "completion")
	in.ID = tid
	in.Schedule.Interval = 366
	in.Schedule.StartsOn = String(choreTestRecord(t, current["snapshot"])["starts_on"])
	in.Version = Int(choreTestRecord(t, current["profile"])["version"])
	in.BaseVersion = Int(choreTestRecord(t, current["template"])["version"])
	reviewed := choreTestSave(t, f, in)
	_, err = f.store.ControlChoreSeries(f.ctx, f.identity, tid, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(choreTestRecord(t, reviewed["profile"])["version"])}, Action: "resume"})
	if err != nil {
		t.Fatal("long paused rule must resume without an invented past start", err)
	}
	var completed, open int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.completions WHERE template_id=$1),
 (SELECT count(*) FROM chores.task_occurrences WHERE template_id=$1 AND state='open')`, tid).Scan(&completed, &open); err != nil || completed != 1 || open != 0 {
		t.Fatal("resume invented completion or overdue work inside the pause", completed, open, err)
	}
}
