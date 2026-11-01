package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// FinanceResourceRef identifies stored facts, never caller-authoritative policies.
type FinanceResourceRef struct {
	Kind      string `json:"kind"`
	ID        string `json:"id,omitempty"`
	RowNumber int64  `json:"row_number,omitempty"`
	AuditID   int64  `json:"audit_id,omitempty"`
}

func financeAuthorityError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if problem, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch problem.Code {
		case "P0002":
			return ErrNotFound
		case "42501":
			return ErrForbidden
		case "40001", "40P01", "55P03", "23505":
			return ErrConflict
		case "22023":
			return Invalid{"Finanzquelle und aktuellen Zugriffsstand erneut prüfen"}
		}
	}
	return err
}

func financeReferenceKind(kind string) bool {
	return slices.Contains([]string{
		"entries", "entry_lines", "accounts", "entry_finance", "entry_original_lines", "cash_events",
		"account_movements", "entry_relations", "entry_split_sets", "entry_person_shares", "settlement_details",
		"saved_filters", "category_budgets", "savings_goals", "goal_contributions", "recurring_templates",
		"recurring_occurrences", "categorization_rules", "bank_profiles", "reconciliations", "reconciliation_items",
		"receipt_reviews", "receipt_review_lines", "attachment", "attachment_derivative", "capture",
		"import_batch", "import_row", "audit", "shared_fuel_fillups", "shared_fuel_vehicle_costs", "shared_shopping_checkout",
	}, kind)
}

func canonicalFinanceRefs(input []FinanceResourceRef) ([]FinanceResourceRef, error) {
	if len(input) > 1024 {
		return nil, Invalid{"Vollständige Finanzquellen überschreiten die sichere Grenze"}
	}
	refs := make([]FinanceResourceRef, 0, len(input))
	seen := make(map[string]bool, len(input))
	for _, ref := range input {
		if !financeReferenceKind(ref.Kind) || ref.RowNumber < 0 || ref.AuditID < 0 {
			return nil, ErrNotFound
		}
		if ref.Kind == "audit" {
			if ref.AuditID <= 0 || ref.ID != "" || ref.RowNumber != 0 {
				return nil, ErrNotFound
			}
		} else {
			if !value.ValidUUID(ref.ID) || ref.AuditID != 0 || (ref.Kind == "import_row") != (ref.RowNumber > 0) {
				return nil, ErrNotFound
			}
			ref.ID = strings.ToLower(ref.ID)
		}
		key := ref.Kind + "/" + ref.ID + "/" + strconv.FormatInt(ref.RowNumber, 10) + "/" + strconv.FormatInt(ref.AuditID, 10)
		if !seen[key] {
			seen[key] = true
			refs = append(refs, ref)
		}
	}
	encoded := func(ref FinanceResourceRef) string {
		return ref.Kind + "/" + ref.ID + "/" + strconv.FormatInt(ref.RowNumber, 10) + "/" + strconv.FormatInt(ref.AuditID, 10)
	}
	slices.SortFunc(refs, func(a, b FinanceResourceRef) int { return strings.Compare(encoded(a), encoded(b)) })
	return refs, nil
}

func financeProspectiveAccess(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope,
	refs []FinanceResourceRef, intent string,
) (string, error) {
	if scope.householdID != id.HouseholdID || scope.generation <= 0 || !slices.Contains([]string{"view", "edit", "manage", "preference"}, intent) {
		return "", ErrForbidden
	}
	canonical, err := canonicalFinanceRefs(refs)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	var stamp string
	err = tx.QueryRow(ctx, `SELECT core.finance_authority($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7::jsonb,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, raw, intent).Scan(&stamp)
	if err != nil {
		return "", financeAuthorityError(err)
	}
	if len(stamp) != 64 {
		return "", ErrForbidden
	}
	return stamp, nil
}

func financeResourceAccess(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid, intent string) (string, error) {
	return financeProspectiveAccess(ctx, tx, id, scope, []FinanceResourceRef{{Kind: kind, ID: rid}}, intent)
}

func prepareFinanceOrigin(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope,
	kind, rid, origin string, sources []FinanceResourceRef,
) (string, error) {
	if !financeReferenceKind(kind) || !value.ValidUUID(rid) || scope.householdID != id.HouseholdID || scope.generation <= 0 ||
		!slices.Contains([]string{"manual", "bank", "recurrence", "correction", "refund", "reversal", "fuel", "capture", "import", "preference", "access"}, origin) {
		return "", ErrForbidden
	}
	canonical, err := canonicalFinanceRefs(sources)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	var operation string
	initialSpec, err := canonicalFinanceInitialAccess(financeInitialAccessFor(ctx, kind, rid), id.MembershipID)
	if err != nil {
		return "", err
	}
	initial, err := json.Marshal(initialSpec)
	if err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `SELECT core.finance_origin_begin($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation,
		kind, strings.ToLower(rid), origin, raw, initial).Scan(&operation)
	if err != nil {
		return "", financeAuthorityError(err)
	}
	if !value.ValidUUID(operation) {
		return "", ErrForbidden
	}
	return operation, nil
}

func completeFinanceOrigin(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, operationID string) error {
	if !value.ValidUUID(operationID) || scope.householdID != id.HouseholdID {
		return ErrForbidden
	}
	_, err := tx.Exec(ctx, `SELECT core.finance_origin_finish($1,$2,$3,$4,nullif($5,'')::uuid,$6,$7)`,
		operationID, id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation)
	return financeAuthorityError(err)
}

func financeAccessProjection(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string) (Record, error) {
	if _, err := financeResourceAccess(ctx, tx, id, scope, kind, rid, "view"); err != nil {
		return nil, err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT core.finance_access_projection($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,false)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, kind, strings.ToLower(rid)).Scan(&raw)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return decode(raw)
}

func financeOriginProjection(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string) (Record, error) {
	if _, err := financeResourceAccess(ctx, tx, id, scope, kind, rid, "view"); err != nil {
		return nil, err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT core.finance_origin_projection($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, kind, strings.ToLower(rid)).Scan(&raw)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	return decode(raw)
}
