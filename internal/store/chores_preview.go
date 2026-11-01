package store

import (
	"context"
	"time"

	"household/internal/auth"
	chore "household/internal/chores"
	"household/internal/value"
)

func choreDeadline(detail Record, in ChoreActionInput) (time.Time, string, error) {
	if in.DueOn == "" {
		return time.Time{}, "", Invalid{"Neues lokales Datum zum Verschieben angeben"}
	}
	timezone := in.Timezone
	if timezone == "" {
		timezone = String(detail["timezone"])
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, "", Invalid{"Zeitzone prüfen"}
	}
	clock := in.LocalTime
	if clock == "" {
		clock = String(detail["local_time"])
		if len(clock) > 5 {
			clock = clock[:5]
		}
	}
	due, resolution, err := chore.ResolveLocal(in.DueOn, clock, location)
	if err != nil {
		return time.Time{}, "", Invalid{err.Error()}
	}
	return due, resolution, nil
}

func (s *Store) PreviewChoreOccurrence(ctx context.Context, id auth.Identity, occurrenceID string, in ChoreActionInput) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(occurrenceID) || in.Version < 1 || in.Action != "snooze" && in.Action != "edit_once" && in.Action != "skip" {
		return nil, Invalid{"Einzelterminentscheidung prüfen"}
	}
	if in.ActualMinutes != nil || in.ChecklistKey != "" || in.Done || in.PauseUntil != "" || in.Action == "skip" && (in.Title != nil || in.Description != nil || in.AssigneeID != nil || in.DueOn != "" || in.LocalTime != "" || in.Timezone != "") ||
		in.Action == "snooze" && (in.Title != nil || in.Description != nil || in.AssigneeID != nil) || in.DueOn == "" && (in.LocalTime != "" || in.Timezone != "") {
		return nil, Invalid{"Vorschaufelder passen nicht zu dieser Einzelterminentscheidung"}
	}
	scope, err := s.choreRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	if in.Generation != scope.generation {
		return nil, ErrConflict
	}
	item, err := choreObject(scope.ctx, scope.tx, "SELECT "+choreTaskSummary+choreTaskJoins+" WHERE o.household_id=$1 AND o.id=$2", id.HouseholdID, occurrenceID)
	if err != nil {
		return nil, err
	}
	occurrence, ok := item["occurrence"].(map[string]any)
	if !ok {
		return nil, ErrNotFound
	}
	detail, ok := item["detail"].(map[string]any)
	if !ok {
		return nil, ErrNotFound
	}
	if Int(occurrence["version"]) != in.Version || occurrence["state"] != "open" {
		return nil, ErrConflict
	}
	if item["series_active"] != true {
		return nil, Invalid{"Archivierte Serie zuerst ausdrücklich aktivieren"}
	}
	due, err := choreInstant(occurrence["due_at"])
	if err != nil {
		return nil, err
	}
	paused, err := choreInstant(item["paused_at"])
	if err != nil {
		return nil, err
	}
	if item["series_paused"] == true && due.After(paused) {
		return nil, Invalid{"Dieser Termin ist durch die Serienpause ausgesetzt"}
	}
	var next any
	resolution := "unchanged"
	if in.Action == "snooze" || in.DueOn != "" {
		instant, resolved, resolveErr := choreDeadline(detail, in)
		if resolveErr != nil {
			return nil, resolveErr
		}
		next, resolution = instant, resolved
	}
	if in.Title != nil && !choreTextValid(*in.Title, 200, true) || in.Description != nil && !choreTextValid(*in.Description, 10000, false) ||
		in.AssigneeID != nil && *in.AssigneeID != "" && !value.ValidUUID(*in.AssigneeID) {
		return nil, Invalid{"Einzelnen Titel, Anleitung oder Zuweisung prüfen"}
	}
	if in.AssigneeID != nil && *in.AssigneeID != "" {
		if err = choreValidateReferences(scope.ctx, scope.tx, id.HouseholdID, ChoreSeriesInput{AssigneeID: *in.AssigneeID}); err != nil {
			return nil, err
		}
	}
	return Record{"normalized": in, "old_due_at": occurrence["due_at"], "original_due_at": detail["original_due_at"], "new_due_at": next,
		"resolution": resolution, "generation": scope.generation, "counts_as_completion": false,
		"note": "Genau dieses Vorkommen ändern. Ursprünglicher Termin und andere Vorkommen bleiben erhalten; Auslassen erzeugt keinen Erledigungsfakt."}, nil
}
