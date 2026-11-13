package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	chore "household/internal/chores"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

// NotificationNextAllowed keeps chore delivery rules in the chore module. It
// deliberately takes no domain row/advisory locks: senders may already own jobs.
func (s *Store) NotificationNextAllowed(ctx context.Context, tx pgx.Tx, i notification.Intent, now time.Time) (time.Time, error) {
	var quiet notification.QuietHours
	var err error
	if i.Module == "chores" {
		err = tx.QueryRow(ctx, `SELECT coalesce(p.timezone,h.timezone),p.quiet_start,p.quiet_end
 FROM core.households h LEFT JOIN chores.member_preferences p ON p.household_id=h.id AND p.user_id=$2
 WHERE h.id=$1`, i.HouseholdID, i.UserID).Scan(&quiet.Timezone, &quiet.Start, &quiet.End)
	} else {
		err = tx.QueryRow(ctx, `SELECT timezone,quiet_start,quiet_end FROM core.notification_preferences
 WHERE household_id=$1 AND user_id=$2 AND category=$3`, i.HouseholdID, i.UserID, i.Category).
			Scan(&quiet.Timezone, &quiet.Start, &quiet.End)
	}
	if err != nil {
		return time.Time{}, err
	}
	return quiet.NextAllowed(now)
}

// Every chore writer already holds household/module authority and the chores
// advisory lock. Lock the three consent categories in one stable order before
// source/intent writes; the worker can then enqueue after source cancellation.
func lockChoreNotificationConsent(ctx context.Context, tx pgx.Tx, household string) error {
	rows, err := tx.Query(ctx, `SELECT p.user_id FROM core.notification_preferences p
 JOIN core.memberships m ON m.household_id=p.household_id AND m.user_id=p.user_id
 JOIN core.users u ON u.id=p.user_id
 WHERE p.household_id=$1 AND p.category IN('chores_due','chores_digest','chores_escalation')
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 ORDER BY p.user_id,p.category LIMIT 1501 FOR SHARE OF p`, household)
	if err != nil {
		return err
	}
	defer rows.Close()
	count, members := 0, 0
	previous := ""
	for rows.Next() {
		var user string
		if err = rows.Scan(&user); err != nil {
			return err
		}
		count++
		if user != previous {
			members++
			previous = user
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count > 1500 || members > 500 {
		return Invalid{"Geräte-Erinnerungen unterstützen höchstens 500 Personen je Haushalt"}
	}
	return nil
}

// Due alerts use the stored responsibility snapshot. New absences affect future
// materialization, never silently transfer an existing task to another person.
// An active claim takes precedence; an unassigned task is never broadcast.
const choreNotificationDueSource = ` FROM chores.task_occurrences o
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 JOIN chores.series_profiles p ON p.household_id=o.household_id AND p.template_id=o.template_id
 JOIN chores.occurrence_details d ON d.household_id=o.household_id AND d.occurrence_id=o.id
 JOIN core.memberships m ON m.household_id=o.household_id AND m.id=CASE
 WHEN d.claimed_by IS NOT NULL AND d.claim_expires_at>clock_timestamp() THEN d.claimed_by ELSE o.assignee_id END
 JOIN core.users u ON u.id=m.user_id
 LEFT JOIN chores.member_preferences pref ON pref.household_id=m.household_id AND pref.user_id=m.user_id `

const choreNotificationDueCurrent = ` o.household_id=$1 AND o.state='open' AND t.active
 AND(NOT p.paused OR o.due_at<=p.paused_at) AND o.due_at<=clock_timestamp()
 AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND coalesce(pref.notification_mode,'legacy')<>'off' `

func choreDueOccurrence(at time.Time) string {
	return "due/" + strconv.FormatInt(at.UnixMicro(), 10)
}

// Selection excludes already processed revisions before LIMIT, so an old page
// cannot starve later tasks. Partial multi-device acceptance also suppresses a
// second alert after a checklist-only version bump for the same deadline/user.
func enqueueChoreDueNotifications(ctx context.Context, tx pgx.Tx, household string, generation int64) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT o.id::text,o.version,o.due_at,m.user_id::text`+choreNotificationDueSource+`
 JOIN core.notification_preferences consent ON consent.household_id=o.household_id AND consent.user_id=m.user_id
 AND consent.category='chores_due' AND consent.enabled AND consent.enabled_since<=o.due_at
 JOIN core.module_states module ON module.household_id=o.household_id AND module.module_id='chores'
 AND module.state='enabled' AND module.data_generation=$2 AND module.changed_at<=o.due_at
 WHERE `+choreNotificationDueCurrent+` AND o.due_at>clock_timestamp()-interval '1 hour'
 AND NOT EXISTS(SELECT 1 FROM core.notification_intents i WHERE i.household_id=o.household_id AND i.module_id='chores'
 AND i.module_generation=$2 AND i.resource_type='task_occurrences' AND i.resource_id=o.id AND i.user_id=m.user_id
 AND i.category='chores_due' AND i.event_at=o.due_at AND(i.source_revision=o.version OR i.state='accepted'
 OR EXISTS(SELECT 1 FROM core.notification_deliveries accepted WHERE accepted.intent_id=i.id AND accepted.state='accepted')))
 ORDER BY o.due_at,o.id,m.user_id LIMIT 51`, household, generation)
	if err != nil {
		return false, err
	}
	var intents []notification.Intent
	for rows.Next() {
		i := notification.Intent{Schema: notification.Schema, HouseholdID: household, Module: "chores", Generation: generation,
			ResourceType: "task_occurrences", Category: "chores_due"}
		if err = rows.Scan(&i.ResourceID, &i.Revision, &i.EventAt, &i.UserID); err != nil {
			break
		}
		i.Occurrence, i.NotBefore, i.ExpiresAt = choreDueOccurrence(i.EventAt), i.EventAt, i.EventAt.Add(time.Hour)
		intents = append(intents, i)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	more := len(intents) > 50
	if more {
		intents = intents[:50]
	}
	for _, i := range intents {
		if _, err = EnqueueNotificationIntent(ctx, tx, i); err != nil {
			return false, err
		}
	}
	return more, nil
}

// A domain opt-in must not replay an earlier deadline. Renew only consent that
// already exists; never enable a category or alter an unrelated preference.
func renewChoreNotificationCutoff(ctx context.Context, tx pgx.Tx, household, user string, all bool) error {
	_, err := tx.Exec(ctx, `UPDATE core.notification_preferences SET enabled_since=clock_timestamp(),
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND user_id=$2 AND enabled
 AND(category='chores_escalation' OR($3 AND category IN('chores_due','chores_digest')))`, household, user, all)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH cancelled AS(SELECT id FROM core.notification_intents
 WHERE household_id=$1 AND user_id=$2 AND module_id='chores' AND state='pending'
 AND(category='chores_escalation' OR($3 AND category IN('chores_due','chores_digest')))
 ORDER BY id LIMIT 256 FOR UPDATE)
 UPDATE core.notification_intents i SET state='cancelled',version=version+1,updated_at=clock_timestamp()
 FROM cancelled WHERE i.id=cancelled.id`, household, user, all)
	return err
}

func choreNotificationAudience(ctx context.Context, tx pgx.Tx, i notification.Intent) (bool, error) {
	if i.Category == "chores_due" {
		if i.ResourceType != "task_occurrences" || i.Occurrence != choreDueOccurrence(i.EventAt) ||
			!i.ExpiresAt.Equal(i.EventAt.Add(time.Hour)) {
			return false, nil
		}
		var valid bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1`+choreNotificationDueSource+` WHERE `+choreNotificationDueCurrent+`
 AND o.id=$2 AND o.version=$3 AND o.due_at=$4 AND m.user_id=$5)`,
			i.HouseholdID, i.ResourceID, i.Revision, i.EventAt, i.UserID).Scan(&valid)
		return valid, err
	}
	return choreDigestNotificationAudience(ctx, tx, i)
}

func choreDigestNotificationAudience(ctx context.Context, tx pgx.Tx, i notification.Intent) (bool, error) {
	if i.ResourceType != "reminder_deliveries" || i.Category != "chores_digest" && i.Category != "chores_escalation" {
		return false, nil
	}
	var sourceMember, sourceUser, day, stage, timezone string
	var target *string
	var start, end *int
	var minute, afterDays int
	var escalation bool
	err := tx.QueryRow(ctx, `SELECT m.id::text,m.user_id::text,r.local_day::text,r.stage,p.timezone,p.digest_minute,
 p.quiet_start,p.quiet_end,p.escalation_enabled,p.escalation_after_days,p.escalation_recipient_id::text
 FROM chores.reminder_deliveries r JOIN chores.member_preferences p ON p.household_id=r.household_id AND p.user_id::text=r.source_key
 JOIN core.memberships m ON m.household_id=p.household_id AND m.user_id=p.user_id JOIN core.users u ON u.id=m.user_id
 WHERE r.household_id=$1 AND r.id=$2 AND r.module_generation=$3 AND r.recipient_user_id=$4 AND r.notification_id IS NOT NULL
 AND p.version=$5 AND p.notification_mode='digest' AND p.digest_enabled
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`,
		i.HouseholdID, i.ResourceID, i.Generation, i.UserID, i.Revision).
		Scan(&sourceMember, &sourceUser, &day, &stage, &timezone, &minute, &start, &end, &escalation, &afterDays, &target)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return false, err
	}
	eventAt, err := chore.DigestInstant(day, minute, start, end, location)
	if err != nil {
		return false, err
	}
	policy, _ := notification.Lookup(i.Category)
	if i.Category != "chores_"+stage || i.Occurrence != stage+"/"+day || !i.EventAt.Equal(eventAt) ||
		!i.ExpiresAt.Equal(eventAt.Add(policy.Freshness)) {
		return false, nil
	}
	if stage == "digest" {
		if i.UserID != sourceUser {
			return false, nil
		}
		var active bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.task_occurrences o
 JOIN chores.series_profiles p ON p.household_id=o.household_id AND p.template_id=o.template_id
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 JOIN chores.occurrence_details d ON d.household_id=o.household_id AND d.occurrence_id=o.id
 WHERE o.household_id=$1 AND o.state='open' AND o.due_at<=clock_timestamp() AND t.active
 AND(NOT p.paused OR o.due_at<=p.paused_at)
 AND(o.assignee_id=$2 OR d.claimed_by=$2 AND d.claim_expires_at>clock_timestamp()))`, i.HouseholdID, sourceMember).Scan(&active)
		return active, err
	}
	if !escalation || target == nil {
		return false, nil
	}
	var accepted bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 JOIN chores.member_preferences p ON p.household_id=m.household_id AND p.user_id=m.user_id
 WHERE m.household_id=$1 AND m.id=$2 AND m.user_id=$3 AND m.inactive_at IS NULL AND u.disabled_at IS NULL
 AND NOT u.must_change_password AND p.allow_escalations AND p.notification_mode<>'off')`, i.HouseholdID, *target, i.UserID).Scan(&accepted)
	if err != nil || !accepted {
		return false, err
	}
	date, err := chore.ParseDay(day)
	if err != nil {
		return false, err
	}
	cutoff, err := choreAtDay(date.AddDate(0, 0, -afterDays).Format(time.DateOnly), timezone)
	if err != nil {
		return false, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.task_occurrences o
 JOIN chores.series_profiles p ON p.household_id=o.household_id AND p.template_id=o.template_id
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 WHERE o.household_id=$1 AND o.assignee_id=$2 AND o.state='open' AND o.due_at<$3 AND t.active
 AND(NOT p.paused OR o.due_at<=p.paused_at))`, i.HouseholdID, sourceMember, cutoff).Scan(&active)
	return active, err
}

// Recheck pending chore work after a mutation. Source revision/mode/ownership
// changes are selected before LIMIT, avoiding valid rows starving invalid ones.
// The sender repeats the complete current audience check for every delivery,
// including any backlog above this bounded mutation cleanup batch.
func reconcileChoreNotificationIntents(ctx context.Context, tx pgx.Tx, household string) error {
	_, err := tx.Exec(ctx, `WITH invalid AS(SELECT i.id FROM core.notification_intents i
 WHERE i.household_id=$1 AND i.module_id='chores' AND i.state='pending' AND(
 (i.category='chores_due' AND NOT EXISTS(SELECT 1 FROM chores.task_occurrences o
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 JOIN chores.series_profiles p ON p.household_id=o.household_id AND p.template_id=o.template_id
 JOIN chores.occurrence_details d ON d.household_id=o.household_id AND d.occurrence_id=o.id
 JOIN core.memberships m ON m.household_id=o.household_id AND m.id=CASE WHEN d.claimed_by IS NOT NULL
 AND d.claim_expires_at>clock_timestamp() THEN d.claimed_by ELSE o.assignee_id END
 LEFT JOIN chores.member_preferences pref ON pref.household_id=m.household_id AND pref.user_id=m.user_id
 WHERE o.household_id=i.household_id AND o.id=i.resource_id AND o.version=i.source_revision AND o.due_at=i.event_at
 AND o.state='open' AND t.active AND(NOT p.paused OR o.due_at<=p.paused_at) AND m.user_id=i.user_id
 AND m.inactive_at IS NULL AND m.role IN('admin','editor')
 AND EXISTS(SELECT 1 FROM core.users u WHERE u.id=m.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password)
 AND coalesce(pref.notification_mode,'legacy')<>'off'))
 OR(i.category IN('chores_digest','chores_escalation') AND NOT EXISTS(SELECT 1 FROM chores.reminder_deliveries r
 JOIN chores.member_preferences pref ON pref.household_id=r.household_id AND pref.user_id::text=r.source_key
 WHERE r.household_id=i.household_id AND r.id=i.resource_id AND r.recipient_user_id=i.user_id
 AND pref.version=i.source_revision AND pref.notification_mode='digest' AND pref.digest_enabled
 AND EXISTS(SELECT 1 FROM core.memberships source JOIN core.users source_user ON source_user.id=source.user_id
 WHERE source.household_id=i.household_id AND source.user_id=pref.user_id AND source.inactive_at IS NULL
 AND source_user.disabled_at IS NULL AND NOT source_user.must_change_password
 AND EXISTS(SELECT 1 FROM chores.task_occurrences o
 JOIN chores.series_profiles p ON p.household_id=o.household_id AND p.template_id=o.template_id
 JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 JOIN chores.occurrence_details d ON d.household_id=o.household_id AND d.occurrence_id=o.id
 WHERE o.household_id=i.household_id AND o.state='open' AND o.due_at<=clock_timestamp() AND t.active
 AND(NOT p.paused OR o.due_at<=p.paused_at)
 AND((i.category='chores_digest' AND(o.assignee_id=source.id OR d.claimed_by=source.id AND d.claim_expires_at>clock_timestamp()))
 OR(i.category='chores_escalation' AND o.assignee_id=source.id
 AND o.due_at<((r.local_day-pref.escalation_after_days)::timestamp AT TIME ZONE pref.timezone)))))
 AND(i.category='chores_digest' OR(pref.escalation_enabled AND EXISTS(SELECT 1 FROM core.memberships target
 JOIN chores.member_preferences receiver ON receiver.household_id=target.household_id AND receiver.user_id=target.user_id
 WHERE target.household_id=i.household_id AND target.id=pref.escalation_recipient_id AND target.user_id=i.user_id
 AND target.inactive_at IS NULL
 AND EXISTS(SELECT 1 FROM core.users u WHERE u.id=target.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password)
 AND receiver.allow_escalations AND receiver.notification_mode<>'off'))))))
 ORDER BY i.id LIMIT 256 FOR UPDATE OF i)
 UPDATE core.notification_intents i SET state='cancelled',version=version+1,updated_at=clock_timestamp()
 FROM invalid WHERE i.id=invalid.id`, household)
	return err
}
