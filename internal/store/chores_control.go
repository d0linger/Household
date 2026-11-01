package store

import (
	"context"
	"errors"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ControlChoreSeries(ctx context.Context, id auth.Identity, templateID, key, requestID string,
	in ChoreActionInput) (Record, error) {
	if !value.ValidUUID(templateID) || in.Version < 1 ||
		in.Action != "pause" && in.Action != "resume" && in.Action != "archive" && in.Action != "activate" {
		return nil, Invalid{"Serienaktion und Versionsstand prüfen"}
	}
	if in.DueOn != "" || in.LocalTime != "" || in.Timezone != "" || in.Title != nil || in.Description != nil || in.AssigneeID != nil || in.ActualMinutes != nil || in.ChecklistKey != "" || in.Done {
		return nil, Invalid{"Serienaktion getrennt von Einzelterminfeldern senden"}
	}
	if in.PauseUntil != "" {
		if _, err := chore.ParseDay(in.PauseUntil); err != nil || in.Action != "pause" {
			return nil, Invalid{"Pausenende nur bei einer gültigen Pausenaktion angeben"}
		}
	}
	var current Record
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		var err error
		current, err = choreSeriesRecord(ctx, tx, id.HouseholdID, templateID, true)
		return err
	}
	return s.choreWrite(ctx, id, key, requestID, "chores/workspace/series/"+templateID+"/"+in.Action,
		in.ChoreCommand, in, false, authorize, func(ctx context.Context, tx pgx.Tx) (Record, error) {
			profile, _ := current["profile"].(map[string]any)
			template, _ := current["template"].(map[string]any)
			snapshot, _ := current["snapshot"].(map[string]any)
			if Int(profile["version"]) != in.Version {
				return nil, ErrConflict
			}
			switch in.Action {
			case "pause":
				if profile["paused"] == true || template["active"] != true {
					return nil, ErrConflict
				}
				var resume any
				var endDay any
				if in.PauseUntil != "" {
					instant, err := choreAtDay(in.PauseUntil, String(snapshot["timezone"]))
					if err != nil {
						return nil, Invalid{err.Error()}
					}
					var now time.Time
					if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
						return nil, err
					}
					if !instant.After(now) || instant.Sub(now) > 366*24*time.Hour {
						return nil, Invalid{"Pausenende muss in den nächsten 366 Tagen liegen"}
					}
					resume, endDay = instant, in.PauseUntil
				}
				_, err := tx.Exec(ctx, `UPDATE chores.series_profiles SET paused=true,paused_at=clock_timestamp(),
 paused_until=$3::date,resume_at=$4,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND template_id=$2`,
					id.HouseholdID, templateID, endDay, resume)
				if err != nil {
					return nil, err
				}
			case "resume":
				if profile["paused"] != true || template["active"] != true {
					return nil, ErrConflict
				}
				if err := resumeChoreSeries(ctx, tx, id.HouseholdID, templateID, id.UserID); err != nil {
					return nil, err
				}
			case "archive", "activate":
				active := in.Action == "activate"
				if template["active"] == active {
					return nil, ErrConflict
				}
				if active {
					var count int
					if err := tx.QueryRow(ctx, "SELECT count(*) FROM chores.task_templates WHERE household_id=$1 AND active", id.HouseholdID).Scan(&count); err != nil {
						return nil, err
					}
					if count >= choreMaximumActiveSeries {
						return nil, Invalid{"Höchstens 500 aktive Aufgabenserien"}
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE chores.task_templates SET active=$3,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2`, id.HouseholdID, templateID, active); err != nil {
					return nil, err
				}
				if _, err := tx.Exec(ctx, `UPDATE chores.series_profiles SET version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND template_id=$2`, id.HouseholdID, templateID); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE chores.schedule_state SET next_check_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND template_id=$2`, id.HouseholdID, templateID); err != nil {
				return nil, err
			}
			if in.Action == "resume" || in.Action == "activate" {
				if _, err := s.materializeChoreSeries(ctx, tx, id, templateID, in.Generation, 12, false); err != nil {
					return nil, err
				}
			}
			result, err := choreSeriesRecord(ctx, tx, id.HouseholdID, templateID, false)
			if err == nil {
				result["generation"] = in.Generation
			}
			return result, err
		})
}

func resumeChoreSeries(ctx context.Context, tx pgx.Tx, household, templateID, actor string) error {
	var pausedAt, resumedAt time.Time
	err := tx.QueryRow(ctx, `SELECT paused_at,least(clock_timestamp(),coalesce(resume_at,clock_timestamp())) FROM chores.series_profiles WHERE household_id=$1 AND template_id=$2 AND paused FOR UPDATE`,
		household, templateID).Scan(&pausedAt, &resumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.occurrence_details detail SET skipped_reason='series_paused',skipped_at=clock_timestamp(),skip_anchor_at=$5,
 skipped_by=nullif($4,'')::uuid,claimed_by=NULL,claim_started_at=NULL,claim_expires_at=NULL,version=detail.version+1,updated_at=clock_timestamp()
 FROM chores.task_occurrences occurrence WHERE occurrence.household_id=$1 AND occurrence.template_id=$2
 AND occurrence.state='open' AND occurrence.due_at>$3 AND occurrence.due_at<=$5 AND detail.occurrence_id=occurrence.id`,
		household, templateID, pausedAt, actor, resumedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.task_occurrences SET state='skipped',version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND template_id=$2 AND state='open' AND due_at>$3 AND due_at<=$4`, household, templateID, pausedAt, resumedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.series_profiles profile SET paused=false,paused_at=NULL,paused_until=NULL,resume_at=NULL,
 resume_anchor_at=CASE WHEN EXISTS(SELECT 1 FROM chores.template_versions v WHERE v.id=profile.current_version_id AND v.anchor_kind='completion')
 THEN greatest(profile.resume_anchor_at,$3::timestamptz) ELSE profile.resume_anchor_at END,
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND template_id=$2`, household, templateID, resumedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chores.schedule_state SET generated_through=greatest(generated_through,$3::timestamptz),
 next_check_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND template_id=$2`, household, templateID, resumedAt)
	return err
}
