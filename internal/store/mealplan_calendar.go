package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/ical"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const planningCalendarCancellations = 512

type PlanningCalendarInput struct {
	PlanningExtensionCommand
	Action  string `json:"action"`
	Version int64  `json:"version,omitempty"`
}

type PlanningCalendar struct {
	Week   string
	Events []ical.Event
}

const planningFeedMetadata = `jsonb_build_object('id',f.id,'week_start',f.week_start,
 'expires_at',f.expires_at,'revoked_at',f.revoked_at,'created_at',f.created_at,
 'version',f.version,'issuer_user_id',f.issuer_user_id,
 'active',f.revoked_at IS NULL AND f.expires_at>now(),
 'can_revoke',f.issuer_user_id=$3::uuid OR $4::text='admin')`

func (s *Store) PlanningCalendarFeeds(ctx context.Context, id auth.Identity, pid, week string) (Record, error) {
	tx, plan, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err = planningExtensionWindow(plan, week); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+planningFeedMetadata+` FROM mealplan.calendar_feeds f
 WHERE f.household_id=$1 AND f.plan_id=$2 AND f.issuer_user_id=$3
 AND f.module_generation=$5 AND f.week_start=$6::date
 ORDER BY (f.revoked_at IS NULL AND f.expires_at>now()) DESC,f.created_at DESC,f.id LIMIT 101`, id.HouseholdID, pid, id.UserID, id.Role, generation, week)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	feeds := []Record{}
	more := false
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(feeds) == 100 {
			more = true
			continue
		}
		feed, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		feeds = append(feeds, feed)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return Record{"generation": generation, "plan_version": Int(plan["version"]), "week": week, "feeds": feeds, "more": more}, nil
}

// MutatePlanningCalendarFeed persists only a capability hash. Even an
// idempotency receipt contains metadata only: a lost first response requires
// explicit revoke/recreate, rather than retaining a recoverable bearer secret.
func (s *Store) MutatePlanningCalendarFeed(ctx context.Context, id auth.Identity, pid, fid, key, requestID string, in PlanningCalendarInput) (Record, error) {
	if in.Action != "create" && in.Action != "revoke" || in.Action == "create" && fid != "" ||
		in.Action == "revoke" && (!value.ValidUUID(fid) || in.Version < 1) {
		return nil, Invalid{"Kalenderfreigabe und Versionsstand prüfen"}
	}
	var token string
	payload := struct {
		PlanningCalendarInput
		FeedID string `json:"feed_id"`
	}{in, fid}
	result, err := s.planningExtensionTransaction(ctx, id, pid, key, requestID, "calendar/"+in.Action,
		in.PlanningExtensionCommand, payload, true, func(ctx context.Context, tx pgx.Tx, _ Record) (Record, error) {
			if in.Action == "revoke" {
				var raw []byte
				err := tx.QueryRow(ctx, `UPDATE mealplan.calendar_feeds f
 SET revoked_at=coalesce(f.revoked_at,clock_timestamp()),version=f.version+1
 WHERE f.household_id=$1 AND f.plan_id=$2 AND f.id=$5 AND f.module_generation=$6
 AND f.week_start=$7::date AND f.version=$8 AND (f.issuer_user_id=$3 OR $4='admin')
 RETURNING `+planningFeedMetadata, id.HouseholdID, pid, id.UserID, id.Role, fid, in.Generation, in.Week, in.Version).Scan(&raw)
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, PlanningConflict{"Kalenderfreigabe fehlt, wurde geändert oder darf nicht widerrufen werden"}
				}
				if err != nil {
					return nil, err
				}
				feed, err := decode(raw)
				return Record{"feed": feed}, err
			}
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, id.HouseholdID+id.UserID+"calendar-feed-quota"); err != nil {
				return nil, err
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM mealplan.calendar_feeds
 WHERE household_id=$1 AND issuer_user_id=$2 AND revoked_at IS NULL AND expires_at>now()
 AND module_generation=$3`, id.HouseholdID, id.UserID, in.Generation).Scan(&count); err != nil {
				return nil, err
			}
			if count >= 20 {
				return nil, Invalid{"Höchstens20 aktive Kalenderfreigaben pro Person. Alte Freigaben zuerst widerrufen."}
			}
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return nil, err
			}
			token = base64.RawURLEncoding.EncodeToString(secret)
			digest := sha256.Sum256([]byte(token))
			fid = value.UUID()
			var raw []byte
			err := tx.QueryRow(ctx, `INSERT INTO mealplan.calendar_feeds AS f
 (id,household_id,plan_id,week_start,issuer_user_id,issuer_membership_id,issuer_auth_version,
 issuer_role,module_generation,token_hash,expires_at)
 VALUES($5,$1,$2,$6::date,$3,$7,$8,$4,$9,$10,clock_timestamp()+interval '90 days')
 RETURNING `+planningFeedMetadata, id.HouseholdID, pid, id.UserID, id.Role, fid, in.Week,
				id.MembershipID, id.AuthVersion, in.Generation, digest[:]).Scan(&raw)
			if err != nil {
				return nil, err
			}
			feed, err := decode(raw)
			return Record{"feed": feed}, err
		})
	if err != nil {
		return nil, err
	}
	if in.Action == "create" {
		if token == "" {
			result["secret_unavailable"] = true
		} else {
			result["url"] = "/calendar/mealplan/" + token + ".ics"
			result["secret_shown_once"] = true
		}
	}
	return result, nil
}

func (s *Store) PlanningCalendarDownload(ctx context.Context, id auth.Identity, pid, week string) (PlanningCalendar, error) {
	tx, plan, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return PlanningCalendar{}, err
	}
	defer rollback(tx)
	if _, err = planningExtensionWindow(plan, week); err != nil {
		return PlanningCalendar{}, err
	}
	return readPlanningCalendar(ctx, tx, id.HouseholdID, pid, week, generation)
}

// PlanningCalendarFeed grants only the bounded calendar projection. It checks
// current issuer authority on every fetch and does not accept session tokens.
func (s *Store) PlanningCalendarFeed(ctx context.Context, token string) (PlanningCalendar, error) {
	if len(token) != 43 || strings.ContainsAny(token, " \t\r\n") {
		return PlanningCalendar{}, ErrNotFound
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return PlanningCalendar{}, ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlanningCalendar{}, err
	}
	defer rollback(tx)
	var household, pid, week string
	var generation int64
	err = tx.QueryRow(ctx, `SELECT f.household_id,f.plan_id,f.week_start::text,f.module_generation
 FROM mealplan.calendar_feeds f JOIN core.users u ON u.id=f.issuer_user_id
 JOIN core.memberships member ON member.id=f.issuer_membership_id
  AND member.household_id=f.household_id AND member.user_id=u.id
 JOIN core.module_states state ON state.household_id=f.household_id AND state.module_id='mealplan'
 JOIN mealplan.plans p ON p.household_id=f.household_id AND p.id=f.plan_id
 WHERE f.token_hash=$1 AND f.revoked_at IS NULL AND f.expires_at>now()
 AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=f.issuer_auth_version
 AND member.inactive_at IS NULL AND member.role=f.issuer_role
 AND member.role IN('admin','editor','reader') AND state.state='enabled'
 AND state.data_generation=f.module_generation`, digest[:]).Scan(&household, &pid, &week, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanningCalendar{}, ErrNotFound
	}
	if err != nil {
		return PlanningCalendar{}, err
	}
	return readPlanningCalendar(ctx, tx, household, pid, week, generation)
}

func readPlanningCalendar(ctx context.Context, tx pgx.Tx, household, pid, week string, generation int64) (PlanningCalendar, error) {
	out := PlanningCalendar{Week: week, Events: []ical.Event{}}
	days, err := planningWindow(week)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id,planned_on::text,title,meal_slot,status,version,updated_at
 FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND planned_on BETWEEN $3::date AND $4::date
 ORDER BY planned_on,id LIMIT 43`, household, pid, days[0], days[6])
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var mid, date, title, slot, status string
		var version int64
		var updated time.Time
		if err = rows.Scan(&mid, &date, &title, &slot, &status, &version, &updated); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Events) >= planningMealLimit {
			rows.Close()
			return out, Invalid{"Kalenderwoche überschreitet42 Mahlzeiten; Auswahl prüfen"}
		}
		seen[mid] = true
		slotNames := map[string]string{"breakfast": "Frühstück", "lunch": "Mittagessen", "dinner": "Abendessen", "snack": "Zwischenmahlzeit"}
		out.Events = append(out.Events, ical.Event{UID: ical.MealUID(household, generation, mid), Date: date,
			Summary: title + " · " + slotNames[slot], Sequence: version, Modified: updated, Cancelled: status == "cancelled"})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON(meal_id) meal_id,planned_on::text,sequence,deleted_at
 FROM mealplan.calendar_tombstones WHERE household_id=$1 AND plan_id=$2 AND module_generation=$3
 AND planned_on BETWEEN $4::date AND $5::date ORDER BY meal_id,sequence DESC,planned_on DESC LIMIT 513`,
		household, pid, generation, days[0], days[6])
	if err != nil {
		return out, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var mid, date string
		var version int64
		var updated time.Time
		if err = rows.Scan(&mid, &date, &version, &updated); err != nil {
			return out, err
		}
		count++
		if count > planningCalendarCancellations {
			return out, Invalid{"Kalenderwoche überschreitet die Stornierungsgrenze. Neue begrenzte Freigabe prüfen."}
		}
		if !seen[mid] {
			out.Events = append(out.Events, ical.Event{UID: ical.MealUID(household, generation, mid), Date: date,
				Sequence: version, Modified: updated, Cancelled: true})
		}
	}
	return out, rows.Err()
}
