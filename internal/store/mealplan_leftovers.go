package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PlanningLeftoverInput reviews a forecast. It never authorizes stock posting.
type PlanningLeftoverInput struct {
	PlanningExtensionCommand
	Action            string `json:"action"`
	SourceID          string `json:"source_id"`
	SourceVersion     int64  `json:"source_version"`
	MealID            string `json:"meal_id"`
	MealVersion       int64  `json:"meal_version"`
	ComponentID       string `json:"component_id"`
	ComponentVersion  int64  `json:"component_version"`
	ExpectedServings  string `json:"expected_servings"`
	AllocationID      string `json:"allocation_id"`
	AllocationVersion int64  `json:"allocation_version"`
	TargetMealID      string `json:"target_meal_id"`
	TargetVersion     int64  `json:"target_version"`
	Servings          string `json:"servings"`
}

func normalizePlanningLeftover(in *PlanningLeftoverInput) error {
	for _, pair := range []struct {
		id      string
		version int64
	}{{in.SourceID, in.SourceVersion}, {in.AllocationID, in.AllocationVersion}} {
		if pair.id == "" && pair.version != 0 || pair.id != "" && (!value.ValidUUID(pair.id) || pair.version <= 0) {
			return Invalid{"Restequelle und Zuordnung mit ihren aktuellen Versionen prüfen"}
		}
	}
	switch in.Action {
	case "source_save":
		if !value.ValidUUID(in.MealID) || !value.ValidUUID(in.ComponentID) || in.MealVersion <= 0 || in.ComponentVersion <= 0 {
			return Invalid{"Aktuelle Quellmahlzeit und Rezeptkomponente auswählen"}
		}
		amount, err := planningDecimal(in.ExpectedServings)
		if err != nil {
			return err
		}
		in.ExpectedServings = amount
	case "source_delete":
		if in.SourceID == "" {
			return Invalid{"Gespeicherte Restequelle auswählen"}
		}
	case "allocation_save", "allocation_review":
		if in.SourceID == "" || !value.ValidUUID(in.TargetMealID) || in.TargetVersion <= 0 || in.Action == "allocation_review" && in.AllocationID == "" {
			return Invalid{"Aktuelle Quelle und spätere Restemahlzeit ausdrücklich prüfen"}
		}
		amount, err := planningDecimal(in.Servings)
		if err != nil {
			return err
		}
		in.Servings = amount
	case "allocation_delete":
		if in.AllocationID == "" {
			return Invalid{"Gespeicherte Restezuordnung auswählen"}
		}
	default:
		return Invalid{"Unbekannte Aktion für geplante Reste"}
	}
	return nil
}

func planningLeftoverError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23514", "23503", "23505", "55P03", "40P01":
			return PlanningConflict{"Restequelle, Mengen oder zeitliche Reihenfolge wurden geändert. Gespeicherten Stand laden und ausdrücklich prüfen."}
		}
	}
	return err
}

func planningLeftoverMutationError(module string, err error) error {
	var pg *pgconn.PgError
	if module == "mealplan" && errors.As(err, &pg) && pg.ConstraintName == "leftover_meal_chronology" {
		return PlanningConflict{"Geplante Reste zuerst ausdrücklich lösen oder Quelle und Ziel in gültiger Reihenfolge prüfen."}
	}
	return err
}

func (s *Store) MutatePlanningLeftovers(ctx context.Context, id auth.Identity, pid, key, requestID string, in PlanningLeftoverInput) (Record, error) {
	if err := normalizePlanningLeftover(&in); err != nil {
		return nil, err
	}
	result, err := s.planningExtensionWrite(ctx, id, pid, key, requestID, "leftovers", in.PlanningExtensionCommand, in,
		func(ctx context.Context, tx pgx.Tx, plan Record) (Record, error) {
			if _, err := planningExtensionWindow(plan, in.Week); err != nil {
				return nil, err
			}
			if err := mutatePlanningLeftover(ctx, tx, id, pid, in); err != nil {
				return nil, err
			}
			return readPlanningLeftovers(ctx, tx, id, plan, in.Generation, in.Week)
		})
	return result, planningLeftoverError(err)
}

func mutatePlanningLeftover(ctx context.Context, tx pgx.Tx, id auth.Identity, pid string, in PlanningLeftoverInput) error {
	switch in.Action {
	case "source_save":
		meal, err := lockPlanningMeal(ctx, tx, id, pid, in.MealID, in.MealVersion)
		if err != nil {
			return err
		}
		if meal.Kind != "recipe" || meal.Status == "cancelled" || meal.ServingsMode != "manual" {
			return Invalid{"Produktion zuerst ausdrücklich als manuelle Gesamtportionen prüfen; Teilnehmer vergrößern sie nicht automatisch"}
		}
		found := false
		for _, component := range meal.Components {
			if component.ID == in.ComponentID && component.Version == in.ComponentVersion {
				found = true
			}
		}
		if !found {
			return PlanningConflict{"Die Quellkomponente wurde geändert. Produktion erneut prüfen."}
		}
		var command pgconn.CommandTag
		if in.SourceID == "" {
			command, err = tx.Exec(ctx, `INSERT INTO mealplan.leftover_sources
 (household_id,plan_id,source_meal_id,source_component_id,expected_servings,reviewed_meal_version,reviewed_component_version,source_hash,created_by,changed_by)
 SELECT $1,$2,$3,c.id,$5,$6,$7,sha256(convert_to(c.snapshot::text,'UTF8')),$8,$8
 FROM mealplan.components c WHERE c.household_id=$1 AND c.id=$4 AND c.meal_id=$3`,
				id.HouseholdID, pid, in.MealID, in.ComponentID, in.ExpectedServings, in.MealVersion, in.ComponentVersion, id.UserID)
		} else {
			command, err = tx.Exec(ctx, `UPDATE mealplan.leftover_sources s SET expected_servings=$5,reviewed_meal_version=$6,
 reviewed_component_version=$7,source_hash=sha256(convert_to(c.snapshot::text,'UTF8')),changed_by=$8
 FROM mealplan.components c WHERE s.household_id=$1 AND s.plan_id=$2 AND s.id=$3 AND s.version=$4
 AND c.household_id=s.household_id AND c.id=s.source_component_id AND c.id=$9 AND s.source_meal_id=$10`,
				id.HouseholdID, pid, in.SourceID, in.SourceVersion, in.ExpectedServings, in.MealVersion, in.ComponentVersion, id.UserID, in.ComponentID, in.MealID)
		}
		return planningLeftoverChanged(command, err)
	case "source_delete":
		command, err := tx.Exec(ctx, `DELETE FROM mealplan.leftover_sources WHERE household_id=$1 AND plan_id=$2 AND id=$3 AND version=$4`,
			id.HouseholdID, pid, in.SourceID, in.SourceVersion)
		return planningLeftoverChanged(command, err)
	case "allocation_delete":
		command, err := tx.Exec(ctx, `DELETE FROM mealplan.leftover_allocations WHERE household_id=$1 AND plan_id=$2 AND id=$3 AND version=$4`,
			id.HouseholdID, pid, in.AllocationID, in.AllocationVersion)
		return planningLeftoverChanged(command, err)
	}
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM mealplan.leftover_sources WHERE household_id=$1 AND plan_id=$2 AND id=$3 FOR UPDATE NOWAIT`,
		id.HouseholdID, pid, in.SourceID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if version != in.SourceVersion {
		return PlanningConflict{"Die erwartete Restemenge wurde geändert. Quelle erneut prüfen."}
	}
	if _, err = lockPlanningMeal(ctx, tx, id, pid, in.TargetMealID, in.TargetVersion); err != nil {
		return err
	}
	var command pgconn.CommandTag
	if in.AllocationID == "" {
		command, err = tx.Exec(ctx, `INSERT INTO mealplan.leftover_allocations
 (household_id,plan_id,source_id,target_meal_id,servings,reviewed_target_version,created_by,changed_by,reviewed_source_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8)`, id.HouseholdID, pid, in.SourceID, in.TargetMealID, in.Servings, in.TargetVersion, id.UserID, in.SourceVersion)
	} else {
		command, err = tx.Exec(ctx, `UPDATE mealplan.leftover_allocations SET source_id=$5,servings=$6,reviewed_target_version=$7,changed_by=$8,reviewed_source_version=$10
 WHERE household_id=$1 AND plan_id=$2 AND id=$3 AND version=$4 AND target_meal_id=$9`,
			id.HouseholdID, pid, in.AllocationID, in.AllocationVersion, in.SourceID, in.Servings, in.TargetVersion, id.UserID, in.TargetMealID, in.SourceVersion)
	}
	return planningLeftoverChanged(command, err)
}

func planningLeftoverChanged(command pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return PlanningConflict{"Die gespeicherte Restezuordnung wurde geändert oder entfernt"}
	}
	return nil
}
