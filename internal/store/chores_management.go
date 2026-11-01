package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type ChoreAreaInput struct {
	ChoreCommand
	Name     string `json:"name"`
	Position int    `json:"position"`
	Archived bool   `json:"archived"`
}

type choreAreaCursor struct {
	Household  string `json:"household"`
	Session    string `json:"session"`
	Generation int64  `json:"generation"`
	Search     string `json:"search"`
	Archived   bool   `json:"archived"`
	Position   int    `json:"position"`
	ID         string `json:"id"`
}

type ChoreAbsenceInput struct {
	ChoreCommand
	MembershipID string `json:"membership_id"`
	StartsOn     string `json:"starts_on"`
	EndsOn       string `json:"ends_on"`
	SubstituteID string `json:"substitute_id"`
	Delete       bool   `json:"delete"`
}

type ChorePreferencesInput struct {
	ChoreCommand
	DailyMinutes          int    `json:"daily_minutes"`
	MaxDifficulty         *int   `json:"max_difficulty"`
	NotificationMode      string `json:"notification_mode"`
	DigestMinute          int    `json:"digest_minute"`
	QuietStart            *int   `json:"quiet_start"`
	QuietEnd              *int   `json:"quiet_end"`
	Timezone              string `json:"timezone"`
	EscalationEnabled     bool   `json:"escalation_enabled"`
	EscalationAfterDays   int    `json:"escalation_after_days"`
	EscalationRecipientID string `json:"escalation_recipient_id"`
	AllowEscalations      bool   `json:"allow_escalations"`
}

// Management lists use UUID keyset pages. Totals are computed for the selected
// page/window rather than silently presenting a truncated global list.
func (s *Store) ChoreManagement(ctx context.Context, id auth.Identity, kind, after, search string, archived bool) (Record, error) {
	if after != "" && kind != "areas" && !value.ValidUUID(after) || len(after) > 4096 || utf8.RuneCountInString(search) > 120 {
		return nil, Invalid{"Seite oder Suchtext prüfen"}
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	var rows []Record
	switch kind {
	case "areas":
		var position, areaID any
		if after != "" {
			raw, decodeErr := base64.RawURLEncoding.DecodeString(after)
			if decodeErr != nil || len(raw) > 2048 {
				return nil, Invalid{"Bereichsseite prüfen"}
			}
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.DisallowUnknownFields()
			var c choreAreaCursor
			if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.Household != id.HouseholdID || c.Session != id.SessionID || c.Generation != scope.generation ||
				c.Search != search || c.Archived != archived || c.Position < 0 || c.Position > 10000 || !value.ValidUUID(c.ID) {
				return nil, Invalid{"Bereichsseite gehört zu einem anderen Filter oder Anmeldestand"}
			}
			position, areaID = c.Position, c.ID
		}
		rows, err = choreRows(scope.ctx, scope.tx, 101, `SELECT to_jsonb(a)||jsonb_build_object('open_count',
 (SELECT count(*) FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 JOIN chores.template_versions v ON v.id=d.template_version_id JOIN chores.task_templates t ON t.id=o.template_id
 JOIN chores.series_profiles p ON p.template_id=t.id WHERE o.household_id=a.household_id AND v.area_id=a.id
 AND o.state='open' AND t.active AND (NOT p.paused OR o.due_at<=p.paused_at)), 'overdue_count',
 (SELECT count(*) FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 JOIN chores.template_versions v ON v.id=d.template_version_id JOIN chores.task_templates t ON t.id=o.template_id
 JOIN chores.series_profiles p ON p.template_id=t.id WHERE o.household_id=a.household_id AND v.area_id=a.id
 AND o.state='open' AND o.due_at<transaction_timestamp() AND t.active AND (NOT p.paused OR o.due_at<=p.paused_at)))
 FROM chores.areas a WHERE a.household_id=$1 AND ($2::int IS NULL OR(a.position,a.id)>($2::int,$5::uuid))
 AND ($3 OR a.archived_at IS NULL) AND ($4='' OR position(lower($4) in lower(a.name))>0) ORDER BY a.position,a.id LIMIT 101`, id.HouseholdID, position, archived, search, areaID)
	case "series":
		rows, err = choreRows(scope.ctx, scope.tx, 101, `SELECT jsonb_build_object('id',t.id,'template',to_jsonb(t),'profile',to_jsonb(p),
 'snapshot',to_jsonb(v),'schedule_state',to_jsonb(state),'last_completed_at',
 (SELECT max(c.completed_at) FROM chores.completions c WHERE c.household_id=t.household_id AND c.template_id=t.id))
 FROM chores.task_templates t JOIN chores.series_profiles p ON p.template_id=t.id
 JOIN chores.template_versions v ON v.id=p.current_version_id LEFT JOIN chores.schedule_state state ON state.template_id=t.id
 WHERE t.household_id=$1 AND ($2='' OR t.id>nullif($2,'')::uuid) AND ($3 OR t.active)
 AND ($4='' OR position(lower($4) in lower(t.title))>0) ORDER BY t.id LIMIT 101`, id.HouseholdID, after, archived, search)
	case "absences":
		rows, err = choreRows(scope.ctx, scope.tx, 101, `SELECT to_jsonb(a)||jsonb_build_object('member_name',u.display_name,'substitute_name',su.display_name)
 FROM chores.absences a JOIN core.memberships m ON m.household_id=a.household_id AND m.id=a.membership_id
 JOIN core.users u ON u.id=m.user_id LEFT JOIN core.memberships sm ON sm.household_id=a.household_id AND sm.id=a.substitute_id
 LEFT JOIN core.users su ON su.id=sm.user_id WHERE a.household_id=$1 AND ($2='' OR a.id>nullif($2,'')::uuid)
 AND ($3 OR a.ends_on>=(transaction_timestamp() AT TIME ZONE $5)::date)
 AND ($4='' OR position(lower($4) in lower(u.display_name))>0) ORDER BY a.id LIMIT 101`, id.HouseholdID, after, archived, search, scope.timezone)
	default:
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 100 {
		rows = rows[:100]
		next = String(rows[99]["id"])
		if kind == "areas" {
			c := choreAreaCursor{Household: id.HouseholdID, Session: id.SessionID, Generation: scope.generation, Search: search, Archived: archived, Position: int(Int(rows[99]["position"])), ID: next}
			raw, marshalErr := json.Marshal(c)
			if marshalErr != nil {
				return nil, marshalErr
			}
			next = base64.RawURLEncoding.EncodeToString(raw)
		}
	}
	return Record{"items": rows, "next_cursor": next, "generation": scope.generation, "can_write": id.CanWrite()}, nil
}

func (s *Store) SaveChoreArea(ctx context.Context, id auth.Identity, areaID, key, requestID string, in ChoreAreaInput) (Record, error) {
	in.Name = strings.TrimSpace(in.Name)
	if areaID != "" && !value.ValidUUID(areaID) || utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 100 ||
		strings.ContainsRune(in.Name, 0) || in.Position < 0 || in.Position > 10000 || areaID == "" && in.Version != 0 || areaID != "" && in.Version < 1 {
		return nil, Invalid{"Bereich, Name, Reihenfolge und Version prüfen"}
	}
	return s.choreWrite(ctx, id, key, requestID, "chores/workspace/area/"+areaID, in.ChoreCommand, in, false, nil, func(ctx context.Context, tx pgx.Tx) (Record, error) {
		if !in.Archived {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM chores.areas WHERE household_id=$1 AND archived_at IS NULL AND ($2='' OR id<>nullif($2,'')::uuid)`, id.HouseholdID, areaID).Scan(&count); err != nil {
				return nil, err
			}
			if count >= 200 {
				return nil, Invalid{"Höchstens 200 aktive Bereiche"}
			}
		}
		var record Record
		var err error
		if areaID == "" {
			record, err = choreObject(ctx, tx, `INSERT INTO chores.areas AS a(household_id,name,position,archived_at,created_by)
 VALUES($1,$2,$3,CASE WHEN $4 THEN clock_timestamp() END,$5) RETURNING to_jsonb(a)`, id.HouseholdID, in.Name, in.Position, in.Archived, id.UserID)
		} else {
			record, err = choreObject(ctx, tx, `UPDATE chores.areas a SET name=$3,position=$4,
 archived_at=CASE WHEN $5 THEN coalesce(archived_at,clock_timestamp()) END,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$6 RETURNING to_jsonb(a)`, id.HouseholdID, areaID, in.Name, in.Position, in.Archived, in.Version)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrConflict
			}
		}
		return Record{"area": record, "generation": in.Generation}, err
	})
}

func choreActiveMember(ctx context.Context, tx pgx.Tx, household, memberID string, writer bool) error {
	var found string
	err := tx.QueryRow(ctx, `SELECT member.id::text FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 WHERE member.household_id=$1 AND member.id=$2 AND member.inactive_at IS NULL AND u.disabled_at IS NULL
 AND NOT u.must_change_password AND (NOT $3::boolean OR member.role IN('admin','editor')) FOR SHARE OF member,u`, household, memberID, writer).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invalid{"Person ist nicht aktiv oder für die Aufgabe nicht berechtigt"}
	}
	return err
}

func choreSuitableMember(ctx context.Context, tx pgx.Tx, household, versionID, memberID string) error {
	if err := choreActiveMember(ctx, tx, household, memberID, true); err != nil {
		return err
	}
	var suitable bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM chores.rotation_members WHERE household_id=$1 AND version_id=$2)
 OR EXISTS(SELECT 1 FROM chores.rotation_members WHERE household_id=$1 AND version_id=$2 AND membership_id=$3 AND eligible)`, household, versionID, memberID).Scan(&suitable); err != nil {
		return err
	}
	if !suitable {
		return Invalid{"Person ist in diesem Vorlagenstand nicht als geeignet hinterlegt"}
	}
	return nil
}

func (s *Store) SaveChoreAbsence(ctx context.Context, id auth.Identity, absenceID, key, requestID string, in ChoreAbsenceInput) (Record, error) {
	if absenceID != "" && !value.ValidUUID(absenceID) || in.Delete && absenceID == "" || absenceID != "" && in.Version < 1 || absenceID == "" && in.Version != 0 {
		return nil, Invalid{"Abwesenheitskennung und Version prüfen"}
	}
	if !in.Delete {
		from, err := chore.ParseDay(in.StartsOn)
		through, endErr := chore.ParseDay(in.EndsOn)
		if err != nil || endErr != nil || through.Before(from) || (through.Unix()-from.Unix())/86400 > 366 || !value.ValidUUID(in.MembershipID) ||
			in.SubstituteID != "" && (!value.ValidUUID(in.SubstituteID) || in.SubstituteID == in.MembershipID) {
			return nil, Invalid{"Abwesenheitszeitraum und Vertretung prüfen"}
		}
	}
	return s.choreWrite(ctx, id, key, requestID, "chores/workspace/absence/"+absenceID, in.ChoreCommand, in, false, nil, func(ctx context.Context, tx pgx.Tx) (Record, error) {
		if in.Delete {
			tag, err := tx.Exec(ctx, `DELETE FROM chores.absences WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, absenceID, in.Version)
			if err == nil && tag.RowsAffected() != 1 {
				return nil, ErrConflict
			}
			return Record{"deleted": true, "generation": in.Generation}, err
		}
		if err := choreActiveMember(ctx, tx, id.HouseholdID, in.MembershipID, false); err != nil {
			return nil, err
		}
		if in.SubstituteID != "" {
			if err := choreActiveMember(ctx, tx, id.HouseholdID, in.SubstituteID, true); err != nil {
				return nil, err
			}
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.absences WHERE household_id=$1 AND membership_id=$2
 AND starts_on<=$4::date AND ends_on>=$3::date AND ($5='' OR id<>nullif($5,'')::uuid))`, id.HouseholdID, in.MembershipID, in.StartsOn, in.EndsOn, absenceID).Scan(&overlap); err != nil {
			return nil, err
		}
		if overlap {
			return nil, Invalid{"Für diese Person überschneidet sich bereits eine Abwesenheit"}
		}
		var record Record
		var err error
		if absenceID == "" {
			record, err = choreObject(ctx, tx, `INSERT INTO chores.absences AS a(household_id,membership_id,starts_on,ends_on,substitute_id,created_by)
 VALUES($1,$2,$3::date,$4::date,nullif($5,'')::uuid,$6) RETURNING to_jsonb(a)`, id.HouseholdID, in.MembershipID, in.StartsOn, in.EndsOn, in.SubstituteID, id.UserID)
		} else {
			record, err = choreObject(ctx, tx, `UPDATE chores.absences a SET membership_id=$3,starts_on=$4::date,ends_on=$5::date,
 substitute_id=nullif($6,'')::uuid,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$7
 RETURNING to_jsonb(a)`, id.HouseholdID, absenceID, in.MembershipID, in.StartsOn, in.EndsOn, in.SubstituteID, in.Version)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrConflict
			}
		}
		return Record{"absence": record, "generation": in.Generation}, err
	})
}

func chorePreferences(ctx context.Context, tx pgx.Tx, household, userID, timezone string) (Record, error) {
	prefs, err := choreObject(ctx, tx, `SELECT to_jsonb(p) FROM chores.member_preferences p WHERE household_id=$1 AND user_id=$2`, household, userID)
	if errors.Is(err, ErrNotFound) {
		return Record{"version": int64(0), "daily_minutes": 60, "max_difficulty": nil, "notification_mode": "legacy",
			"digest_enabled": false, "digest_minute": 540, "quiet_start": nil, "quiet_end": nil, "timezone": timezone,
			"escalation_enabled": false, "escalation_after_days": 3, "escalation_recipient_id": nil, "allow_escalations": false}, nil
	}
	return prefs, err
}

func (s *Store) ChorePreferences(ctx context.Context, id auth.Identity) (Record, error) {
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	prefs, err := chorePreferences(scope.ctx, scope.tx, id.HouseholdID, id.UserID, scope.timezone)
	return Record{"preferences": prefs, "generation": scope.generation}, err
}

func (s *Store) SaveChorePreferences(ctx context.Context, id auth.Identity, key, requestID string, in ChorePreferencesInput) (Record, error) {
	if in.DailyMinutes < 0 || in.DailyMinutes > 1440 || in.MaxDifficulty != nil && (*in.MaxDifficulty < 1 || *in.MaxDifficulty > 5) ||
		in.NotificationMode != "off" && in.NotificationMode != "legacy" && in.NotificationMode != "digest" || in.DigestMinute < 0 || in.DigestMinute > 1439 ||
		(in.QuietStart == nil) != (in.QuietEnd == nil) || in.QuietStart != nil && (*in.QuietStart < 0 || *in.QuietStart > 1439 || *in.QuietEnd < 0 || *in.QuietEnd > 1439 || *in.QuietStart == *in.QuietEnd) ||
		in.EscalationAfterDays < 1 || in.EscalationAfterDays > 30 || in.EscalationRecipientID != "" && !value.ValidUUID(in.EscalationRecipientID) ||
		in.EscalationEnabled && in.EscalationRecipientID == "" || in.Version < 0 || len(in.Timezone) > 100 {
		return nil, Invalid{"Zeitbudget, Benachrichtigungen und Ruhezeiten prüfen"}
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return nil, Invalid{"Gültige IANA-Zeitzone wählen"}
	}
	return s.choreWrite(ctx, id, key, requestID, "chores/workspace/preferences", in.ChoreCommand, in, true, nil, func(ctx context.Context, tx pgx.Tx) (Record, error) {
		if in.EscalationEnabled {
			if err := choreActiveMember(ctx, tx, id.HouseholdID, in.EscalationRecipientID, false); err != nil {
				return nil, err
			}
			var accepted bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.member_preferences p JOIN core.memberships m ON m.household_id=p.household_id
 AND m.user_id=p.user_id WHERE m.household_id=$1 AND m.id=$2 AND p.allow_escalations)`, id.HouseholdID, in.EscalationRecipientID).Scan(&accepted); err != nil {
				return nil, err
			}
			if !accepted {
				return nil, Invalid{"Die gewählte Person muss Eskalationen zuerst ausdrücklich erlauben"}
			}
		}
		var current int64
		err := tx.QueryRow(ctx, `SELECT version FROM chores.member_preferences WHERE household_id=$1 AND user_id=$2 FOR UPDATE`, id.HouseholdID, id.UserID).Scan(&current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if current != in.Version {
			return nil, ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO chores.member_preferences(household_id,user_id,daily_minutes,max_difficulty,notification_mode,digest_enabled,
 digest_minute,quiet_start,quiet_end,timezone,escalation_enabled,escalation_after_days,escalation_recipient_id,allow_escalations,created_by)
 VALUES($1,$2,$3,$4,$5,$5='digest',$6,$7,$8,$9,$10,$11,nullif($12,'')::uuid,$13,$2)
 ON CONFLICT(household_id,user_id) DO UPDATE SET daily_minutes=excluded.daily_minutes,max_difficulty=excluded.max_difficulty,
 notification_mode=excluded.notification_mode,digest_enabled=excluded.digest_enabled,digest_minute=excluded.digest_minute,
 quiet_start=excluded.quiet_start,quiet_end=excluded.quiet_end,timezone=excluded.timezone,escalation_enabled=excluded.escalation_enabled,
 escalation_after_days=excluded.escalation_after_days,escalation_recipient_id=excluded.escalation_recipient_id,
 allow_escalations=excluded.allow_escalations,version=chores.member_preferences.version+1,updated_at=clock_timestamp()`, id.HouseholdID, id.UserID,
			in.DailyMinutes, in.MaxDifficulty, in.NotificationMode, in.DigestMinute, in.QuietStart, in.QuietEnd, in.Timezone, in.EscalationEnabled, in.EscalationAfterDays, in.EscalationRecipientID, in.AllowEscalations)
		if err != nil {
			return nil, err
		}
		prefs, err := chorePreferences(ctx, tx, id.HouseholdID, id.UserID, in.Timezone)
		return Record{"preferences": prefs, "generation": in.Generation}, err
	})
}
