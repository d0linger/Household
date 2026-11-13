package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const PlanningNotificationScanJobKind = "mealplan_notification_scan_v1"

func planningNotificationIntents(ctx context.Context, tx pgx.Tx, household, meal string, now time.Time) ([]notification.Intent, error) {
	var user, plan, date string
	var generation, revision int64
	var configured, activated time.Time
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT member.user_id::text,m.plan_id::text,m.planned_on::text,ms.data_generation,n.version,
 n.updated_at,ms.changed_at,n.steps FROM mealplan.notification_settings n
 JOIN mealplan.meals m ON m.household_id=n.household_id AND m.id=n.id
 JOIN mealplan.plans p ON p.household_id=m.household_id AND p.id=m.plan_id
 JOIN core.households h ON h.id=n.household_id
 JOIN core.module_states ms ON ms.household_id=n.household_id AND ms.module_id='mealplan'
 JOIN core.memberships member ON member.household_id=n.household_id AND member.id=n.responsible_member_id
 JOIN core.users u ON u.id=member.user_id WHERE n.household_id=$1 AND n.id=$2 AND n.enabled
 AND n.meal_version=m.version AND n.module_generation=ms.data_generation AND n.timezone=h.timezone
 AND NOT p.is_template AND p.notification_active_since<=n.updated_at AND m.status IN('planned','purchased')
 AND ms.state='enabled' AND member.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`,
		household, meal).Scan(&user, &plan, &date, &generation, &revision, &configured, &activated, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var steps []planningNotificationStep
	if len(raw) > 16384 || shoppingDecodeClosed(raw, &steps) != nil || len(steps) > 8 {
		return nil, ErrConflict
	}
	intents := make([]notification.Intent, 0, len(steps))
	for _, step := range steps {
		at := step.ScheduledAt
		if at.After(now) || !at.Add(4*time.Hour).After(now) || configured.After(at) || activated.After(at) {
			continue
		}
		i := notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: user,
			Module: "mealplan", Generation: generation, ResourceType: "mealplan_preparation", ResourceID: meal,
			Revision: revision, Category: "mealplan", Occurrence: "step/" + plan + "/" + step.ID + "/" + date,
			EventAt: at, NotBefore: at, ExpiresAt: at.Add(4 * time.Hour)}
		if i.Validate() != nil || planningNotificationTarget(i) == "" {
			return nil, ErrConflict
		}
		intents = append(intents, i)
	}
	return intents, nil
}

func (s *Store) planningNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Validate() != nil || i.Category != "mealplan" || i.ResourceType != "mealplan_preparation" {
		return time.Time{}, nil
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, err
	}
	candidates, err := planningNotificationIntents(ctx, tx, i.HouseholdID, i.ResourceID, now)
	if err != nil {
		return time.Time{}, err
	}
	for _, candidate := range candidates {
		if candidate.UserID == i.UserID && candidate.Generation == i.Generation && candidate.Revision == i.Revision &&
			candidate.Occurrence == i.Occurrence && candidate.EventAt.Equal(i.EventAt) &&
			candidate.NotBefore.Equal(i.NotBefore) && candidate.ExpiresAt.Equal(i.ExpiresAt) {
			return candidate.EventAt, nil
		}
	}
	return time.Time{}, nil
}

func planningNotificationTarget(i notification.Intent) string {
	if i.Validate() != nil || i.Category != "mealplan" || i.ResourceType != "mealplan_preparation" {
		return ""
	}
	parts := strings.Split(i.Occurrence, "/")
	if len(parts) != 4 || parts[0] != "step" || !shoppingCanonicalUUID(parts[1]) || !shoppingCanonicalUUID(parts[2]) || !value.Date(parts[3]) {
		return ""
	}
	return "/m/mealplan/workspace/" + parts[1] + "?week=" + parts[3]
}
