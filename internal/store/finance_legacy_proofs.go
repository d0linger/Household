package store

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func financeLegacyReplay(ctx context.Context, tx pgx.Tx, id auth.Identity, key, operation, kind, expectedID string, hash []byte) (Record, bool, error) {
	if kind != "entries" && kind != "entry_lines" {
		return nil, false, ErrNotFound
	}
	key = strings.ToLower(key)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('finance-actor-key/'||$1||'/'||$2||'/'||$3,0))`, id.HouseholdID, id.UserID, key); err != nil {
		return nil, false, err
	}
	var priorOperation, targetKind, target string
	var priorHash []byte
	var generation, version int64
	err := tx.QueryRow(ctx, `SELECT operation,request_hash,resource_type,resource_id,expenses_generation,result_version
 FROM core.finance_operations WHERE household_id=$1 AND created_by=$2 AND request_key=$3`, id.HouseholdID, id.UserID, key).
		Scan(&priorOperation, &priorHash, &targetKind, &target, &generation, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		var occupied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.finance_key_tombstones WHERE household_id=$1 AND created_by=$2 AND request_key=$3)
 OR EXISTS(SELECT 1 FROM core.expense_edit_operations WHERE household_id=$1 AND created_by=$2 AND request_key=$3)`, id.HouseholdID, id.UserID, key).Scan(&occupied); err != nil {
			return nil, false, err
		}
		if occupied {
			return nil, false, ErrConflict
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if priorOperation != operation || targetKind != kind || !bytes.Equal(priorHash, hash) || expectedID != "" && !strings.EqualFold(expectedID, target) {
		return nil, true, ErrConflict
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, true, err
	}
	if scope.generation != generation {
		return nil, true, ErrNotFound
	}
	if err = lockFinancialReplay(ctx, tx, id, targetKind, target, financialEdit); err != nil {
		return nil, true, err
	}
	var currentVersion int64
	var state string
	query := "SELECT version,'' FROM " + pgx.Identifier{"expenses", kind}.Sanitize() + " WHERE household_id=$1 AND id=$2"
	if kind == "entries" {
		query = "SELECT version,state FROM expenses.entries WHERE household_id=$1 AND id=$2"
	}
	if err = tx.QueryRow(ctx, query, id.HouseholdID, target).Scan(&currentVersion, &state); err != nil {
		return nil, true, financeAuthorityError(err)
	}
	return Record{"id": target, "version": version, "accepted_version": version, "accepted": true, "replayed": true,
		"current_exists": true, "current_version": currentVersion, "current_state": state}, true, nil
}

func financeStoreLegacyProof(ctx context.Context, tx pgx.Tx, id auth.Identity, key, operation, kind, target string, hash []byte, generation, version int64) error {
	if kind != "entries" && kind != "entry_lines" || !value.ValidUUID(target) || version <= 0 {
		return ErrNotFound
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.finance_operations(id,household_id,created_by,membership_id,request_key,purpose,operation,
 request_hash,expenses_generation,resource_type,resource_id,result_version) VALUES($1,$2,$3,$4,$5,'expense-finance-v1',$6,$7,$8,$9,$10,$11)`,
		value.UUID(), id.HouseholdID, id.UserID, id.MembershipID, key, operation, hash, generation, kind, target, version)
	return financeAuthorityError(err)
}
