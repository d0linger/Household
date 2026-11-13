//go:build integration

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/value"
)

func notificationChoreRun(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	job, lease := choreTestLease(t, f)
	if err := f.store.CompleteChoreWorkspaceJob(f.ctx, job, lease); err != nil {
		t.Fatal("complete notification-producing chore job", err)
	}
}

func notificationChoreConsent(t *testing.T, f *recipeExpansionFixture, id auth.Identity, category string, prior bool) NotificationPreference {
	t.Helper()
	prefs, err := f.store.NotificationPreferences(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, pref := range prefs {
		if pref.Category != category {
			continue
		}
		pref.Enabled = true
		pref.Timezone = "UTC"
		pref, err = f.store.SaveNotificationPreference(f.ctx, id, pref)
		if err != nil {
			t.Fatal(err)
		}
		if prior {
			// Model consent that already existed before the synthetic deadlines;
			// only the explicitly isolated fixture changes its historical clock.
			if _, err = f.owner.Exec(f.ctx, `UPDATE core.notification_preferences SET enabled_since=clock_timestamp()-interval '2 days' WHERE household_id=$1 AND user_id=$2 AND category=$3`, id.HouseholdID, id.UserID, category); err != nil {
				t.Fatal(err)
			}
		}
		return pref
	}
	t.Fatal("category missing", category)
	return NotificationPreference{}
}

func notificationChorePrefs(t *testing.T, f *recipeExpansionFixture, id auth.Identity, mode string) ChorePreferencesInput {
	t.Helper()
	var version int64
	var minute int
	if err := f.owner.QueryRow(f.ctx, `SELECT coalesce((SELECT version FROM chores.member_preferences WHERE household_id=$1 AND user_id=$2),0),extract(hour FROM clock_timestamp() AT TIME ZONE 'UTC')::int*60+extract(minute FROM clock_timestamp() AT TIME ZONE 'UTC')::int`, id.HouseholdID, id.UserID).Scan(&version, &minute); err != nil {
		t.Fatal(err)
	}
	return ChorePreferencesInput{ChoreCommand: ChoreCommand{Generation: f.store.States()["chores"].Generation, Version: version}, DailyMinutes: 30, NotificationMode: mode, DigestMinute: minute, Timezone: "UTC", EscalationAfterDays: 3}
}

func notificationChoreSavePrefs(t *testing.T, f *recipeExpansionFixture, id auth.Identity, in ChorePreferencesInput) ChorePreferencesInput {
	t.Helper()
	out, err := f.store.SaveChorePreferences(f.ctx, id, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.Version = Int(choreTestRecord(t, out["preferences"])["version"])
	if _, err = f.owner.Exec(f.ctx, `UPDATE chores.member_preferences SET created_at=clock_timestamp()-interval '2 days',updated_at=clock_timestamp()-interval '2 days' WHERE household_id=$1 AND user_id=$2`, id.HouseholdID, id.UserID); err != nil {
		t.Fatal(err)
	}
	return in
}

func notificationChoreTask(t *testing.T, f *recipeExpansionFixture, assignee string, legacy bool, age time.Duration) (string, string) {
	t.Helper()
	var template, occurrence string
	if legacy {
		template, occurrence = value.UUID(), value.UUID()
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO chores.task_templates(id,household_id,title,assignee_id,timezone,created_by) VALUES($1,$2,'Synthetic legacy notification task',nullif($3,'')::uuid,'UTC',$4)`, template, f.identity.HouseholdID, assignee, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO chores.task_occurrences(id,household_id,template_id,occurrence_key,due_at,assignee_id,created_by) VALUES($1::uuid,$2,$3,$1::uuid::text,clock_timestamp(),nullif($4,'')::uuid,$5)`, occurrence, f.identity.HouseholdID, template, assignee, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	} else {
		in := choreTestInput(t, f, "completion")
		in.AssigneeID = assignee
		if assignee == "" {
			in.AssignmentKind = "unassigned"
		}
		created := choreTestSave(t, f, in)
		template = String(choreTestRecord(t, created["template"])["id"])
		occurrence = String(choreTestOccurrence(t, f, template)["id"])
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE chores.task_occurrences SET due_at=clock_timestamp()-$2::interval WHERE id=$1`, occurrence, fmt.Sprintf("%f seconds", age.Seconds())); err != nil {
		t.Fatal(err)
	}
	return template, occurrence
}

func notificationChorePending(t *testing.T, f *recipeExpansionFixture, resource, user, category string) []string {
	t.Helper()
	rows, err := f.app.Query(f.ctx, `SELECT id::text FROM core.notification_intents WHERE resource_id=$1 AND user_id=$2 AND category=$3 AND state='pending' ORDER BY id`, resource, user, category)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func notificationChoreAudience(t *testing.T, f *recipeExpansionFixture, intentID string, want bool) notification.Intent {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	intent, err := ReadNotificationIntent(f.ctx, tx, intentID)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.store.NotificationAudience(f.ctx, tx, intent)
	if err != nil || allowed != want {
		t.Fatal("current chore audience", allowed, want, err)
	}
	return intent
}

func notificationChoreOne(t *testing.T, f *recipeExpansionFixture, occurrence, user string) string {
	t.Helper()
	ids := notificationChorePending(t, f, occurrence, user, "chores_due")
	if len(ids) != 1 {
		t.Fatal("expected one pending due intent", len(ids))
	}
	return ids[0]
}

func notificationChoreAction(t *testing.T, f *recipeExpansionFixture, id auth.Identity, occurrence string, in ChoreActionInput) {
	t.Helper()
	in.Generation = f.store.States()["chores"].Generation
	if err := f.app.QueryRow(f.ctx, `SELECT version FROM chores.task_occurrences WHERE id=$1`, occurrence).Scan(&in.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MutateChoreOccurrence(f.ctx, id, occurrence, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("mutate source occurrence", err)
	}
}

func TestNotificationChoresDueConsentRevisionAndMutationFences(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	other := financeAuthorityMember(t, f, "editor")
	// Historical deadlines are fixture inputs, not wall-clock sleeps.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET changed_at=clock_timestamp()-interval '7 days' WHERE household_id=$1 AND module_id='chores'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	t.Run("opt in never replays pre-consent due event", func(t *testing.T) {
		_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 10*time.Second)
		notificationChoreRun(t, f)
		if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("due push implicitly enabled")
		}
		notificationChoreConsent(t, f, f.identity, "chores_due", false)
		notificationChoreRun(t, f)
		if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("enabling consent replayed historical deadline")
		}
	})
	t.Run("enabling module mode does not replay prior deadline", func(t *testing.T) {
		notificationChoreConsent(t, f, f.identity, "chores_due", true)
		prefs := notificationChoreSavePrefs(t, f, f.identity, notificationChorePrefs(t, f, f.identity, "off"))
		_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		prefs.NotificationMode = "legacy"
		if _, err := f.store.SaveChorePreferences(f.ctx, f.identity, value.UUID(), value.UUID(), prefs); err != nil {
			t.Fatal(err)
		}
		notificationChoreRun(t, f)
		if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("mode activation replayed pre-activation deadline")
		}
	})
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed and legacy due legacy=%v", legacy), func(t *testing.T) {
			notificationChoreSavePrefs(t, f, f.identity, notificationChorePrefs(t, f, f.identity, "legacy"))
			notificationChoreConsent(t, f, f.identity, "chores_due", true)
			_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, legacy, 5*time.Second)
			notificationChoreRun(t, f)
			notificationChoreRun(t, f)
			intent := notificationChoreAudience(t, f, notificationChoreOne(t, f, occurrence, f.identity.UserID), true)
			var due time.Time
			if err := f.app.QueryRow(f.ctx, `SELECT due_at FROM chores.task_occurrences WHERE id=$1`, occurrence).Scan(&due); err != nil {
				t.Fatal(err)
			}
			if !intent.EventAt.Equal(due) || !intent.ExpiresAt.Equal(due.Add(time.Hour)) || intent.ResourceType != "task_occurrences" {
				t.Fatal("source deadline was replaced by scheduler time", intent)
			}
			if len(notificationChorePending(t, f, occurrence, other.UserID, "chores_due")) != 0 {
				t.Fatal("assigned task broadcast to unrelated member")
			}
		})
	}
	t.Run("planning edits preserve due alerts and quiet edits defer them", func(t *testing.T) {
		member := financeAuthorityMember(t, f, "editor")
		prefs := notificationChoreSavePrefs(t, f, member, notificationChorePrefs(t, f, member, "legacy"))
		notificationChoreConsent(t, f, member, "chores_due", true)
		_, occurrence := notificationChoreTask(t, f, member.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		id := notificationChoreOne(t, f, occurrence, member.UserID)
		original := notificationChoreAudience(t, f, id, true)
		// This second deadline has already occurred but has not been scanned.
		// An unrelated preference edit must not turn it into forbidden history.
		_, unscanned := notificationChoreTask(t, f, member.MembershipID, false, 5*time.Second)
		difficulty := 4
		prefs.DailyMinutes, prefs.MaxDifficulty = 75, &difficulty
		out, err := f.store.SaveChorePreferences(f.ctx, member, value.UUID(), value.UUID(), prefs)
		if err != nil {
			t.Fatal(err)
		}
		prefs.Version = Int(choreTestRecord(t, out["preferences"])["version"])
		notificationChoreAudience(t, f, id, true)
		notificationChoreRun(t, f)
		if notificationChoreOne(t, f, occurrence, member.UserID) != id {
			t.Fatal("planning-only preference edit replaced existing due intent")
		}
		notificationChoreAudience(t, f, notificationChoreOne(t, f, unscanned, member.UserID), true)
		var now time.Time
		if err = f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			t.Fatal(err)
		}
		now = now.UTC()
		minute := now.Hour()*60 + now.Minute()
		start, end := (minute+1439)%1440, (minute+10)%1440
		prefs.QuietStart, prefs.QuietEnd = &start, &end
		if _, err = f.store.SaveChorePreferences(f.ctx, member, value.UUID(), value.UUID(), prefs); err != nil {
			t.Fatal(err)
		}
		current := notificationChoreAudience(t, f, id, true)
		if notificationChoreOne(t, f, occurrence, member.UserID) != id || !current.ExpiresAt.Equal(original.ExpiresAt) {
			t.Fatal("quiet edit cancelled or extended an existing due episode")
		}
		tx, err := f.app.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(tx)
		next, err := f.store.NotificationNextAllowed(f.ctx, tx, current, now)
		want := now.Truncate(time.Minute).Add(10 * time.Minute)
		if err != nil || !next.Equal(want) || !next.Before(current.ExpiresAt) {
			t.Fatal("quiet edit did not defer still-fresh due alert", next, want, current.ExpiresAt, err)
		}
	})
	t.Run("accepted due episode is not repeated after text edit", func(t *testing.T) {
		notificationChoreConsent(t, f, f.identity, "chores_due", true)
		_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		id := notificationChoreOne(t, f, occurrence, f.identity.UserID)
		if _, err := f.owner.Exec(f.ctx, `UPDATE core.notification_intents SET state='accepted' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		description := "Edited instructions must not produce a second device alert"
		notificationChoreAction(t, f, f.identity, occurrence, ChoreActionInput{Action: "edit_once", Description: &description})
		notificationChoreRun(t, f)
		if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("incidental revision replayed accepted due episode")
		}
	})
	for _, action := range []string{"complete", "reassign", "claim expiry"} {
		t.Run(action, func(t *testing.T) {
			notificationChoreConsent(t, f, f.identity, "chores_due", true)
			notificationChoreConsent(t, f, other, "chores_due", true)
			assignee := f.identity.MembershipID
			if action == "claim expiry" {
				assignee = ""
			}
			_, occurrence := notificationChoreTask(t, f, assignee, false, 5*time.Second)
			if action == "claim expiry" {
				notificationChoreRun(t, f)
				if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
					t.Fatal("unassigned task was broadcast")
				}
				notificationChoreAction(t, f, f.identity, occurrence, ChoreActionInput{Action: "claim"})
			}
			notificationChoreRun(t, f)
			intent := notificationChoreOne(t, f, occurrence, f.identity.UserID)
			switch action {
			case "complete":
				notificationChoreAction(t, f, f.identity, occurrence, ChoreActionInput{Action: "complete"})
			case "reassign":
				notificationChoreAction(t, f, f.identity, occurrence, ChoreActionInput{Action: "edit_once", AssigneeID: &other.MembershipID})
			case "claim expiry":
				if _, err := f.owner.Exec(f.ctx, `UPDATE chores.occurrence_details SET claim_started_at=clock_timestamp()-interval '5 hours',claim_expires_at=clock_timestamp()-interval '1 second' WHERE occurrence_id=$1`, occurrence); err != nil {
					t.Fatal(err)
				}
			}
			notificationChoreAudience(t, f, intent, false)
			notificationChoreRun(t, f)
			if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
				t.Fatal("superseded due intent remains pending", action)
			}
			if action == "reassign" {
				notificationChoreAudience(t, f, notificationChoreOne(t, f, occurrence, other.UserID), true)
			}
		})
	}
	t.Run("pause preserves overdue work but holds future occurrences", func(t *testing.T) {
		notificationChoreConsent(t, f, f.identity, "chores_due", true)
		template, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		intent := notificationChoreOne(t, f, occurrence, f.identity.UserID)
		current, err := f.store.ChoreSeries(f.ctx, f.identity, template)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ControlChoreSeries(f.ctx, f.identity, template, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: f.store.States()["chores"].Generation, Version: Int(choreTestRecord(t, current["profile"])["version"])}, Action: "pause"}); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, intent, true)
		// Move only the fixture pause timestamp to model a pause before due.
		if _, err = f.owner.Exec(f.ctx, `UPDATE chores.series_profiles SET paused_at=clock_timestamp()-interval '1 minute' WHERE template_id=$1`, template); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, intent, false)
		notificationChoreRun(t, f)
		if len(notificationChorePending(t, f, occurrence, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("paused future occurrence remained pending")
		}
	})
	t.Run("absence preserves existing assignment and captures explicit substitute for new work", func(t *testing.T) {
		notificationChoreConsent(t, f, f.identity, "chores_due", true)
		notificationChoreConsent(t, f, other, "chores_due", true)
		_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		intent := notificationChoreOne(t, f, occurrence, f.identity.UserID)
		in := choreTestInput(t, f, "completion")
		if _, err := f.store.SaveChoreAbsence(f.ctx, f.identity, "", value.UUID(), value.UUID(), ChoreAbsenceInput{ChoreCommand: in.ChoreCommand, MembershipID: f.identity.MembershipID, StartsOn: in.Schedule.StartsOn, EndsOn: in.Schedule.StartsOn, SubstituteID: other.MembershipID}); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, intent, true)
		_, substituted := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		var assigned string
		if err := f.app.QueryRow(f.ctx, `SELECT assignee_id::text FROM chores.task_occurrences WHERE id=$1`, substituted).Scan(&assigned); err != nil || assigned != other.MembershipID {
			t.Fatal("new task did not capture explicit substitution", assigned, err)
		}
		notificationChoreRun(t, f)
		notificationChoreAudience(t, f, notificationChoreOne(t, f, substituted, other.UserID), true)
		if len(notificationChorePending(t, f, substituted, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("new substituted task alerted absent original assignee")
		}
	})
	t.Run("historical inactive users do not block active household chores", func(t *testing.T) {
		// Half have an inactive membership; the remainder have a disabled user
		// with an otherwise active membership. Reuse only the synthetic hash.
		tag, err := f.owner.Exec(f.ctx, `WITH candidates AS MATERIALIZED(
 SELECT gen_random_uuid() AS id,n FROM generate_series(1,501) n),
 users AS(INSERT INTO core.users(id,username,display_name,password_hash,must_change_password,disabled_at)
 SELECT candidate.id,'notification-history-'||candidate.id::text,'Historical notification fixture',source.password_hash,false,
 CASE WHEN candidate.n%2=0 THEN clock_timestamp() END
 FROM candidates candidate CROSS JOIN core.users source WHERE source.id=$1 RETURNING id,disabled_at),
 members AS(INSERT INTO core.memberships(household_id,user_id,role,inactive_at)
 SELECT $2,id,'reader',CASE WHEN disabled_at IS NULL THEN clock_timestamp() END FROM users
 RETURNING household_id,user_id)
 INSERT INTO core.notification_preferences(household_id,user_id,category,enabled,timezone,enabled_since)
 SELECT household_id,user_id,'chores_due',true,'UTC',clock_timestamp()-interval '1 day' FROM members`,
			f.identity.UserID, f.identity.HouseholdID)
		if err != nil || tag.RowsAffected() != 501 {
			t.Fatal("seed bounded historical preferences", tag.RowsAffected(), err)
		}
		_, err = f.store.SaveChoreArea(f.ctx, f.identity, "", value.UUID(), value.UUID(), ChoreAreaInput{
			ChoreCommand: ChoreCommand{Generation: f.store.States()["chores"].Generation}, Name: "Active household work after historical notifications",
		})
		if err != nil {
			t.Fatal("historical notification recipients blocked a current chore write", err)
		}
	})
}

func notificationChoreStageIntent(t *testing.T, f *recipeExpansionFixture, user, stage string) string {
	t.Helper()
	var id string
	if err := f.app.QueryRow(f.ctx, `SELECT i.id::text FROM core.notification_intents i JOIN chores.reminder_deliveries d ON d.id=i.resource_id
 WHERE i.user_id=$1 AND i.category=$2 AND i.state='pending' AND d.stage=$3 ORDER BY i.created_at DESC LIMIT 1`, user, "chores_"+stage, stage).Scan(&id); err != nil {
		t.Fatal("expected stage intent", stage, err)
	}
	return id
}

func TestNotificationChoresDigestEscalationConsentAndQuietHours(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET changed_at=clock_timestamp()-interval '7 days' WHERE household_id=$1 AND module_id='chores'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	t.Run("digest opt in does not replay existing inbox delivery", func(t *testing.T) {
		member := financeAuthorityMember(t, f, "editor")
		notificationChoreSavePrefs(t, f, member, notificationChorePrefs(t, f, member, "digest"))
		notificationChoreTask(t, f, member.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		notificationChoreConsent(t, f, member, "chores_digest", false)
		notificationChoreRun(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE user_id=$1 AND category='chores_digest'`, member.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatal("old inbox digest was promoted to device delivery after consent", count, err)
		}
	})
	t.Run("digest dedupe and completion withdraw current audience", func(t *testing.T) {
		member := financeAuthorityMember(t, f, "editor")
		prefs := notificationChoreSavePrefs(t, f, member, notificationChorePrefs(t, f, member, "digest"))
		notificationChoreConsent(t, f, member, "chores_digest", true)
		_, occurrence := notificationChoreTask(t, f, member.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		notificationChoreRun(t, f)
		id := notificationChoreStageIntent(t, f, member.UserID, "digest")
		intent := notificationChoreAudience(t, f, id, true)
		var count int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE user_id=$1 AND category='chores_digest'`, member.UserID).Scan(&count); err != nil || count != 1 {
			t.Fatal("duplicate local-day digest", count, err)
		}
		if intent.Revision != prefs.Version || intent.ResourceType != "reminder_deliveries" || intent.ExpiresAt.Sub(intent.EventAt) != 4*time.Hour {
			t.Fatal("digest source/deadline contract", intent)
		}
		notificationChoreAction(t, f, member, occurrence, ChoreActionInput{Action: "complete"})
		notificationChoreAudience(t, f, id, false)
	})
	for _, withdrawal := range []string{"source", "receiver"} {
		t.Run("escalation withdrawal "+withdrawal, func(t *testing.T) {
			source := financeAuthorityMember(t, f, "editor")
			receiver := financeAuthorityMember(t, f, "editor")
			rp := notificationChorePrefs(t, f, receiver, "legacy")
			rp.AllowEscalations = true
			rp = notificationChoreSavePrefs(t, f, receiver, rp)
			sp := notificationChorePrefs(t, f, source, "digest")
			sp.EscalationEnabled, sp.EscalationRecipientID = true, receiver.MembershipID
			sp = notificationChoreSavePrefs(t, f, source, sp)
			notificationChoreConsent(t, f, receiver, "chores_escalation", true)
			notificationChoreTask(t, f, source.MembershipID, false, 4*24*time.Hour)
			notificationChoreRun(t, f)
			id := notificationChoreStageIntent(t, f, receiver.UserID, "escalation")
			intent := notificationChoreAudience(t, f, id, true)
			if intent.Revision != sp.Version || intent.ExpiresAt.Sub(intent.EventAt) != time.Hour {
				t.Fatal("escalation source/deadline contract", intent)
			}
			if withdrawal == "source" {
				sp.EscalationEnabled = false
				notificationChoreSavePrefs(t, f, source, sp)
			} else {
				rp.AllowEscalations = false
				notificationChoreSavePrefs(t, f, receiver, rp)
			}
			notificationChoreAudience(t, f, id, false)
			var pending bool
			if err := f.app.QueryRow(f.ctx, `SELECT state='pending' FROM core.notification_intents WHERE id=$1`, id).Scan(&pending); err != nil || pending {
				t.Fatal("withdrawn escalation stayed pending", pending, err)
			}
		})
	}
	t.Run("module quiet hours remain authoritative for device digest", func(t *testing.T) {
		member := financeAuthorityMember(t, f, "editor")
		prefs := notificationChorePrefs(t, f, member, "digest")
		start, end := prefs.DigestMinute, (prefs.DigestMinute+10)%1440
		prefs.QuietStart, prefs.QuietEnd = &start, &end
		prefs = notificationChoreSavePrefs(t, f, member, prefs)
		notificationChoreConsent(t, f, member, "chores_digest", true)
		notificationChoreTask(t, f, member.MembershipID, false, 5*time.Second)
		notificationChoreRun(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE user_id=$1 AND category='chores_digest'`, member.UserID).Scan(&count); err != nil || count != 0 {
			t.Fatal("quiet digest generated device intent", count, err)
		}
		prefs.QuietStart, prefs.QuietEnd = nil, nil
		notificationChoreSavePrefs(t, f, member, prefs)
		notificationChoreRun(t, f)
		notificationChoreAudience(t, f, notificationChoreStageIntent(t, f, member.UserID, "digest"), true)
	})
}

func TestNotificationChoresConcurrentWorkersDSTAndGeneration(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET changed_at=clock_timestamp()-interval '7 days' WHERE household_id=$1 AND module_id='chores'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	notificationChoreConsent(t, f, f.identity, "chores_due", true)
	_, occurrence := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
	t.Run("concurrent workers deduplicate same occurrence", func(t *testing.T) {
		firstJob, firstLease := choreTestLease(t, f)
		secondJob, secondLease := choreTestLease(t, f)
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		start := make(chan struct{})
		for _, pair := range [][2]string{{firstJob, firstLease}, {secondJob, secondLease}} {
			wg.Go(func() {
				<-start
				errors <- f.store.CompleteChoreWorkspaceJob(f.ctx, pair[0], pair[1])
			})
		}
		close(start)
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		notificationChoreAudience(t, f, notificationChoreOne(t, f, occurrence, f.identity.UserID), true)
	})
	t.Run("DST quiet intervals defer without extending source deadline", func(t *testing.T) {
		prefs := notificationChorePrefs(t, f, f.identity, "legacy")
		start, end := 90, 210
		prefs.Timezone, prefs.QuietStart, prefs.QuietEnd = "Europe/Vienna", &start, &end
		notificationChoreSavePrefs(t, f, f.identity, prefs)
		for _, tc := range []struct{ name, now, want string }{
			{"spring forward", "2026-03-29T00:45:00Z", "2026-03-29T01:30:00Z"},
			{"fall back", "2026-10-25T00:45:00Z", "2026-10-25T02:30:00Z"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				now, err := time.Parse(time.RFC3339, tc.now)
				if err != nil {
					t.Fatal(err)
				}
				want, err := time.Parse(time.RFC3339, tc.want)
				if err != nil {
					t.Fatal(err)
				}
				intent := notification.Intent{Schema: 1, HouseholdID: f.identity.HouseholdID, UserID: f.identity.UserID,
					Module: "chores", Generation: f.store.States()["chores"].Generation, ResourceType: "task_occurrences", ResourceID: occurrence,
					Revision: 1, Occurrence: "dst-fixture", Category: "chores_due", EventAt: now, NotBefore: now, ExpiresAt: now.Add(time.Hour)}
				tx, err := f.app.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rollback(tx)
				actual, err := f.store.NotificationNextAllowed(f.ctx, tx, intent, now)
				if err != nil || !actual.Equal(want) || !intent.ExpiresAt.Equal(now.Add(time.Hour)) {
					t.Fatal("DST quiet boundary/deadline", actual, want, intent.ExpiresAt, err)
				}
			})
		}
	})
	t.Run("completion serialized against worker cannot leave authorized stale due", func(t *testing.T) {
		// Restore ordinary quiet settings before competing for the module lock.
		notificationChoreSavePrefs(t, f, f.identity, notificationChorePrefs(t, f, f.identity, "legacy"))
		_, target := notificationChoreTask(t, f, f.identity.MembershipID, false, 5*time.Second)
		job, lease := choreTestLease(t, f)
		var version int64
		if err := f.app.QueryRow(f.ctx, `SELECT version FROM chores.task_occurrences WHERE id=$1`, target).Scan(&version); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		start := make(chan struct{})
		wg.Go(func() { <-start; failures <- f.store.CompleteChoreWorkspaceJob(ctx, job, lease) })
		wg.Go(func() {
			<-start
			_, err := f.store.MutateChoreOccurrence(ctx, f.identity, target, value.UUID(), value.UUID(), ChoreActionInput{ChoreCommand: ChoreCommand{Generation: f.store.States()["chores"].Generation, Version: version}, Action: "complete"})
			failures <- err
		})
		close(start)
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("worker/mutation serialization", err)
			}
		}
		if len(notificationChorePending(t, f, target, f.identity.UserID, "chores_due")) != 0 {
			t.Fatal("completion left a pending due alert after concurrent worker")
		}
	})
	t.Run("current module generation and deactivation fence old intents", func(t *testing.T) {
		notificationChoreRun(t, f)
		id := notificationChoreOne(t, f, occurrence, f.identity.UserID)
		intent := notificationChoreAudience(t, f, id, true)
		tx, err := f.app.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		intent.Generation++
		allowed, err := f.store.NotificationAudience(f.ctx, tx, intent)
		rollback(tx)
		if err != nil || allowed {
			t.Fatal("foreign generation authorized", allowed, err)
		}
		state := f.store.States()["chores"]
		if err = f.store.SetModule(f.ctx, f.identity, "chores", false, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		notificationChoreAudience(t, f, id, false)
	})
}
