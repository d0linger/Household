package store

import (
	"context"
	"errors"
	"time"

	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

// NotificationCategoryAvailable is an explicit rollout allowlist. A preference
// alone cannot enable a category whose domain adapter has not been installed.
func NotificationCategoryAvailable(category string) bool {
	return category == "test" || category == "chores_due" || category == "chores_digest" ||
		category == "chores_escalation" || category == "cooking_timer" || category == "shopping" || category == "pantry" || category == "fuel" || category == "expenses" || category == "meters" || category == "documents" || category == "mealplan" || category == "security"
}

func ReadNotificationIntent(ctx context.Context, tx pgx.Tx, id string) (notification.Intent, error) {
	var i notification.Intent
	err := tx.QueryRow(ctx, `SELECT schema_version,household_id::text,user_id::text,coalesce(module_id,''),module_generation,
 resource_type,resource_id::text,source_revision,occurrence,category,event_at,not_before,expires_at
 FROM core.notification_intents WHERE id=$1`, id).Scan(&i.Schema, &i.HouseholdID, &i.UserID, &i.Module, &i.Generation, &i.ResourceType, &i.ResourceID, &i.Revision, &i.Occurrence, &i.Category, &i.EventAt, &i.NotBefore, &i.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	return i, i.Validate()
}

// NotificationAudience fails closed for unimplemented adapters. A module must
// prove resource visibility, current revision and domain rules here; membership
// alone is never an authorization decision for private finance/document data.
func (s *Store) NotificationAudience(ctx context.Context, tx pgx.Tx, i notification.Intent) (bool, error) {
	if i.Validate() != nil || !NotificationCategoryAvailable(i.Category) {
		return false, nil
	}
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 WHERE m.household_id=$1 AND m.user_id=$2 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password)`, i.HouseholdID, i.UserID).Scan(&eligible)
	if err != nil || !eligible {
		return false, err
	}
	if i.Module != "" {
		var state string
		var generation int64
		var changed time.Time
		err = tx.QueryRow(ctx, `SELECT state,data_generation,changed_at FROM core.module_states WHERE household_id=$1 AND module_id=$2 FOR SHARE`, i.HouseholdID, i.Module).Scan(&state, &generation, &changed)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if state != "enabled" || generation != i.Generation || changed.After(i.EventAt) {
			return false, nil
		}
	}
	switch i.Category {
	case "security":
		cutoff, err := securityNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "mealplan":
		cutoff, err := s.planningNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "expenses":
		// Financial data requires the actual device/request identity through
		// FinanceNotificationAuthority, never this generic membership guard.
		return false, nil
	case "meters":
		cutoff, err := s.meterNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "documents":
		cutoff, err := s.documentNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "fuel":
		cutoff, err := s.fuelNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "pantry":
		cutoff, err := s.pantryNotificationCutoff(ctx, tx, i)
		return !cutoff.IsZero(), err
	case "shopping":
		return s.shoppingNotificationAudience(ctx, tx, i)
	case "cooking_timer":
		return cookingNotificationAudience(ctx, tx, i)
	case "chores_due", "chores_digest", "chores_escalation":
		return choreNotificationAudience(ctx, tx, i)
	case "test":
		if i.ResourceType != "push_subscriptions" {
			return false, nil
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.push_subscriptions WHERE id=$1 AND household_id=$2 AND user_id=$3 AND disabled_at IS NULL)`, i.ResourceID, i.HouseholdID, i.UserID).Scan(&eligible)
		return eligible, err
	default:
		return false, nil
	}
}

// NotificationConsentCutoff keeps observation-based enrollment conservative.
// A digest may contain several references, but at least one currently valid
// source must predate every required account, device and registration consent.
func (s *Store) NotificationConsentCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Validate() != nil {
		return time.Time{}, nil
	}
	if i.Category == "expenses" {
		return time.Time{}, nil
	}
	if i.Category == "security" {
		return securityNotificationCutoff(ctx, tx, i)
	}
	if i.Category == "pantry" {
		return s.pantryNotificationCutoff(ctx, tx, i)
	}
	if i.Category == "meters" {
		return s.meterNotificationCutoff(ctx, tx, i)
	}
	if i.Category == "documents" {
		return s.documentNotificationCutoff(ctx, tx, i)
	}
	if i.Category == "mealplan" {
		return s.planningNotificationCutoff(ctx, tx, i)
	}
	if i.Category == "fuel" {
		return s.fuelNotificationCutoff(ctx, tx, i)
	}
	return i.EventAt, nil
}

func NotificationTarget(i notification.Intent) string {
	// Each domain adapter adds a compiled route here, never a stored URL.
	if securityNotificationReference(i) {
		return "/settings/security"
	}
	if i.Category == "mealplan" {
		return planningNotificationTarget(i)
	}
	if i.Category == "documents" && i.Validate() == nil {
		switch i.ResourceType {
		case "document_deadline":
			return "/m/documents/workspace/deadlines"
		case "document_processing":
			return "/m/documents/workspace/processing"
		case "document_import":
			return "/m/documents/workspace/integrations"
		}
	}
	if i.Category == "meters" && i.Validate() == nil {
		if i.ResourceType == "meter_reading" {
			return "/m/meters/workspace/capture?register=" + i.ResourceID
		}
		if i.ResourceType == "meter_observation" {
			return "/m/meters/workspace/settings?register=" + i.ResourceID
		}
	}
	if i.Category == "expenses" && i.Validate() == nil {
		if i.ResourceType == "payment_reminders" {
			return "/m/expenses/workspace/entries?id=" + i.ResourceID
		}
		if i.ResourceType == "budget_thresholds" {
			return "/m/expenses/workspace/planning"
		}
	}
	if i.Category == "fuel" && i.Validate() == nil {
		if i.ResourceType == "maintenance_plans" {
			return "/m/fuel/workspace/maintenance"
		}
		if i.ResourceType == "vehicles" {
			return "/m/fuel/workspace/overview"
		}
	}
	if i.Category == "pantry" && i.ResourceType == "pantry_digest" && i.Validate() == nil {
		return "/m/pantry/stock"
	}
	if i.Category == "cooking_timer" && i.ResourceType == "cooking_timers" && i.Validate() == nil {
		return "/m/recipes/cooking/timer/" + i.ResourceID
	}
	if i.Category == "chores_due" && i.ResourceType == "task_occurrences" && i.Validate() == nil {
		return "/m/chores/workspace/work/" + i.ResourceID
	}
	if i.Category == "chores_digest" || i.Category == "chores_escalation" {
		return "/m/chores/workspace/today"
	}
	if i.Category == "test" {
		return "/settings/notifications"
	}
	return "/notifications"
}
