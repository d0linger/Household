//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func choreVersionTestEdit(t *testing.T, in ChoreSeriesInput, series Record) ChoreSeriesInput {
	t.Helper()
	in.ID = String(choreTestRecord(t, series["template"])["id"])
	in.Version = Int(choreTestRecord(t, series["profile"])["version"])
	in.BaseVersion = Int(choreTestRecord(t, series["template"])["version"])
	in.Title = "Reviewed next version"
	in.Checklist = []ChoreChecklistInput{{Title: "New instructions", Required: true}}
	return in
}

func TestChoreNativeOnceEditsRespectEffectiveVersionAndReplay(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	for _, test := range []struct {
		name      string
		dueOffset int
		effective int
		completed bool
		replaced  bool
	}{
		{name: "earlier today"},
		{name: "explicit historical once", dueOffset: -400},
		{name: "completed today", completed: true},
		{name: "future occurrence", dueOffset: 2, replaced: true},
		{name: "change effective after due", dueOffset: 2, effective: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := choreTestInput(t, f, "scheduled")
			today, err := chore.ParseDay(in.Schedule.StartsOn)
			if err != nil {
				t.Fatal(err)
			}
			in.Schedule.Kind = "once"
			in.Schedule.StartsOn = today.AddDate(0, 0, test.dueOffset).Format(time.DateOnly)
			in.Checklist = []ChoreChecklistInput{{Title: "Original instructions"}}
			series := choreTestSave(t, f, in)
			templateID := String(choreTestRecord(t, series["template"])["id"])
			oldVersion := String(choreTestRecord(t, series["snapshot"])["id"])
			original := choreTestOccurrence(t, f, templateID)
			occurrenceID := String(original["id"])
			if test.completed {
				_, err = f.store.MutateChoreOccurrence(f.ctx, f.identity, occurrenceID, value.UUID(), value.UUID(), ChoreActionInput{
					ChoreCommand: ChoreCommand{Generation: in.Generation, Version: Int(original["version"])}, Action: "complete",
				})
				if err != nil {
					t.Fatal("complete original once", err)
				}
			}
			in = choreVersionTestEdit(t, in, series)
			in.EffectiveOn = today.AddDate(0, 0, test.effective).Format(time.DateOnly)
			preview, err := f.store.PreviewChoreSeries(f.ctx, f.identity, in)
			if err != nil {
				t.Fatal("preview edit", err)
			}
			projection, ok := preview["projection"].(chore.Projection)
			if !ok {
				t.Fatal("missing typed projection")
			}
			wantCreated := 0
			if test.replaced {
				wantCreated = 1
			}
			if len(projection.Slots) != wantCreated || Int(preview["affected_open"]) != int64(wantCreated) {
				t.Errorf("preview advertised an occurrence outside the effective window: slots=%v affected=%v", projection.Slots, preview["affected_open"])
			}
			raw, err := json.Marshal(preview["normalized"])
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &in); err != nil {
				t.Fatal(err)
			}
			key := value.UUID()
			updated, err := f.store.SaveChoreSeries(f.ctx, f.identity, key, value.UUID(), in)
			if err != nil {
				t.Fatal("save existing once", err)
			}
			newVersion := String(choreTestRecord(t, updated["snapshot"])["id"])
			if newVersion == oldVersion || Int(choreTestRecord(t, updated["generated"])["created"]) != int64(wantCreated) {
				t.Fatal("edit did not capture exactly the applicable new version", updated)
			}
			var capturedVersion, capturedTitle, state, skippedReason, checklistTitle string
			var count, versionCount, outboxCount int
			if err = f.app.QueryRow(f.ctx, `SELECT detail.template_version_id,version.title,occurrence.state,detail.skipped_reason,
 (SELECT title FROM chores.occurrence_checklist_items WHERE occurrence_id=occurrence.id LIMIT 1),
 (SELECT count(*) FROM chores.task_occurrences WHERE template_id=occurrence.template_id),
 (SELECT count(*) FROM chores.template_versions WHERE template_id=occurrence.template_id),
 (SELECT count(*) FROM core.outbox_events WHERE household_id=occurrence.household_id)
 FROM chores.task_occurrences occurrence JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 JOIN chores.template_versions version ON version.id=detail.template_version_id WHERE occurrence.id=$1`, occurrenceID).
				Scan(&capturedVersion, &capturedTitle, &state, &skippedReason, &checklistTitle, &count, &versionCount, &outboxCount); err != nil {
				t.Fatal(err)
			}
			wantState := "open"
			if test.completed {
				wantState = "completed"
			} else if test.replaced {
				wantState = "skipped"
			}
			if capturedVersion != oldVersion || capturedTitle != "Native chore" || checklistTitle != "Original instructions" ||
				state != wantState || count != 1+wantCreated || test.replaced && skippedReason != "series_changed" {
				t.Fatal("captured occurrence history changed or duplicate was created", capturedVersion, capturedTitle, checklistTitle, state, skippedReason, count)
			}
			if test.replaced {
				fresh := choreTestOccurrence(t, f, templateID)
				work, err := f.store.ChoreWork(f.ctx, f.identity, String(fresh["id"]))
				if err != nil || choreTestRecord(t, work["item"])["title"] != in.Title {
					t.Fatal("future replacement did not use new snapshot", work, err)
				}
			}
			replayed, err := f.store.SaveChoreSeries(f.ctx, f.identity, key, value.UUID(), in)
			if err != nil || String(choreTestRecord(t, replayed["snapshot"])["id"]) != newVersion {
				t.Fatal("series edit replay changed version", err)
			}
			var replayCount, replayVersions, replayOutbox int
			if err = f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.task_occurrences WHERE template_id=$1),
 (SELECT count(*) FROM chores.template_versions WHERE template_id=$1),
 (SELECT count(*) FROM core.outbox_events WHERE household_id=$2)`, templateID, f.identity.HouseholdID).
				Scan(&replayCount, &replayVersions, &replayOutbox); err != nil || replayCount != count || replayVersions != versionCount || replayOutbox != outboxCount {
				t.Fatal("replay produced new occurrence/version/events", replayCount, replayVersions, replayOutbox, err)
			}
			if _, err = f.store.SaveChoreSeries(f.ctx, f.identity, value.UUID(), value.UUID(), in); !errors.Is(err, ErrConflict) {
				t.Fatal("stale edit must conflict", err)
			}
			if !test.replaced {
				_, err = f.app.Exec(f.ctx, `INSERT INTO chores.task_occurrences(household_id,template_id,occurrence_key,due_at,created_by)
 VALUES($1,$2,$3,$4,$5)`, f.identity.HouseholdID, templateID, "v/"+newVersion+"/forged", original["due_at"], f.identity.UserID)
				var pgError *pgconn.PgError
				if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.Message != "managed occurrence requires its exact effective reviewed version" {
					t.Fatal("expected exact effective version trigger rejection", err)
				}
			}
		})
	}
}

func TestChoreNativeRecurringBacklogSurvivesFutureOnceRevision(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "scheduled")
	in.Schedule.Interval = 1
	start, err := chore.ParseDay(in.Schedule.StartsOn)
	if err != nil {
		t.Fatal(err)
	}
	series := choreTestSave(t, f, in)
	templateID := String(choreTestRecord(t, series["template"])["id"])
	oldVersion := String(choreTestRecord(t, series["snapshot"])["id"])
	if choreTestRecord(t, series["generated"])["backlog"] != true {
		t.Fatal("fixture needs a genuinely unfinished calendar backlog")
	}
	in = choreVersionTestEdit(t, in, series)
	in.Schedule.Kind = "once"
	in.Schedule.StartsOn = start.AddDate(0, 0, 30).Format(time.DateOnly)
	in.EffectiveOn = in.Schedule.StartsOn
	updated := choreTestSave(t, f, in)
	newVersion := String(choreTestRecord(t, updated["snapshot"])["id"])
	if choreTestRecord(t, updated["generated"])["backlog"] != true {
		t.Fatal("future once revision falsely completed earlier recurring backlog")
	}
	job, lease := choreTestLease(t, f)
	if _, err = f.owner.Exec(f.ctx, `UPDATE chores.schedule_state SET next_check_at=clock_timestamp() WHERE template_id=$1`, templateID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
		t.Fatal("worker finishes effective version windows", err)
	}
	var historical, future, wrongVersions int
	if err = f.app.QueryRow(f.ctx, `SELECT
 count(*) FILTER(WHERE detail.template_version_id=$2 AND occurrence.state='open'),
 count(*) FILTER(WHERE detail.template_version_id=$3 AND occurrence.state='open'),
 count(*) FILTER(WHERE detail.template_version_id IS DISTINCT FROM
 (SELECT id FROM chores.template_versions WHERE template_id=occurrence.template_id AND NOT legacy_capture
 AND effective_at<=detail.original_due_at ORDER BY revision DESC LIMIT 1))
 FROM chores.task_occurrences occurrence JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 WHERE occurrence.template_id=$1`, templateID, oldVersion, newVersion).Scan(&historical, &future, &wrongVersions); err != nil || historical != 30 || future != 1 || wrongVersions != 0 {
		t.Fatal("old recurring dates or future once snapshot were lost", historical, future, wrongVersions, err)
	}
	job, lease = choreTestLease(t, f)
	if _, err = f.owner.Exec(f.ctx, `UPDATE chores.schedule_state SET next_check_at=clock_timestamp() WHERE template_id=$1`, templateID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
		t.Fatal("repeat materialization", err)
	}
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM chores.task_occurrences WHERE template_id=$1`, templateID).Scan(&count); err != nil || count != 31 {
		t.Fatal("repeated worker generated duplicate occurrences", count, err)
	}
	// A later review effective today supersedes even an already saved future
	// revision, while today's earlier occurrence keeps its original capture.
	in = choreVersionTestEdit(t, in, updated)
	in.Schedule.StartsOn = start.AddDate(0, 0, 45).Format(time.DateOnly)
	in.EffectiveOn = start.Format(time.DateOnly)
	revised := choreTestSave(t, f, in)
	latestVersion := String(choreTestRecord(t, revised["snapshot"])["id"])
	var oldOpen, supersededOpen, latestOpen, skipped int
	if err = f.app.QueryRow(f.ctx, `SELECT
 count(*) FILTER(WHERE detail.template_version_id=$2 AND occurrence.state='open'),
 count(*) FILTER(WHERE detail.template_version_id=$3 AND occurrence.state='open'),
 count(*) FILTER(WHERE detail.template_version_id=$4 AND occurrence.state='open'),
 count(*) FILTER(WHERE occurrence.state='skipped' AND detail.skipped_reason='series_changed')
 FROM chores.task_occurrences occurrence JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 WHERE occurrence.template_id=$1`, templateID, oldVersion, newVersion, latestVersion).
		Scan(&oldOpen, &supersededOpen, &latestOpen, &skipped); err != nil || oldOpen != 1 || supersededOpen != 0 || latestOpen != 1 || skipped != 30 {
		t.Fatal("latest effective review did not supersede future revision while retaining history", oldOpen, supersededOpen, latestOpen, skipped, err)
	}
}

func TestChoreNativeFutureOnceDoesNotInventCompletionHistory(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	in.Schedule.Interval = 1
	start, err := chore.ParseDay(in.Schedule.StartsOn)
	if err != nil {
		t.Fatal(err)
	}
	series := choreTestSave(t, f, in)
	templateID := String(choreTestRecord(t, series["template"])["id"])
	original := choreTestOccurrence(t, f, templateID)
	// Represent a worker whose previous generation window has not yet advanced.
	if _, err = f.owner.Exec(f.ctx, `UPDATE chores.schedule_state SET generated_through=$2 WHERE template_id=$1`, templateID, original["due_at"]); err != nil {
		t.Fatal(err)
	}
	in = choreVersionTestEdit(t, in, series)
	in.Schedule.Kind, in.Schedule.Anchor = "once", "scheduled"
	in.Schedule.StartsOn = start.AddDate(0, 0, 120).Format(time.DateOnly)
	in.EffectiveOn = in.Schedule.StartsOn
	updated := choreTestSave(t, f, in)
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM chores.task_occurrences WHERE template_id=$1`, templateID).Scan(&count); err != nil || count != 1 ||
		Int(choreTestRecord(t, updated["generated"])["created"]) != 0 {
		t.Fatal("completion history became a calendar cadence without an actual completion", count, err)
	}
}

func TestChoreNativeVersionBacklogBoundsBeforeReadingHistory(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "scheduled")
	today, err := chore.ParseDay(in.Schedule.StartsOn)
	if err != nil {
		t.Fatal(err)
	}
	in.Schedule.Interval = 1
	in.Schedule.StartsOn = today.AddDate(0, 0, 400).Format(time.DateOnly)
	series := choreTestSave(t, f, in)
	templateID := String(choreTestRecord(t, series["template"])["id"])
	baseVersion := String(choreTestRecord(t, series["snapshot"])["id"])
	startDay := today.AddDate(0, 0, -400).Format(time.DateOnly)
	start, err := choreAtDay(startDay, in.Schedule.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	// Construct a real, immutable history from a long worker outage. Every
	// 90-day slice fits the version limit, but the complete backlog does not.
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO chores.template_versions(id,household_id,template_id,revision,title,description,
 rule_kind,interval_value,anchor_kind,starts_on,local_time,timezone,effective_at,assignment_kind,assignee_id,created_by)
 SELECT gen_random_uuid(),household_id,template_id,revision+ordinal,title,description,
 rule_kind,interval_value,anchor_kind,$2::date,local_time,timezone,
 ($2::date+ordinal-1)::timestamp AT TIME ZONE timezone,assignment_kind,assignee_id,created_by
 FROM chores.template_versions CROSS JOIN generate_series(1,400) ordinal WHERE id=$1`, baseVersion, startDay); err != nil {
		t.Fatal("construct long reviewed history", err)
	}
	var currentVersion string
	if err = tx.QueryRow(f.ctx, `SELECT id FROM chores.template_versions WHERE template_id=$1 ORDER BY revision DESC LIMIT 1`, templateID).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE chores.series_profiles SET current_version_id=$2,materialize_from=$3 WHERE template_id=$1`, templateID, currentVersion, start); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE chores.schedule_state SET template_version_id=$2,generated_through=$3,next_check_at=clock_timestamp()
 WHERE template_id=$1`, templateID, currentVersion, start.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	job, lease := choreTestLease(t, f)
	if err = f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
		t.Fatal("bounded worker must progress through more than 366 effective versions", err)
	}
	var count, workerEvents int
	var through time.Time
	var backlog bool
	if err = f.app.QueryRow(f.ctx, `SELECT generated_through,backlog,
 (SELECT count(*) FROM chores.task_occurrences WHERE template_id=$1),
 (SELECT count(*) FROM core.outbox_events WHERE household_id=$2 AND module_id='chores' AND actor_user_id IS NULL AND dedupe_key=id::text)
 FROM chores.schedule_state WHERE template_id=$1`, templateID, f.identity.HouseholdID).
		Scan(&through, &backlog, &count, &workerEvents); err != nil || !backlog || count != choreGenerationBatch || workerEvents != choreGenerationBatch ||
		!through.After(start) || !through.Before(start.AddDate(0, 0, 90)) {
		t.Fatal("worker did not publish one finite, honest batch", through, backlog, count, workerEvents, err)
	}
}
