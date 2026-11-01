package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const ChoreWorkspaceJobKind = "chores_workspace"

func (s *Store) ScheduleChoreWorkspace(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := audit.Exec(audit.WithSystem(ctx, "worker"), s.Pool, `INSERT INTO core.jobs(household_id,module_id,module_generation,kind,dedupe_key)
 SELECT state.household_id,'chores',state.data_generation,$1,
 'chores-workspace/'||state.household_id::text||'/'||state.data_generation::text||'/'||to_char(clock_timestamp(),'YYYY-MM-DD-HH24-MI')
 FROM core.module_states state WHERE state.module_id='chores' AND state.state='enabled'
 AND(EXISTS(SELECT 1 FROM chores.schedule_state schedule JOIN chores.task_templates t ON t.id=schedule.template_id
 JOIN chores.series_profiles p ON p.template_id=t.id WHERE t.household_id=state.household_id AND t.active AND p.managed
 AND NOT p.review_required AND schedule.module_generation=state.data_generation AND schedule.next_check_at<=clock_timestamp()
 AND(NOT p.paused OR p.resume_at<=clock_timestamp()))
 OR EXISTS(SELECT 1 FROM chores.series_profiles p JOIN chores.task_templates t ON t.id=p.template_id
 WHERE p.household_id=state.household_id AND t.active AND p.paused AND p.resume_at<=clock_timestamp())
 OR EXISTS(SELECT 1 FROM chores.member_preferences pref WHERE pref.household_id=state.household_id AND pref.notification_mode='digest')
 OR EXISTS(SELECT 1 FROM core.reminders r WHERE r.household_id=state.household_id AND r.module_id='chores'
 AND r.module_generation=state.data_generation AND r.state='pending' AND r.scheduled_at<=clock_timestamp()))
 AND NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.dedupe_key='chores-workspace/'||state.household_id::text||'/'||state.data_generation::text||'/'||to_char(clock_timestamp(),'YYYY-MM-DD-HH24-MI'))
 ORDER BY state.household_id LIMIT 50 ON CONFLICT(dedupe_key) DO NOTHING`, ChoreWorkspaceJobKind)
	return err
}

// The module lock and household advisory lock precede every reminder, series
// and job row lock. The real lease is checked again against the wall clock at
// commit; a lost lease rolls back every generated task and notification.
func (s *Store) CompleteChoreWorkspaceJob(ctx context.Context, job, lease string) error {
	if !value.ValidUUID(job) || !value.ValidUUID(lease) {
		return ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, choreOperationTimeout)
	defer cancel()
	ctx = audit.WithSystem(ctx, "worker")
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var household string
	err = tx.QueryRow(ctx, `SELECT household_id FROM core.jobs WHERE id=$1 AND lease_token=$2 AND state='running'
 AND lease_until>clock_timestamp() AND kind=$3`, job, lease, ChoreWorkspaceJobKind).Scan(&household)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return err
	}
	var generation int64
	var enabled string
	if err = tx.QueryRow(ctx, `SELECT data_generation,state FROM core.module_states WHERE household_id=$1 AND module_id='chores' FOR SHARE`, household).Scan(&generation, &enabled); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('household/chores/'||$1::text,0))`, household); err != nil {
		return err
	}
	var jobGeneration int64
	var cursor string
	err = tx.QueryRow(ctx, `SELECT module_generation,coalesce(payload->>'member_cursor','') FROM core.jobs WHERE id=$1 AND lease_token=$2
 AND state='running' AND lease_until>clock_timestamp() AND kind=$3 AND module_id='chores' AND household_id=$4 FOR UPDATE`, job, lease, ChoreWorkspaceJobKind, household).Scan(&jobGeneration, &cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if enabled != "enabled" || generation != jobGeneration {
		return ErrConflict
	}
	if cursor != "" && !value.ValidUUID(cursor) {
		return Invalid{"Ungültige Aufgabenjob-Seite"}
	}
	ctx = audit.WithHousehold(ctx, household)
	if err = audit.Bind(ctx, tx); err != nil {
		return err
	}
	series, err := choreRows(ctx, tx, 5, `SELECT jsonb_build_object('id',t.id,'created_by',t.created_by,'paused',p.paused)
 FROM chores.series_profiles p JOIN chores.task_templates t ON t.id=p.template_id LEFT JOIN chores.schedule_state schedule ON schedule.template_id=t.id
 WHERE t.household_id=$1 AND t.active AND
 ((p.paused AND p.resume_at<=clock_timestamp() AND(NOT p.managed OR schedule.module_generation=$2))
 OR(p.managed AND NOT p.review_required AND NOT p.paused AND schedule.module_generation=$2 AND schedule.next_check_at<=clock_timestamp()))
 ORDER BY coalesce(p.resume_at,schedule.next_check_at),t.id LIMIT 5`, household, generation)
	if err != nil {
		return err
	}
	more := len(series) > 4
	if more {
		series = series[:4]
	}
	for _, template := range series {
		if template["paused"] == true {
			if err = resumeChoreSeries(ctx, tx, household, String(template["id"]), ""); err != nil {
				return err
			}
		}
		provenance := auth.Identity{HouseholdID: household, UserID: String(template["created_by"])}
		result, runErr := s.materializeChoreSeries(ctx, tx, provenance, String(template["id"]), generation, choreGenerationBatch, true)
		if runErr != nil {
			return runErr
		}
		more = more || result["backlog"] == true
	}
	legacyMore, err := choreDeliverLegacyReminders(ctx, tx, household, generation)
	if err != nil {
		return err
	}
	more = more || legacyMore
	next, err := choreDeliverDigests(ctx, tx, household, generation, cursor)
	if err != nil {
		return err
	}
	more = more || next != ""
	state := "done"
	if more {
		state = "pending"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,finished_at=CASE WHEN $3='done' THEN clock_timestamp() END,
 available_at=clock_timestamp()+interval '30 seconds',lease_token=NULL,lease_until=NULL,attempts=0,
 payload=jsonb_build_object('member_cursor',$4::text)
 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>clock_timestamp()`, job, lease, state, next)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func choreDeliverLegacyReminders(ctx context.Context, tx pgx.Tx, household string, generation int64) (bool, error) {
	items, err := choreRows(ctx, tx, 51, `SELECT jsonb_build_object('id',r.id,'resource_id',r.resource_id,'recipient',r.recipient_user_id,'dedupe_key',r.dedupe_key)
 FROM core.reminders r WHERE r.household_id=$1 AND r.module_id='chores' AND r.module_generation=$2
 AND r.state='pending' AND r.scheduled_at<=clock_timestamp() ORDER BY r.scheduled_at,r.id LIMIT 51 FOR UPDATE`, household, generation)
	if err != nil {
		return false, err
	}
	more := len(items) > 50
	if more {
		items = items[:50]
	}
	for _, item := range items {
		var current bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.task_occurrences o JOIN chores.task_templates t ON t.id=o.template_id
 JOIN chores.series_profiles p ON p.template_id=t.id WHERE o.household_id=$1 AND o.id=$2 AND o.state='open' AND t.active
 AND NOT p.managed AND(NOT p.paused OR o.due_at<=p.paused_at))`, household, item["resource_id"]).Scan(&current); err != nil {
			return false, err
		}
		if !current {
			if _, err = tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE id=$1`, item["id"]); err != nil {
				return false, err
			}
			continue
		}
		var audienceCount int
		deliverErr := tx.QueryRow(ctx, `WITH audience AS(SELECT member.household_id,member.user_id
 FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 LEFT JOIN chores.member_preferences pref ON pref.household_id=member.household_id AND pref.user_id=member.user_id
 WHERE member.household_id=$1 AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND($2::uuid IS NULL OR member.user_id=$2) AND coalesce(pref.notification_mode,'legacy')='legacy'
 ORDER BY member.id LIMIT 501 FOR SHARE OF member,u)
 ,inserted AS(INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 SELECT member.household_id,member.user_id,'chores','task_due','Eine Haushaltsaufgabe ist fällig',$3,$4||'/'||member.user_id::text
 FROM audience member
 ON CONFLICT(dedupe_key) DO NOTHING RETURNING id) SELECT count(*) FROM audience`, household, item["recipient"], "/m/chores/workspace/work/"+String(item["resource_id"]), item["dedupe_key"]).Scan(&audienceCount)
		if deliverErr != nil {
			return false, deliverErr
		}
		if audienceCount > 500 {
			return false, Invalid{"Einzel-Erinnerung überschreitet 500 Empfänger; persönliche Zusammenfassungen wählen"}
		}
		if _, err = tx.Exec(ctx, `UPDATE core.reminders SET state='delivered',delivered_at=clock_timestamp() WHERE id=$1`, item["id"]); err != nil {
			return false, err
		}
	}
	return more, nil
}

func choreMinute(record Record, key string) *int {
	if record[key] == nil {
		return nil
	}
	n := int(Int(record[key]))
	return &n
}

func choreDeliverDigests(ctx context.Context, tx pgx.Tx, household string, generation int64, cursor string) (string, error) {
	prefs, err := choreRows(ctx, tx, 51, `SELECT to_jsonb(pref)||jsonb_build_object('membership_id',member.id)
 FROM chores.member_preferences pref JOIN core.memberships member ON member.household_id=pref.household_id AND member.user_id=pref.user_id
 JOIN core.users u ON u.id=pref.user_id WHERE pref.household_id=$1 AND pref.notification_mode='digest' AND member.inactive_at IS NULL
 AND u.disabled_at IS NULL AND NOT u.must_change_password AND($2='' OR pref.user_id>nullif($2,'')::uuid) ORDER BY pref.user_id LIMIT 51 FOR SHARE OF member,u`, household, cursor)
	if err != nil {
		return "", err
	}
	next := ""
	if len(prefs) > 50 {
		prefs = prefs[:50]
		next = String(prefs[49]["user_id"])
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", err
	}
	for _, pref := range prefs {
		location, loadErr := time.LoadLocation(String(pref["timezone"]))
		if loadErr != nil {
			return "", loadErr
		}
		local := now.In(location)
		quietStart, quietEnd := choreMinute(pref, "quiet_start"), choreMinute(pref, "quiet_end")
		if chore.QuietAt(local.Hour()*60+local.Minute(), quietStart, quietEnd) {
			continue
		}
		created, parseErr := choreInstant(pref["created_at"])
		if parseErr != nil {
			return "", parseErr
		}
		for _, offset := range []int{-1, 0} {
			day := local.AddDate(0, 0, offset).Format(time.DateOnly)
			due, resolveErr := chore.DigestInstant(day, int(Int(pref["digest_minute"])), quietStart, quietEnd, location)
			if resolveErr != nil {
				return "", resolveErr
			}
			if due.After(now) || due.Before(created) {
				continue
			}
			if err = choreDigestForDay(ctx, tx, household, generation, pref, day, now); err != nil {
				return "", err
			}
		}
	}
	return next, nil
}

func choreDigestForDay(ctx context.Context, tx pgx.Tx, household string, generation int64, pref Record, day string, now time.Time) error {
	var count int64
	err := tx.QueryRow(ctx, `SELECT count(*) FROM chores.task_occurrences o JOIN chores.series_profiles p ON p.template_id=o.template_id
 JOIN chores.task_templates t ON t.id=o.template_id JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 WHERE o.household_id=$1 AND o.state='open' AND o.due_at<=$3 AND t.active AND(NOT p.paused OR o.due_at<=p.paused_at)
 AND(o.assignee_id=$2::uuid OR d.claimed_by=$2::uuid AND d.claim_expires_at>$3)`, household, pref["membership_id"], now).Scan(&count)
	if err != nil {
		return err
	}
	source := String(pref["user_id"])
	if err = choreNotifyOnce(ctx, tx, household, generation, String(pref["user_id"]), day, "digest", source, source, count,
		fmt.Sprintf("%d fällige Haushaltsaufgaben für dich", count)); err != nil {
		return err
	}
	if pref["escalation_enabled"] != true {
		return nil
	}
	var target, userID, timezone string
	var start, end *int
	err = tx.QueryRow(ctx, `SELECT member.id,member.user_id,receiver.timezone,receiver.quiet_start,receiver.quiet_end
 FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 JOIN chores.member_preferences receiver ON receiver.household_id=member.household_id AND receiver.user_id=member.user_id
 WHERE member.household_id=$1 AND member.id=$2 AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND receiver.allow_escalations AND receiver.notification_mode<>'off' FOR SHARE OF member,u,receiver`, household, pref["escalation_recipient_id"]).Scan(&target, &userID, &timezone, &start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	local := now.In(location)
	if chore.QuietAt(local.Hour()*60+local.Minute(), start, end) {
		return nil
	}
	date, _, err := choreLocalDay(now, String(pref["timezone"]))
	if err != nil {
		return err
	}
	parsed, err := chore.ParseDay(date)
	if err != nil {
		return err
	}
	cutoff, err := choreAtDay(parsed.AddDate(0, 0, -int(Int(pref["escalation_after_days"]))).Format(time.DateOnly), String(pref["timezone"]))
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM chores.task_occurrences o JOIN chores.series_profiles p ON p.template_id=o.template_id
 JOIN chores.task_templates t ON t.id=o.template_id WHERE o.household_id=$1 AND o.assignee_id=$2 AND o.state='open'
 AND o.due_at<$3 AND t.active AND(NOT p.paused OR o.due_at<=p.paused_at)`, household, pref["membership_id"], cutoff).Scan(&count)
	if err != nil {
		return err
	}
	return choreNotifyOnce(ctx, tx, household, generation, userID, day, "escalation", source, source, count,
		fmt.Sprintf("%d länger überfällige Aufgaben – ausdrücklich freigegebene Erinnerung", count))
}

func choreNotifyOnce(ctx context.Context, tx pgx.Tx, household string, generation int64, user, day, stage, source, creator string, count int64, title string) error {
	var notificationID any
	if count > 0 {
		notificationID = value.UUID()
	}
	var deliveryID string
	err := tx.QueryRow(ctx, `INSERT INTO chores.reminder_deliveries(household_id,module_generation,recipient_user_id,local_day,stage,source_key,created_by,notification_id)
 VALUES($1,$2,$3,$4::date,$5,$6,$7,$8) ON CONFLICT DO NOTHING RETURNING id`, household, generation, user, day, stage, source, creator, notificationID).Scan(&deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.notifications(id,household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 VALUES($6,$1,$2,'chores',$3,$4,'/m/chores/workspace/today',$5) ON CONFLICT(dedupe_key) DO NOTHING`, household, user, "chores_"+stage, title, "chores-delivery/"+deliveryID, notificationID)
	return err
}
