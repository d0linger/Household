package store

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) movePlanningMeal(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, in PlanningInput) (Record, error) {
	pid := String(plan["id"])
	ids := []string{in.MealID}
	versions := map[string]int64{in.MealID: in.Version}
	if in.Action == "swap" {
		if in.OtherID == in.MealID {
			return nil, Invalid{"Zum Tauschen eine andere Mahlzeit auswählen"}
		}
		ids = append(ids, in.OtherID)
		versions[in.OtherID] = in.OtherVersion
	}
	for _, mid := range ids {
		if !value.ValidUUID(mid) {
			return nil, ErrNotFound
		}
	}
	slices.Sort(ids)
	locked, err := tx.Exec(ctx, "SELECT id FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id=ANY($3::uuid[]) ORDER BY id FOR UPDATE NOWAIT", id.HouseholdID, pid, ids)
	if err != nil {
		return nil, err
	}
	if locked.RowsAffected() != int64(len(ids)) {
		return nil, ErrNotFound
	}
	meals := map[string]PlanningMeal{}
	for _, mid := range ids {
		meal, lockErr := lockPlanningMeal(ctx, tx, id, pid, mid, versions[mid])
		if lockErr != nil {
			return nil, lockErr
		}
		meals[mid] = meal
	}
	meal := meals[in.MealID]
	if in.Action == "delete" {
		if _, err = tx.Exec(ctx, "DELETE FROM mealplan.meals WHERE household_id=$1 AND id=$2", id.HouseholdID, meal.ID); err != nil {
			return nil, err
		}
		m, _ := s.Module("mealplan")
		if err = s.cleanupOrphans(ctx, tx, id, m); err != nil {
			return nil, err
		}
		return planningResult(pid, ""), nil
	}
	for _, item := range meals {
		if item.Status == "cooked" || item.Status == "cancelled" {
			return nil, Invalid{"Mahlzeit „" + item.Title + "“ zuerst ausdrücklich wieder öffnen"}
		}
	}
	if in.Action == "move" {
		if !value.Date(in.TargetDate) || !planningSlot(in.TargetSlot) || in.TargetDate < String(plan["starts_on"]) || in.TargetDate > String(plan["ends_on"]) {
			return nil, Invalid{"Zieldatum und Mahlzeit innerhalb des Plans auswählen"}
		}
		_, err = tx.Exec(ctx, "UPDATE mealplan.meals SET planned_on=$3,meal_slot=$4 WHERE household_id=$1 AND id=$2", id.HouseholdID, meal.ID, in.TargetDate, in.TargetSlot)
	} else {
		other := meals[in.OtherID]
		_, err = tx.Exec(ctx, `UPDATE mealplan.meals SET planned_on=CASE WHEN id=$2 THEN $4::date ELSE $5::date END,meal_slot=CASE WHEN id=$2 THEN $6 ELSE $7 END WHERE household_id=$1 AND id=ANY($3::uuid[])`, id.HouseholdID, meal.ID, ids, other.PlannedOn, meal.PlannedOn, other.Slot, meal.Slot)
	}
	if err != nil {
		return nil, err
	}
	for _, mid := range ids {
		if err = s.projectPlanning(ctx, tx, id, "meals", mid, in.Generation); err != nil {
			return nil, err
		}
	}
	return planningResult(pid, meal.ID), nil
}

func (s *Store) copyPlanningWeek(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, key string, requestHash []byte, in PlanningInput) (Record, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return nil, Invalid{"Name der neuen Woche: 1 bis 100 Zeichen erforderlich"}
	}
	days, err := planningWindow(in.Week)
	if err != nil {
		return nil, err
	}
	target, err := planningWindow(in.TargetDate)
	if err != nil {
		return nil, err
	}
	if days[6] < String(plan["starts_on"]) || days[0] > String(plan["ends_on"]) {
		return nil, Invalid{"Quellwoche liegt außerhalb des Plans"}
	}
	pid := String(plan["id"])
	week, err := lockPlanningWeek(ctx, tx, id, pid, in.Week)
	if err != nil {
		return nil, err
	}
	var total int64
	ids := make([]string, 0)
	for _, meal := range week.Meals {
		for _, component := range meal.Components {
			total += component.SnapshotBytes
			ids = append(ids, component.ID)
		}
	}
	if total > planningCopyBytes {
		return nil, Invalid{"Rezeptstände der gewählten Woche überschreiten 16 MiB. Auswahl vor dem Kopieren verkleinern."}
	}
	if err = lockPlanningComponents(ctx, tx, id, ids); err != nil {
		return nil, err
	}
	newPlan := value.UUID()
	if _, err = tx.Exec(ctx, "INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,is_template,created_by,copy_source_plan,copy_key,copy_request_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", newPlan, id.HouseholdID, name, target[0], target[6], in.IsTemplate, id.UserID, pid, key, requestHash); err != nil {
		return nil, err
	}
	for _, meal := range week.Meals {
		date, dateErr := planningCopyDate(meal.PlannedOn, in.Week, in.TargetDate)
		if dateErr != nil {
			return nil, dateErr
		}
		newMeal := value.UUID()
		if _, err = tx.Exec(ctx, `INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,meal_slot,title,servings,notes,kind,status,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'open',$10)`, newMeal, id.HouseholdID, newPlan, date, meal.Slot, meal.Title, meal.Servings, meal.Notes, meal.Kind, id.UserID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mealplan.components(household_id,meal_id,position,servings,prep_minutes,cook_minutes,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,copied_at,created_by)
 SELECT household_id,$3,position,servings,prep_minutes,cook_minutes,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,copied_at,$4 FROM mealplan.components WHERE household_id=$1 AND meal_id=$2 ORDER BY position,id`, id.HouseholdID, meal.ID, newMeal, id.UserID); err != nil {
			return nil, err
		}
		if err = s.projectPlanning(ctx, tx, id, "meals", newMeal, in.Generation); err != nil {
			return nil, err
		}
	}
	result := planningResult(newPlan, "")
	result["url"] = String(result["url"]) + "?week=" + in.TargetDate
	return result, nil
}
