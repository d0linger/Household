package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/civiltime"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningNotificationStepInput struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	LocalDate string `json:"local_date"`
	LocalTime string `json:"local_time"`
}

type PlanningNotificationInput struct {
	Generation      int64                           `json:"generation"`
	PlanVersion     int64                           `json:"plan_version"`
	MealVersion     int64                           `json:"meal_version"`
	SettingsVersion int64                           `json:"settings_version"`
	Timezone        string                          `json:"timezone"`
	Responsible     string                          `json:"responsible_member_id"`
	Enabled         bool                            `json:"enabled"`
	Confirmed       bool                            `json:"confirmed"`
	Steps           []PlanningNotificationStepInput `json:"steps"`
}

type planningNotificationStep struct {
	PlanningNotificationStepInput
	ScheduledAt time.Time `json:"scheduled_at"`
}

func planningNotificationObject(ctx context.Context, tx pgx.Tx, query string, args ...any) (Record, error) {
	var raw []byte
	err := tx.QueryRow(ctx, query, args...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 128<<10 {
		return nil, Invalid{"Zubereitungshinweise sind unvollständig, ungültig oder zu groß"}
	}
	return decode(raw)
}

func planningNotificationMembers(ctx context.Context, tx pgx.Tx, household string) ([]Record, error) {
	rows, err := tx.Query(ctx, `SELECT m.id::text,u.display_name,u.username
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 AND m.inactive_at IS NULL
 AND u.disabled_at IS NULL AND NOT u.must_change_password ORDER BY u.username,m.id LIMIT 251`, household)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var id, name, username string
		if err := rows.Scan(&id, &name, &username); err != nil {
			return nil, err
		}
		if len(result) == 250 {
			return nil, Invalid{"Zubereitungshinweise sind unvollständig, ungültig oder zu groß"}
		}
		result = append(result, Record{"id": id, "name": name, "username": username})
	}
	return result, rows.Err()
}

func planningNotificationRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// The policy is shown before confirmation: nonexistent local times are rejected;
// an ambiguous fall-back time denotes its first occurrence, never a second send.
func planningNotificationTime(date, clock, zone string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil || d.Format(time.DateOnly) != date || d.Year() < 2000 || d.Year() > 9998 {
		return time.Time{}, Invalid{"Gültiges Datum für den Zubereitungshinweis wählen"}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, Invalid{"Gültige Haushaltszeitzone für den Zubereitungshinweis verwenden"}
	}
	at, policy, err := civiltime.Resolve(d, clock, loc)
	if err != nil || policy == "gap_forward" {
		return time.Time{}, Invalid{"Diese lokale Uhrzeit existiert nicht. Eine gültige Uhrzeit wählen."}
	}
	return at.UTC(), nil
}

func planningNotificationSteps(in PlanningNotificationInput) ([]planningNotificationStep, error) {
	if len(in.Steps) > 8 || in.Enabled && (len(in.Steps) == 0 || in.Responsible == "") ||
		!in.Confirmed || in.Responsible != "" && (!shoppingCanonicalUUID(in.Responsible) || in.Responsible == "00000000-0000-0000-0000-000000000000") {
		return nil, Invalid{"Zuständigkeit und höchstens acht ausdrücklich bestätigte Zubereitungshinweise prüfen"}
	}
	steps := make([]planningNotificationStep, 0, len(in.Steps))
	seen := make(map[string]bool, len(in.Steps))
	starts := 0
	for _, step := range in.Steps {
		if !shoppingCanonicalUUID(step.ID) || step.ID == "00000000-0000-0000-0000-000000000000" || seen[step.ID] || step.Kind != "start" && step.Kind != "step" ||
			!utf8.ValidString(step.Label) || strings.TrimSpace(step.Label) != step.Label ||
			utf8.RuneCountInString(step.Label) < 1 || utf8.RuneCountInString(step.Label) > 120 ||
			strings.ContainsFunc(step.Label, unicode.IsControl) {
			return nil, Invalid{"Zubereitungsschritte brauchen eindeutige Kennungen und eine kurze Bezeichnung"}
		}
		seen[step.ID] = true
		if step.Kind == "start" {
			starts++
		}
		if starts > 1 {
			return nil, Invalid{"Höchstens einen Zubereitungsbeginn bestätigen"}
		}
		at, err := planningNotificationTime(step.LocalDate, step.LocalTime, in.Timezone)
		if err != nil {
			return nil, err
		}
		steps = append(steps, planningNotificationStep{step, at})
	}
	return steps, nil
}

func (s *Store) PlanningNotificationSettings(ctx context.Context, id auth.Identity, pid, meal string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if !value.ValidUUID(meal) {
		return nil, ErrNotFound
	}
	tx, _, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return nil, err
	}
	defer planningNotificationRollback(tx)
	out, err := planningNotificationObject(ctx, tx, `SELECT jsonb_build_object('generation',$4::bigint,
 'plan_version',p.version,'meal_version',m.version,'timezone',h.timezone,
 'eligible',NOT p.is_template AND m.status IN('planned','purchased'),
 'needs_confirmation',n.id IS NOT NULL AND (n.meal_version<>m.version OR n.module_generation<>$4
 OR n.timezone<>h.timezone OR p.notification_active_since>n.updated_at),
 'settings',jsonb_build_object('id',m.id,'version',coalesce(n.version,0),
 'enabled',coalesce(n.enabled,false),'responsible_member_id',n.responsible_member_id,'steps',coalesce(n.steps,'[]'::jsonb)))
 FROM mealplan.meals m JOIN mealplan.plans p ON p.household_id=m.household_id AND p.id=m.plan_id
 JOIN core.households h ON h.id=m.household_id LEFT JOIN mealplan.notification_settings n ON n.household_id=m.household_id AND n.id=m.id
 WHERE m.household_id=$1 AND p.id=$2 AND m.id=$3`, id.HouseholdID, pid, meal, generation)
	if err != nil {
		return nil, err
	}
	members, err := planningNotificationMembers(ctx, tx, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	out["members"] = members
	return out, nil
}

// This dedicated transaction preserves the meal/plan revisions and unrelated
// drafts. It retains the existing authority, locking and durable replay rules.
func (s *Store) SavePlanningNotificationSettings(ctx context.Context, id auth.Identity, pid, meal, key, requestID string, in PlanningNotificationInput) (Record, error) {
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 15*time.Second)
	defer cancel()
	if !planningIdentityValid(id) || !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !shoppingCanonicalUUID(pid) || !shoppingCanonicalUUID(meal) || !shoppingCanonicalUUID(key) ||
		in.Generation < 1 || in.Generation > notification.MaxRevision || in.PlanVersion < 1 || in.PlanVersion > notification.MaxRevision ||
		in.MealVersion < 1 || in.MealVersion > notification.MaxRevision || in.SettingsVersion < 0 || in.SettingsVersion >= notification.MaxRevision {
		return nil, Invalid{"Plan, Mahlzeit und Versionsstände des Zubereitungshinweises prüfen"}
	}
	steps, err := planningNotificationSteps(in)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	tx, err := s.BeginMutation(ctx, id, "mealplan", in.Generation)
	if err != nil {
		return nil, err
	}
	defer planningNotificationRollback(tx)
	op := "mealplan/notifications/" + pid + "/" + meal
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var planVersion, mealVersion int64
	var template bool
	var status, zone string
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT p.version,p.is_template,h.timezone,clock_timestamp() FROM mealplan.plans p
 JOIN core.households h ON h.id=p.household_id WHERE p.household_id=$1 AND p.id=$2 FOR UPDATE OF p NOWAIT`, id.HouseholdID, pid).
		Scan(&planVersion, &template, &zone, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	err = tx.QueryRow(ctx, `SELECT version,status FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id=$3
 FOR UPDATE NOWAIT`, id.HouseholdID, pid, meal).Scan(&mealVersion, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if planVersion != in.PlanVersion || mealVersion != in.MealVersion || zone != in.Timezone {
		return nil, PlanningConflict{"Plan, Mahlzeit oder Zeitzone geändert. Hinweise erneut prüfen und bestätigen."}
	}
	if template || status != "planned" && status != "purchased" {
		return nil, Invalid{"Zubereitungshinweise sind nur für geplante oder eingekaufte Mahlzeiten außerhalb von Vorlagen möglich"}
	}
	for _, step := range steps {
		if in.Enabled && !step.ScheduledAt.After(now) {
			return nil, Invalid{"Aktive Zubereitungshinweise müssen in der Zukunft liegen. Vergangene Hinweise werden nicht nachgeholt."}
		}
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return nil, err
	}
	var result Record
	if in.SettingsVersion == 0 {
		result, err = planningNotificationObject(ctx, tx, `INSERT INTO mealplan.notification_settings AS n
 (id,household_id,responsible_member_id,enabled,meal_version,module_generation,timezone,steps,created_by)
 VALUES($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8::jsonb,$9)
 RETURNING jsonb_build_object('id',n.id,'version',n.version)`, meal, id.HouseholdID, in.Responsible, in.Enabled,
			in.MealVersion, in.Generation, in.Timezone, raw, id.UserID)
	} else {
		result, err = planningNotificationObject(ctx, tx, `UPDATE mealplan.notification_settings AS n SET responsible_member_id=nullif($4,'')::uuid,
 enabled=$5,meal_version=$6,module_generation=$7,timezone=$8,steps=$9::jsonb,version=version+1
 WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING jsonb_build_object('id',n.id,'version',n.version)`,
			id.HouseholdID, meal, in.SettingsVersion, in.Responsible, in.Enabled, in.MealVersion, in.Generation, in.Timezone, raw)
	}
	if errors.Is(err, ErrNotFound) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	out := Record{"generation": in.Generation, "plan_version": planVersion, "meal_version": mealVersion, "settings": result}
	response, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records
 (household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'mealplan',$6,200,$7)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, response)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return out, nil
}
