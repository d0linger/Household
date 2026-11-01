package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func recheckFinancialTarget(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, resource string, intent financialIntent, access financialAccess) error {
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return err
	}
	predicate, args, err := financialPredicate(scope, entity, intent, 6)
	if err != nil {
		return err
	}
	var found string
	// security: financialPredicate validates the compiled source entity; the
	// required subject pins this new RC snapshot to the policy already held.
	err = tx.QueryRow(ctx, "SELECT t.id FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
		` t WHERE t.household_id=$1 AND t.id=$2 AND EXISTS(SELECT 1 FROM core.financial_subjects required
 WHERE required.household_id=t.household_id AND required.module_id='expenses' AND required.module_generation=$4
 AND required.resource_type=$5 AND required.resource_id=t.id AND required.policy_id=$3 AND required.retired_at IS NULL) AND `+predicate,
		append([]any{id.HouseholdID, resource, access.policyID, scope.generation, entity}, args...)...).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func lockFinancialReplay(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, resource string, intent financialIntent) error {
	access, err := lockFinancialPolicy(ctx, tx, id, entity, resource, intent)
	if err != nil {
		return err
	}
	return recheckFinancialTarget(ctx, tx, id, entity, resource, intent, access)
}

func lockFinancialResource(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, resource string) error {
	access, err := lockFinancialPolicy(ctx, tx, id, entity, resource, financialEdit)
	if err != nil {
		return err
	}
	var found string
	err = tx.QueryRow(ctx, "SELECT id FROM expenses.entries WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT",
		id.HouseholdID, access.entryID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if entity == "entry_lines" {
		err = tx.QueryRow(ctx, `SELECT id FROM expenses.entry_lines
 WHERE household_id=$1 AND id=$2 AND entry_id=$3 FOR UPDATE NOWAIT`, id.HouseholdID, resource, access.entryID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return recheckFinancialTarget(ctx, tx, id, entity, resource, financialEdit, access)
}

func financialWriteScope(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, resource string, data Record, creating bool) error {
	if !creating {
		// A supplied new parent never substitutes for the stored line's parent.
		return lockFinancialResource(ctx, tx, id, entity, resource)
	}
	if entity == "entry_lines" {
		parent := String(data["entry_id"])
		if !value.ValidUUID(parent) {
			return Invalid{"Gültige übergeordnete Buchung erforderlich"}
		}
		return lockFinancialResource(ctx, tx, id, "entries", parent)
	}
	_, err := readFinancialScope(ctx, tx, id)
	return err
}

func financialActionReplay(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, resource, action string, result Record) error {
	if financialEntity(mid, entity) {
		targets := []string{resource}
		if target := String(result["id"]); !strings.EqualFold(target, resource) {
			if !value.ValidUUID(target) {
				return ErrNotFound
			}
			targets = append(targets, target)
		}
		slices.Sort(targets)
		for _, target := range targets {
			if err := lockFinancialReplay(ctx, tx, id, entity, target, financialEdit); err != nil {
				return err
			}
		}
	}
	if mid == "fuel" && entity == "fillups" && action == "expense" {
		if err := lockFinancialReplay(ctx, tx, id, "entries", String(result["expense_id"]), financialView); err != nil {
			return err
		}
		var found string
		err := tx.QueryRow(ctx, "SELECT id FROM fuel.fillups WHERE household_id=$1 AND id=$2 FOR KEY SHARE NOWAIT", id.HouseholdID, resource).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func existingFuelExpense(ctx context.Context, tx pgx.Tx, id auth.Identity, fillup string) (string, bool, error) {
	// Inspect the complete typed relationship, including malformed/ambiguous
	// existing identities. A denied old link never falls through to creation.
	rows, err := tx.Query(ctx, `SELECT source_type,target_module,target_type,target_id
 FROM core.resource_links WHERE household_id=$1 AND source_module='fuel' AND source_id=$2
 AND relation='fuel_expense' ORDER BY id LIMIT 2`, id.HouseholdID, fillup)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var sourceType, targetModule, targetType, target string
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&sourceType, &targetModule, &targetType, &target); err != nil {
			return "", false, err
		}
	}
	if err = rows.Err(); err != nil {
		return "", false, err
	}
	rows.Close()
	if count == 0 {
		return "", false, nil
	}
	if count != 1 || sourceType != "fillups" || targetModule != "expenses" || targetType != "entries" {
		return "", false, ErrNotFound
	}
	if err = lockFinancialReplay(ctx, tx, id, "entries", target, financialView); err != nil {
		return "", false, err
	}
	return target, true, nil
}
