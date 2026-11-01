package store

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func financeIngestFingerprint(kind, fingerprint string) ([]byte, error) {
	if !slices.Contains([]string{"bank_transaction", "bank_file_row", "recurrence"}, kind) || len(fingerprint) != 64 {
		return nil, ErrNotFound
	}
	raw, err := hex.DecodeString(fingerprint)
	if err != nil || len(raw) != 32 || strings.ToLower(fingerprint) != fingerprint {
		return nil, ErrNotFound
	}
	return raw, nil
}

func financeIngestStatus(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope,
	sourceKind, fingerprint string,
) (bool, error) {
	raw, err := financeIngestFingerprint(sourceKind, fingerprint)
	if err != nil {
		return false, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT core.finance_ingest_status($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, sourceKind, raw).Scan(&status)
	if err != nil {
		return false, financeAuthorityError(err)
	}
	switch status {
	case "available":
		return false, nil
	case "duplicate":
		return true, nil
	case "unavailable":
		// A hidden prior result must not abort the caller's preview transaction.
		return false, ErrNotFound
	default:
		return false, ErrForbidden
	}
}

func financeClaimIngest(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope,
	sourceKind, fingerprint, targetKind, targetID string,
) error {
	raw, err := financeIngestFingerprint(sourceKind, fingerprint)
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"entries", "cash_events"}, targetKind) || !value.ValidUUID(targetID) {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `SELECT core.finance_ingest_claim($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10)`,
		id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation,
		sourceKind, raw, targetKind, strings.ToLower(targetID))
	return financeAuthorityError(err)
}
