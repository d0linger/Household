package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const choreGenerationCells = 512

type choreVersionSlot struct {
	versionID string
	slot      chore.Slot
	key       string
	checks    int
}

func choreInstant(value any) (time.Time, error) {
	if value == nil || String(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, String(value))
}

func choreVersionWindow(ctx context.Context, tx pgx.Tx, household, templateID string, from, through time.Time) ([]Record, error) {
	return choreRows(ctx, tx, 366, `WITH spans AS(
 SELECT id,effective_at,min(effective_at) OVER(ORDER BY revision DESC ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) until
 FROM chores.template_versions WHERE household_id=$1 AND template_id=$2 AND NOT legacy_capture)
 SELECT to_jsonb(version)||jsonb_build_object('until',span.until,'checklist_count',
 (SELECT count(*) FROM chores.version_checklist_items WHERE version_id=version.id))
 FROM spans span JOIN chores.template_versions version ON version.id=span.id
 WHERE span.effective_at<$4 AND(span.until IS NULL OR span.until>$3)
 AND(span.until IS NULL OR span.effective_at<span.until) ORDER BY version.revision DESC LIMIT 367`,
		household, templateID, from, through)
}

func choreCandidateSlots(versions []Record, from, through time.Time, limit int) ([]choreVersionSlot, bool, error) {
	result := []choreVersionSlot{}
	more := false
	for _, version := range versions {
		// Completion-anchored history never supplies calendar occurrences.
		if String(version["anchor_kind"]) == "completion" {
			continue
		}
		schedule, err := choreSchedule(version)
		if err != nil {
			return nil, false, Invalid{"Gespeicherte Kalenderregel muss geprüft werden"}
		}
		location, _ := time.LoadLocation(schedule.Timezone)
		effective, err := choreInstant(version["effective_at"])
		if err != nil {
			return nil, false, err
		}
		until, err := choreInstant(version["until"])
		if err != nil {
			return nil, false, err
		}
		first, last := from, through
		if effective.After(first) {
			first = effective
		}
		if !until.IsZero() && until.Before(last) {
			last = until
		}
		if !first.Before(last) {
			continue
		}
		// Gap forwarding can move an entire civil day. Include the original
		// dates just before the UTC window, then filter by the actual instant.
		start := first.In(location).AddDate(0, 0, -2).Format(time.DateOnly)
		if start < "1900-01-01" {
			start = "1900-01-01"
		}
		end := last.Add(-time.Microsecond).In(location).Format(time.DateOnly)
		if end > "2200-12-31" {
			end = "2200-12-31"
		}
		if schedule.Kind == "once" {
			// An explicitly captured old one-time task needs one civil date,
			// even when the effective window spans more than 366 days.
			start, end = schedule.StartsOn, schedule.StartsOn
		}
		projection, err := chore.Project(schedule, start, end, limit+5)
		if err != nil {
			return nil, false, Invalid{err.Error()}
		}
		for _, slot := range projection.Slots {
			if slot.DueAt.Before(from) || !slot.DueAt.Before(through) || slot.DueAt.Before(effective) ||
				!until.IsZero() && !slot.DueAt.Before(until) {
				continue
			}
			versionID := String(version["id"])
			result = append(result, choreVersionSlot{versionID: versionID, slot: slot,
				key: "v/" + versionID + "/" + slot.Day + "/" + slot.LocalTime, checks: int(Int(version["checklist_count"]))})
		}
		more = more || projection.More
	}
	slices.SortFunc(result, func(left, right choreVersionSlot) int {
		if order := left.slot.DueAt.Compare(right.slot.DueAt); order != 0 {
			return order
		}
		if left.key < right.key {
			return -1
		}
		if left.key > right.key {
			return 1
		}
		return 0
	})
	if len(result) > limit {
		cut := limit
		for cut > 0 && result[cut-1].slot.DueAt.Equal(result[cut].slot.DueAt) {
			cut--
		}
		if cut == 0 {
			return nil, false, Invalid{"Zu viele gleichzeitige Kalendertermine für einen sicheren Batch"}
		}
		result, more = result[:cut], true
	}
	return result, more, nil
}

func choreCompletionAnchor(ctx context.Context, tx pgx.Tx, household, templateID string) (time.Time, string, error) {
	var last time.Time
	var anchorID string
	err := tx.QueryRow(ctx, `SELECT completed_at,id FROM chores.completions
 WHERE household_id=$1 AND template_id=$2 ORDER BY completed_at DESC,id DESC LIMIT 1`, household, templateID).Scan(&last, &anchorID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, "", err
	}
	var skipped time.Time
	var skippedID string
	skipErr := tx.QueryRow(ctx, `SELECT coalesce(detail.skip_anchor_at,detail.original_due_at),occurrence.id FROM chores.task_occurrences occurrence
 JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 WHERE occurrence.household_id=$1 AND occurrence.template_id=$2 AND occurrence.state='skipped'
 AND detail.skipped_reason NOT IN('series_changed','series_archived')
 ORDER BY coalesce(detail.skip_anchor_at,detail.original_due_at) DESC,occurrence.id DESC LIMIT 1`, household, templateID).Scan(&skipped, &skippedID)
	if skipErr != nil && !errors.Is(skipErr, pgx.ErrNoRows) {
		return time.Time{}, "", skipErr
	}
	if skipped.After(last) {
		last, anchorID = skipped, "skip/"+skippedID
	}
	var resumeAnchor *time.Time
	if err = tx.QueryRow(ctx, `SELECT resume_anchor_at FROM chores.series_profiles WHERE household_id=$1 AND template_id=$2`, household, templateID).Scan(&resumeAnchor); err != nil {
		return time.Time{}, "", err
	}
	if resumeAnchor != nil && resumeAnchor.After(last) {
		last, anchorID = *resumeAnchor, "resume/"+resumeAnchor.UTC().Format(time.RFC3339Nano)
	}
	return last, anchorID, nil
}

func choreCompletionSlot(ctx context.Context, tx pgx.Tx, household, templateID string, snapshot Record,
	through time.Time) ([]choreVersionSlot, error) {
	var open bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chores.task_occurrences
 WHERE household_id=$1 AND template_id=$2 AND state='open')`, household, templateID).Scan(&open); err != nil || open {
		return nil, err
	}
	schedule, err := choreSchedule(snapshot)
	if err != nil {
		return nil, Invalid{"Erledigungsregel muss geprüft werden"}
	}
	last, anchorID, err := choreCompletionAnchor(ctx, tx, household, templateID)
	if err != nil {
		return nil, err
	}
	var slot chore.Slot
	if !last.IsZero() {
		slot, err = chore.AfterCompletion(schedule, last)
	} else {
		projection, projectErr := chore.Project(schedule, schedule.StartsOn, schedule.StartsOn, 1)
		err = projectErr
		if err == nil && len(projection.Slots) > 0 {
			slot = projection.Slots[0]
		}
		anchorID = "initial"
	}
	if errors.Is(err, chore.ErrNoOccurrence) || slot.DueAt.IsZero() && err == nil {
		return nil, nil
	}
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	effective, err := choreInstant(snapshot["effective_at"])
	if err != nil {
		return nil, err
	}
	if slot.DueAt.Before(effective) {
		location, _ := time.LoadLocation(schedule.Timezone)
		initial, resolution, initialErr := chore.ResolveLocal(schedule.StartsOn, schedule.LocalTime, location)
		if initialErr != nil || initial.Before(effective) {
			return nil, Invalid{"Erster Termin der geänderten Erledigungsregel liegt vor ihrem Gültigkeitsbeginn; Startdatum prüfen"}
		}
		slot = chore.Slot{Day: schedule.StartsOn, LocalTime: schedule.LocalTime, Timezone: schedule.Timezone,
			DueAt: initial, Resolution: resolution}
		anchorID = "rule_start"
	}
	if !slot.DueAt.Before(through) {
		return nil, nil
	}
	var checks int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM chores.version_checklist_items WHERE version_id=$1", snapshot["id"]).Scan(&checks); err != nil {
		return nil, err
	}
	versionID := String(snapshot["id"])
	return []choreVersionSlot{{versionID: versionID, slot: slot, checks: checks,
		key: "v/" + versionID + "/c/" + anchorID + "/" + slot.Day + "/" + slot.LocalTime}}, nil
}

func (s *Store) materializeChoreSeries(ctx context.Context, tx pgx.Tx, id auth.Identity, templateID string,
	generation int64, limit int, worker bool) (Record, error) {
	if limit < 1 || limit > choreGenerationBatch {
		return nil, Invalid{"Terminbatch ist zu groß"}
	}
	series, err := choreSeriesRecord(ctx, tx, id.HouseholdID, templateID, true)
	if err != nil {
		return nil, err
	}
	profile, _ := series["profile"].(map[string]any)
	template, _ := series["template"].(map[string]any)
	snapshot, _ := series["snapshot"].(map[string]any)
	if profile["managed"] != true || profile["review_required"] == true || template["active"] != true || profile["paused"] == true {
		return Record{"created": 0, "paused": profile["paused"] == true}, nil
	}
	var now time.Time
	var timezone string
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp(),timezone FROM core.households WHERE id=$1", id.HouseholdID).
		Scan(&now, &timezone); err != nil {
		return nil, err
	}
	_, location, err := choreLocalDay(now, timezone)
	if err != nil {
		return nil, err
	}
	local := now.In(location)
	throughDay := local.AddDate(0, 0, 91).Format(time.DateOnly)
	if throughDay > "2201-01-01" {
		throughDay = "2201-01-01"
	}
	through, err := choreAtDay(throughDay, timezone)
	if err != nil {
		return nil, err
	}
	materializeFrom, err := choreInstant(profile["materialize_from"])
	if err != nil || materializeFrom.IsZero() {
		return nil, Invalid{"Erzeugungsbeginn der Serie muss geprüft werden"}
	}
	state, err := choreObject(ctx, tx, "SELECT to_jsonb(state) FROM chores.schedule_state state WHERE household_id=$1 AND template_id=$2 FOR UPDATE",
		id.HouseholdID, templateID)
	if err != nil {
		return nil, err
	}
	previous, err := choreInstant(state["generated_through"])
	if err != nil {
		return nil, err
	}
	from := materializeFrom
	if !previous.IsZero() && previous.Add(time.Microsecond).After(from) {
		from = previous.Add(time.Microsecond)
	}
	if !from.Before(through) && String(snapshot["anchor_kind"]) != "completion" {
		_, err = tx.Exec(ctx, `UPDATE chores.schedule_state SET next_check_at=clock_timestamp()+interval '1 hour',last_run_at=clock_timestamp(),
 backlog=false,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND template_id=$2`, id.HouseholdID, templateID)
		return Record{"created": 0, "through": previous, "backlog": false}, err
	}
	var candidates []choreVersionSlot
	windowBacklog, batchLimited := false, false
	if String(snapshot["anchor_kind"]) == "completion" {
		candidates, err = choreCompletionSlot(ctx, tx, id.HouseholdID, templateID, snapshot, through)
	} else {
		// Bound the version query as well as calendar projection so an old
		// backlog with many revisions can advance in finite windows.
		if through.Sub(from) > 90*24*time.Hour {
			through = from.AddDate(0, 0, 90)
			windowBacklog = true
		}
		versions, readErr := choreVersionWindow(ctx, tx, id.HouseholdID, templateID, from, through)
		if readErr != nil {
			return nil, readErr
		}
		var limited bool
		candidates, limited, err = choreCandidateSlots(versions, from, through, limit)
		batchLimited = limited
	}
	if err != nil {
		return nil, err
	}
	created, visited, cells := 0, 0, 0
	progress := through.Add(-time.Microsecond)
	for index := 0; index < len(candidates); {
		end, groupCells := index, 0
		for end < len(candidates) && candidates[end].slot.DueAt.Equal(candidates[index].slot.DueAt) {
			groupCells += candidates[end].checks + 1
			end++
		}
		if groupCells > choreGenerationCells {
			return nil, Invalid{"Gleichzeitige Aufgaben überschreiten das sichere Erzeugungsbudget"}
		}
		if cells+groupCells > choreGenerationCells || visited+end-index > limit {
			batchLimited = true
			break
		}
		for _, candidate := range candidates[index:end] {
			inserted, createErr := s.createChoreOccurrence(ctx, tx, id, templateID, candidate, generation, worker)
			if createErr != nil {
				return nil, createErr
			}
			if inserted {
				created++
			}
			visited++
		}
		cells += groupCells
		index = end
	}
	if batchLimited {
		if visited > 0 {
			progress = candidates[visited-1].slot.DueAt
		} else {
			progress = previous
		}
	}
	more := windowBacklog || batchLimited
	_, err = tx.Exec(ctx, `UPDATE chores.schedule_state SET generated_through=$3,last_run_at=clock_timestamp(),
 next_check_at=clock_timestamp()+CASE WHEN $4 THEN INTERVAL '30 seconds' ELSE INTERVAL '1 hour' END,
 backlog=$4,last_error='',updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND template_id=$2`,
		id.HouseholdID, templateID, progress, more)
	return Record{"created": created, "through": progress, "backlog": more, "cells": cells}, err
}

func (s *Store) createChoreOccurrence(ctx context.Context, tx pgx.Tx, id auth.Identity, templateID string,
	candidate choreVersionSlot, generation int64, worker bool) (bool, error) {
	var existing time.Time
	err := tx.QueryRow(ctx, `SELECT detail.original_due_at FROM chores.task_occurrences occurrence
 JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 WHERE occurrence.household_id=$1 AND occurrence.template_id=$2 AND occurrence.occurrence_key=$3`,
		id.HouseholdID, templateID, candidate.key).Scan(&existing)
	if err == nil {
		if !existing.Equal(candidate.slot.DueAt) {
			return false, Invalid{"Zeitregel eines vorhandenen Termins hat sich geändert; zukünftige Serie ausdrücklich prüfen"}
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	assignment, err := choreAssign(ctx, tx, id.HouseholdID, templateID, candidate.versionID, candidate.slot)
	if err != nil {
		return false, err
	}
	if assignment.MembershipID != "" {
		if err = choreActiveMember(ctx, tx, id.HouseholdID, assignment.MembershipID, true); err != nil {
			return false, err
		}
	}
	occurrenceID := value.UUID()
	var raw []byte
	err = tx.QueryRow(ctx, `INSERT INTO chores.task_occurrences AS occurrence(id,household_id,template_id,occurrence_key,due_at,assignee_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(occurrence)`, occurrenceID, id.HouseholdID, templateID,
		candidate.key, candidate.slot.DueAt, choreOptionalID(assignment.MembershipID), id.UserID).Scan(&raw)
	if err != nil {
		return false, err
	}
	assignmentRaw, err := json.Marshal(assignment)
	if err != nil {
		return false, err
	}
	producer := "user"
	if worker {
		producer = "worker"
	}
	_, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET assignment_reason=$3,assignment_snapshot=$4,
 generated_by=$5,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2`,
		id.HouseholdID, occurrenceID, assignment.Reason, assignmentRaw, producer)
	if err != nil {
		return false, err
	}
	record, err := decode(raw)
	if err != nil {
		return false, err
	}
	if err = s.publishChoreOccurrence(ctx, tx, id, record, generation, worker); err != nil {
		return false, err
	}
	return true, nil
}

func choreAssign(ctx context.Context, tx pgx.Tx, household, templateID, versionID string, slot chore.Slot) (chore.Assignment, error) {
	var kind string
	var fixed *string
	if err := tx.QueryRow(ctx, "SELECT assignment_kind,assignee_id FROM chores.template_versions WHERE household_id=$1 AND id=$2",
		household, versionID).Scan(&kind, &fixed); err != nil {
		return chore.Assignment{}, err
	}
	if kind == "unassigned" {
		return chore.Assignment{ReasonCode: "unassigned", Reason: "Ausdrücklich ohne feste Zuständigkeit"}, nil
	}
	rows, err := tx.Query(ctx, `SELECT member.id,u.disabled_at IS NULL AND NOT u.must_change_password AND member.inactive_at IS NULL
 AND member.role IN('admin','editor'),coalesce(rotation.eligible,true),absence.id IS NOT NULL,coalesce(absence.substitute_id::text,'')
 FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 LEFT JOIN chores.rotation_members rotation ON rotation.household_id=member.household_id AND rotation.membership_id=member.id AND rotation.version_id=$2
 LEFT JOIN LATERAL(SELECT id,substitute_id FROM chores.absences WHERE household_id=member.household_id AND membership_id=member.id
 AND starts_on<=$3::date AND ends_on>=$3::date ORDER BY starts_on DESC,id LIMIT 1) absence ON true
 WHERE member.household_id=$1 AND(rotation.id IS NOT NULL OR member.id=$4::uuid)
 ORDER BY CASE WHEN member.id=$4::uuid AND $5='fixed' THEN -1 ELSE rotation.position END,member.id LIMIT 34`,
		household, versionID, slot.Day, fixed, kind)
	if err != nil {
		return chore.Assignment{}, err
	}
	candidates := []chore.Candidate{}
	for rows.Next() {
		var candidate chore.Candidate
		if err = rows.Scan(&candidate.MembershipID, &candidate.Active, &candidate.Eligible, &candidate.Absent, &candidate.SubstituteID); err != nil {
			break
		}
		candidates = append(candidates, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return chore.Assignment{}, err
	}
	if len(candidates) > 32 {
		return chore.Assignment{}, Invalid{"Zuständigkeitsauswahl ist zu groß"}
	}
	if len(candidates) == 0 {
		return chore.Assignment{ReasonCode: "inactive", Reason: "Verantwortliche Person ist nicht mehr verfügbar"}, nil
	}
	if kind == "fixed" {
		fixedCandidate := candidates[0]
		if fixedCandidate.Active && fixedCandidate.Eligible && !fixedCandidate.Absent {
			return chore.Assignment{MembershipID: fixedCandidate.MembershipID, ReasonCode: "fixed", Reason: "Ausdrücklich fest zugewiesene Person"}, nil
		}
		if fixedCandidate.Absent && fixedCandidate.SubstituteID != "" {
			var suitable bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 WHERE member.household_id=$1 AND member.id=$2 AND member.inactive_at IS NULL AND member.role IN('admin','editor')
 AND u.disabled_at IS NULL AND NOT u.must_change_password
 AND NOT EXISTS(SELECT 1 FROM chores.absences a WHERE a.household_id=$1 AND a.membership_id=member.id AND a.starts_on<=$3::date AND a.ends_on>=$3::date)
 AND (NOT EXISTS(SELECT 1 FROM chores.rotation_members WHERE version_id=$4)
  OR EXISTS(SELECT 1 FROM chores.rotation_members WHERE version_id=$4 AND membership_id=member.id AND eligible)))`,
				household, fixedCandidate.SubstituteID, slot.Day, versionID).Scan(&suitable)
			if err != nil {
				return chore.Assignment{}, err
			}
			if suitable {
				return chore.Assignment{MembershipID: fixedCandidate.SubstituteID, ReasonCode: "substitute", Reason: "Ausdrücklich gewählte, geeignete Vertretung"}, nil
			}
		}
		return chore.Assignment{ReasonCode: "fixed_unavailable", Reason: "Verantwortliche Person oder geeignete Vertretung ist nicht verfügbar"}, nil
	}
	var position uint64
	if kind == "rotation" {
		var ordinal int64
		err = tx.QueryRow(ctx, `SELECT count(*) FROM chores.task_occurrences occurrence JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 WHERE occurrence.household_id=$1 AND occurrence.template_id=$2 AND occurrence.due_at<$3 AND detail.skipped_reason<>'series_changed'`,
			household, templateID, slot.DueAt).Scan(&ordinal)
		if err != nil {
			return chore.Assignment{}, fmt.Errorf("inspect chore rotation ordinal: %w", err)
		}
		if ordinal < 0 {
			return chore.Assignment{}, errors.New("invalid negative chore rotation ordinal")
		}
		position = uint64(ordinal)
	}
	assignment, err := chore.Rotate(candidates, position)
	if err == nil && kind == "fixed" && fixed != nil && assignment.MembershipID == *fixed {
		assignment.ReasonCode, assignment.Reason = "fixed", "Ausdrücklich fest zugewiesene, geeignete Person"
	}
	return assignment, err
}

func (s *Store) publishChoreOccurrence(ctx context.Context, tx pgx.Tx, id auth.Identity,
	record Record, generation int64, worker bool) error {
	if !worker {
		module, _ := s.Module("chores")
		entity, _ := module.Entity("task_occurrences")
		return s.afterWrite(ctx, tx, id, module, entity, record, generation)
	}
	var title, body string
	err := tx.QueryRow(ctx, `SELECT coalesce(detail.overrides->>'title',version.title),
 left(coalesce(detail.overrides->>'description',version.description),10000)
 FROM chores.occurrence_details detail JOIN chores.template_versions version ON version.id=detail.template_version_id
 WHERE detail.household_id=$1 AND detail.occurrence_id=$2`, id.HouseholdID, record["id"]).Scan(&title, &body)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 VALUES($1,'chores',$2,'task_occurrences',$3,$4,$5) ON CONFLICT(household_id,module_id,resource_type,resource_id)
 DO UPDATE SET title=excluded.title,body=excluded.body,module_generation=excluded.module_generation,updated_at=clock_timestamp()`,
		id.HouseholdID, generation, record["id"], title, body)
	if err != nil {
		return err
	}
	payload, eventVersion, err := shoppingEventPayload(ctx, "chores", "task_occurrences", record)
	if err != nil {
		return err
	}
	event := value.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,event_version,
 actor_user_id,payload,dedupe_key) VALUES($1::uuid,$2,'chores',$3,'record_changed',$4,NULL,$5,($1::uuid)::text)`,
		event, id.HouseholdID, generation, eventVersion, payload)
	return err
}
