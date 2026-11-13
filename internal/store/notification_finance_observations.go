package store

import (
	"bytes"
	"context"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

type financeBudgetObservation struct {
	generation, version, epoch, membership, auth, episode int64
	activated                                             time.Time
	month, state                                          string
	stamp                                                 []byte
	healthy, detected, cutoff                             *time.Time
}

func nextFinanceBudgetObservation(old financeBudgetObservation, stable bool, b financeNotificationBudget, threshold int, now time.Time) financeBudgetObservation {
	next := old
	if !b.eligible {
		next.state = "ineligible"
		next.healthy = nil
		next.detected = nil
		next.cutoff = nil
		return next
	}
	if !stable || old.state == "ineligible" {
		next.state = "baseline"
		next.healthy = nil
		next.detected = nil
		next.cutoff = nil
		if !financeBudgetCrossed(b, threshold) {
			next.state = "healthy"
			next.healthy = &now
		}
		return next
	}
	switch old.state {
	case "baseline", "crossed":
		if financeBudgetRearmed(b, threshold) {
			next.state = "healthy"
			next.healthy = &now
			next.detected = nil
			next.cutoff = nil
		}
	case "healthy":
		if financeBudgetCrossed(b, threshold) {
			next.state = "crossed"
			next.episode++
			next.detected = &now
			next.cutoff = old.healthy
		} else {
			next.healthy = &now
		}
	default:
		next.state = "baseline"
		next.healthy = nil
		next.detected = nil
		next.cutoff = nil
	}
	return next
}

func observeFinanceBudget(ctx context.Context, tx pgx.Tx, id auth.Identity, c financeNotificationContext, budgetID, month string, b financeNotificationBudget, threshold int, sampled time.Time) (*notification.Intent, error) {
	var old financeBudgetObservation
	err := tx.QueryRow(ctx, `SELECT module_generation,module_activated_at,month::text,budget_version,permission_epoch,membership_version,auth_version,
 scope_stamp,state,episode,last_healthy_at,detected_at,consent_cutoff FROM expenses.notification_budget_observations
 WHERE household_id=$1 AND user_id=$2 AND budget_id=$3 AND threshold=$4 FOR UPDATE`, id.HouseholdID, id.UserID, budgetID, threshold).
		Scan(&old.generation, &old.activated, &old.month, &old.version, &old.epoch, &old.membership, &old.auth, &old.stamp, &old.state, &old.episode, &old.healthy, &old.detected, &old.cutoff)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	stable := err == nil && old.generation == c.scope.generation && old.activated.Equal(c.activated) && old.month == month+"-01" && old.version == b.version &&
		old.epoch == c.epoch && old.membership == c.membershipVersion && old.auth == id.AuthVersion && bytes.Equal(old.stamp, b.stamp)
	next := nextFinanceBudgetObservation(old, stable, b, threshold, sampled)
	if next.episode > notification.MaxRevision {
		return nil, ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.notification_budget_observations
 (household_id,user_id,budget_id,threshold,module_generation,module_activated_at,month,budget_version,permission_epoch,membership_version,auth_version,
 scope_stamp,state,episode,last_healthy_at,observed_at,detected_at,consent_cutoff)
 VALUES($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
 ON CONFLICT(household_id,user_id,budget_id,threshold) DO UPDATE SET
 module_generation=excluded.module_generation,module_activated_at=excluded.module_activated_at,month=excluded.month,budget_version=excluded.budget_version,
 permission_epoch=excluded.permission_epoch,membership_version=excluded.membership_version,auth_version=excluded.auth_version,scope_stamp=excluded.scope_stamp,
 state=excluded.state,episode=excluded.episode,last_healthy_at=excluded.last_healthy_at,observed_at=excluded.observed_at,detected_at=excluded.detected_at,consent_cutoff=excluded.consent_cutoff`,
		id.HouseholdID, id.UserID, budgetID, threshold, c.scope.generation, c.activated, month+"-01", b.version, c.epoch, c.membershipVersion, id.AuthVersion,
		b.stamp, next.state, next.episode, next.healthy, sampled, next.detected, next.cutoff)
	if err != nil {
		return nil, err
	}
	if next.state != "crossed" || next.detected == nil || next.cutoff == nil || !next.detected.Add(24*time.Hour).After(sampled) {
		return nil, nil
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: id.HouseholdID, UserID: id.UserID, Module: "expenses", Generation: c.scope.generation,
		ResourceType: "budget_thresholds", ResourceID: budgetID, Revision: b.version, Occurrence: financeBudgetOccurrence(month, threshold, next.episode), Category: "expenses",
		EventAt: *next.detected, NotBefore: *next.detected, ExpiresAt: next.detected.Add(24 * time.Hour)}, nil
}
