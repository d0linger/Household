package store

import (
	"context"

	"household/internal/auth"
	"household/internal/mealportions"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func normalizePlanningParticipationPayload(in *PlanningInput) error {
	if in.Meal.Participation == nil {
		return nil
	}
	if in.Action != "save" {
		return Invalid{"Teilnehmer nur zusammen mit einer Mahlzeit speichern"}
	}
	p := in.Meal.Participation
	review, err := mealportions.Normalize(mealportions.Input{Mode: p.Mode, Members: p.MembershipIDs, Guests: p.GuestCount, Servings: in.Meal.Servings})
	if err != nil {
		return Invalid{"Teilnehmer und Portionen prüfen: höchstens100 unterschiedliche Mitglieder und100 Gäste; positive exakte Portionen und ausdrücklich gewählter Modus erforderlich"}
	}
	// Own the normalized payload before its durable request hash is calculated.
	in.Meal.Participation = &PlanningParticipationInput{Mode: review.Mode, MembershipIDs: review.Members, GuestCount: review.Guests}
	in.Meal.Servings = review.Servings
	return nil
}

func planningParticipantReview(ctx context.Context, tx pgx.Tx, id auth.Identity, old PlanningMeal, in *PlanningInput) (mealportions.Review, error) {
	selected := mealportions.Input{Mode: old.ServingsMode, Guests: old.GuestCount, Servings: in.Meal.Servings}
	existing := make(map[string]bool, len(old.Participants))
	for _, participant := range old.Participants {
		existing[participant.MembershipID] = true
		selected.Members = append(selected.Members, participant.MembershipID)
	}
	if p := in.Meal.Participation; p != nil {
		selected.Mode, selected.Guests, selected.Members = p.Mode, p.GuestCount, p.MembershipIDs
	}
	review, err := mealportions.Normalize(selected)
	if err != nil {
		return review, Invalid{"Mahlzeitenportionen passen nicht zum gespeicherten Teilnehmermodus. Im Planungseditor ausdrücklich prüfen oder manuell überschreiben."}
	}
	added := make([]string, 0, len(review.Members))
	for _, member := range review.Members {
		if !existing[member] {
			added = append(added, member)
		}
	}
	if len(added) > 0 {
		locked, lockErr := tx.Exec(ctx, `SELECT member.id FROM core.memberships member JOIN core.users u ON u.id=member.user_id
 WHERE member.household_id=$1 AND member.id=ANY($2::uuid[]) AND member.inactive_at IS NULL AND u.disabled_at IS NULL
 ORDER BY member.id FOR SHARE OF member,u NOWAIT`, id.HouseholdID, added)
		if lockErr != nil {
			return review, shoppingContention(lockErr)
		}
		if locked.RowsAffected() != int64(len(added)) {
			return review, Invalid{"Neue Teilnehmer müssen aktive Mitglieder dieses Haushalts sein. Bereits gespeicherte inaktive Teilnehmer bleiben erhalten."}
		}
	}
	in.Meal.Servings = review.Servings
	return review, nil
}

func savePlanningParticipants(ctx context.Context, tx pgx.Tx, id auth.Identity, mealID string, old PlanningMeal, review mealportions.Review) error {
	// Remove deselections first so replacing one member at the100-member limit
	// cannot trip the immediate insertion bound.
	if _, err := tx.Exec(ctx, "DELETE FROM mealplan.participants WHERE household_id=$1 AND meal_id=$2 AND NOT(membership_id=ANY($3::uuid[]))", id.HouseholdID, mealID, review.Members); err != nil {
		return err
	}
	retained := make(map[string]bool, len(old.Participants))
	for _, p := range old.Participants {
		retained[p.MembershipID] = true
	}
	for _, member := range review.Members {
		if retained[member] {
			continue
		}
		if _, err := tx.Exec(ctx, "INSERT INTO mealplan.participants(household_id,meal_id,membership_id,created_by) VALUES($1,$2,$3,$4)", id.HouseholdID, mealID, member, id.UserID); err != nil {
			return err
		}
	}
	return nil
}

func planningComponentPortions(input *PlanningComponentInput, old PlanningComponent, mealServings string) error {
	implicit := input.ServingsMode == ""
	if implicit {
		input.ServingsMode = old.ServingsMode
		if input.ServingsMode == "" {
			input.ServingsMode = "independent"
		}
	}
	if implicit && input.ServingsMode == "meal" {
		supplied, err := value.Decimal(input.Servings)
		derived, derivedErr := value.Decimal(mealServings)
		if err != nil || derivedErr != nil || supplied.Cmp(derived) != 0 {
			return Invalid{"Verknüpfte Komponentenportionen im Planungseditor ausdrücklich prüfen oder als eigene Portionen freigeben."}
		}
	}
	servings, err := mealportions.ComponentServings(input.ServingsMode, input.Servings, mealServings)
	if err != nil {
		return Invalid{"Komponentenportionen und Modus prüfen: eigene positive exakte Menge oder ausdrücklich mit der Mahlzeit verknüpfen"}
	}
	input.Servings = servings
	return nil
}

func validatePlanningGenericPortions(ctx context.Context, tx pgx.Tx, id auth.Identity, mealID string, data Record) error {
	var mode, actual string
	var linked bool
	if err := tx.QueryRow(ctx, `SELECT m.servings_mode,m.servings::text,EXISTS(
 SELECT 1 FROM mealplan.components c WHERE c.household_id=m.household_id AND c.meal_id=m.id AND c.servings_mode='meal')
 FROM mealplan.meals m WHERE m.household_id=$1 AND m.id=$2`, id.HouseholdID, mealID).Scan(&mode, &actual, &linked); err != nil {
		return err
	}
	if mode != "participants" && !linked {
		return nil
	}
	proposed, err := value.Decimal(String(data["servings"]))
	current, currentErr := value.Decimal(actual)
	if err != nil || currentErr != nil || proposed.Cmp(current) != 0 {
		return Invalid{"Teilnehmerabhängige oder verknüpfte Portionen in der Wochenplanung ausdrücklich ändern oder freigeben. Die übrigen Angaben können hier weiter bearbeitet werden."}
	}
	return nil
}
