package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/securityevent"

	"github.com/jackc/pgx/v5"
)

func securityNotificationReference(i notification.Intent) bool {
	return i.Validate() == nil && i.Category == "security" && i.Module == "" && i.Generation == 0 &&
		i.ResourceType == "security_event" && i.Revision == 1 && securityevent.ValidKind(i.Occurrence) &&
		i.NotBefore.Equal(i.EventAt) && i.ExpiresAt.Equal(i.EventAt.Add(time.Hour))
}

func securityNotificationIntent(ctx context.Context, tx pgx.Tx, household, event string) (*notification.Intent, error) {
	var user, kind string
	var occurred, now time.Time
	err := tx.QueryRow(ctx, `SELECT n.user_id::text,n.kind,n.created_at,clock_timestamp()
 FROM core.notifications n JOIN core.memberships m ON m.household_id=n.household_id AND m.user_id=n.user_id
 JOIN core.users u ON u.id=n.user_id WHERE n.id=$1 AND n.household_id=$2 AND n.module_id IS NULL
 AND n.dismissed_at IS NULL AND n.title='Sicherheitsereignis' AND n.target_path='/settings/security'
 AND n.dedupe_key='security-event/'||n.id::text
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, event, household).
		Scan(&user, &kind, &occurred, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	occurrence, found := strings.CutPrefix(kind, "security_")
	if !found || !securityevent.ValidKind(occurrence) || occurred.After(now) || !occurred.Add(time.Hour).After(now) {
		return nil, nil
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: user,
		ResourceType: "security_event", ResourceID: event, Revision: 1, Occurrence: occurrence, Category: "security",
		EventAt: occurred, NotBefore: occurred, ExpiresAt: occurred.Add(time.Hour)}, nil
}

func securityNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if !securityNotificationReference(i) {
		return time.Time{}, nil
	}
	candidate, err := securityNotificationIntent(ctx, tx, i.HouseholdID, i.ResourceID)
	if err != nil || candidate == nil {
		return time.Time{}, err
	}
	if candidate.UserID != i.UserID || candidate.Occurrence != i.Occurrence || !candidate.EventAt.Equal(i.EventAt) {
		return time.Time{}, nil
	}
	return candidate.EventAt, nil
}

// StageSecurityNotifications consumes only atomically recorded source jobs, never
// scans old inbox history. Ineligible/expired events retain their personal inbox.
func (s *Store) StageSecurityNotifications(ctx context.Context, limit int) (int, error) {
	ctx, cancel := context.WithTimeout(audit.WithSystem(ctx, "worker"), 15*time.Second)
	defer cancel()
	type sourceJob struct{ id, household string }
	rows, err := s.Pool.Query(ctx, `SELECT id::text,household_id::text FROM core.jobs
 WHERE kind=$1 AND state='pending' AND available_at<=clock_timestamp() AND attempts<max_attempts
 ORDER BY available_at,id LIMIT $2`, securityevent.JobKind, max(1, min(limit, 50)))
	if err != nil {
		return 0, err
	}
	jobs := make([]sourceJob, 0, max(1, min(limit, 50)))
	for rows.Next() {
		var job sourceJob
		if err = rows.Scan(&job.id, &job.household); err != nil {
			break
		}
		jobs = append(jobs, job)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	completed := 0
	var failures []error
	for _, job := range jobs {
		if ctx.Err() != nil {
			return completed, errors.Join(append(failures, ctx.Err())...)
		}
		done, stageErr := s.stageSecurityNotificationJob(audit.WithHousehold(ctx, job.household), job.household, job.id)
		if stageErr == nil {
			if done {
				completed++
			}
			continue
		}
		// The transaction rolled back: a successful competitor cannot be reset.
		cleanup, stop := context.WithTimeout(context.WithoutCancel(audit.WithHousehold(ctx, job.household)), 3*time.Second)
		_, retryErr := audit.Exec(cleanup, s.Pool, `UPDATE core.jobs SET attempts=attempts+1,
 state=CASE WHEN attempts+1>=max_attempts THEN 'failed' ELSE 'pending' END,
 available_at=clock_timestamp()+make_interval(secs=>least(3600,30*power(2,least(attempts,7))::integer)),
 finished_at=CASE WHEN attempts+1>=max_attempts THEN clock_timestamp() ELSE NULL END,
 last_error='security notification source failed',lease_token=NULL,lease_until=NULL
 WHERE id=$1 AND household_id=$2 AND kind=$3 AND state='pending' AND available_at<=clock_timestamp()
 AND attempts<max_attempts`, job.id, job.household, securityevent.JobKind)
		stop()
		failures = append(failures, errors.Join(stageErr, retryErr))
	}
	return completed, errors.Join(failures...)
}

func (s *Store) stageSecurityNotificationJob(ctx context.Context, household, job string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", household); err != nil {
		return false, err
	}
	var raw []byte
	var key string
	var version int
	var unscoped bool
	err = tx.QueryRow(ctx, `SELECT CASE WHEN octet_length(payload::text)<=128 THEN payload END,dedupe_key,payload_version,
 module_id IS NULL AND module_generation IS NULL FROM core.jobs WHERE id=$1 AND household_id=$2 AND kind=$3
 AND state='pending' AND available_at<=clock_timestamp() AND attempts<max_attempts FOR UPDATE SKIP LOCKED`, job, household, securityevent.JobKind).Scan(&raw, &key, &version, &unscoped)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var payload struct {
		EventID string `json:"event_id"`
	}
	if version != 1 || !unscoped || shoppingDecodeClosed(raw, &payload) != nil || !shoppingCanonicalUUID(payload.EventID) || key != "security-event-job/"+payload.EventID {
		return false, ErrConflict
	}
	intent, err := securityNotificationIntent(ctx, tx, household, payload.EventID)
	if err != nil {
		return false, err
	}
	if intent != nil {
		var consent bool
		err = tx.QueryRow(ctx, `SELECT true FROM core.notification_preferences WHERE household_id=$1 AND user_id=$2
 AND category='security' AND enabled AND enabled_since<=$3 FOR SHARE`, household, intent.UserID, intent.EventAt).Scan(&consent)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		if consent {
			if _, err = EnqueueNotificationIntent(ctx, tx, *intent); err != nil {
				return false, err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE core.jobs SET state='done',attempts=attempts+1,finished_at=clock_timestamp(),
 last_error=NULL,lease_token=NULL,lease_until=NULL WHERE id=$1`, job)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
