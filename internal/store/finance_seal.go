package store

import (
	"context"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"
)

// FinanceSealOperation permanently prevents an unknown old key from creating a
// late effect. It makes no statement about prior acceptance or private content.
// Current identity is required; the original generation is never rebased.
func (s *Store) FinanceSealOperation(ctx context.Context, id auth.Identity, key, requestID string, generation int64) error {
	if !value.ValidUUID(key) || !value.ValidUUID(requestID) || generation <= 0 {
		return Invalid{"Ursprünglichen Vorgangsschlüssel und Generation prüfen"}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	_, err = tx.Exec(ctx, `SELECT core.finance_seal_key($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, generation, key)
	if err != nil {
		return financeAuthorityError(err)
	}
	return tx.Commit(ctx)
}
