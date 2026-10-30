package store

import (
	"context"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func reviewPlanningLeftoverCopy(ctx context.Context, tx pgx.Tx, id auth.Identity, pid string, week PlanningWeek) error {
	ids := make([]string, 0, len(week.Meals))
	for _, meal := range week.Meals {
		ids = append(ids, meal.ID)
	}
	var crossing, stale bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mealplan.leftover_allocations a
 JOIN mealplan.leftover_sources s ON s.household_id=a.household_id AND s.id=a.source_id
 WHERE a.household_id=$1 AND a.plan_id=$2 AND (s.source_meal_id=ANY($3::uuid[]))<>(a.target_meal_id=ANY($3::uuid[]))),
 EXISTS(SELECT 1 FROM mealplan.leftover_sources s JOIN mealplan.meals m ON m.household_id=s.household_id AND m.id=s.source_meal_id
 JOIN mealplan.components c ON c.household_id=s.household_id AND c.id=s.source_component_id
 WHERE s.household_id=$1 AND s.plan_id=$2 AND s.source_meal_id=ANY($3::uuid[]) AND(NOT `+planningLeftoverSourceCurrent+` OR EXISTS(
 SELECT 1 FROM mealplan.leftover_allocations a JOIN mealplan.meals t ON t.household_id=a.household_id AND t.id=a.target_meal_id
 WHERE a.household_id=s.household_id AND a.source_id=s.id AND(a.reviewed_source_version<>s.version OR a.reviewed_target_version<>t.version OR t.status='cancelled' OR a.servings>t.servings))))`,
		id.HouseholdID, pid, ids).Scan(&crossing, &stale)
	if err != nil {
		return err
	}
	if crossing {
		return PlanningConflict{"Resteverknüpfung überschreitet die Kopierwoche: Quelle und Ziel gemeinsam auswählen oder die Zuordnung zuerst ausdrücklich lösen."}
	}
	if stale {
		return PlanningConflict{"Geplante Reste vor der Wochenkopie erneut prüfen oder ausdrücklich lösen. Eine Kopie bestätigt keinen ungeprüften Restestand."}
	}
	return nil
}

// All meals, components and participants have their final copied identities
// before this second pass. Calendar credentials and physical stock are absent.
func copyPlanningLeftovers(ctx context.Context, tx pgx.Tx, id auth.Identity, oldPlan, newPlan string, mealIDs map[string]string) error {
	oldMeals := make([]string, 0, len(mealIDs))
	for oldID := range mealIDs {
		oldMeals = append(oldMeals, oldID)
	}
	rows, err := tx.Query(ctx, `SELECT s.id,s.source_meal_id,c.position,s.expected_servings::text
 FROM mealplan.leftover_sources s JOIN mealplan.components c ON c.household_id=s.household_id AND c.id=s.source_component_id
 WHERE s.household_id=$1 AND s.plan_id=$2 AND s.source_meal_id=ANY($3::uuid[]) ORDER BY s.id LIMIT 421`, id.HouseholdID, oldPlan, oldMeals)
	if err != nil {
		return err
	}
	type copiedSource struct {
		id, meal, expected string
		position           int
	}
	sources := []copiedSource{}
	for rows.Next() {
		var source copiedSource
		if err = rows.Scan(&source.id, &source.meal, &source.position, &source.expected); err != nil {
			rows.Close()
			return err
		}
		sources = append(sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(sources) > planningMealLimit*planningComponentLimit {
		return Invalid{"Mehr als420 Restequellen in der Kopierwoche"}
	}
	for _, source := range sources {
		newSource := value.UUID()
		command, writeErr := tx.Exec(ctx, `INSERT INTO mealplan.leftover_sources
 (id,household_id,plan_id,source_meal_id,source_component_id,expected_servings,reviewed_meal_version,reviewed_component_version,source_hash,created_by,changed_by)
 SELECT $1,$2,$3,m.id,c.id,$6,m.version,c.version,sha256(convert_to(c.snapshot::text,'UTF8')),$7,$7
 FROM mealplan.meals m JOIN mealplan.components c ON c.household_id=m.household_id AND c.meal_id=m.id
 WHERE m.household_id=$2 AND m.plan_id=$3 AND m.id=$4 AND c.position=$5`,
			newSource, id.HouseholdID, newPlan, mealIDs[source.meal], source.position, source.expected, id.UserID)
		if err = planningLeftoverChanged(command, writeErr); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT target_meal_id,servings::text FROM mealplan.leftover_allocations
 WHERE household_id=$1 AND plan_id=$2 AND source_id=$3 ORDER BY target_meal_id LIMIT 43`, id.HouseholdID, oldPlan, source.id)
		if err != nil {
			return err
		}
		allocations := []Record{}
		for rows.Next() {
			var target, servings string
			if err = rows.Scan(&target, &servings); err != nil {
				rows.Close()
				return err
			}
			allocations = append(allocations, Record{"target": target, "servings": servings})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(allocations) > planningMealLimit {
			return PlanningConflict{"Restezuordnungen überschreiten die geprüfte Kopierwoche"}
		}
		for _, allocation := range allocations {
			target, ok := mealIDs[String(allocation["target"])]
			if !ok {
				return PlanningConflict{"Resteziel fehlt in der unabhängig kopierten Woche"}
			}
			_, err = tx.Exec(ctx, `INSERT INTO mealplan.leftover_allocations
 (household_id,plan_id,source_id,target_meal_id,servings,reviewed_target_version,created_by,changed_by,reviewed_source_version)
 SELECT $1,$2,$3,m.id,$5,m.version,$6,$6,1 FROM mealplan.meals m WHERE m.household_id=$1 AND m.plan_id=$2 AND m.id=$4`,
				id.HouseholdID, newPlan, newSource, target, allocation["servings"], id.UserID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
