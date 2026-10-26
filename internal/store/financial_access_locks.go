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
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return financialAccess{}, err
	}
	predicate, args, err := financialPredicate(scope, entity, intent, 3)
	if err != nil {
		return financialAccess{}, err
	}
	parent := "t.id"
	if entity == "entry_lines" {
		parent = "t.entry_id"
	}
	var out financialAccess
	// security: entity and parent are validated compiled choices above.
	err = tx.QueryRow(ctx, "SELECT p.id,p.resource_id,p.version FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
		" t JOIN core.financial_policies p ON p.household_id=t.household_id AND p.module_id='expenses'"+
		" AND p.module_generation=$4 AND p.resource_type='entries' AND p.resource_id="+parent+
		" WHERE t.household_id=$1 AND t.id=$2 AND p.retired_at IS NULL AND p.access_mode='household' AND "+predicate+" FOR SHARE OF p NOWAIT",
		append([]any{scope.householdID, resource}, args...)...).Scan(&out.policyID, &out.entryID, &out.version)
	if errors.Is(err, pgx.ErrNoRows) {
		return financialAccess{}, ErrNotFound
	}
	if err != nil {
		return financialAccess{}, err
	}
	return out, nil
}
