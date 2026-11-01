package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type FinanceReferenceQuery struct {
	Kind, Query, Selected, Cursor string
}

type financeReferenceCursor struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// FinanceReferences filters in PostgreSQL before the page limit. A revoked
// selected reference returns no label, not a broadened search or hidden count.
func (s *Store) FinanceReferences(ctx context.Context, id auth.Identity, query FinanceReferenceQuery) (Record, error) {
	if !financeText(query.Query, 0, 100) || len(query.Cursor) > 1024 || query.Selected != "" && !value.ValidUUID(query.Selected) {
		return nil, Invalid{"Referenzsuche erneut prüfen"}
	}
	var cursor financeReferenceCursor
	if query.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || decodeFinancePayload(raw, &cursor) != nil || !value.ValidUUID(cursor.ID) || len(cursor.Name) > 400 {
			return nil, Invalid{"Referenzseite erneut laden"}
		}
	}
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
	from, label, active := "", "", ""
	currency, exponent := "''::text", "0::integer"
	predicate, args := "true", []any{id.HouseholdID, financeLike(query.Query), cursor.Name, cursor.ID, query.Selected}
	switch query.Kind {
	case "accounts":
		from, label, active = "expenses.accounts t", "t.name", "t.archived_at IS NULL"
		currency, exponent = "t.currency_code", "t.currency_exponent"
		var policyArgs []any
		predicate, policyArgs, err = financeResourcePredicate(scope, "accounts", "view", "t", 6)
		if err != nil {
			return nil, err
		}
		args = append(args, policyArgs...)
	case "categories":
		from, label, active = "expenses.categories t", "t.name", "t.archived_at IS NULL"
	case "members":
		from, label, active = "core.memberships t JOIN core.users u ON u.id=t.user_id", "u.display_name", "t.inactive_at IS NULL AND u.disabled_at IS NULL"
	default:
		return nil, ErrNotFound
	}
	// security: all expressions and relation names above are compiled constants;
	// cursor/search/selection and authority facts are only parameter values.
	statement := fmt.Sprintf(`SELECT t.id,%s,t.version,%s,%s,(%s),lower(%s) COLLATE "C"
 FROM %s WHERE t.household_id=$1 AND ($5='' OR t.id=NULLIF($5,'')::uuid)
 AND ($2='%%' OR %s ILIKE $2 ESCAPE '\')
 AND ($4='' OR (lower(%s) COLLATE "C",t.id)>($3 COLLATE "C",NULLIF($4,'')::uuid))
 AND (%s) ORDER BY lower(%s) COLLATE "C",t.id LIMIT 51`, label, currency, exponent, active, label, from, label, label, predicate, label)
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	items := []Record{}
	keys := []financeReferenceCursor{}
	for rows.Next() {
		var rid, name, code, sortName string
		var version int64
		var exp int
		var usable bool
		if err = rows.Scan(&rid, &name, &version, &code, &exp, &usable, &sortName); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, Record{"id": rid, "label": name, "version": version, "currency": code, "exponent": exp, "active": usable})
		keys = append(keys, financeReferenceCursor{Name: sortName, ID: rid})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		raw, encodeErr := json.Marshal(keys[49])
		if encodeErr != nil {
			return nil, encodeErr
		}
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	if query.Kind == "accounts" {
		for _, item := range items {
			stamp, stampErr := financeResourceAccess(ctx, tx, id, scope, "accounts", String(item["id"]), "view")
			if stampErr != nil {
				return nil, stampErr
			}
			item["access_version"] = stamp
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return Record{"rows": items, "next_cursor": next, "selected_available": query.Selected == "" || len(items) > 0}, nil
}

func financeLike(query string) string {
	escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query)
	return "%" + escaped + "%"
}

func financeNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
