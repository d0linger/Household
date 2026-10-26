package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningComponent struct {
	ID               string `json:"id"`
	Position         int    `json:"position"`
	Version          int64  `json:"version"`
	SnapshotBytes    int64  `json:"snapshot_bytes"`
	Servings         string `json:"servings"`
	ServingsMode     string `json:"servings_mode"`
	Title            string `json:"title"`
	BaseServings     string `json:"base_servings"`
	SourceRecipeID   string `json:"source_recipe_id"`
	SourceVersionID  string `json:"source_version_id"`
	SourceGeneration int64  `json:"source_generation"`
	SourceRevision   int64  `json:"source_revision"`
	PrepMinutes      *int64 `json:"prep_minutes"`
	CookMinutes      *int64 `json:"cook_minutes"`
}

type PlanningMeal struct {
	ID              string                `json:"id"`
	PlanID          string                `json:"plan_id"`
	Version         int64                 `json:"version"`
	Title           string                `json:"title"`
	PlannedOn       string                `json:"planned_on"`
	Slot            string                `json:"meal_slot"`
	Kind            string                `json:"kind"`
	Status          string                `json:"status"`
	Servings        string                `json:"servings"`
	ServingsMode    string                `json:"servings_mode"`
	GuestCount      int                   `json:"guest_count"`
	DerivedServings string                `json:"derived_servings"`
	Participants    []PlanningParticipant `json:"participants"`
	Notes           string                `json:"notes"`
	Components      []PlanningComponent   `json:"components"`
	RequiresReview  bool                  `json:"requires_recipe_review"`
}

type PlanningParticipant struct {
	ID           string `json:"id"`
	MembershipID string `json:"membership_id"`
	Name         string `json:"name"`
	Active       bool   `json:"active"`
}

type PlanningWeek struct {
	Plan   Record                    `json:"plan"`
	Days   []string                  `json:"days"`
	Meals  []PlanningMeal            `json:"meals"`
	Effort map[string]PlanningEffort `json:"effort"`
}

type planningQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const planningComponentJSON = `jsonb_build_object('id',c.id,'position',c.position,'version',c.version,'snapshot_bytes',octet_length(c.snapshot::text),'servings',c.servings::text,
 'title',c.snapshot->'recipe'->>'title','base_servings',c.snapshot->'recipe'->>'base_servings',
 'source_recipe_id',c.source_recipe_id,'source_version_id',c.source_version_id,'source_generation',c.source_generation,'source_revision',c.source_revision,
 'prep_minutes',c.prep_minutes,'cook_minutes',c.cook_minutes,'servings_mode',c.servings_mode)`

const planningMealJSON = `to_jsonb(m)||jsonb_build_object('servings',m.servings::text,
 'derived_servings',((SELECT count(*) FROM mealplan.participants part WHERE part.household_id=m.household_id AND part.meal_id=m.id)+m.guest_count)::text,
 'participants',coalesce((SELECT jsonb_agg(x.item ORDER BY x.membership_id) FROM
 (SELECT part.membership_id,jsonb_build_object('id',part.id,'membership_id',part.membership_id,'name',u.display_name,
 'active',member.inactive_at IS NULL AND u.disabled_at IS NULL) item
 FROM mealplan.participants part JOIN core.memberships member ON member.household_id=part.household_id AND member.id=part.membership_id
 JOIN core.users u ON u.id=member.user_id WHERE part.household_id=m.household_id AND part.meal_id=m.id
 ORDER BY part.membership_id LIMIT 101) x),'[]'::jsonb),
 'requires_recipe_review',m.kind='recipe' AND NOT EXISTS(SELECT 1 FROM mealplan.components c WHERE c.household_id=m.household_id AND c.meal_id=m.id),
 'components',coalesce((SELECT jsonb_agg(x.item ORDER BY x.position,x.id) FROM
 (SELECT c.position,c.id,` + planningComponentJSON + ` AS item FROM mealplan.components c WHERE c.household_id=m.household_id AND c.meal_id=m.id ORDER BY c.position,c.id LIMIT 11) x),'[]'::jsonb))`

func planningDecode(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(target)
}

func (s *Store) PlanningWeek(ctx context.Context, id auth.Identity, pid, start string) (PlanningWeek, error) {
	return readPlanningWeek(ctx, s.Pool, id, pid, start)
}

func readPlanningWeek(ctx context.Context, q planningQuerier, id auth.Identity, pid, start string) (PlanningWeek, error) {
	var week PlanningWeek
	if !value.ValidUUID(pid) {
		return week, ErrNotFound
	}
	days, err := planningWindow(start)
	if err != nil {
		return week, err
	}
	var raw []byte
	err = q.QueryRow(ctx, `SELECT jsonb_build_object('plan',to_jsonb(p),'meals',coalesce((SELECT jsonb_agg(x.item ORDER BY x.planned_on,x.meal_slot,x.id) FROM
 (SELECT m.planned_on,m.meal_slot,m.id,`+planningMealJSON+` AS item FROM mealplan.meals m WHERE m.household_id=p.household_id AND m.plan_id=p.id AND m.planned_on BETWEEN $3::date AND $4::date ORDER BY m.planned_on,m.meal_slot,m.id LIMIT 43) x),'[]'::jsonb))
 FROM mealplan.plans p WHERE p.household_id=$1 AND p.id=$2`, id.HouseholdID, pid, days[0], days[6]).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return week, ErrNotFound
	}
	if err != nil {
		return week, err
	}
	if err = planningDecode(raw, &week); err != nil {
		return week, err
	}
	if len(week.Meals) > planningMealLimit {
		return week, Invalid{"Mehr als 42 Mahlzeiten in diesem Zeitraum. Vorhandene Mahlzeiten in den Einzellisten prüfen und die Woche aufteilen."}
	}
	week.Days, week.Effort = days, map[string]PlanningEffort{}
	for _, day := range days {
		week.Effort[day] = PlanningEffort{}
	}
	for _, meal := range week.Meals {
		if len(meal.Participants) > 100 {
			return week, Invalid{"Mahlzeit enthält mehr als100 gespeicherte Teilnehmer"}
		}
		if len(meal.Components) > planningComponentLimit {
			return week, Invalid{"Mahlzeit überschreitet die Komponentengrenze"}
		}
		if meal.Status == "cancelled" || meal.Kind == "out" || meal.Kind == "order" {
			continue
		}
		effort := week.Effort[meal.PlannedOn]
		if len(meal.Components) == 0 {
			effort.add(nil, nil)
		} else {
			for _, component := range meal.Components {
				effort.add(component.PrepMinutes, component.CookMinutes)
			}
		}
		week.Effort[meal.PlannedOn] = effort
	}
	return week, nil
}

func readPlanningMeal(ctx context.Context, q planningQuerier, id auth.Identity, pid, mid string) (PlanningMeal, error) {
	var meal PlanningMeal
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) {
		return meal, ErrNotFound
	}
	var raw []byte
	err := q.QueryRow(ctx, "SELECT "+planningMealJSON+" FROM mealplan.meals m WHERE household_id=$1 AND plan_id=$2 AND id=$3", id.HouseholdID, pid, mid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return meal, ErrNotFound
	}
	if err != nil {
		return meal, err
	}
	if err = planningDecode(raw, &meal); err != nil {
		return meal, err
	}
	if len(meal.Components) > planningComponentLimit {
		return meal, Invalid{"Mahlzeit überschreitet die Komponentengrenze"}
	}
	if len(meal.Participants) > 100 {
		return meal, Invalid{"Mahlzeit enthält mehr als100 gespeicherte Teilnehmer"}
	}
	return meal, nil
}

func (s *Store) PlanningMeal(ctx context.Context, id auth.Identity, pid, mid string) (PlanningMeal, error) {
	return readPlanningMeal(ctx, s.Pool, id, pid, mid)
}

func (s *Store) PlanningComponent(ctx context.Context, id auth.Identity, pid, mid, cid string) (Record, error) {
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) || !value.ValidUUID(cid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('component',`+planningComponentJSON+`,'source',c.snapshot)
 FROM mealplan.components c JOIN mealplan.meals m ON m.household_id=c.household_id AND m.id=c.meal_id WHERE c.household_id=$1 AND m.plan_id=$2 AND c.meal_id=$3 AND c.id=$4`, id.HouseholdID, pid, mid, cid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

// PlanningSource previews an enabled source; committing a selection locks it again.
func (s *Store) PlanningSource(ctx context.Context, id auth.Identity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	var raw []byte
	var vid string
	var revision, generation, size int64
	err := s.Pool.QueryRow(ctx, `SELECT v.id,v.revision,st.data_generation,octet_length(v.snapshot::text),CASE WHEN octet_length(v.snapshot::text)<=1048576 THEN v.snapshot END
 FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id
 JOIN core.module_states st ON st.household_id=v.household_id AND st.module_id='recipes' AND st.state='enabled'
 WHERE v.household_id=$1 AND r.id=$2 ORDER BY v.revision DESC LIMIT 1`, id.HouseholdID, rid).Scan(&vid, &revision, &generation, &size, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if size > planningComponentBytes {
		return nil, Invalid{"Rezeptstand ist größer als 1 MiB. Rezept vor der Übernahme aufteilen."}
	}
	bundle, err := recipeBundle(raw)
	if err != nil {
		return nil, err
	}
	return Record{"source": bundle, "source_version_id": vid, "source_revision": revision, "source_generation": generation}, nil
}

func (s *Store) PlanningSourceStatus(ctx context.Context, id auth.Identity, pid, mid, cid string) (Record, error) {
	if !value.ValidUUID(pid) || !value.ValidUUID(mid) || !value.ValidUUID(cid) {
		return nil, ErrNotFound
	}
	var rid, vid string
	var generation int64
	err := s.Pool.QueryRow(ctx, `SELECT c.source_recipe_id,c.source_version_id,c.source_generation FROM mealplan.components c JOIN mealplan.meals m ON m.household_id=c.household_id AND m.id=c.meal_id WHERE c.household_id=$1 AND m.plan_id=$2 AND c.meal_id=$3 AND c.id=$4`, id.HouseholdID, pid, mid, cid).Scan(&rid, &vid, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	current, err := s.PlanningSource(ctx, id, rid)
	if errors.Is(err, ErrNotFound) || err == nil && Int(current["source_generation"]) != generation {
		return Record{"available": false, "reason": "Die ursprüngliche Quelle ist gelöscht, deaktiviert oder wurde neu angelegt. Der gespeicherte Rezeptstand bleibt erhalten."}, nil
	}
	if err != nil {
		return nil, err
	}
	return Record{"available": true, "changed": String(current["source_version_id"]) != vid, "current": current}, nil
}
