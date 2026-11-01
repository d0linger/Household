package store

import (
	"context"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financialScope struct {
	householdID  string
	userID       string
	membershipID string
	sessionID    string
	authVersion  int64
	stateVersion int64
	role         string
	generation   int64
	timezone     string
	currency     string
	readAt       time.Time
}

// Construct scope in the same snapshot used for content, never from cached state.
func readFinancialScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (financialScope, error) {
	if id.SessionID == "" {
		return financeCurrentAutomationScope(ctx, tx, id)
	}
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return financialScope{}, ErrForbidden
	}
	out := financialScope{householdID: id.HouseholdID, role: id.Role, userID: id.UserID,
		membershipID: id.MembershipID, sessionID: id.SessionID, authVersion: id.AuthVersion}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT h.timezone,h.currency_code,transaction_timestamp(),
 coalesce(ms.state='enabled',false),coalesce(ms.data_generation,0),coalesce(ms.state_version,0)
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.user_id=$2 AND m.id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND u.auth_version=$5 AND NOT u.must_change_password
 JOIN core.sessions s ON s.user_id=u.id AND s.id=$6 AND s.revoked_at IS NULL AND s.auth_version=u.auth_version
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 LEFT JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='expenses'
 WHERE h.id=$1`, id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.AuthVersion, id.SessionID).Scan(
		&out.timezone, &out.currency, &out.readAt, &enabled, &out.generation, &out.stateVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return financialScope{}, ErrForbidden
	}
	if err != nil {
		return financialScope{}, err
	}
	if !enabled || out.generation <= 0 {
		return financialScope{}, ErrNotFound
	}
	return out, nil
}

func financialEntity(mid, entity string) bool {
	return mid == "expenses" && (entity == "entries" || entity == "entry_lines")
}

// The caller supplies only compiled entity kinds and a fixed t alias. Every
// actual child must inherit the live policy; partial financial facts are denied.
type financialIntent uint8

const (
	financialView financialIntent = iota
	financialEdit
)

func financialPredicate(scope financialScope, entity string, intent financialIntent, first int) (string, []any, error) {
	if intent != financialView && intent != financialEdit || entity != "entries" && entity != "entry_lines" {
		return "", nil, ErrForbidden
	}
	wanted := "view"
	if intent == financialEdit {
		wanted = "edit"
	}
	return financeResourcePredicate(scope, entity, wanted, "t", first)
}

func readFinancialAccessVersion(ctx context.Context, tx pgx.Tx, scope financialScope, entry string) (int64, error) {
	predicate, args, err := financialPredicate(scope, "entries", financialView, 4)
	if err != nil {
		return 0, err
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT p.version FROM expenses.entries t JOIN core.financial_policies p
 ON p.household_id=t.household_id AND p.module_id='expenses' AND p.module_generation=$3
 AND p.resource_type='entries' AND p.resource_id=t.id
 WHERE t.household_id=$1 AND t.id=$2 AND `+predicate,
		append([]any{scope.householdID, entry, scope.generation}, args...)...).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return version, err
}

func (s *Store) listFinancial(ctx context.Context, id auth.Identity, e module.Entity, query string, limit, offset int) ([]Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	predicate, args, err := financialPredicate(scope, e.ID, financialView, 6)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{"expenses", e.ID}.Sanitize()+
		" t WHERE t.household_id=$1 AND ($2='' OR t."+pgx.Identifier{e.Primary}.Sanitize()+
		"::text ILIKE $3) AND "+predicate+" ORDER BY t.created_at DESC,t.id DESC LIMIT $4 OFFSET $5",
		append([]any{scope.householdID, query, "%" + query + "%", limit, offset}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		record, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		list = append(list, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) getFinancial(ctx context.Context, id auth.Identity, e module.Entity, rid string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	predicate, args, err := financialPredicate(scope, e.ID, financialView, 3)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{"expenses", e.ID}.Sanitize()+
		" t WHERE t.household_id=$1 AND t.id=$2 AND "+predicate,
		append([]any{scope.householdID, rid}, args...)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return record, nil
}
