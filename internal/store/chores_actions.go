package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) MutateChoreOccurrence(ctx context.Context, id auth.Identity, occurrenceID, key, requestID string,
	in ChoreActionInput) (Record, error) {
	return s.mutateChoreOccurrence(ctx, id, occurrenceID, key, requestID, in,
		"chores/workspace/occurrence/"+occurrenceID+"/"+in.Action, in)
}

func (s *Store) choreLegacyAction(ctx context.Context, id auth.Identity, occurrenceID, action, key, requestID string,
	in Mutation) (Record, error) {
	if action != "complete" {
		return nil, ErrNotFound
	}
	result, err := s.mutateChoreOccurrence(ctx, id, occurrenceID, key, requestID,
		ChoreActionInput{ChoreCommand: ChoreCommand{Generation: in.Generation, Version: in.Version}, Action: "complete"},
		"action/chores/task_occurrences/"+occurrenceID+"/complete", in)
	if err != nil {
		return nil, err
	}
	if occurrence, ok := result["occurrence"].(map[string]any); ok {
		return occurrence, nil
	}
	// Released action receipts contain the base row directly.
	return result, nil
}

func (s *Store) mutateChoreOccurrence(ctx context.Context, id auth.Identity, occurrenceID, key, requestID string,
	in ChoreActionInput, operation string, payload any) (Record, error) {
	if !value.ValidUUID(occurrenceID) || in.Version < 1 || in.ActualMinutes != nil && (*in.ActualMinutes < 0 || *in.ActualMinutes > 1440) {
		return nil, Invalid{"Vorkommen, Versionsstand und tatsächlichen Aufwand prüfen"}
	}
	if in.Action != "complete" && in.ActualMinutes != nil || in.Action != "check" && (in.ChecklistKey != "" || in.Done) || in.PauseUntil != "" {
		return nil, Invalid{"Zusatzfelder passen nicht zur gewählten Aufgabenaktion"}
	}
	if in.Action != "edit_once" && in.Action != "snooze" &&
		(in.Title != nil || in.Description != nil || in.AssigneeID != nil || in.DueOn != "" || in.LocalTime != "" || in.Timezone != "") {
		return nil, Invalid{"Terminänderungen getrennt von der gewählten Aktion prüfen"}
	}
	if in.Action == "snooze" && (in.Title != nil || in.Description != nil || in.AssigneeID != nil) || in.DueOn == "" && (in.LocalTime != "" || in.Timezone != "") {
		return nil, Invalid{"Verschiebung und Einzeltextänderung getrennt mit einem konkreten Datum prüfen"}
	}
	switch in.Action {
	case "complete", "claim", "release", "skip", "snooze", "edit_once":
	case "check":
		if !value.ValidUUID(in.ChecklistKey) {
			return nil, Invalid{"Unteraufgabe auswählen"}
		}
	default:
		return nil, Invalid{"Aufgabenaktion wird nicht unterstützt"}
	}
	var current Record
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		var err error
		current, err = choreObject(ctx, tx, `SELECT to_jsonb(occurrence) FROM chores.task_occurrences occurrence
 WHERE household_id=$1 AND id=$2 FOR UPDATE`, id.HouseholdID, occurrenceID)
		return err
	}
	return s.choreWrite(ctx, id, key, requestID, operation, in.ChoreCommand, payload, false, authorize,
		func(ctx context.Context, tx pgx.Tx) (Record, error) {
			if Int(current["version"]) != in.Version || String(current["state"]) != "open" {
				return nil, ErrConflict
			}
			var active bool
			if err := tx.QueryRow(ctx, "SELECT active FROM chores.task_templates WHERE household_id=$1 AND id=$2",
				id.HouseholdID, current["template_id"]).Scan(&active); err != nil {
				return nil, err
			}
			if !active {
				return nil, Invalid{"Serie ist archiviert; keine offene Aufgabe mehr verändern"}
			}
			var held bool
			if err := tx.QueryRow(ctx, `SELECT p.paused AND o.due_at>p.paused_at FROM chores.task_occurrences o
 JOIN chores.series_profiles p ON p.template_id=o.template_id WHERE o.household_id=$1 AND o.id=$2`, id.HouseholdID, occurrenceID).Scan(&held); err != nil {
				return nil, err
			}
			if held {
				return nil, Invalid{"Dieser Termin ist durch die Serienpause ausgesetzt"}
			}
			detail, err := choreObject(ctx, tx, `SELECT to_jsonb(detail) FROM chores.occurrence_details detail
 WHERE household_id=$1 AND occurrence_id=$2 FOR UPDATE`, id.HouseholdID, occurrenceID)
			if err != nil {
				return nil, err
			}
			expires, err := choreInstant(detail["claim_expires_at"])
			if err != nil {
				return nil, err
			}
			var now time.Time
			if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
				return nil, err
			}
			claimActive := false
			if String(detail["claimed_by"]) != "" && expires.After(now) {
				if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.id=$2 AND m.inactive_at IS NULL AND m.role IN('admin','editor')
 AND u.disabled_at IS NULL AND NOT u.must_change_password)`, id.HouseholdID, detail["claimed_by"]).Scan(&claimActive); err != nil {
					return nil, err
				}
			}
			if claimActive && String(detail["claimed_by"]) != id.MembershipID {
				if in.Action != "release" || id.Role != "admin" {
					return nil, Invalid{"Aufgabe ist gerade von einer anderen Person übernommen"}
				}
			}
			switch in.Action {
			case "complete":
				return s.completeChoreOccurrence(ctx, tx, id, current, in.ActualMinutes, in.Generation)
			case "check":
				tag, updateErr := tx.Exec(ctx, `UPDATE chores.occurrence_checklist_items SET done=$4,
 done_at=CASE WHEN $4 THEN clock_timestamp() END,done_by=CASE WHEN $4 THEN $5::uuid END,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2 AND item_key=$3`,
					id.HouseholdID, occurrenceID, in.ChecklistKey, in.Done, id.UserID)
				if updateErr != nil {
					return nil, updateErr
				}
				if tag.RowsAffected() != 1 {
					return nil, ErrNotFound
				}
			case "claim":
				if err = choreSuitableMember(ctx, tx, id.HouseholdID, String(detail["template_version_id"]), id.MembershipID); err != nil {
					return nil, err
				}
				if assignee := String(current["assignee_id"]); assignee != "" && assignee != id.MembershipID {
					var assignedActive bool
					if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.id=$2 AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password)`, id.HouseholdID, assignee).Scan(&assignedActive); err != nil {
						return nil, err
					}
					if assignedActive {
						return nil, Invalid{"Zuständigkeit zuerst ausdrücklich ändern oder Vertretung prüfen"}
					}
				}
				_, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET claimed_by=$3,claim_started_at=clock_timestamp(),
 claim_expires_at=clock_timestamp()+INTERVAL '4 hours',updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND occurrence_id=$2`, id.HouseholdID, occurrenceID, id.MembershipID)
			case "release":
				if String(detail["claimed_by"]) != "" && String(detail["claimed_by"]) != id.MembershipID && id.Role != "admin" {
					return nil, ErrForbidden
				}
				_, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET claimed_by=NULL,claim_started_at=NULL,
 claim_expires_at=NULL,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2`, id.HouseholdID, occurrenceID)
			case "skip":
				_, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET skipped_reason='single_skip',skipped_at=clock_timestamp(),skipped_by=$3,claimed_by=NULL,
 claim_started_at=NULL,claim_expires_at=NULL,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2`, id.HouseholdID, occurrenceID, id.UserID)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE chores.task_occurrences SET state='skipped',updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2`, id.HouseholdID, occurrenceID)
				}
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp()
 WHERE household_id=$1 AND module_id='chores' AND resource_type='task_occurrences' AND resource_id=$2 AND state='pending'`, id.HouseholdID, occurrenceID)
				}
				if err == nil {
					_, err = s.materializeChoreSeries(ctx, tx, id, String(current["template_id"]), in.Generation, 12, false)
				}
				if err != nil {
					return nil, err
				}
				return s.finishChoreAction(ctx, tx, id, occurrenceID, in.Generation, false)
			case "snooze", "edit_once":
				if err = choreSingleEdit(ctx, tx, id, current, detail, in); err != nil {
					return nil, err
				}
				return s.finishChoreAction(ctx, tx, id, occurrenceID, in.Generation, false)
			}
			if err != nil {
				return nil, err
			}
			return s.finishChoreAction(ctx, tx, id, occurrenceID, in.Generation, true)
		})
}

func choreActionResult(ctx context.Context, tx pgx.Tx, household, occurrenceID string, generation int64) (Record, error) {
	occurrence, err := choreObject(ctx, tx, "SELECT to_jsonb(occurrence) FROM chores.task_occurrences occurrence WHERE household_id=$1 AND id=$2",
		household, occurrenceID)
	return Record{"occurrence": occurrence, "generation": generation}, err
}

func (s *Store) finishChoreAction(ctx context.Context, tx pgx.Tx, id auth.Identity, occurrenceID string,
	generation int64, touch bool) (Record, error) {
	if touch {
		if _, err := tx.Exec(ctx, `UPDATE chores.task_occurrences SET updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2`, id.HouseholdID, occurrenceID); err != nil {
			return nil, err
		}
	}
	result, err := choreActionResult(ctx, tx, id.HouseholdID, occurrenceID, generation)
	if err != nil {
		return nil, err
	}
	occurrence, ok := result["occurrence"].(map[string]any)
	if !ok {
		return nil, errors.New("chore action returned no occurrence")
	}
	if err = s.publishChoreOccurrence(ctx, tx, id, occurrence, generation, false); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) completeChoreOccurrence(ctx context.Context, tx pgx.Tx, id auth.Identity, current Record,
	minutes *int, generation int64) (Record, error) {
	occurrenceID := String(current["id"])
	var ready bool
	err := tx.QueryRow(ctx, `SELECT chores.dependencies_met($1,$2) AND NOT EXISTS(
 SELECT 1 FROM chores.occurrence_checklist_items WHERE household_id=$1 AND occurrence_id=$2 AND required AND NOT done)`,
		id.HouseholdID, occurrenceID).Scan(&ready)
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, Invalid{"Pflichtschritte und Voraussetzungen zuerst erledigen"}
	}
	if _, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET claimed_by=NULL,claim_started_at=NULL,claim_expires_at=NULL,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2`, id.HouseholdID, occurrenceID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.task_occurrences SET state='completed',completed_at=clock_timestamp(),completed_by=$3,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2 AND state='open'`, id.HouseholdID, occurrenceID, id.UserID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO chores.completions(household_id,occurrence_id,template_id,template_version_id,
 completed_at,actor_user_id,actor_membership_id,actual_minutes,snapshot,created_by)
 SELECT occurrence.household_id,occurrence.id,occurrence.template_id,detail.template_version_id,occurrence.completed_at,$3,$4,$5,
 jsonb_build_object('actor_display_name',actor.display_name,'title',coalesce(detail.overrides->>'title',version.title),
 'area_id',version.area_id,'area_name',version.area_name,'due_at',occurrence.due_at,'assignee_id',occurrence.assignee_id,
 'estimated_minutes',version.estimated_minutes,'source_revision',version.revision,
 'prerequisites',coalesce((SELECT jsonb_agg(jsonb_build_object('template_id',dependency.prerequisite_id,
 'occurrence_id',previous.id,'completion_id',completed.id,'completed_at',completed.completed_at) ORDER BY dependency.prerequisite_id)
 FROM chores.version_dependencies dependency JOIN LATERAL(SELECT source.id FROM chores.task_occurrences source
 JOIN chores.occurrence_details source_detail ON source_detail.occurrence_id=source.id
 WHERE source.household_id=occurrence.household_id AND source.template_id=dependency.prerequisite_id AND source.due_at<=occurrence.due_at
 AND source_detail.skipped_reason<>'series_changed' ORDER BY source.due_at DESC,source.id DESC LIMIT 1) previous ON true
 JOIN chores.completions completed ON completed.household_id=occurrence.household_id AND completed.occurrence_id=previous.id
 WHERE dependency.version_id=detail.template_version_id),'[]'::jsonb),
 'checklist',coalesce((SELECT jsonb_agg(jsonb_build_object('key',item_key,'done',done,'required',required,'done_by',done_by) ORDER BY position)
 FROM chores.occurrence_checklist_items WHERE occurrence_id=occurrence.id),'[]'::jsonb)), $3
 FROM chores.task_occurrences occurrence JOIN chores.occurrence_details detail ON detail.occurrence_id=occurrence.id
 JOIN chores.template_versions version ON version.id=detail.template_version_id JOIN core.users actor ON actor.id=$3
 WHERE occurrence.household_id=$1 AND occurrence.id=$2`, id.HouseholdID, occurrenceID, id.UserID, id.MembershipID, minutes)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp()
 WHERE household_id=$1 AND module_id='chores' AND resource_type='task_occurrences' AND resource_id=$2 AND state='pending'`, id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	var managed bool
	if err = tx.QueryRow(ctx, "SELECT managed FROM chores.series_profiles WHERE household_id=$1 AND template_id=$2", id.HouseholdID, current["template_id"]).Scan(&managed); err != nil {
		return nil, err
	}
	if managed {
		if _, err = s.materializeChoreSeries(ctx, tx, id, String(current["template_id"]), generation, 12, false); err != nil {
			return nil, err
		}
	} else if err = s.choreLegacySuccessor(ctx, tx, id, current, generation); err != nil {
		return nil, err
	}
	return s.finishChoreAction(ctx, tx, id, occurrenceID, generation, false)
}

func choreSingleEdit(ctx context.Context, tx pgx.Tx, id auth.Identity, current, detail Record, in ChoreActionInput) error {
	occurrenceID := String(current["id"])
	if in.Title != nil && !choreTextValid(*in.Title, 200, true) || in.Description != nil && !choreTextValid(*in.Description, 10000, false) ||
		in.AssigneeID != nil && *in.AssigneeID != "" && !value.ValidUUID(*in.AssigneeID) {
		return Invalid{"Titel, Anleitung oder Person prüfen"}
	}
	if in.Action == "edit_once" && in.Title == nil && in.Description == nil && in.AssigneeID == nil && in.DueOn == "" {
		return Invalid{"Mindestens eine konkrete Terminänderung angeben"}
	}
	var due any
	if in.DueOn != "" {
		if _, err := chore.ParseDay(in.DueOn); err != nil {
			return Invalid{err.Error()}
		}
		timezone := in.Timezone
		if timezone == "" {
			timezone = String(detail["timezone"])
		}
		location, err := time.LoadLocation(timezone)
		if err != nil {
			return Invalid{"Zeitzone prüfen"}
		}
		clock := in.LocalTime
		if clock == "" {
			clock = String(detail["local_time"])
			if len(clock) > 5 {
				clock = clock[:5]
			}
		}
		instant, _, err := chore.ResolveLocal(in.DueOn, clock, location)
		if err != nil {
			return Invalid{err.Error()}
		}
		due = instant
	} else if in.Action == "snooze" {
		return Invalid{"Neuen Termin zum Verschieben angeben"}
	}
	var assignee any
	changeAssignee := false
	if in.Action == "edit_once" {
		if in.AssigneeID != nil {
			changeAssignee = true
			assignee = choreOptionalID(*in.AssigneeID)
		}
		if in.AssigneeID != nil && *in.AssigneeID != "" {
			if err := choreSuitableMember(ctx, tx, id.HouseholdID, String(detail["template_version_id"]), *in.AssigneeID); err != nil {
				return err
			}
		}
		overrides := Record{}
		if in.Description != nil {
			overrides["description"] = *in.Description
		}
		if in.Title != nil {
			overrides["title"] = strings.TrimSpace(*in.Title)
		}
		raw, err := json.Marshal(overrides)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE chores.occurrence_details SET overrides=overrides||$3::jsonb,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND occurrence_id=$2`, id.HouseholdID, occurrenceID, raw); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE chores.task_occurrences SET due_at=coalesce($3::timestamptz,due_at),
 assignee_id=CASE WHEN $5::boolean THEN $4::uuid ELSE assignee_id END,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2`, id.HouseholdID, occurrenceID, due, assignee, changeAssignee)
	return err
}

func (s *Store) choreLegacySuccessor(ctx context.Context, tx pgx.Tx, id auth.Identity, current Record, generation int64) error {
	var raw []byte
	err := tx.QueryRow(ctx, `INSERT INTO chores.task_occurrences AS next(household_id,template_id,occurrence_key,due_at,assignee_id,created_by)
 SELECT occurrence.household_id,occurrence.template_id,
 ((occurrence.due_at AT TIME ZONE template.timezone)+make_interval(days=>template.repeat_days))::text,
 ((occurrence.due_at AT TIME ZONE template.timezone)+make_interval(days=>template.repeat_days)) AT TIME ZONE template.timezone,
 occurrence.assignee_id,$3 FROM chores.task_occurrences occurrence JOIN chores.task_templates template
 ON template.household_id=occurrence.household_id AND template.id=occurrence.template_id
 WHERE occurrence.household_id=$1 AND occurrence.id=$2 AND template.active AND template.repeat_days>0
 ON CONFLICT(template_id,occurrence_key) DO NOTHING RETURNING to_jsonb(next)`, id.HouseholdID, current["id"], id.UserID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	record, err := decode(raw)
	if err != nil {
		return err
	}
	return s.publishChoreOccurrence(ctx, tx, id, record, generation, false)
}
