package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type CookingSession struct {
	ID              string             `json:"id"`
	UserID          string             `json:"user_id"`
	SourceVersionID string             `json:"source_version_id"`
	SourceRevision  string             `json:"source_revision"`
	Servings        string             `json:"servings"`
	CurrentStep     int                `json:"current_step"`
	CheckedIDs      []string           `json:"checked_ids"`
	State           string             `json:"state"`
	Version         int64              `json:"version"`
	Source          RecipeBundle       `json:"source"`
	Quantities      []*CookingQuantity `json:"quantities"`
	Timers          []CookingTimer     `json:"timers"`
	ServerTime      time.Time          `json:"server_time"`
	Today           string             `json:"today"`
	ResultID        string             `json:"result_id"`
}

type cookingQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readCooking(ctx context.Context, q cookingQuerier, id auth.Identity, sid string) (CookingSession, error) {
	var raw []byte
	var session CookingSession
	err := q.QueryRow(ctx, `SELECT jsonb_build_object(
 'id',s.id,'user_id',s.user_id,'source_version_id',s.source_version_id,'source_revision',v.revision::text,
 'servings',s.servings::text,'current_step',s.current_step,'checked_ids',s.checked_ids,'state',s.state,'version',s.version,
 'source',v.snapshot,'server_time',clock_timestamp(),'today',(clock_timestamp() AT TIME ZONE h.timezone)::date::text,
 'result_id',coalesce((SELECT r.id::text FROM recipes.cooking_records r WHERE r.household_id=s.household_id AND r.session_id=s.id),''),
 'timers',coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY t.created_at,t.id) FROM
 (SELECT id,name,duration_seconds,state,deadline,remaining_millis,version,created_at FROM recipes.cooking_timers WHERE household_id=s.household_id AND session_id=s.id ORDER BY created_at,id LIMIT 21) t),'[]'::jsonb))
 FROM recipes.cooking_sessions s JOIN recipes.versions v ON v.household_id=s.household_id AND v.id=s.source_version_id
 JOIN core.households h ON h.id=s.household_id
 WHERE s.household_id=$1 AND s.user_id=$2 AND s.id=$3`, id.HouseholdID, id.UserID, sid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, ErrNotFound
	}
	if err != nil {
		return session, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&session); err != nil {
		return session, err
	}
	if len(session.Timers) > 20 || len(session.Source.Ingredients) > 500 || len(session.Source.Steps) == 0 || len(session.Source.Steps) > 500 {
		return session, Invalid{"Kochsitzung überschreitet die unterstützten Grenzen"}
	}
	base, err := cookingServings(String(session.Source.Recipe["base_servings"]))
	if err != nil {
		return session, err
	}
	servings, err := cookingServings(session.Servings)
	if err != nil {
		return session, err
	}
	session.Quantities = make([]*CookingQuantity, len(session.Source.Ingredients))
	for i, ingredient := range session.Source.Ingredients {
		session.Quantities[i], err = cookingQuantity(ingredient["quantity"], base, servings)
		if err != nil {
			return session, err
		}
	}
	return session, nil
}

func (s *Store) CookingSession(ctx context.Context, id auth.Identity, sid string) (CookingSession, error) {
	if !value.ValidUUID(sid) {
		return CookingSession{}, ErrNotFound
	}
	return readCooking(ctx, s.Pool, id, sid)
}

func (s *Store) CookingSource(ctx context.Context, id auth.Identity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	var raw []byte
	var vid, revision, today string
	err := s.Pool.QueryRow(ctx, `SELECT v.id,v.revision::text,v.snapshot,(clock_timestamp() AT TIME ZONE h.timezone)::date::text
 FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id JOIN core.households h ON h.id=v.household_id
 WHERE v.household_id=$1 AND v.recipe_id=$2 ORDER BY v.revision DESC LIMIT 1`, id.HouseholdID, rid).Scan(&vid, &revision, &raw, &today)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	bundle, err := recipeBundle(raw)
	if err != nil {
		return nil, err
	}
	return Record{"source": bundle, "source_version_id": vid, "source_revision": revision, "today": today}, nil
}

func (s *Store) CookingSessions(ctx context.Context, id auth.Identity) ([]Record, error) {
	rows, err := s.Pool.Query(ctx, `SELECT jsonb_build_object('id',s.id,'title',v.snapshot->'recipe'->>'title','servings',s.servings::text,'current_step',s.current_step,'steps',jsonb_array_length(v.snapshot->'steps'),'created_at',s.created_at)
 FROM recipes.cooking_sessions s JOIN recipes.versions v ON v.household_id=s.household_id AND v.id=s.source_version_id
 WHERE s.household_id=$1 AND s.user_id=$2 AND s.state='active' ORDER BY s.created_at DESC,s.id DESC LIMIT 11`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return cookingRecords(rows)
}

func cookingRecords(rows pgx.Rows) ([]Record, error) {
	items := []Record{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		record, err := decode(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	return items, rows.Err()
}

func (s *Store) CookingHistory(ctx context.Context, id auth.Identity, rid, before string) ([]Record, error) {
	if rid != "" && !value.ValidUUID(rid) || before != "" && !value.ValidUUID(before) {
		return nil, Invalid{"Ungültiger Rezept- oder Verlaufszeiger"}
	}
	rows, err := s.Pool.Query(ctx, `SELECT jsonb_build_object('id',c.id,'title',v.snapshot->'recipe'->>'title','source_revision',v.revision::text,'cooked_on',c.cooked_on,'actual_servings',c.actual_servings::text,'rating',c.rating,'author',u.display_name)
 FROM recipes.cooking_records c JOIN recipes.versions v ON v.household_id=c.household_id AND v.id=c.source_version_id JOIN core.users u ON u.id=c.user_id
 WHERE c.household_id=$1 AND ($2='' OR v.recipe_id=nullif($2,'')::uuid)
 AND ($3='' OR (c.created_at,c.id)<(SELECT b.created_at,b.id FROM recipes.cooking_records b WHERE b.household_id=$1 AND b.id=nullif($3,'')::uuid))
 ORDER BY c.created_at DESC,c.id DESC LIMIT 21`, id.HouseholdID, rid, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return cookingRecords(rows)
}

func (s *Store) CookingRecord(ctx context.Context, id auth.Identity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT to_jsonb(c)||jsonb_build_object('actual_servings',c.actual_servings::text,'source',v.snapshot,'source_revision',v.revision::text,'author',u.display_name)
 FROM recipes.cooking_records c JOIN recipes.versions v ON v.household_id=c.household_id AND v.id=c.source_version_id JOIN core.users u ON u.id=c.user_id
 WHERE c.household_id=$1 AND c.id=$2`, id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}
