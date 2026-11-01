package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"
)

const choreTaskSummary = `jsonb_build_object('occurrence',to_jsonb(o),'detail',to_jsonb(d),
 'title',coalesce(d.overrides->>'title',v.title),'area_id',v.area_id,'area_name',v.area_name,
 'estimated_minutes',v.estimated_minutes,'difficulty',v.difficulty,'priority',v.priority,'legacy_capture',v.legacy_capture,
 'assignee_name',assignee.display_name,'assignee_active',assignee.id IS NOT NULL AND member.inactive_at IS NULL
  AND assignee.disabled_at IS NULL AND NOT assignee.must_change_password AND member.role IN('admin','editor'),
 'claim_name',claim_user.display_name,'claim_active',d.claim_expires_at>transaction_timestamp() AND claim_member.inactive_at IS NULL
  AND claim_user.id IS NOT NULL AND claim_user.disabled_at IS NULL AND NOT claim_user.must_change_password AND claim_member.role IN('admin','editor'),
 'required_remaining',(SELECT count(*) FROM chores.occurrence_checklist_items item WHERE item.occurrence_id=o.id AND item.required AND NOT item.done),
 'series_active',template.active,'series_paused',profile.paused,'paused_at',profile.paused_at,'resume_at',profile.resume_at,
 'prerequisites_met',chores.dependencies_met(o.household_id,o.id))`

const choreTaskJoins = ` FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id AND d.household_id=o.household_id
 JOIN chores.template_versions v ON v.id=d.template_version_id AND v.household_id=o.household_id
 JOIN chores.task_templates template ON template.household_id=o.household_id AND template.id=o.template_id
 JOIN chores.series_profiles profile ON profile.household_id=o.household_id AND profile.template_id=o.template_id
 LEFT JOIN core.memberships member ON member.household_id=o.household_id AND member.id=o.assignee_id
 LEFT JOIN core.users assignee ON assignee.id=member.user_id
 LEFT JOIN core.memberships claim_member ON claim_member.household_id=o.household_id AND claim_member.id=d.claimed_by
 LEFT JOIN core.users claim_user ON claim_user.id=claim_member.user_id `

type choreCursor struct {
	Kind        string `json:"kind"`
	Household   string `json:"household"`
	User        string `json:"user"`
	Session     string `json:"session"`
	Generation  int64  `json:"generation"`
	Fingerprint string `json:"fingerprint"`
	DueAt       string `json:"due_at"`
	ID          string `json:"id"`
}

func choreLocalDay(now time.Time, timezone string) (string, *time.Location, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", nil, Invalid{"Haushaltszeitzone prüfen"}
	}
	return now.In(location).Format(time.DateOnly), location, nil
}

func choreQueryNormalize(query ChoreQuery, today string) (ChoreQuery, error) {
	query.Search = strings.TrimSpace(query.Search)
	if query.Scope == "" {
		query.Scope = "mine"
	}
	if query.State == "" {
		query.State = "open"
	}
	if query.Through == "" {
		query.Through = today
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 100 || len(query.Cursor) > 4096 || utf8.RuneCountInString(query.Search) > 120 ||
		query.Scope != "mine" && query.Scope != "household" && query.Scope != "unassigned" ||
		query.State != "open" && query.State != "completed" && query.State != "skipped" && query.State != "all" ||
		query.AreaID != "" && !value.ValidUUID(query.AreaID) || query.TemplateID != "" && !value.ValidUUID(query.TemplateID) {
		return ChoreQuery{}, Invalid{"Aufgabenfilter oder Seitenumfang prüfen"}
	}
	through, err := chore.ParseDay(query.Through)
	if err != nil {
		return ChoreQuery{}, Invalid{err.Error()}
	}
	if query.From != "" {
		from, parseErr := chore.ParseDay(query.From)
		if parseErr != nil || from.After(through) || (through.Unix()-from.Unix())/86400 >= chore.MaximumWindow {
			return ChoreQuery{}, Invalid{"Zeitraum auf höchstens 366 Tage begrenzen"}
		}
	}
	return query, nil
}

func (s *Store) ChoreTasks(ctx context.Context, id auth.Identity, input ChoreQuery) (Record, error) {
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	today, _, err := choreLocalDay(scope.now, scope.timezone)
	if err != nil {
		return nil, err
	}
	query, err := choreQueryNormalize(input, today)
	if err != nil {
		return nil, err
	}
	encodedCursor := query.Cursor
	query.Cursor = ""
	fingerprintBytes, _ := json.Marshal(query)
	fingerprintHash := sha256.Sum256(fingerprintBytes)
	fingerprint := hex.EncodeToString(fingerprintHash[:])
	var cursorDue any
	var cursorID any
	if encodedCursor != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(encodedCursor)
		if decodeErr != nil || len(raw) > 2048 {
			return nil, Invalid{"Aufgabenseite ist ungültig; Filter neu laden"}
		}
		var cursor choreCursor
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&cursor) != nil || cursor.Kind != "chores/tasks/v1" || cursor.Household != id.HouseholdID ||
			cursor.User != id.UserID || cursor.Session != id.SessionID || cursor.Generation != scope.generation ||
			cursor.Fingerprint != fingerprint || !value.ValidUUID(cursor.ID) {
			return nil, Invalid{"Aufgabenseite gehört zu einem anderen Filter oder Anmeldestand"}
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, Invalid{"Aufgabenseite enthält zusätzliche Daten"}
		}
		instant, parseErr := time.Parse(time.RFC3339Nano, cursor.DueAt)
		if parseErr != nil {
			return nil, Invalid{"Termin der Aufgabenseite ist ungültig"}
		}
		cursorDue, cursorID = instant, cursor.ID
	}
	throughDay, _ := chore.ParseDay(query.Through)
	end, err := choreAtDay(throughDay.AddDate(0, 0, 1).Format(time.DateOnly), scope.timezone)
	if err != nil {
		return nil, err
	}
	var from any
	if query.From != "" {
		from, err = choreAtDay(query.From, scope.timezone)
		if err != nil {
			return nil, err
		}
	}
	rows, err := choreRows(scope.ctx, scope.tx, query.Limit+1, "SELECT "+choreTaskSummary+choreTaskJoins+`
 WHERE o.household_id=$1 AND ($2='all' OR o.state=$2) AND o.due_at<$3
 AND ($4::timestamptz IS NULL OR o.due_at>=$4::timestamptz)
 AND ($5::timestamptz IS NULL OR(o.due_at,o.id)>($5::timestamptz,$6::uuid))
 AND ($7='' OR v.area_id::text=$7) AND ($8='' OR o.template_id::text=$8)
 AND ($9='' OR position(lower($9) in lower(coalesce(d.overrides->>'title',v.title)))>0)
 AND ($10='household' OR $10='mine' AND(o.assignee_id=$11::uuid OR d.claimed_by=$11::uuid AND d.claim_expires_at>transaction_timestamp())
  OR $10='unassigned' AND(o.assignee_id IS NULL OR member.inactive_at IS NOT NULL OR assignee.disabled_at IS NOT NULL OR assignee.must_change_password OR member.role NOT IN('admin','editor')))
 AND ($13::boolean OR NOT profile.paused OR o.due_at<=profile.paused_at)
 AND ($14::boolean OR template.active)
 ORDER BY o.due_at,o.id LIMIT $12`, id.HouseholdID, query.State, end, from, cursorDue, cursorID,
		query.AreaID, query.TemplateID, query.Search, query.Scope, id.MembershipID, query.Limit+1, query.IncludePaused, query.IncludeArchived)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > query.Limit {
		rows = rows[:query.Limit]
		last, ok := rows[len(rows)-1]["occurrence"].(map[string]any)
		if !ok {
			return nil, errors.New("invalid captured chore row")
		}
		cursor := choreCursor{Kind: "chores/tasks/v1", Household: id.HouseholdID, User: id.UserID,
			Session: id.SessionID, Generation: scope.generation, Fingerprint: fingerprint,
			DueAt: String(last["due_at"]), ID: String(last["id"])}
		raw, marshalErr := json.Marshal(cursor)
		if marshalErr != nil {
			return nil, marshalErr
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return Record{"items": rows, "next_cursor": next, "generation": scope.generation,
		"today": today, "timezone": scope.timezone, "scope": query.Scope, "can_write": id.CanWrite()}, nil
}

func (s *Store) ChoreWork(ctx context.Context, id auth.Identity, occurrenceID string) (Record, error) {
	if !value.ValidUUID(occurrenceID) {
		return nil, ErrNotFound
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	item, err := choreObject(scope.ctx, scope.tx, "SELECT "+choreTaskSummary+choreTaskJoins+
		" WHERE o.household_id=$1 AND o.id=$2", id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	version, err := choreObject(scope.ctx, scope.tx, `SELECT to_jsonb(v) FROM chores.template_versions v
 JOIN chores.occurrence_details d ON d.household_id=v.household_id AND d.template_version_id=v.id
 WHERE d.household_id=$1 AND d.occurrence_id=$2`, id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	checklist, err := choreRows(scope.ctx, scope.tx, 50, `SELECT to_jsonb(item) FROM chores.occurrence_checklist_items item
 WHERE household_id=$1 AND occurrence_id=$2 ORDER BY position,id LIMIT 51`, id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	dependencies, err := choreRows(scope.ctx, scope.tx, 16, `SELECT jsonb_build_object('template_id',dependency.prerequisite_id,
 'title',template.title,'occurrence_id',previous.id,'due_at',previous.due_at,'state',previous.state,'completed_at',previous.completed_at)
 FROM chores.occurrence_details detail JOIN chores.version_dependencies dependency ON dependency.version_id=detail.template_version_id
 JOIN chores.task_templates template ON template.household_id=detail.household_id AND template.id=dependency.prerequisite_id
 LEFT JOIN LATERAL(SELECT source.* FROM chores.task_occurrences source JOIN chores.occurrence_details source_detail ON source_detail.occurrence_id=source.id
 WHERE source.household_id=detail.household_id AND source.template_id=dependency.prerequisite_id
 AND source.due_at<=(SELECT due_at FROM chores.task_occurrences WHERE id=detail.occurrence_id)
 AND source_detail.skipped_reason<>'series_changed' ORDER BY source.due_at DESC,source.id DESC LIMIT 1) previous ON true
 WHERE detail.household_id=$1 AND detail.occurrence_id=$2 ORDER BY dependency.prerequisite_id LIMIT 17`, id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	occurrence, ok := item["occurrence"].(map[string]any)
	if !ok {
		return nil, errors.New("missing captured chore occurrence")
	}
	facts, err := choreObject(scope.ctx, scope.tx, `SELECT jsonb_build_object('completion',
 (SELECT to_jsonb(c) FROM chores.completions c WHERE c.household_id=$1 AND c.occurrence_id=$2),
 'last_completed_at',(SELECT max(c.completed_at) FROM chores.completions c WHERE c.household_id=$1 AND c.template_id=$3),
 'next_open',(SELECT jsonb_build_object('id',o.id,'due_at',o.due_at) FROM chores.task_occurrences o
 WHERE o.household_id=$1 AND o.template_id=$3 AND o.state='open' AND o.id<>$2 ORDER BY o.due_at,o.id LIMIT 1))`,
		id.HouseholdID, occurrenceID, occurrence["template_id"])
	if err != nil {
		return nil, err
	}
	origins, err := choreRows(scope.ctx, scope.tx, 16, `SELECT jsonb_build_object('kind','maintenance_task','label','Fahrzeugwartung',
 'path','/m/fuel/maintenance_plans/'||link.source_id::text)
 FROM core.resource_links link JOIN core.module_states state ON state.household_id=link.household_id AND state.module_id='fuel' AND state.state='enabled'
 WHERE link.household_id=$1 AND link.source_module='fuel' AND link.source_type='maintenance_plans' AND link.relation='maintenance_task'
 AND link.target_module='chores' AND link.target_type='task_occurrences' AND link.target_id=$2 ORDER BY link.id LIMIT 17`, id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	return Record{"item": item, "snapshot": version, "checklist": checklist, "dependencies": dependencies, "facts": facts, "origins": origins,
		"generation": scope.generation, "can_write": id.CanWrite(), "timezone": scope.timezone}, nil
}
