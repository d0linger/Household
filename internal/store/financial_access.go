package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financialScope struct {
	householdID string
	role        string
	generation  int64
	timezone    string
	currency    string
	readAt      time.Time
}

// Construct scope in the same snapshot used for content, never from cached state.
func readFinancialScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (financialScope, error) {
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return financialScope{}, ErrForbidden
	}
	out := financialScope{householdID: id.HouseholdID, role: id.Role}
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT h.timezone,h.currency_code,transaction_timestamp(),
 coalesce(ms.state='enabled',false),coalesce(ms.data_generation,0)
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.user_id=$2 AND m.id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND u.auth_version=$5 AND NOT u.must_change_password
 JOIN core.sessions s ON s.user_id=u.id AND s.id=$6 AND s.revoked_at IS NULL AND s.auth_version=u.auth_version
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 LEFT JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='expenses'
 WHERE h.id=$1`, id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.AuthVersion, id.SessionID).Scan(
		&out.timezone, &out.currency, &out.readAt, &enabled, &out.generation)
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
	if intent != financialView && intent != financialEdit || intent == financialEdit && scope.role != "admin" && scope.role != "editor" {
		return "", nil, ErrForbidden
	}
	parent := "t.id"
	child := ""
	switch entity {
	case "entries":
	case "entry_lines":
		parent = "t.entry_id"
		child = ` AND EXISTS(SELECT 1 FROM core.financial_subjects own
 WHERE own.household_id=p.household_id AND own.module_id=p.module_id AND own.module_generation=p.module_generation
 AND own.resource_type='entry_lines' AND own.resource_id=t.id AND own.policy_id=p.id AND own.retired_at IS NULL)`
	default:
		return "", nil, ErrNotFound
	}
	// security: expressions are the two constants above; only placeholder indexes
	// are formatted. Household and generation are parameterized snapshot facts.
	predicate := fmt.Sprintf(`EXISTS(SELECT 1 FROM core.financial_policies p
 JOIN core.financial_subjects parent_binding ON parent_binding.household_id=p.household_id
 AND parent_binding.module_id=p.module_id AND parent_binding.module_generation=p.module_generation
 AND parent_binding.resource_type='entries' AND parent_binding.resource_id=p.resource_id
 AND parent_binding.policy_id=p.id AND parent_binding.retired_at IS NULL
 WHERE p.household_id=$%d AND t.household_id=p.household_id AND p.module_id='expenses'
 AND p.module_generation=$%d AND p.resource_type='entries' AND p.resource_id=%s
 AND p.access_mode='household' AND p.retired_at IS NULL
 AND EXISTS(SELECT 1 FROM expenses.entries parent WHERE parent.household_id=p.household_id AND parent.id=p.resource_id)
 AND NOT EXISTS(SELECT 1 FROM expenses.entry_lines actual
 WHERE actual.household_id=p.household_id AND actual.entry_id=p.resource_id
 AND NOT EXISTS(SELECT 1 FROM core.financial_subjects inherited
 WHERE inherited.household_id=p.household_id AND inherited.module_id=p.module_id
 AND inherited.module_generation=p.module_generation AND inherited.resource_type='entry_lines'
 AND inherited.resource_id=actual.id AND inherited.policy_id=p.id AND inherited.retired_at IS NULL))%s)`,
		first, first+1, parent, child)
	return predicate, []any{scope.householdID, scope.generation}, nil
}

func readFinancialAccessVersion(ctx context.Context, tx pgx.Tx, scope financialScope, entry string) (int64, error) {
	predicate, args, err := financialPredicate(scope, "entries", financialView, 3)
	if err != nil {
		return 0, err
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT p.version FROM expenses.entries t JOIN core.financial_policies p
 ON p.household_id=t.household_id AND p.module_id='expenses' AND p.module_generation=$4
 AND p.resource_type='entries' AND p.resource_id=t.id
 WHERE t.household_id=$1 AND t.id=$2 AND `+predicate,
		append([]any{scope.householdID, entry}, args...)...).Scan(&version)
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
