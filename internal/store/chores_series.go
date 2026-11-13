package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func choreTextValid(text string, maximum int, required bool) bool {
	return utf8.ValidString(text) && !strings.ContainsRune(text, 0) && utf8.RuneCountInString(text) <= maximum &&
		(!required || strings.TrimSpace(text) != "")
}

func normalizeChoreSeries(input ChoreSeriesInput) (ChoreSeriesInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	if !choreTextValid(input.Title, 200, true) || !choreTextValid(input.Description, 10000, false) ||
		input.ID != "" && (!value.ValidUUID(input.ID) || input.Version < 1 || input.BaseVersion < 1) ||
		input.AreaID != "" && !value.ValidUUID(input.AreaID) ||
		input.EstimatedMinutes != nil && (*input.EstimatedMinutes < 1 || *input.EstimatedMinutes > 1440) ||
		input.Difficulty != nil && (*input.Difficulty < 1 || *input.Difficulty > 5) || input.Priority < 0 || input.Priority > 3 ||
		len(input.Materials) > 20 || len(input.Checklist) > 50 || len(input.Members) > 32 || len(input.PrerequisiteIDs) > 16 {
		return ChoreSeriesInput{}, Invalid{"Titel, Beschreibung, Aufwand, Umfang und Versionsstand prüfen"}
	}
	var err error
	input.Schedule, err = chore.Normalize(input.Schedule)
	if err != nil {
		return ChoreSeriesInput{}, Invalid{err.Error()}
	}
	input.Materials = append([]string{}, input.Materials...)
	for index, material := range input.Materials {
		input.Materials[index] = strings.TrimSpace(material)
		if !choreTextValid(input.Materials[index], 300, true) {
			return ChoreSeriesInput{}, Invalid{"Materialzeilen auf je 300 Zeichen begrenzen"}
		}
	}
	if input.AssignmentKind == "" {
		input.AssignmentKind = "unassigned"
		if input.AssigneeID != "" {
			input.AssignmentKind = "fixed"
		}
	}
	if input.AssignmentKind != "fixed" && input.AssignmentKind != "rotation" && input.AssignmentKind != "unassigned" ||
		input.AssignmentKind == "fixed" && !value.ValidUUID(input.AssigneeID) ||
		input.AssignmentKind != "fixed" && input.AssigneeID != "" {
		return ChoreSeriesInput{}, Invalid{"Zuständigkeitsart und Person passen nicht zusammen"}
	}
	memberIDs := make(map[string]bool, len(input.Members))
	eligible := 0
	for _, member := range input.Members {
		if !value.ValidUUID(member.MembershipID) || memberIDs[member.MembershipID] {
			return ChoreSeriesInput{}, Invalid{"Rotationspersonen müssen eindeutig sein"}
		}
		memberIDs[member.MembershipID] = true
		if member.Eligible {
			eligible++
		}
	}
	if input.AssignmentKind == "rotation" && eligible == 0 {
		return ChoreSeriesInput{}, Invalid{"Mindestens eine geeignete Rotationsperson auswählen"}
	}
	if input.AssignmentKind == "fixed" && len(input.Members) > 0 {
		found := false
		for _, member := range input.Members {
			found = found || member.MembershipID == input.AssigneeID && member.Eligible
		}
		if !found {
			return ChoreSeriesInput{}, Invalid{"Verantwortliche Person muss für diese Aufgabe geeignet sein"}
		}
	}
	keys := make(map[string]bool, len(input.Checklist))
	input.Checklist = append([]ChoreChecklistInput{}, input.Checklist...)
	for index := range input.Checklist {
		item := &input.Checklist[index]
		item.Title = strings.TrimSpace(item.Title)
		if !choreTextValid(item.Title, 300, true) || item.Key != "" && (!value.ValidUUID(item.Key) || keys[item.Key]) {
			return ChoreSeriesInput{}, Invalid{"Unteraufgaben und eindeutige Punktkennungen prüfen"}
		}
		if item.Key != "" {
			keys[item.Key] = true
		}
	}
	dependencies := make(map[string]bool, len(input.PrerequisiteIDs))
	input.PrerequisiteIDs = append([]string{}, input.PrerequisiteIDs...)
	for _, prerequisite := range input.PrerequisiteIDs {
		if !value.ValidUUID(prerequisite) || dependencies[prerequisite] || prerequisite == input.ID {
			return ChoreSeriesInput{}, Invalid{"Voraussetzungen müssen eindeutig und von der Aufgabe verschieden sein"}
		}
		dependencies[prerequisite] = true
	}
	slices.Sort(input.PrerequisiteIDs)
	for _, day := range []string{input.EffectiveOn, input.CatchUpFrom} {
		if day != "" {
			if _, err = chore.ParseDay(day); err != nil {
				return ChoreSeriesInput{}, Invalid{err.Error()}
			}
		}
	}
	return input, nil
}

func choreOptionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func choreIntegers(value any) []int {
	result := []int{}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			result = append(result, int(Int(item)))
		}
	}
	return result
}

func choreSchedule(record Record) (chore.Schedule, error) {
	clock := String(record["local_time"])
	if len(clock) >= 5 {
		clock = clock[:5]
	}
	return chore.Normalize(chore.Schedule{Kind: String(record["rule_kind"]), Interval: int(Int(record["interval_value"])),
		Anchor: String(record["anchor_kind"]), StartsOn: String(record["starts_on"]), LocalTime: clock,
		Timezone: String(record["timezone"]), Weekdays: choreIntegers(record["weekdays"]), Months: choreIntegers(record["months"]),
		MonthDay: int(Int(record["month_day"])), EndsOn: String(record["ends_on"]), Policy: String(record["dst_policy"])})
}

func choreSeriesRecord(ctx context.Context, tx pgx.Tx, household, templateID string, lock bool) (Record, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF template,profile"
	}
	return choreObject(ctx, tx, `SELECT jsonb_build_object('template',to_jsonb(template),'profile',to_jsonb(profile),
 'snapshot',to_jsonb(captured)) FROM chores.task_templates template
 JOIN chores.series_profiles profile ON profile.household_id=template.household_id AND profile.template_id=template.id
 JOIN chores.template_versions captured ON captured.id=profile.current_version_id
 WHERE template.household_id=$1 AND template.id=$2`+suffix, household, templateID)
}

func (s *Store) ChoreSeries(ctx context.Context, id auth.Identity, templateID string) (Record, error) {
	if !value.ValidUUID(templateID) {
		return nil, ErrNotFound
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	result, err := choreSeriesRecord(scope.ctx, scope.tx, id.HouseholdID, templateID, false)
	if err != nil {
		return nil, err
	}
	profile, ok := result["profile"].(map[string]any)
	if !ok {
		return nil, errors.New("missing chore profile")
	}
	versionID := String(profile["current_version_id"])
	for _, part := range []struct {
		name, query string
		limit       int
	}{
		{"checklist", `SELECT jsonb_build_object('key',item_key,'title',title,'required',required) FROM chores.version_checklist_items WHERE household_id=$1 AND version_id=$2 ORDER BY position,id LIMIT 51`, 50},
		{"members", `SELECT jsonb_build_object('membership_id',rotation.membership_id,'eligible',rotation.eligible,'name',u.display_name,
 'active',member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password AND member.role IN('admin','editor'))
 FROM chores.rotation_members rotation JOIN core.memberships member ON member.household_id=rotation.household_id AND member.id=rotation.membership_id
 JOIN core.users u ON u.id=member.user_id WHERE rotation.household_id=$1 AND rotation.version_id=$2 ORDER BY rotation.position,rotation.id LIMIT 33`, 32},
		{"prerequisites", `SELECT jsonb_build_object('id',template.id,'title',template.title,'active',template.active)
 FROM chores.version_dependencies dependency JOIN chores.task_templates template ON template.household_id=dependency.household_id AND template.id=dependency.prerequisite_id
 WHERE dependency.household_id=$1 AND dependency.version_id=$2 ORDER BY template.title,template.id LIMIT 17`, 16},
	} {
		result[part.name], err = choreRows(scope.ctx, scope.tx, part.limit, part.query, id.HouseholdID, versionID)
		if err != nil {
			return nil, err
		}
	}
	result["generation"], result["can_write"] = scope.generation, id.CanWrite()
	result["timezone"] = scope.timezone
	return result, nil
}

func choreValidateReferences(ctx context.Context, tx pgx.Tx, household string, in ChoreSeriesInput) error {
	if in.Schedule.Timezone != "" {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=$1)`, in.Schedule.Timezone).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return Invalid{"Zeitzone muss auch in der Datenbank verfügbar sein"}
		}
	}
	if in.AreaID != "" {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT true FROM chores.areas WHERE household_id=$1 AND id=$2 AND archived_at IS NULL`,
			household, in.AreaID).Scan(&allowed); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Invalid{"Bereich ist nicht mehr verfügbar"}
			}
			return err
		}
	}
	memberIDs := []string{}
	if in.AssigneeID != "" {
		memberIDs = append(memberIDs, in.AssigneeID)
	}
	for _, member := range in.Members {
		if !slices.Contains(memberIDs, member.MembershipID) {
			memberIDs = append(memberIDs, member.MembershipID)
		}
	}
	if len(memberIDs) > 0 {
		var count int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 WHERE member.household_id=$1 AND member.id=ANY($2::uuid[]) AND member.inactive_at IS NULL
 AND member.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password`, household, memberIDs).Scan(&count)
		if err != nil {
			return err
		}
		if count != len(memberIDs) {
			return Invalid{"Person ist nicht aktiv oder darf Aufgaben nicht bearbeiten"}
		}
	}
	if len(in.PrerequisiteIDs) > 0 {
		var count int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM chores.task_templates WHERE household_id=$1 AND id=ANY($2::uuid[])`,
			household, in.PrerequisiteIDs).Scan(&count)
		if err != nil {
			return err
		}
		if count != len(in.PrerequisiteIDs) {
			return Invalid{"Voraussetzung ist nicht verfügbar"}
		}
	}
	return nil
}

func choreGraphCheck(ctx context.Context, tx pgx.Tx, household, templateID string, prerequisites []string) error {
	if len(prerequisites) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE reachable(id) AS(
 SELECT unnest($2::uuid[]) UNION SELECT dependency.prerequisite_id FROM reachable r
 JOIN chores.series_profiles profile ON profile.household_id=$1 AND profile.template_id=r.id
 JOIN chores.version_dependencies dependency ON dependency.version_id=profile.current_version_id)
 SELECT template.id,coalesce(array_agg(dependency.prerequisite_id::text ORDER BY dependency.prerequisite_id)
 FILTER(WHERE dependency.prerequisite_id IS NOT NULL),'{}'::text[])
 FROM reachable r JOIN chores.task_templates template ON template.id=r.id AND template.household_id=$1
 JOIN chores.series_profiles profile ON profile.template_id=template.id
 LEFT JOIN chores.version_dependencies dependency ON dependency.version_id=profile.current_version_id
 GROUP BY template.id ORDER BY template.id LIMIT 2001`, household, prerequisites)
	if err != nil {
		return err
	}
	graph := make(map[string][]string)
	for rows.Next() {
		var id string
		var edges []string
		if err = rows.Scan(&id, &edges); err != nil {
			break
		}
		graph[id] = edges
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	graph[templateID] = append([]string{}, prerequisites...)
	if err = chore.ValidateGraph(graph); err != nil {
		return Invalid{err.Error()}
	}
	return nil
}

func choreAtDay(day, timezone string) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, Invalid{"Zeitzone ist nicht verfügbar"}
	}
	return chore.DayBoundary(day, location)
}

type choreTiming struct {
	now             time.Time
	effectiveAt     time.Time
	materializeFrom time.Time
	needsPastChoice bool
}

func choreSeriesTiming(ctx context.Context, tx pgx.Tx, household string, in ChoreSeriesInput,
	current Record) (ChoreSeriesInput, choreTiming, error) {
	var now time.Time
	var timezone string
	if err := tx.QueryRow(ctx, "SELECT transaction_timestamp(),timezone FROM core.households WHERE id=$1", household).
		Scan(&now, &timezone); err != nil {
		return in, choreTiming{}, err
	}
	today, _, err := choreLocalDay(now, in.Schedule.Timezone)
	if err != nil {
		return in, choreTiming{}, err
	}
	if in.EffectiveOn == "" {
		in.EffectiveOn = today
		if in.ID == "" && in.Schedule.StartsOn < today {
			in.EffectiveOn = in.Schedule.StartsOn
		}
	}
	if in.Schedule.Anchor == "completion" && in.EffectiveOn > today {
		return in, choreTiming{}, Invalid{"Erledigungsregeln gelten ab heute für die nächste Wiederholung; zukünftige feste Termine im Kalendermodus ändern"}
	}
	if in.ID != "" && in.EffectiveOn < today {
		return in, choreTiming{}, Invalid{"Serienänderungen gelten ab heute; vergangene Erledigungen bleiben unverändert"}
	}
	effective, err := choreAtDay(in.EffectiveOn, in.Schedule.Timezone)
	if err != nil {
		return in, choreTiming{}, Invalid{err.Error()}
	}
	timing := choreTiming{now: now, effectiveAt: effective}
	if in.ID != "" && in.EffectiveOn == today && effective.Before(now) {
		timing.effectiveAt = now
	}
	if current != nil {
		profile, _ := current["profile"].(map[string]any)
		if profile["managed"] == true {
			timing.materializeFrom, err = time.Parse(time.RFC3339Nano, String(profile["materialize_from"]))
			if err != nil {
				return in, timing, errors.New("managed chore materialization start missing")
			}
			in.CatchUpFrom, in.AcknowledgePast = "", false
			return in, timing, nil
		}
	}
	if in.CatchUpFrom == "" {
		in.CatchUpFrom = in.Schedule.StartsOn
		if in.ID != "" {
			in.CatchUpFrom = in.EffectiveOn
		}
	}
	if in.Schedule.Anchor == "completion" && in.CatchUpFrom > today {
		in.CatchUpFrom = today
	}
	minimum, _ := chore.ParseDay(today)
	minimum = minimum.AddDate(0, 0, -90)
	if in.Schedule.Kind != "once" && in.Schedule.Anchor != "completion" && in.CatchUpFrom < minimum.Format(time.DateOnly) {
		in.CatchUpFrom = today
		timing.needsPastChoice = !in.AcknowledgePast
	}
	if in.CatchUpFrom > in.Schedule.StartsOn && !in.AcknowledgePast && in.Schedule.Kind != "once" && in.Schedule.Anchor != "completion" {
		timing.needsPastChoice = true
	}
	timing.materializeFrom, err = choreAtDay(in.CatchUpFrom, in.Schedule.Timezone)
	if err != nil {
		return in, timing, Invalid{err.Error()}
	}
	if timing.materializeFrom.Before(timing.effectiveAt) {
		timing.materializeFrom = timing.effectiveAt
	}
	return in, timing, nil
}

func (s *Store) PreviewChoreSeries(ctx context.Context, id auth.Identity, input ChoreSeriesInput) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	in, err := normalizeChoreSeries(input)
	if err != nil {
		return nil, err
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	if in.Generation != scope.generation {
		return nil, ErrConflict
	}
	var current Record
	if in.ID != "" {
		current, err = choreSeriesRecord(scope.ctx, scope.tx, id.HouseholdID, in.ID, false)
		if err != nil {
			return nil, err
		}
	}
	in, timing, err := choreSeriesTiming(scope.ctx, scope.tx, id.HouseholdID, in, current)
	if err != nil {
		return nil, err
	}
	if err = choreValidateReferences(scope.ctx, scope.tx, id.HouseholdID, in); err != nil {
		return nil, err
	}
	templateID := in.ID
	if templateID == "" {
		templateID = value.UUID()
	}
	if err = choreGraphCheck(scope.ctx, scope.tx, id.HouseholdID, templateID, in.PrerequisiteIDs); err != nil {
		return nil, err
	}
	location, _ := time.LoadLocation(in.Schedule.Timezone)
	from := timing.materializeFrom.In(location).Format(time.DateOnly)
	if effectiveDay := timing.effectiveAt.In(location).Format(time.DateOnly); from < effectiveDay {
		from = effectiveDay
	}
	first, _ := chore.ParseDay(from)
	through := first.AddDate(0, 0, 365)
	if through.Year() > 2200 {
		through = time.Date(2200, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	projection, err := chore.Project(in.Schedule, from, through.Format(time.DateOnly), 13)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	if in.Schedule.Anchor == "scheduled" {
		projection.Slots = slices.DeleteFunc(projection.Slots, func(slot chore.Slot) bool {
			return slot.DueAt.Before(timing.effectiveAt) || slot.DueAt.Before(timing.materializeFrom)
		})
		if len(projection.Slots) > 12 {
			projection.Slots, projection.More = projection.Slots[:12], true
		}
	}
	var retained Record
	if in.Schedule.Anchor == "completion" {
		if in.ID != "" {
			retained, err = choreRetainedOccurrence(scope.ctx, scope.tx, id.HouseholdID, in.ID, timing.effectiveAt)
			if err != nil {
				return nil, err
			}
		}
		projection.Slots = []chore.Slot{}
		if retained != nil {
			due, parseErr := choreInstant(retained["due_at"])
			if parseErr != nil {
				return nil, parseErr
			}
			projection.Slots = append(projection.Slots, chore.Slot{DueAt: due, Day: due.In(location).Format(time.DateOnly), LocalTime: due.In(location).Format("15:04"), Timezone: in.Schedule.Timezone, Resolution: "existing_unchanged"})
		} else {
			var last time.Time
			if in.ID != "" {
				last, _, err = choreCompletionAnchor(scope.ctx, scope.tx, id.HouseholdID, in.ID)
				if err != nil {
					return nil, err
				}
			}
			var slot chore.Slot
			if !last.IsZero() {
				slot, err = chore.AfterCompletion(in.Schedule, last)
			} else {
				firstProjection, projectErr := chore.Project(in.Schedule, in.Schedule.StartsOn, in.Schedule.StartsOn, 1)
				err = projectErr
				if len(firstProjection.Slots) > 0 {
					slot = firstProjection.Slots[0]
				}
			}
			if errors.Is(err, chore.ErrNoOccurrence) {
				err = nil
			}
			if err != nil {
				return nil, Invalid{err.Error()}
			}
			if !slot.DueAt.IsZero() {
				projection.Slots = append(projection.Slots, slot)
			}
		}
		projection.More = false
	}
	explanation, _ := chore.Explain(in.Schedule)
	var affected int
	if in.ID != "" {
		err = scope.tx.QueryRow(scope.ctx, `SELECT count(*) FROM chores.task_occurrences
 WHERE household_id=$1 AND template_id=$2 AND state='open' AND due_at>=$3`, id.HouseholdID, in.ID, timing.effectiveAt).Scan(&affected)
		if err != nil {
			return nil, err
		}
		if retained != nil {
			due, parseErr := choreInstant(retained["due_at"])
			if parseErr != nil {
				return nil, parseErr
			}
			if !due.Before(timing.effectiveAt) {
				affected--
			}
		}
	}
	paused := false
	if current != nil {
		profile, _ := current["profile"].(map[string]any)
		paused = profile["paused"] == true
	}
	if paused {
		projection.Slots = []chore.Slot{}
		projection.More = false
	}
	return Record{"normalized": in, "projection": projection, "explanation": explanation, "scheduling_paused": paused,
		"effective_at": timing.effectiveAt, "affected_open": affected, "needs_past_confirmation": timing.needsPastChoice,
		"completion_mode": in.Schedule.Anchor == "completion", "retained_open": retained, "generation": scope.generation,
		"note": "Kalenderänderungen sagen betroffene zukünftige Vorkommen ab; bei Erledigungsankern bleibt höchstens der früheste offene Termin unverändert. Weitere Folgetermine entstehen erst nach tatsächlicher Entscheidung."}, nil
}

func (s *Store) SaveChoreSeries(ctx context.Context, id auth.Identity, key, requestID string, input ChoreSeriesInput) (Record, error) {
	in, err := normalizeChoreSeries(input)
	if err != nil {
		return nil, err
	}
	if in.EffectiveOn == "" {
		return nil, Invalid{"Zuerst die vollständige Vorschau mit Gültigkeitsdatum prüfen"}
	}
	var current Record
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		if in.ID == "" {
			return nil
		}
		var readErr error
		current, readErr = choreSeriesRecord(ctx, tx, id.HouseholdID, in.ID, true)
		return readErr
	}
	return s.choreWrite(ctx, id, key, requestID, "chores/workspace/save-series", in.ChoreCommand, in, false, authorize,
		func(ctx context.Context, tx pgx.Tx) (Record, error) {
			if current != nil {
				profile, _ := current["profile"].(map[string]any)
				template, _ := current["template"].(map[string]any)
				if Int(profile["version"]) != in.Version || Int(template["version"]) != in.BaseVersion {
					return nil, ErrConflict
				}
			}
			in, timing, timingErr := choreSeriesTiming(ctx, tx, id.HouseholdID, in, current)
			if timingErr != nil {
				return nil, timingErr
			}
			if timing.needsPastChoice {
				return nil, Invalid{"Termine vor dem Erzeugungsstart werden nicht nachgeholt; diese Auswahl ausdrücklich bestätigen"}
			}
			if err := choreValidateReferences(ctx, tx, id.HouseholdID, in); err != nil {
				return nil, err
			}
			templateID := in.ID
			if templateID == "" {
				var count int
				if err := tx.QueryRow(ctx, "SELECT count(*) FROM chores.task_templates WHERE household_id=$1 AND active", id.HouseholdID).Scan(&count); err != nil {
					return nil, err
				}
				if count >= choreMaximumActiveSeries {
					return nil, Invalid{"Höchstens 500 aktive Aufgabenserien; zuerst nicht mehr benötigte Serien archivieren"}
				}
				templateID = value.UUID()
				if _, err := tx.Exec(ctx, `INSERT INTO chores.task_templates(id,household_id,title,description,assignee_id,repeat_days,timezone,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, templateID, id.HouseholdID, in.Title, in.Description, choreOptionalID(in.AssigneeID),
					choreLegacyInterval(in.Schedule), in.Schedule.Timezone, id.UserID); err != nil {
					return nil, err
				}
			}
			if err := choreGraphCheck(ctx, tx, id.HouseholdID, templateID, in.PrerequisiteIDs); err != nil {
				return nil, err
			}
			versionID, err := insertChoreVersion(ctx, tx, id, templateID, in, timing.effectiveAt)
			if err != nil {
				return nil, err
			}
			keep := ""
			if in.Schedule.Anchor == "completion" {
				retained, retainErr := choreRetainedOccurrence(ctx, tx, id.HouseholdID, templateID, timing.effectiveAt)
				if retainErr != nil {
					return nil, retainErr
				}
				if retained != nil {
					keep = String(retained["id"])
				}
			}
			if err = cancelChoreFuture(ctx, tx, id, templateID, timing.effectiveAt, keep); err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `UPDATE chores.series_profiles SET current_version_id=$3,managed=true,review_required=false,
 review_reason='',materialize_from=$4,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND template_id=$2`,
				id.HouseholdID, templateID, versionID, timing.materializeFrom)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `UPDATE chores.task_templates SET title=$3,description=$4,assignee_id=$5,repeat_days=$6,
 timezone=$7,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2`, id.HouseholdID, templateID,
				in.Title, in.Description, choreOptionalID(in.AssigneeID), choreLegacyInterval(in.Schedule), in.Schedule.Timezone)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO chores.schedule_state(household_id,template_id,template_version_id,module_generation,generated_through,created_by)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(template_id) DO UPDATE SET template_version_id=$3,module_generation=$4,
 generated_through=least(chores.schedule_state.generated_through,$5),next_check_at=clock_timestamp(),last_error='',version=chores.schedule_state.version+1`,
				id.HouseholdID, templateID, versionID, in.Generation, timing.effectiveAt.Add(-time.Microsecond), id.UserID)
			if err != nil {
				return nil, err
			}
			header, headerErr := choreObject(ctx, tx, `SELECT to_jsonb(t) FROM chores.task_templates t WHERE household_id=$1 AND id=$2`, id.HouseholdID, templateID)
			if headerErr != nil {
				return nil, headerErr
			}
			mod, _ := s.Module("chores")
			entity, _ := mod.Entity("task_templates")
			if err = s.afterWrite(ctx, tx, id, mod, entity, header, in.Generation); err != nil {
				return nil, err
			}
			generated, err := s.materializeChoreSeries(ctx, tx, id, templateID, in.Generation, 12, false)
			if err != nil {
				return nil, err
			}
			result, err := choreSeriesRecord(ctx, tx, id.HouseholdID, templateID, false)
			if err == nil {
				result["generated"] = generated
				result["generation"] = in.Generation
			}
			return result, err
		})
}

func choreLegacyInterval(schedule chore.Schedule) any {
	if schedule.Kind == "days" {
		return schedule.Interval
	}
	if schedule.Kind == "once" {
		return 0
	}
	return nil
}

func insertChoreVersion(ctx context.Context, tx pgx.Tx, id auth.Identity, templateID string,
	in ChoreSeriesInput, effective time.Time) (string, error) {
	versionID := value.UUID()
	var revision int64
	if err := tx.QueryRow(ctx, "SELECT coalesce(max(revision),0)+1 FROM chores.template_versions WHERE household_id=$1 AND template_id=$2",
		id.HouseholdID, templateID).Scan(&revision); err != nil {
		return "", err
	}
	var areaName any
	if in.AreaID != "" {
		var name string
		if err := tx.QueryRow(ctx, "SELECT name FROM chores.areas WHERE household_id=$1 AND id=$2 AND archived_at IS NULL",
			id.HouseholdID, in.AreaID).Scan(&name); err != nil {
			return "", choreStoreError(err)
		}
		areaName = name
	}
	var end any
	if in.Schedule.EndsOn != "" {
		end = in.Schedule.EndsOn
	}
	var monthDay any
	if in.Schedule.Kind == "monthly" {
		monthDay = in.Schedule.MonthDay
	}
	_, err := tx.Exec(ctx, `INSERT INTO chores.template_versions(id,household_id,template_id,revision,title,description,materials,
 area_id,area_name,estimated_minutes,difficulty,priority,rule_kind,interval_value,anchor_kind,starts_on,local_time,timezone,
 weekdays,months,month_day,ends_on,effective_at,assignment_kind,assignee_id,legacy_capture,dst_policy,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::date,$17::time,$18,
 $19::integer[],$20::integer[],$21,$22::date,$23,$24,$25,false,$26,$27)`, versionID, id.HouseholdID, templateID, revision,
		in.Title, in.Description, in.Materials, choreOptionalID(in.AreaID), areaName, in.EstimatedMinutes, in.Difficulty,
		in.Priority, in.Schedule.Kind, in.Schedule.Interval, in.Schedule.Anchor, in.Schedule.StartsOn, in.Schedule.LocalTime,
		in.Schedule.Timezone, in.Schedule.Weekdays, in.Schedule.Months, monthDay, end, effective, in.AssignmentKind,
		choreOptionalID(in.AssigneeID), in.Schedule.Policy, id.UserID)
	if err != nil {
		return "", err
	}
	for index, item := range in.Checklist {
		key := item.Key
		if key == "" {
			key = value.UUID()
		}
		_, err = tx.Exec(ctx, `INSERT INTO chores.version_checklist_items(household_id,version_id,item_key,title,position,required,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, versionID, key, item.Title, index, item.Required, id.UserID)
		if err != nil {
			return "", err
		}
	}
	for _, prerequisite := range in.PrerequisiteIDs {
		_, err = tx.Exec(ctx, `INSERT INTO chores.version_dependencies(household_id,version_id,template_id,prerequisite_id,created_by)
 VALUES($1,$2,$3,$4,$5)`, id.HouseholdID, versionID, templateID, prerequisite, id.UserID)
		if err != nil {
			return "", err
		}
	}
	for index, member := range in.Members {
		_, err = tx.Exec(ctx, `INSERT INTO chores.rotation_members(household_id,version_id,membership_id,position,eligible,created_by)
 VALUES($1,$2,$3,$4,$5,$6)`, id.HouseholdID, versionID, member.MembershipID, index, member.Eligible, id.UserID)
		if err != nil {
			return "", err
		}
	}
	return versionID, nil
}

func choreRetainedOccurrence(ctx context.Context, tx pgx.Tx, household, templateID string, effective time.Time) (Record, error) {
	var past int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM chores.task_occurrences WHERE household_id=$1 AND template_id=$2 AND state='open' AND due_at<$3`, household, templateID, effective).Scan(&past); err != nil {
		return nil, err
	}
	if past > 1 {
		return nil, Invalid{"Vor Wechsel zum Erledigungsanker mehrere bereits fällige Termine ausdrücklich erledigen oder auslassen"}
	}
	row, err := choreObject(ctx, tx, `SELECT to_jsonb(o) FROM chores.task_occurrences o WHERE household_id=$1 AND template_id=$2 AND state='open' ORDER BY due_at,id LIMIT 1`, household, templateID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return row, err
}

func cancelChoreFuture(ctx context.Context, tx pgx.Tx, id auth.Identity, templateID string, effective time.Time, keep string) error {
	_, err := tx.Exec(ctx, `UPDATE chores.occurrence_details detail SET skipped_reason='series_changed',skipped_at=clock_timestamp(),skipped_by=$5,claimed_by=NULL,
 claim_started_at=NULL,claim_expires_at=NULL,updated_at=clock_timestamp(),version=detail.version+1
 FROM chores.task_occurrences occurrence WHERE occurrence.household_id=$1 AND occurrence.template_id=$2
 AND occurrence.state='open' AND occurrence.due_at>=$3 AND($4='' OR occurrence.id<>nullif($4,'')::uuid)
 AND detail.occurrence_id=occurrence.id`, id.HouseholdID, templateID, effective, keep, id.UserID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.task_occurrences SET state='skipped',updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND template_id=$2 AND state='open' AND due_at>=$3 AND($4='' OR id<>nullif($4,'')::uuid)`, id.HouseholdID, templateID, effective, keep)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE core.reminders reminder SET state='cancelled',cancelled_at=clock_timestamp()
 FROM chores.task_occurrences occurrence WHERE occurrence.household_id=$1 AND occurrence.template_id=$2
 AND occurrence.state='skipped' AND reminder.household_id=occurrence.household_id AND reminder.module_id='chores'
 AND reminder.resource_type='task_occurrences' AND reminder.resource_id=occurrence.id AND reminder.state='pending'`, id.HouseholdID, templateID)
	return err
}
