package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type planningSelection struct {
	input    PlanningComponentInput
	recipeID string
	revision int64
	bytes    int64
	snapshot []byte
}

func planningSelections(ctx context.Context, tx pgx.Tx, id auth.Identity, old PlanningMeal, in PlanningInput) ([]planningSelection, error) {
	if len(in.Components) > planningComponentLimit {
		return nil, Invalid{"Höchstens zehn Komponenten pro Mahlzeit"}
	}
	if in.Meal.Kind != "recipe" && len(in.Components) > 0 {
		return nil, Invalid{"Nur Rezeptmahlzeiten enthalten Rezeptkomponenten"}
	}
	available := map[string]PlanningComponent{}
	for _, component := range old.Components {
		available[component.ID] = component
	}
	selected := make([]planningSelection, 0, len(in.Components))
	var total int64
	for _, input := range in.Components {
		if err := planningComponentPortions(&input, available[input.ID], in.Meal.Servings); err != nil {
			return nil, err
		}
		if err := planningComponentValues(&input); err != nil {
			return nil, err
		}
		item := planningSelection{input: input}
		if input.ID != "" {
			current, ok := available[input.ID]
			if !ok || current.Version != input.Version {
				return nil, PlanningConflict{"Komponente wurde geändert oder gehört nicht zu dieser Mahlzeit"}
			}
			if input.SourceVersionID != "" && input.SourceVersionID != current.SourceVersionID || input.SourceGeneration != 0 && input.SourceGeneration != current.SourceGeneration {
				return nil, Invalid{"Eine andere Rezeptquelle als neue Komponente prüfen"}
			}
			delete(available, input.ID)
			item.recipeID, item.revision, item.bytes = current.SourceRecipeID, current.SourceRevision, current.SnapshotBytes
		} else {
			if !value.ValidUUID(input.SourceVersionID) {
				return nil, Invalid{"Einen geprüften Rezeptstand auswählen"}
			}
			err := tx.QueryRow(ctx, `SELECT v.recipe_id,v.revision,octet_length(v.snapshot::text),CASE WHEN octet_length(v.snapshot::text)<=1048576 THEN v.snapshot END
 FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id WHERE v.household_id=$1 AND v.id=$2 FOR KEY SHARE OF r NOWAIT`, id.HouseholdID, input.SourceVersionID).Scan(&item.recipeID, &item.revision, &item.bytes, &item.snapshot)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			if item.bytes > planningComponentBytes {
				return nil, Invalid{"Rezeptstand ist größer als 1 MiB. Quelle vor der Übernahme aufteilen."}
			}
			if _, err = recipeBundle(item.snapshot); err != nil {
				return nil, err
			}
		}
		total += item.bytes
		if total > planningMealBytes {
			return nil, Invalid{"Rezeptstände dieser Mahlzeit überschreiten zusammen 2 MiB"}
		}
		selected = append(selected, item)
	}
	return selected, nil
}

func (s *Store) savePlanningMeal(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, in PlanningInput) (Record, error) {
	pid, mid := String(plan["id"]), in.MealID
	var old PlanningMeal
	var err error
	if mid != "" {
		old, err = lockPlanningMeal(ctx, tx, id, pid, mid, in.Version)
		if err != nil {
			return nil, err
		}
	}
	participation, err := planningParticipantReview(ctx, tx, id, old, &in)
	if err != nil {
		return nil, err
	}
	if err = normalizePlanningMeal(&in.Meal); err != nil {
		return nil, err
	}
	if in.Meal.PlannedOn < String(plan["starts_on"]) || in.Meal.PlannedOn > String(plan["ends_on"]) {
		return nil, Invalid{"Mahlzeit liegt außerhalb des Planzeitraums"}
	}
	if old.ID != "" && (old.Status == "cooked" || old.Status == "cancelled") && (old.PlannedOn != in.Meal.PlannedOn || old.Slot != in.Meal.Slot) {
		return nil, Invalid{"Abgeschlossene oder abgesagte Mahlzeit zuerst ausdrücklich wieder öffnen"}
	}
	selected, err := planningSelections(ctx, tx, id, old, in)
	if err != nil {
		return nil, err
	}
	kept := []string{}
	recipes := []string{}
	for _, item := range selected {
		if item.input.ID != "" {
			kept = append(kept, item.input.ID)
		}
		recipes = append(recipes, item.recipeID)
	}
	if old.ID != "" {
		if _, err = tx.Exec(ctx, "DELETE FROM mealplan.components WHERE household_id=$1 AND meal_id=$2 AND NOT(id=ANY($3::uuid[]))", id.HouseholdID, mid, kept); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, "UPDATE mealplan.meals SET title=$3,planned_on=$4,meal_slot=$5,kind=$6,status=$7,servings=$8,notes=$9,servings_mode=$10,guest_count=$11 WHERE household_id=$1 AND id=$2", id.HouseholdID, mid, in.Meal.Title, in.Meal.PlannedOn, in.Meal.Slot, in.Meal.Kind, in.Meal.Status, in.Meal.Servings, in.Meal.Notes, participation.Mode, participation.Guests)
	} else {
		mid = value.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO mealplan.meals(id,household_id,plan_id,title,planned_on,meal_slot,kind,status,servings,notes,created_by,servings_mode,guest_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, mid, id.HouseholdID, pid, in.Meal.Title, in.Meal.PlannedOn, in.Meal.Slot, in.Meal.Kind, in.Meal.Status, in.Meal.Servings, in.Meal.Notes, id.UserID, participation.Mode, participation.Guests)
	}
	if err != nil {
		return nil, err
	}
	if in.Meal.Participation != nil {
		if err = savePlanningParticipants(ctx, tx, id, mid, old, participation); err != nil {
			return nil, err
		}
	}
	if len(selected) > 0 || in.Meal.Kind != "recipe" {
		if _, err = tx.Exec(ctx, "DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='mealplan' AND source_type='meals' AND source_id=$2 AND relation='meal_recipe' AND NOT(target_id=ANY($3::uuid[]))", id.HouseholdID, mid, recipes); err != nil {
			return nil, err
		}
	}
	for position, item := range selected {
		input := item.input
		if input.ID != "" {
			_, err = tx.Exec(ctx, "UPDATE mealplan.components SET position=$3,servings=$4,prep_minutes=$5,cook_minutes=$6,servings_mode=$7 WHERE household_id=$1 AND id=$2", id.HouseholdID, input.ID, position, input.Servings, input.PrepMinutes, input.CookMinutes, input.ServingsMode)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO mealplan.components(household_id,meal_id,position,servings,prep_minutes,cook_minutes,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by,servings_mode) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, id.HouseholdID, mid, position, input.Servings, input.PrepMinutes, input.CookMinutes, item.recipeID, input.SourceVersionID, input.SourceGeneration, item.revision, item.snapshot, id.UserID, input.ServingsMode)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation) VALUES($1,'mealplan','meals',$2,'recipes','recipes',$3,'meal_recipe') ON CONFLICT DO NOTHING`, id.HouseholdID, mid, item.recipeID)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if err = s.projectPlanning(ctx, tx, id, "meals", mid, in.Generation); err != nil {
		return nil, err
	}
	meal, err := readPlanningMeal(ctx, tx, id, pid, mid)
	if err != nil {
		return nil, err
	}
	result := planningResult(pid, mid)
	result["meal"] = meal
	return result, nil
}
