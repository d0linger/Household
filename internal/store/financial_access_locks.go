package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financialAccess struct {
	policyID string
	entryID  string
	version  int64
}

// BeginMutation holds current household/session/module locks before this call.
// SHARE protects access-changing columns; KEY SHARE would permit their update.
func lockFinancialPolicy(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, resource string, intent financialIntent) (financialAccess, error) {
	if !value.ValidUUID(resource) {
		return financialAccess{}, ErrNotFound
	}
	if intent != financialView && intent != financialEdit {
		return financialAccess{}, ErrForbidden
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return financialAccess{}, err
	}
	wanted := "view"
	if intent == financialEdit {
		wanted = "edit"
	}
	if _, err = financeResourceAccess(ctx, tx, id, scope, entity, resource, wanted); err != nil {
		return financialAccess{}, err
	}
	parent := "t.id"
	if entity == "entry_lines" {
		parent = "t.entry_id"
	}
	var out financialAccess
	if entity != "entries" && entity != "entry_lines" {
		return financialAccess{}, ErrNotFound
	}
	// security: entity and parent are validated compiled choices above. The
	// complete policy/source union has already been locked by the authority gate.
	err = tx.QueryRow(ctx, "SELECT p.id,p.resource_id,p.version FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
		" t JOIN core.financial_policies p ON p.household_id=t.household_id AND p.module_id='expenses'"+
		" AND p.module_generation=$3 AND p.resource_type='entries' AND p.resource_id="+parent+
		" WHERE t.household_id=$1 AND t.id=$2 AND p.retired_at IS NULL FOR SHARE OF p NOWAIT",
		scope.householdID, resource, scope.generation).Scan(&out.policyID, &out.entryID, &out.version)
	if errors.Is(err, pgx.ErrNoRows) {
		return financialAccess{}, ErrNotFound
	}
	if err != nil {
		return financialAccess{}, err
	}
	return out, nil
}
