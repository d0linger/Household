package store

import (
	"context"
	"time"

	"household/internal/auth"
)

// PlanningTodayMeal is a bounded summary of a saved household meal, without
// recipe snapshots, private notes, or inferred notification recipients.
type PlanningTodayMeal struct {
	ID     string `json:"id"`
	PlanID string `json:"plan_id"`
	Title  string `json:"title"`
	Slot   string `json:"slot"`
	Status string `json:"status"`
}

// TodayMeals reads one household civil day. Callers hold the module gate and
// check that meal planning is enabled before accessing its schema.
func (s *Store) TodayMeals(ctx context.Context, id auth.Identity, day string, limit int) ([]PlanningTodayMeal, error) {
	if _, err := time.Parse(time.DateOnly, day); err != nil || limit < 1 || limit > 12 {
		return nil, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT m.id,m.plan_id,m.title,m.meal_slot,m.status
 FROM mealplan.meals m JOIN mealplan.plans p ON p.household_id=m.household_id AND p.id=m.plan_id
 WHERE m.household_id=$1 AND m.planned_on=$2::date AND NOT p.is_template
 AND m.status IN('planned','purchased','cooked')
 ORDER BY CASE m.meal_slot WHEN 'breakfast' THEN 1 WHEN 'lunch' THEN 2 WHEN 'dinner' THEN 3 ELSE 4 END,m.id
 LIMIT $3`, id.HouseholdID, day, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PlanningTodayMeal, 0, limit)
	for rows.Next() {
		var meal PlanningTodayMeal
		if err = rows.Scan(&meal.ID, &meal.PlanID, &meal.Title, &meal.Slot, &meal.Status); err != nil {
			return nil, err
		}
		result = append(result, meal)
	}
	return result, rows.Err()
}
