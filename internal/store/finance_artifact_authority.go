package store

import (
	"context"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// PrepareFinanceArtifact reserves a sensitive Core artifact before its first
// audit. An empty operation identifies a proved nonfinancial source only.
func (s *Store) PrepareFinanceArtifact(ctx context.Context, tx pgx.Tx, id auth.Identity,
	kind, rid string, sources []FinanceResourceRef,
) (string, error) {
	var sensitive bool
	for _, ref := range sources {
		if financeDomainKind(ref.Kind) {
			sensitive = true
			break
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.finance_origin_resources
 WHERE household_id=$1 AND resource_type=$2 AND resource_id=$3 AND row_number=0)`, id.HouseholdID, ref.Kind, ref.ID).Scan(&exists); err != nil {
			return "", err
		}
		sensitive = sensitive || exists
	}
	if !sensitive {
		return "", nil
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return "", err
	}
	return prepareFinanceArtifact(ctx, tx, id, scope, kind, rid, sources)
}

func (s *Store) CompleteFinanceArtifact(ctx context.Context, tx pgx.Tx, id auth.Identity, operation string) error {
	if operation == "" {
		return nil
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return err
	}
	return completeFinanceOrigin(ctx, tx, id, scope, operation)
}

func (s *Store) PrepareFinanceImport(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, batch string, refs []FinanceResourceRef) (string, error) {
	if mid != "expenses" {
		return "", nil
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return "", err
	}
	return prepareFinanceArtifact(ctx, tx, id, scope, "import_batch", batch, refs)
}

// AuthorizeFinanceArtifact checks the retained original scope as well as the
// current parent. Call before exposing any filename, metadata, bytes or OCR.
func (s *Store) AuthorizeFinanceArtifact(ctx context.Context, tx pgx.Tx, id auth.Identity, kind, rid, intent string) error {
	if !value.ValidUUID(rid) {
		return ErrNotFound
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT core.finance_artifact_visible($1,$2,$3,$4,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, kind, rid, intent).Scan(&allowed)
	if err != nil {
		return financeAuthorityError(err)
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LockFinanceArtifact(ctx context.Context, tx pgx.Tx, id auth.Identity, kind, rid, intent string) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT core.finance_lock_artifact($1,$2,$3,$4,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, kind, rid, intent).Scan(&allowed)
	if err != nil {
		return financeAuthorityError(err)
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func CheckFinanceArtifactJob(ctx context.Context, tx pgx.Tx, job, lease string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT core.finance_artifact_job_allowed($1,$2)`, job, lease).Scan(&allowed); err != nil {
		return financeAuthorityError(err)
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func PrepareFinanceArtifactJob(ctx context.Context, tx pgx.Tx, job, lease, kind, target string) (*string, error) {
	var operation *string
	err := tx.QueryRow(ctx, `SELECT core.finance_prepare_artifact_job($1,$2,$3,$4)`, job, lease, kind, target).Scan(&operation)
	return operation, financeAuthorityError(err)
}

func CompleteFinanceArtifactJob(ctx context.Context, tx pgx.Tx, operation *string) error {
	_, err := tx.Exec(ctx, `SELECT core.finance_finish_artifact_job($1)`, operation)
	return financeAuthorityError(err)
}
