package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// Search authenticates independently of Expenses activation. Optional financial
// facts require their complete live binding before ranking or the result limit.
func (s *Store) Search(ctx context.Context, id auth.Identity, query string) ([]Record, error) {
	query = strings.TrimSpace(query)
	if len(query) > 100 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		return nil, Invalid{Message: "Suchbegriff ist ungültig oder zu lang"}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, enabled, err := readSurfaceScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	list := []Record{}
	if query == "" {
		return list, tx.Commit(ctx)
	}
	entryPredicate, policyArgs, err := financialPredicate(scope, "entries", financialView, 5)
	if err != nil {
		return nil, err
	}
	linePredicate, _, err := financialPredicate(scope, "entry_lines", financialView, 5)
	if err != nil {
		return nil, err
	}
	modules := make([]string, 0, len(s.Modules))
	for _, m := range s.Modules {
		modules = append(modules, m.ID)
	}
	rows, err := tx.Query(ctx, `SELECT d.module_id,d.resource_type,d.resource_id,d.title
 FROM core.search_documents d JOIN core.module_states m ON m.household_id=d.household_id AND m.module_id=d.module_id
 WHERE d.household_id=$1 AND m.state='enabled' AND m.data_generation=d.module_generation AND d.module_id=ANY($4::text[])
 AND (d.module_id<>'expenses' OR d.resource_type='categories' OR ($7 AND (
 d.resource_type='entries' AND EXISTS(SELECT 1 FROM expenses.entries t WHERE t.household_id=d.household_id AND t.id=d.resource_id AND `+entryPredicate+`)
 OR d.resource_type='entry_lines' AND EXISTS(SELECT 1 FROM expenses.entry_lines t WHERE t.household_id=d.household_id AND t.id=d.resource_id AND `+linePredicate+`))))
 AND (d.search_vector @@ websearch_to_tsquery('german',$2) OR d.title ILIKE $3 OR d.title OPERATOR(core.%) $2)
 ORDER BY (ts_rank(d.search_vector,websearch_to_tsquery('german',$2))+core.similarity(d.title,$2)) DESC,d.title,d.resource_id LIMIT 50`,
		append(append([]any{scope.householdID, query, "%" + query + "%", modules}, policyArgs...), enabled)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, entity, rid, title string
		if err = rows.Scan(&mid, &entity, &rid, &title); err != nil {
			return nil, err
		}
		list = append(list, Record{"module": mid, "entity": entity, "id": rid, "title": title,
			"url": "/m/" + mid + "/" + entity + "/" + rid})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return list, nil
}

// readSurfaceScope has the financial reader's current authentication checks but
// deliberately does not make an unrelated module depend on Expenses activation.
func readSurfaceScope(ctx context.Context, tx pgx.Tx, id auth.Identity) (financialScope, bool, error) {
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) ||
		!value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return financialScope{}, false, ErrForbidden
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
		return financialScope{}, false, ErrForbidden
	}
	if err != nil {
		return financialScope{}, false, err
	}
	return out, enabled && out.generation > 0, nil
}

func (s *Store) financialRead(ctx context.Context, id auth.Identity, read func(context.Context, pgx.Tx, financialScope) error) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = read(ctx, tx, scope); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) financialDescriptor(entity string) (module.Entity, error) {
	m, ok := s.Module("expenses")
	e, exists := m.Entity(entity)
	if !ok || !exists || !financialEntity("expenses", entity) {
		return module.Entity{}, ErrNotFound
	}
	return e, nil
}

type FinancialReferenceQuery struct {
	Query      string
	SelectedID string
	AfterID    string
	After      time.Time
	Generation int64
}

type FinancialReference struct {
	ID      string
	Label   string
	Created time.Time
}

// FinancialReferenceOptions includes one lookahead row. Both a selected-ID
// hydration and every cursor page use the complete predicate before LIMIT.
func (s *Store) FinancialReferenceOptions(ctx context.Context, id auth.Identity, entity string, in FinancialReferenceQuery) ([]FinancialReference, error) {
	e, err := s.financialDescriptor(entity)
	if err != nil {
		return nil, err
	}
	if len(in.Query) > 300 || !utf8.ValidString(in.Query) || strings.ContainsRune(in.Query, '\x00') ||
		in.SelectedID != "" && !value.ValidUUID(in.SelectedID) || in.AfterID != "" && (!value.ValidUUID(in.AfterID) || in.After.IsZero()) {
		return nil, Invalid{Message: "Ungültige Auswahlseite"}
	}
	out := []FinancialReference{}
	err = s.financialRead(ctx, id, func(ctx context.Context, tx pgx.Tx, scope financialScope) error {
		if in.Generation != scope.generation {
			return ErrConflict
		}
		predicate, args, err := financialPredicate(scope, entity, financialView, 6)
		if err != nil {
			return err
		}
		var after, afterID, selected any
		if in.AfterID != "" {
			after, afterID = in.After, in.AfterID
		}
		if in.SelectedID != "" {
			selected = in.SelectedID
		}
		// security: the table and label are resolved compiled descriptors.
		label := "left(coalesce(t." + pgx.Identifier{e.Primary}.Sanitize() + "::text,''),300)"
		rows, err := tx.Query(ctx, "SELECT t.id,"+label+",t.created_at FROM "+
			pgx.Identifier{"expenses", entity}.Sanitize()+` t WHERE t.household_id=$1 AND ($5::uuid IS NULL OR t.id=$5)
 AND ($2='' OR strpos(lower(`+label+`),lower($2))>0)
 AND ($3::timestamptz IS NULL OR (t.created_at,t.id)<($3,$4::uuid)) AND `+predicate+
			" ORDER BY t.created_at DESC,t.id DESC LIMIT 51", append([]any{scope.householdID, in.Query, after, afterID, selected}, args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var option FinancialReference
			if err = rows.Scan(&option.ID, &option.Label, &option.Created); err != nil {
				return err
			}
			out = append(out, option)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FinancialReferences retains an authorized selected value beyond the first
// hundred records without opening a second authorization/content snapshot.
func (s *Store) FinancialReferences(ctx context.Context, id auth.Identity, entity, selected string) ([]Record, error) {
	_, err := s.financialDescriptor(entity)
	if err != nil {
		return nil, err
	}
	if selected != "" && !value.ValidUUID(selected) {
		return nil, ErrNotFound
	}
	out := []Record{}
	err = s.financialRead(ctx, id, func(ctx context.Context, tx pgx.Tx, scope financialScope) error {
		predicate, args, err := financialPredicate(scope, entity, financialView, 2)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT "+financialSurfaceJSON(entity)+" FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
			" t WHERE t.household_id=$1 AND "+predicate+
			" ORDER BY t.created_at DESC,t.id DESC LIMIT 100", append([]any{scope.householdID}, args...)...)
		if err != nil {
			return err
		}
		remainingBytes := 16 << 20
		out, err = scanSurfaceRecordsBudget(rows, &remainingBytes)
		if err != nil || selected == "" {
			return err
		}
		for _, record := range out {
			if String(record["id"]) == selected {
				return nil
			}
		}
		predicate, args, err = financialPredicate(scope, entity, financialView, 3)
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, "SELECT "+financialSurfaceJSON(entity)+" FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
			" t WHERE t.household_id=$1 AND t.id=$2 AND "+predicate, append([]any{scope.householdID, selected}, args...)...)
		if err != nil {
			return err
		}
		extra, err := scanSurfaceRecordsBudget(rows, &remainingBytes)
		if err != nil {
			return err
		}
		out = append(out, extra...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) FinancialRelated(ctx context.Context, id auth.Identity, parent, rid string) ([]Record, error) {
	if !value.ValidUUID(rid) || parent != "entries" && parent != "categories" {
		return nil, ErrNotFound
	}
	field := "entry_id"
	if parent == "categories" {
		field = "category_id"
	}
	out := []Record{}
	err := s.financialRead(ctx, id, func(ctx context.Context, tx pgx.Tx, scope financialScope) error {
		predicate, args, err := financialPredicate(scope, "entry_lines", financialView, 3)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT "+financialSurfaceJSON("entry_lines")+" FROM expenses.entry_lines t WHERE t.household_id=$1 AND t."+
			field+"=$2 AND "+predicate+" ORDER BY t.position,t.id LIMIT 100", append([]any{scope.householdID, rid}, args...)...)
		if err != nil {
			return err
		}
		out, err = scanSurfaceRecords(rows, 16<<20)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ExpenseReport(ctx context.Context, id auth.Identity) ([]Record, error) {
	out := []Record{}
	err := s.financialRead(ctx, id, func(ctx context.Context, tx pgx.Tx, scope financialScope) error {
		predicate, args, err := financialPredicate(scope, "entries", financialView, 2)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT to_char(t.occurred_on,'YYYY-MM'),sum(t.amount_minor)::bigint FROM expenses.entries t
 WHERE t.household_id=$1 AND t.state='posted' AND `+predicate+" GROUP BY 1 ORDER BY 1 DESC LIMIT 24",
			append([]any{scope.householdID}, args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var month string
			var amount int64
			if err = rows.Scan(&month, &amount); err != nil {
				return err
			}
			out = append(out, Record{"month": month, "amount_minor": amount})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ExportFinancial reads all pages inside one bounded authorized snapshot. The
// caller buffers its output until success; errors must never publish a partial CSV.
func (s *Store) ExportFinancial(ctx context.Context, id auth.Identity, entity string, emit func([]Record) error) error {
	if _, err := s.financialDescriptor(entity); err != nil {
		return err
	}
	if emit == nil {
		return Invalid{Message: "Exportziel fehlt"}
	}
	return s.financialRead(ctx, id, func(ctx context.Context, tx pgx.Tx, scope financialScope) error {
		predicate, args, err := financialPredicate(scope, entity, financialView, 4)
		if err != nil {
			return err
		}
		remainingBytes := 16 << 20
		for offset := 0; offset <= 10000; offset += 100 {
			limit := 100
			if offset == 10000 {
				limit = 1
			}
			rows, err := tx.Query(ctx, "SELECT "+financialSurfaceJSON(entity)+" FROM "+pgx.Identifier{"expenses", entity}.Sanitize()+
				" t WHERE t.household_id=$1 AND "+predicate+" ORDER BY t.created_at DESC,t.id DESC LIMIT $2 OFFSET $3",
				append([]any{scope.householdID, limit, offset}, args...)...)
			if err != nil {
				return err
			}
			page, err := scanSurfaceRecordsBudget(rows, &remainingBytes)
			if err != nil {
				return err
			}
			if offset == 10000 && len(page) > 0 {
				return Invalid{Message: "Export umfasst mehr als 10.000 Einträge; bitte über die Betreiber-Sicherung exportieren"}
			}
			if err = emit(page); err != nil {
				return err
			}
			if len(page) < limit {
				return nil
			}
		}
		return nil
	})
}

// Check the variable-width source columns before JSON materialization. One
// record supports at most 1MiB of text; the complete output remains capped at
// 16MiB after JSON escaping. NULL is a bounded overflow sentinel, never content.
func financialSurfaceJSON(entity string) string {
	switch entity {
	case "entries":
		return "CASE WHEN octet_length(t.description)::bigint+octet_length(t.payee)::bigint<=1048576 THEN to_jsonb(t) END"
	case "entry_lines":
		return "CASE WHEN octet_length(t.note)<=1048576 THEN to_jsonb(t) END"
	default:
		return "NULL::jsonb"
	}
}

func scanSurfaceRecords(rows pgx.Rows, bytesLeft int) ([]Record, error) {
	return scanSurfaceRecordsBudget(rows, &bytesLeft)
}

func scanSurfaceRecordsBudget(rows pgx.Rows, bytesLeft *int) ([]Record, error) {
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		*bytesLeft -= len(raw)
		if raw == nil || *bytesLeft < 0 {
			return nil, Invalid{Message: "Finanzdaten überschreiten die unterstützte Ausgabegröße; Betreiber-Sicherung verwenden"}
		}
		record, err := decode(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
