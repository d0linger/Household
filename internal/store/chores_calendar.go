package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/ical"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type ChoreFeedInput struct {
	ChoreCommand
	Action    string `json:"action"`
	Scope     string `json:"scope"`
	DaysBack  int    `json:"days_back"`
	DaysAhead int    `json:"days_ahead"`
}

const choreFeedMetadata = `jsonb_build_object('id',f.id,'scope',f.scope,'days_back',f.days_back,'days_ahead',f.days_ahead,
 'expires_at',f.expires_at,'revoked_at',f.revoked_at,'created_at',f.created_at,'version',f.version,
 'active',f.revoked_at IS NULL AND f.expires_at>transaction_timestamp())`

func (s *Store) ChoreCalendarFeeds(ctx context.Context, id auth.Identity) (Record, error) {
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	feeds, err := choreRows(scope.ctx, scope.tx, 101, `SELECT `+choreFeedMetadata+` FROM chores.calendar_feeds f WHERE f.household_id=$1 AND f.user_id=$2
 AND f.module_generation=$3 ORDER BY (f.revoked_at IS NULL AND f.expires_at>transaction_timestamp()) DESC,f.created_at DESC,f.id LIMIT 101`, id.HouseholdID, id.UserID, scope.generation)
	if err != nil {
		return nil, err
	}
	more := len(feeds) > 100
	if more {
		feeds = feeds[:100]
	}
	return Record{"items": feeds, "more": more, "generation": scope.generation}, nil
}

func (s *Store) MutateChoreCalendarFeed(ctx context.Context, id auth.Identity, feedID, key, requestID string, in ChoreFeedInput) (Record, error) {
	if in.Action != "create" && in.Action != "revoke" || in.Action == "create" && (feedID != "" || in.Version != 0 || in.Scope != "mine" && in.Scope != "household" || in.DaysBack < 0 || in.DaysBack > 31 || in.DaysAhead < 1 || in.DaysAhead > 90) ||
		in.Action == "revoke" && (!value.ValidUUID(feedID) || in.Version < 1) {
		return nil, Invalid{"Kalenderfreigabe und Umfang prüfen"}
	}
	var token string
	result, err := s.choreWrite(ctx, id, key, requestID, "chores/workspace/calendar/"+in.Action+"/"+feedID, in.ChoreCommand, in, true, nil, func(ctx context.Context, tx pgx.Tx) (Record, error) {
		if in.Action == "revoke" {
			feed, err := choreObject(ctx, tx, `UPDATE chores.calendar_feeds f SET revoked_at=coalesce(revoked_at,clock_timestamp()),
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND module_generation=$5
 RETURNING `+choreFeedMetadata, id.HouseholdID, id.UserID, feedID, in.Version, in.Generation)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrConflict
			}
			return Record{"feed": feed, "generation": in.Generation}, err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM chores.calendar_feeds WHERE household_id=$1 AND user_id=$2 AND module_generation=$3
 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, id.HouseholdID, id.UserID, in.Generation).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 20 {
			return nil, Invalid{"Höchstens 20 aktive Kalenderfreigaben; alte Freigaben zuerst widerrufen"}
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		token = base64.RawURLEncoding.EncodeToString(secret)
		digest := sha256.Sum256([]byte(token))
		feed, err := choreObject(ctx, tx, `INSERT INTO chores.calendar_feeds AS f(household_id,user_id,issuer_membership_id,issuer_role,
 token_hash,auth_version,module_generation,scope,days_back,days_ahead,expires_at,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()+interval '90 days',$2) RETURNING `+choreFeedMetadata,
			id.HouseholdID, id.UserID, id.MembershipID, id.Role, digest[:], id.AuthVersion, in.Generation, in.Scope, in.DaysBack, in.DaysAhead)
		return Record{"feed": feed, "generation": in.Generation}, err
	})
	if err != nil {
		return nil, err
	}
	if in.Action == "create" {
		if token == "" {
			result["secret_unavailable"] = true
		} else {
			result["url"] = "/calendar/chores/" + token + ".ics"
			result["secret_shown_once"] = true
		}
	}
	return result, nil
}

func (s *Store) ChoreCalendarDownload(ctx context.Context, id auth.Identity, selection string, back, ahead int) ([]ical.TimedEvent, error) {
	if selection != "mine" && selection != "household" || back < 0 || back > 31 || ahead < 1 || ahead > 90 {
		return nil, Invalid{"Kalenderumfang prüfen"}
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	return readChoreCalendar(scope.ctx, scope.tx, id.HouseholdID, id.MembershipID, selection, scope.timezone, scope.now, scope.generation, back, ahead)
}

func (s *Store) ChoreCalendarFeed(ctx context.Context, token string) ([]ical.TimedEvent, error) {
	if len(token) != 43 || strings.ContainsAny(token, " \t\r\n") {
		return nil, ErrNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, choreReadTimeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	digest := sha256.Sum256([]byte(token))
	var household, member, selection, timezone string
	var now time.Time
	var generation int64
	var back, ahead int
	err = tx.QueryRow(ctx, `SELECT f.household_id,f.issuer_membership_id,f.scope,h.timezone,transaction_timestamp(),f.module_generation,f.days_back,f.days_ahead
 FROM chores.calendar_feeds f JOIN core.users u ON u.id=f.user_id JOIN core.households h ON h.id=f.household_id
 JOIN core.memberships m ON m.id=f.issuer_membership_id AND m.household_id=f.household_id AND m.user_id=f.user_id
 JOIN core.module_states state ON state.household_id=f.household_id AND state.module_id='chores'
 WHERE f.token_hash=$1 AND f.revoked_at IS NULL AND f.expires_at>transaction_timestamp()
 AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=f.auth_version
 AND m.inactive_at IS NULL AND m.role=f.issuer_role AND state.state='enabled' AND state.data_generation=f.module_generation`, digest[:]).Scan(&household, &member, &selection, &timezone, &now, &generation, &back, &ahead)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return readChoreCalendar(ctx, tx, household, member, selection, timezone, now, generation, back, ahead)
}

func readChoreCalendar(ctx context.Context, tx pgx.Tx, household, member, selection, timezone string, now time.Time, generation int64, back, ahead int) ([]ical.TimedEvent, error) {
	_, location, err := choreLocalDay(now, timezone)
	if err != nil {
		return nil, err
	}
	today := now.In(location)
	begin, err := choreAtDay(today.AddDate(0, 0, -back).Format(time.DateOnly), timezone)
	if err != nil {
		return nil, err
	}
	end, err := choreAtDay(today.AddDate(0, 0, ahead+1).Format(time.DateOnly), timezone)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o.due_at,coalesce(v.estimated_minutes,30),left(coalesce(d.overrides->>'title',v.title),500),
 o.version+p.version+t.version,greatest(o.updated_at,p.updated_at,t.updated_at),
 o.state='skipped' OR NOT t.active OR p.paused AND o.due_at>p.paused_at,o.state='completed'
 FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 JOIN chores.template_versions v ON v.id=d.template_version_id JOIN chores.series_profiles p ON p.template_id=o.template_id
 JOIN chores.task_templates t ON t.id=o.template_id
 WHERE o.household_id=$1 AND (o.due_at>=$2 AND o.due_at<$3 OR d.original_due_at>=$2 AND d.original_due_at<$3)
 AND($4='household' OR o.assignee_id=$5::uuid OR d.claimed_by=$5::uuid AND d.claim_expires_at>transaction_timestamp())
 ORDER BY o.due_at,o.id LIMIT 2001`, household, begin, end, selection, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []ical.TimedEvent{}
	for rows.Next() {
		var id string
		var minutes int
		var event ical.TimedEvent
		if err = rows.Scan(&id, &event.Start, &minutes, &event.Summary, &event.Sequence, &event.Modified, &event.Cancelled, &event.Completed); err != nil {
			return nil, err
		}
		if len(events) == 2000 {
			return nil, Invalid{"Kalender enthält mehr als 2000 Termine; Umfang eingrenzen"}
		}
		event.UID = fmt.Sprintf("chores-%s-g%d-%s@household.local", household, generation, id)
		event.End = event.Start.Add(time.Duration(minutes) * time.Minute)
		events = append(events, event)
	}
	return events, rows.Err()
}
