//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"household/internal/value"
)

func choreTestLease(t *testing.T, f *recipeExpansionFixture) (string, string) {
	t.Helper()
	job, lease := value.UUID(), value.UUID()
	generation := f.store.States()["chores"].Generation
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,dedupe_key)
 VALUES($1,$2,'chores',$3,$4,$5)`, job, f.identity.HouseholdID, generation, ChoreWorkspaceJobKind, "native-chores/"+job); err != nil {
		t.Fatal(err)
	}
	var claimed string
	if err := f.owner.QueryRow(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 WHERE id=$1 AND state='pending' RETURNING id`, job, lease).Scan(&claimed); err != nil || claimed != job {
		t.Fatal("actual lease", err)
	}
	return job, lease
}

func TestChoreNativeWorkerDigestDedupeActualSystemActorAndQuietHours(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	choreTestSave(t, f, in)
	prefs := ChorePreferencesInput{ChoreCommand: in.ChoreCommand, DailyMinutes: 30, NotificationMode: "digest", DigestMinute: 0, Timezone: "Europe/Vienna", EscalationAfterDays: 3}
	var minute int
	if err := f.owner.QueryRow(f.ctx, `SELECT extract(hour FROM clock_timestamp() AT TIME ZONE 'Europe/Vienna')::int*60+extract(minute FROM clock_timestamp() AT TIME ZONE 'Europe/Vienna')::int`).Scan(&minute); err != nil {
		t.Fatal(err)
	}
	quietStart, quietEnd := (minute+1380)%1440, (minute+60)%1440
	prefs.QuietStart = &quietStart
	prefs.QuietEnd = &quietEnd
	savedPrefs, err := f.store.SaveChorePreferences(f.ctx, f.identity, value.UUID(), value.UUID(), prefs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE chores.member_preferences SET created_at=((clock_timestamp() AT TIME ZONE timezone)::date::timestamp AT TIME ZONE timezone)-interval '1 second' WHERE household_id=$1 AND user_id=$2`, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	quietJob, quietLease := choreTestLease(t, f)
	if err = f.store.CompleteChoreWorkspaceJob(f.ctx, quietJob, quietLease); err != nil {
		t.Fatal(err)
	}
	var quietDeliveries int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM chores.reminder_deliveries WHERE household_id=$1`, f.identity.HouseholdID).Scan(&quietDeliveries); err != nil || quietDeliveries != 0 {
		t.Fatal("first delivery ignored actual current quiet hours", quietDeliveries, err)
	}
	prefs.Version = Int(choreTestRecord(t, savedPrefs["preferences"])["version"])
	prefs.QuietStart = nil
	prefs.QuietEnd = nil
	if _, err = f.store.SaveChorePreferences(f.ctx, f.identity, value.UUID(), value.UUID(), prefs); err != nil {
		t.Fatal("clear own quiet interval", err)
	}
	if err := f.store.ScheduleChoreWorkspace(f.ctx); err != nil {
		t.Fatal("real scheduling", err)
	}
	for range 2 {
		job, lease := choreTestLease(t, f)
		if err := f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
			t.Fatal("leased digest", err)
		}
	}
	var delivered, notified int
	var actual bool
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.reminder_deliveries WHERE household_id=$1 AND stage='digest'),
 (SELECT count(*) FROM core.notifications WHERE household_id=$1 AND kind='chores_digest'),
 EXISTS(SELECT 1 FROM core.audit_log WHERE household_id=$1 AND module_id='chores' AND resource_type='reminder_deliveries' AND actor_kind='worker' AND actor_user_id IS NULL)`, f.identity.HouseholdID).Scan(&delivered, &notified, &actual); err != nil || delivered != 1 || notified != 1 || !actual {
		t.Fatal("digest dedupe/actor", delivered, notified, actual, err)
	}
}

func TestChoreNativeWorkerLeaseLostWhileWaitingProducesNoTaskOrNotification(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	in := choreTestInput(t, f, "completion")
	choreTestSave(t, f, in)
	job, lease := choreTestLease(t, f)
	lock, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(lock)
	var holderPID int
	if err = lock.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(f.ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/chores/'||$1::text,0))`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM chores.task_occurrences WHERE household_id=$1`, f.identity.HouseholdID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.store.CompleteChoreWorkspaceJob(ctx, job, lease) }()
	blocked := false
	for range 100 {
		if err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks held JOIN pg_locks waiter
 ON(held.locktype,held.database,held.classid,held.objid,held.objsubid) IS NOT DISTINCT FROM
 (waiter.locktype,waiter.database,waiter.classid,waiter.objid,waiter.objsubid)
 WHERE held.pid=$1 AND held.granted AND held.locktype='advisory' AND NOT waiter.granted AND waiter.pid<>held.pid)`, holderPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal(ctx.Err())
		case <-timer.C:
		}
	}
	if !blocked {
		t.Fatal("worker never reached actual advisory wait")
	}
	if _, err = f.owner.Exec(ctx, `UPDATE core.jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal("expired work should be a no-op", err)
	}
	var after, notifications int64
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM chores.task_occurrences WHERE household_id=$1),
 (SELECT count(*) FROM core.notifications WHERE household_id=$1 AND kind LIKE 'chores_%')`, f.identity.HouseholdID).Scan(&after, &notifications); err != nil || after != before || notifications != 0 {
		t.Fatal("lost lease published work", after, before, notifications, err)
	}
}

func TestChoreNativeAutomaticResumeIncludesLegacyProfiles(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	tid := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO chores.task_templates(id,household_id,title,timezone,created_by)
 VALUES($1,$2,'Legacy paused task','Europe/Vienna',$3)`, tid, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE chores.series_profiles SET paused=true,paused_at=clock_timestamp()-interval '2 days',
 paused_until=(clock_timestamp()-interval '1 day')::date,resume_at=clock_timestamp()-interval '1 day',version=version+1
 WHERE template_id=$1`, tid); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ScheduleChoreWorkspace(f.ctx); err != nil {
		t.Fatal(err)
	}
	var queued bool
	if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.jobs WHERE household_id=$1 AND kind=$2 AND state='pending')`, f.identity.HouseholdID, ChoreWorkspaceJobKind).Scan(&queued); err != nil || !queued {
		t.Fatal("expired legacy pause was not scheduled", queued, err)
	}
	job, lease := choreTestLease(t, f)
	if err := f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var paused bool
	if err := f.owner.QueryRow(f.ctx, `SELECT paused FROM chores.series_profiles WHERE template_id=$1`, tid).Scan(&paused); err != nil || paused {
		t.Fatal("legacy pause did not resume", paused, err)
	}
}
