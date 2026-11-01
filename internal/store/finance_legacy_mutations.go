package store

import (
	"context"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func financeMutationRefs(kind string, data Record) []FinanceResourceRef {
	refs := make([]FinanceResourceRef, 0, 8)
	for key, target := range map[string]string{
		"entry_id": "entries", "account_id": "accounts", "reverses_entry_id": "entries",
		"replaces_entry_id": "entries", "original_entry_id": "entries", "event_id": "cash_events",
		"reverses_event_id": "cash_events", "movement_id": "account_movements", "goal_id": "savings_goals",
		"template_id": "recurring_templates", "reconciliation_id": "reconciliations", "review_id": "receipt_reviews",
		"capture_job_id": "capture", "attachment_id": "attachment",
	} {
		if rid := String(data[key]); rid != "" && (target != kind || rid != String(data["id"])) {
			refs = append(refs, FinanceResourceRef{Kind: target, ID: rid})
		}
	}
	return refs
}

func prepareLegacyFinanceMutation(ctx context.Context, tx pgx.Tx, id auth.Identity, kind, rid string, data Record) (string, financialScope, error) {
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return "", financialScope{}, err
	}
	origin, refs := financeOriginFor(ctx, financeMutationRefs(kind, data))
	operation, err := prepareFinanceOrigin(ctx, tx, id, scope, kind, rid, origin, refs)
	return operation, scope, err
}
