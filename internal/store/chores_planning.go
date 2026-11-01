package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"
)

type choreHistoryCursor struct {
	Household   string `json:"household"`
	User        string `json:"user"`
	Session     string `json:"session"`
	Generation  int64  `json:"generation"`
	From        string `json:"from"`
	Through     string `json:"through"`
	Person      string `json:"person"`
	Area        string `json:"area"`
	CompletedAt string `json:"completed_at"`
	ID          string `json:"id"`
}

func (s *Store) ChoreDayPlan(ctx context.Context, id auth.Identity, day string, budget *int) (Record, error) {
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	if day == "" {
		day, _, err = choreLocalDay(scope.now, scope.timezone)
		if err != nil {
			return nil, err
		}
	}
	date, err := chore.ParseDay(day)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	start, err := choreAtDay(day, scope.timezone)
	if err != nil {
		return nil, err
	}
	end, err := choreAtDay(date.AddDate(0, 0, 1).Format(time.DateOnly), scope.timezone)
	if err != nil {
		return nil, err
	}
	prefs, err := chorePreferences(scope.ctx, scope.tx, id.HouseholdID, id.UserID, scope.timezone)
	if err != nil {
		return nil, err
	}
	minutes := int(Int(prefs["daily_minutes"]))
	if budget != nil {
		minutes = *budget
	}
	var difficulty *int
	if prefs["max_difficulty"] != nil {
		n := int(Int(prefs["max_difficulty"]))
		difficulty = &n
	}
	items, err := choreRows(scope.ctx, scope.tx, 500, `SELECT jsonb_build_object('id',o.id,'title',coalesce(d.overrides->>'title',v.title),
 'area',coalesce(v.area_name,''),'due_at',o.due_at,'estimated_minutes',v.estimated_minutes,'difficulty',v.difficulty,'priority',v.priority,
 'overdue',o.due_at<$4,'eligible',true,'blocked_reason',CASE
 WHEN EXISTS(SELECT 1 FROM chores.absences a WHERE a.household_id=$1 AND a.membership_id=$2 AND a.starts_on<=$3::date AND a.ends_on>=$3::date)
 THEN 'Für diesen Tag ist eine Abwesenheit eingetragen'
 WHEN NOT chores.dependencies_met(o.household_id,o.id) THEN 'Voraussetzung ist noch nicht erfüllt'
 WHEN d.claimed_by IS NOT NULL AND d.claimed_by<>$2::uuid AND d.claim_expires_at>transaction_timestamp() THEN 'Von einer anderen Person übernommen'
 WHEN o.assignee_id IS NOT NULL AND o.assignee_id<>$2::uuid AND assigned.inactive_at IS NULL AND assigned.role IN('admin','editor')
 AND assigned_user.id IS NOT NULL AND assigned_user.disabled_at IS NULL AND NOT assigned_user.must_change_password
 AND NOT coalesce(d.claimed_by=$2::uuid AND d.claim_expires_at>transaction_timestamp(),false) THEN 'Einer anderen Person zugewiesen'
 WHEN EXISTS(SELECT 1 FROM chores.rotation_members r WHERE r.version_id=v.id) AND NOT EXISTS(SELECT 1 FROM chores.rotation_members r WHERE r.version_id=v.id AND r.membership_id=$2 AND r.eligible)
 THEN 'Nicht als geeignete Person in diesem Vorlagenstand hinterlegt' ELSE '' END)
 FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 JOIN chores.template_versions v ON v.id=d.template_version_id JOIN chores.series_profiles p ON p.template_id=o.template_id
 JOIN chores.task_templates t ON t.id=o.template_id
 LEFT JOIN core.memberships assigned ON assigned.household_id=o.household_id AND assigned.id=o.assignee_id
 LEFT JOIN core.users assigned_user ON assigned_user.id=assigned.user_id
 WHERE o.household_id=$1 AND o.state='open' AND t.active AND (NOT p.paused OR o.due_at<=p.paused_at) AND o.due_at<$5
 AND(o.assignee_id=$2::uuid OR o.assignee_id IS NULL OR assigned.inactive_at IS NOT NULL OR assigned.role NOT IN('admin','editor')
 OR assigned_user.disabled_at IS NOT NULL OR assigned_user.must_change_password OR d.claimed_by=$2::uuid AND d.claim_expires_at>transaction_timestamp())
 ORDER BY o.due_at,o.id LIMIT 501`, id.HouseholdID, id.MembershipID, day, start, end)
	if err != nil {
		return nil, err
	}
	plannable := make([]chore.PlanItem, 0, len(items))
	for _, record := range items {
		raw, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			return nil, marshalErr
		}
		var item chore.PlanItem
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		plannable = append(plannable, item)
	}
	plan, err := chore.PlanDay(plannable, minutes, difficulty)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	return Record{"day": day, "plan": plan, "generation": scope.generation, "timezone": scope.timezone, "proposal_only": true}, nil
}

func (s *Store) ChoreHistory(ctx context.Context, id auth.Identity, from, through, person, area, after string) (Record, error) {
	if person != "" && !value.ValidUUID(person) || area != "" && !value.ValidUUID(area) || len(after) > 4096 {
		return nil, Invalid{"Verlaufsfilter prüfen"}
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	today, _, err := choreLocalDay(scope.now, scope.timezone)
	if err != nil {
		return nil, err
	}
	if through == "" {
		through = today
	}
	last, err := chore.ParseDay(through)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	if from == "" {
		from = last.AddDate(0, 0, -30).Format(time.DateOnly)
	}
	first, err := chore.ParseDay(from)
	if err != nil || last.Before(first) || (last.Unix()-first.Unix())/86400 >= 366 {
		return nil, Invalid{"Verlauf auf höchstens 366 Tage eingrenzen"}
	}
	begin, err := choreAtDay(from, scope.timezone)
	if err != nil {
		return nil, err
	}
	end, err := choreAtDay(last.AddDate(0, 0, 1).Format(time.DateOnly), scope.timezone)
	if err != nil {
		return nil, err
	}
	const filter = ` FROM chores.completions c JOIN chores.template_versions v ON v.id=c.template_version_id
 WHERE c.household_id=$1 AND c.completed_at>=$2 AND c.completed_at<$3
 AND ($4='' OR c.actor_membership_id=nullif($4,'')::uuid) AND ($5='' OR v.area_id=nullif($5,'')::uuid)`
	var cursorTime, cursorID any
	if after != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(after)
		if decodeErr != nil || len(raw) > 2048 {
			return nil, Invalid{"Verlaufsseite ist ungültig"}
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		var c choreHistoryCursor
		if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.Household != id.HouseholdID || c.User != id.UserID || c.Session != id.SessionID ||
			c.Generation != scope.generation || c.From != from || c.Through != through || c.Person != person || c.Area != area || !value.ValidUUID(c.ID) {
			return nil, Invalid{"Verlaufsseite gehört zu einem anderen Filter oder Anmeldestand"}
		}
		at, parseErr := time.Parse(time.RFC3339Nano, c.CompletedAt)
		if parseErr != nil {
			return nil, Invalid{"Verlaufszeitpunkt ist ungültig"}
		}
		cursorTime, cursorID = at, c.ID
	}
	items, err := choreRows(scope.ctx, scope.tx, 101, `SELECT to_jsonb(c)||jsonb_build_object('title',coalesce(c.snapshot->>'title',v.title),
 'area_name',coalesce(c.snapshot->>'area_name',v.area_name),'actor_name',c.snapshot->>'actor_display_name')`+filter+`
 AND ($6::timestamptz IS NULL OR(c.completed_at,c.id)<($6::timestamptz,$7::uuid)) ORDER BY c.completed_at DESC,c.id DESC LIMIT 101`, id.HouseholdID, begin, end, person, area, cursorTime, cursorID)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		last := items[99]
		c := choreHistoryCursor{Household: id.HouseholdID, User: id.UserID, Session: id.SessionID, Generation: scope.generation,
			From: from, Through: through, Person: person, Area: area, CompletedAt: String(last["completed_at"]), ID: String(last["id"])}
		raw, marshalErr := json.Marshal(c)
		if marshalErr != nil {
			return nil, marshalErr
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	totals, err := choreObject(scope.ctx, scope.tx, `SELECT jsonb_build_object('completions',count(*),'known_minutes',coalesce(sum(c.actual_minutes),0),
 'unknown_duration',count(*) FILTER(WHERE c.actual_minutes IS NULL),'unknown_actor',count(*) FILTER(WHERE c.actor_user_id IS NULL))`+filter, id.HouseholdID, begin, end, person, area)
	if err != nil {
		return nil, err
	}
	return Record{"items": items, "next_cursor": next, "totals": totals, "from": from, "through": through, "timezone": scope.timezone, "generation": scope.generation}, nil
}
