package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type financePage struct {
	Date string `json:"date"`
	ID   string `json:"id"`
	Kind string `json:"kind,omitempty"`
}

func financePageRead(raw string) (financePage, error) {
	if raw == "" {
		return financePage{}, nil
	}
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(bytes) > 160 {
		return financePage{}, Invalid{"Finanzseite erneut öffnen"}
	}
	var page financePage
	if err = json.Unmarshal(bytes, &page); err != nil || !value.ValidUUID(page.ID) || page.Date != "" && !finance.CivilDate(page.Date) {
		return financePage{}, Invalid{"Finanzseite erneut öffnen"}
	}
	return page, nil
}

func financePageNext(date, id string) string {
	return financePageNextKind(date, id, "")
}

func financePageNextKind(date, id, kind string) string {
	raw, _ := json.Marshal(financePage{Date: date, ID: id, Kind: kind})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func financeSQLRecords(ctx context.Context, tx pgx.Tx, query string, args []any, limit int) ([]Record, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	bytes := 0
	for rows.Next() {
		if len(out) >= limit {
			return nil, Invalid{"Finanzauswahl überschreitet die vollständige Auswertungsgrenze"}
		}
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		bytes += len(raw)
		if len(raw) == 0 || len(raw) > 1<<20 || bytes > 16<<20 {
			return nil, Invalid{"Finanzauswahl ist zu groß; keine gekürzte Auswertung"}
		}
		record, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func financeLedgerRead(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	switch q.Pane {
	case "accounts":
		if q.Section == "reconciliations" {
			return financeReadReconciliations(ctx, tx, id, scope, q)
		}
		return financeReadAccounts(ctx, tx, id, scope, q)
	case "people":
		return financeReadPeople(ctx, tx, id, scope, q)
	case "entries", "reports":
		return financeReadEntries(ctx, tx, id, scope, q)
	default:
		return nil, ErrNotFound
	}
}

func financeReadAccounts(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	predicate, authArgs, err := financeResourcePredicate(scope, "accounts", "view", "a", 2)
	if err != nil {
		return nil, err
	}
	args := append([]any{id.HouseholdID}, authArgs...)
	where := "a.household_id=$1 AND " + predicate
	page, err := financePageRead(q.Cursor)
	if err != nil {
		return nil, err
	}
	if q.ID != "" {
		args = append(args, q.ID)
		where += fmt.Sprintf(" AND a.id=$%d", len(args))
	} else if page.ID != "" {
		args = append(args, page.ID)
		where += fmt.Sprintf(" AND a.id>$%d", len(args))
	}
	if q.Account != "" {
		args = append(args, q.Account)
		where += fmt.Sprintf(" AND a.id=$%d", len(args))
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',a.id,'version',a.version,'name',a.name,'label',a.name,'account_type',a.account_type,'currency',a.currency_code,'exponent',a.currency_exponent,'notes',a.notes,'archived',a.archived_at IS NOT NULL) FROM expenses.accounts a WHERE `+where+` ORDER BY a.id LIMIT 51`, args, 51)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = financePageNext("", String(rows[49]["id"]))
	}
	for _, row := range rows {
		rid := String(row["id"])
		stamp, accessErr := financeResourceAccess(ctx, tx, id, scope, "accounts", rid, "view")
		if accessErr != nil {
			return nil, accessErr
		}
		row["access_version"] = stamp
		summary, summaryErr := financeAccountSummary(ctx, tx, id, scope, rid, q.To)
		if summaryErr != nil {
			return nil, summaryErr
		}
		for key, v := range summary {
			row[key] = v
		}
		row["available_actions"] = []string{}
		canEdit, editErr := financeLedgerCanEdit(ctx, tx, id, scope, "accounts", rid)
		if editErr != nil {
			return nil, editErr
		}
		if canEdit && row["archived"] == false {
			row["available_actions"] = []string{"save", "archive", "income", "opening", "adjustment", "transfer", "reconcile"}
		}
	}
	detail := any(nil)
	if q.ID != "" {
		if len(rows) != 1 {
			return nil, ErrNotFound
		}
		statement, statementErr := financeAccountStatement(ctx, tx, id, scope, q.ID, q)
		if statementErr != nil {
			return nil, statementErr
		}
		detail = Record{"resource": "accounts", "record": rows[0], "statement": statement["rows"], "next_cursor": statement["next_cursor"], "income_drafts": statement["income_drafts"]}
	}
	return Record{"rows": rows, "next_cursor": next, "totals": Record{}, "detail": detail, "warnings": []string{"Saldo sichtbarer Buchungen; kein vollständiger Banksaldo."}}, nil
}

func financeAccountSummary(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, accountID, to string) (Record, error) {
	if to == "" {
		zone, err := time.LoadLocation(scope.timezone)
		if err != nil {
			return nil, err
		}
		to = scope.readAt.In(zone).Format(time.DateOnly)
	}
	predicate, args, err := financeResourcePredicate(scope, "account_movements", "view", "m", 4)
	if err != nil {
		return nil, err
	}
	var total string
	var opening bool
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(m.amount_minor),0)::text,coalesce(bool_or(c.event_kind='opening'),false) FROM expenses.account_movements m JOIN expenses.cash_events c ON c.household_id=m.household_id AND c.id=m.event_id WHERE m.household_id=$1 AND m.account_id=$2 AND c.occurred_on<=$3 AND c.state='posted' AND `+predicate, append([]any{id.HouseholdID, accountID, to}, args...)...).Scan(&total, &opening)
	if err != nil {
		return nil, err
	}
	var balance any
	if opening {
		balance = total
	}
	return Record{"opening_known": opening, "visible_movement_minor": total, "visible_balance_minor": balance, "as_of": to, "balance_label": "Saldo sichtbarer Buchungen"}, nil
}

func financeAccountStatement(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, accountID string, q FinanceQuery) (Record, error) {
	predicate, auth, err := financeResourcePredicate(scope, "account_movements", "view", "m", 3)
	if err != nil {
		return nil, err
	}
	args := append([]any{id.HouseholdID, accountID}, auth...)
	where := "m.household_id=$1 AND m.account_id=$2 AND c.state='posted' AND " + predicate
	if q.From != "" {
		args = append(args, q.From)
		where += fmt.Sprintf(" AND c.occurred_on>=$%d::date", len(args))
	}
	if q.To != "" {
		args = append(args, q.To)
		where += fmt.Sprintf(" AND c.occurred_on<=$%d::date", len(args))
	}
	page, err := financePageRead(q.Cursor)
	if err != nil {
		return nil, err
	}
	if page.ID != "" {
		if page.Date == "" {
			return nil, Invalid{"Kontoauszug erneut öffnen"}
		}
		args = append(args, page.Date, page.ID)
		where += fmt.Sprintf(" AND (c.occurred_on,m.id)<($%d::date,$%d::uuid)", len(args)-1, len(args))
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',m.id,'event_id',c.id,'event_kind',c.event_kind,'occurred_on',c.occurred_on::text,'description',c.description,'native_minor',m.amount_minor::text,'base_minor',m.base_minor::text,'currency',m.currency_code,'exponent',m.currency_exponent) FROM expenses.account_movements m JOIN expenses.cash_events c ON c.household_id=m.household_id AND c.id=m.event_id WHERE `+where+` ORDER BY c.occurred_on DESC,m.id DESC LIMIT 51`, args, 51)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = financePageNext(String(rows[49]["occurred_on"]), String(rows[49]["id"]))
	}
	draftPredicate, draftAuth, err := financeResourcePredicate(scope, "cash_events", "view", "c", 3)
	if err != nil {
		return nil, err
	}
	drafts, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',c.id,'version',c.version,'occurred_on',c.occurred_on::text,'description',c.description,'state',c.state,'event_kind',c.event_kind,'original_minor',c.original_minor::text,'currency',c.original_currency,'exponent',c.original_exponent,'base_minor',c.base_minor::text) FROM expenses.cash_events c WHERE c.household_id=$1 AND c.account_id=$2 AND c.event_kind='income' AND c.state='draft' AND `+draftPredicate+` ORDER BY c.occurred_on DESC,c.id DESC LIMIT 50`, append([]any{id.HouseholdID, accountID}, draftAuth...), 50)
	if err != nil {
		return nil, err
	}
	for _, draft := range drafts {
		rid := String(draft["id"])
		stamp, accessErr := financeResourceAccess(ctx, tx, id, scope, "cash_events", rid, "view")
		if accessErr != nil {
			return nil, accessErr
		}
		draft["access_version"], draft["resource"], draft["available_actions"] = stamp, "cash", []string{}
		canEdit, editErr := financeLedgerCanEdit(ctx, tx, id, scope, "cash_events", rid)
		if editErr != nil {
			return nil, editErr
		}
		if canEdit {
			draft["available_actions"] = []string{"income"}
		}
	}
	return Record{"rows": rows, "next_cursor": next, "income_drafts": drafts}, nil
}

func financeEntryWhere(scope financialScope, q FinanceQuery) (string, []any, error) {
	predicate, auth, err := financeResourcePredicate(scope, "entries", "view", "e", 2)
	if err != nil {
		return "", nil, err
	}
	args := append([]any{scope.householdID}, auth...)
	where := "e.household_id=$1 AND " + predicate
	add := func(sql string, v any) { args = append(args, v); where += fmt.Sprintf(sql, len(args)) }
	if q.ID != "" {
		add(" AND e.id=$%d", q.ID)
	}
	if q.From != "" {
		add(" AND e.occurred_on>=$%d::date", q.From)
	}
	if q.To != "" {
		add(" AND e.occurred_on<=$%d::date", q.To)
	}
	if q.From == "" && q.To == "" && q.Month != "" && q.ID == "" {
		add(" AND to_char(e.occurred_on,'YYYY-MM')=$%d", q.Month)
	}
	if q.State == "draft" || q.State == "posted" {
		add(" AND e.state=$%d", q.State)
	}
	if q.Payee != "" {
		add(" AND strpos(lower(e.payee),lower($%d))>0", q.Payee)
	}
	if q.Account != "" {
		add(" AND EXISTS(SELECT 1 FROM expenses.entry_finance f WHERE f.household_id=e.household_id AND f.entry_id=e.id AND f.account_id=$%d)", q.Account)
	}
	if q.Category != "" {
		add(" AND EXISTS(SELECT 1 FROM expenses.entry_lines l WHERE l.household_id=e.household_id AND l.entry_id=e.id AND l.category_id=$%d)", q.Category)
	}
	if q.Person != "" {
		args = append(args, q.Person)
		n := len(args)
		where += fmt.Sprintf(" AND (e.paid_by_membership_id=$%d OR EXISTS(SELECT 1 FROM expenses.entry_person_shares p JOIN expenses.entry_split_sets s ON s.household_id=p.household_id AND s.id=p.split_set_id AND s.active WHERE p.household_id=e.household_id AND p.entry_id=e.id AND p.membership_id=$%d))", n, n)
	}
	if q.State == "refunded" || q.State == "reversed" || q.State == "corrected" {
		relatedPredicate, relatedArgs, relatedErr := financeResourcePredicate(scope, "entries", "view", "related", len(args)+1)
		if relatedErr != nil {
			return "", nil, relatedErr
		}
		args = append(args, relatedArgs...)
		if q.State == "reversed" {
			where += " AND EXISTS(SELECT 1 FROM expenses.entries related WHERE related.household_id=e.household_id AND related.reverses_entry_id=e.id AND related.state='posted' AND " + relatedPredicate + ")"
		} else {
			relation := "refund_of"
			if q.State == "corrected" {
				relation = "correction_of"
			}
			args = append(args, relation)
			where += fmt.Sprintf(" AND EXISTS(SELECT 1 FROM expenses.entry_relations r JOIN expenses.entries related ON related.household_id=r.household_id AND related.id=r.entry_id WHERE r.household_id=e.household_id AND r.original_entry_id=e.id AND r.relation=$%d AND related.state='posted' AND ", len(args)) + relatedPredicate + ")"
		}
	}
	return where, args, nil
}

func financeReadEntries(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if q.ID != "" {
		var detail Record
		var err error
		if q.Section == "ledger" {
			detail, err = financeIncomeDetail(ctx, tx, id, scope, q.ID)
		} else {
			detail, err = financeEntryDetail(ctx, tx, id, scope, q.ID)
		}
		if err != nil {
			return nil, err
		}
		return Record{"rows": []Record{}, "detail": detail, "totals": Record{}, "warnings": []string{}, "next_cursor": ""}, nil
	}
	if q.Month != "" {
		var err error
		q.From, q.To, err = financeDateWindow(q)
		if err != nil {
			return nil, err
		}
	}
	where, args, err := financeEntryWhere(scope, q)
	if err != nil {
		return nil, err
	}
	var posted, draft string
	var unknown int64
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(e.amount_minor) FILTER(WHERE e.state='posted'),0)::text,coalesce(sum(e.amount_minor) FILTER(WHERE e.state='draft'),0)::text,count(*) FILTER(WHERE e.amount_minor IS NULL) FROM expenses.entries e WHERE `+where, args...).Scan(&posted, &draft, &unknown)
	if err != nil {
		return nil, err
	}
	if q.Category != "" {
		argsForTotal := append(append([]any{}, args...), q.Category)
		err = tx.QueryRow(ctx, `SELECT coalesce(sum(l.amount_minor) FILTER(WHERE e.state='posted'),0)::text,coalesce(sum(l.amount_minor) FILTER(WHERE e.state='draft'),0)::text FROM expenses.entries e JOIN expenses.entry_lines l ON l.household_id=e.household_id AND l.entry_id=e.id WHERE `+where+fmt.Sprintf(" AND l.category_id=$%d", len(argsForTotal)), argsForTotal...).Scan(&posted, &draft)
		if err != nil {
			return nil, err
		}
	}
	incomeWhere, incomeArgs, err := financeIncomeWhere(scope, q)
	if err != nil {
		return nil, err
	}
	var incomePosted, incomeDraft string
	var incomeUnknown int64
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(c.base_minor) FILTER(WHERE c.state='posted'),0)::text,coalesce(sum(c.base_minor) FILTER(WHERE c.state='draft'),0)::text,count(*) FILTER(WHERE c.base_minor IS NULL) FROM expenses.cash_events c WHERE `+incomeWhere, incomeArgs...).Scan(&incomePosted, &incomeDraft, &incomeUnknown)
	if err != nil {
		return nil, err
	}
	incomeWhere = financeShiftPlaceholders(incomeWhere, len(args)-1)
	args = append(args, incomeArgs[1:]...)
	page, err := financePageRead(q.Cursor)
	if err != nil {
		return nil, err
	}
	pageWhere := ""
	if page.ID != "" {
		if page.Date == "" || page.Kind != "entries" && page.Kind != "cash" {
			return nil, Invalid{"Buchungsseite erneut öffnen"}
		}
		args = append(args, page.Date, page.ID, page.Kind)
		pageWhere = fmt.Sprintf(" WHERE (selected.occurred_on,selected.id,selected.resource_kind)<($%d::date,$%d::uuid,$%d::text)", len(args)-2, len(args)-1, len(args))
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT selected.record FROM(SELECT `+financeEntryJSON()+` record,e.occurred_on,e.id,'entries'::text resource_kind FROM expenses.entries e LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id WHERE `+where+` UNION ALL SELECT `+financeIncomeJSON()+` record,c.occurred_on,c.id,'cash'::text resource_kind FROM expenses.cash_events c WHERE `+incomeWhere+`) selected`+pageWhere+` ORDER BY selected.occurred_on DESC,selected.id DESC,selected.resource_kind DESC LIMIT 51`, args, 51)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = financePageNextKind(String(rows[49]["occurred_on"]), String(rows[49]["id"]), String(rows[49]["resource"]))
	}
	for _, row := range rows {
		if row["resource"] == "cash" {
			err = financeIncomeActions(ctx, tx, id, scope, row)
		} else {
			err = financeEntryActions(ctx, tx, id, scope, row)
		}
		if err != nil {
			return nil, err
		}
	}
	return Record{"rows": rows, "totals": Record{"posted_minor": posted, "draft_known_minor": draft, "unknown_draft_count": unknown, "income_known_minor": incomePosted, "income_draft_known_minor": incomeDraft, "income_unknown_draft_count": incomeUnknown, "currency": "EUR", "person_scope_label": "Buchungen mit Beteiligung"}, "detail": nil, "next_cursor": next, "warnings": []string{}}, nil
}

func financeShiftPlaceholders(sql string, shift int) string {
	return regexp.MustCompile(`\$([0-9]+)`).ReplaceAllStringFunc(sql, func(raw string) string {
		n, _ := strconv.Atoi(raw[1:])
		if n == 1 {
			return raw
		}
		return "$" + strconv.Itoa(n+shift)
	})
}

func financeIncomeWhere(scope financialScope, q FinanceQuery) (string, []any, error) {
	predicate, auth, err := financeResourcePredicate(scope, "cash_events", "view", "c", 2)
	if err != nil {
		return "", nil, err
	}
	args := append([]any{scope.householdID}, auth...)
	where := "c.household_id=$1 AND c.event_kind='income' AND " + predicate
	add := func(condition string, v any) { args = append(args, v); where += fmt.Sprintf(condition, len(args)) }
	if q.From != "" {
		add(" AND c.occurred_on>=$%d::date", q.From)
	}
	if q.To != "" {
		add(" AND c.occurred_on<=$%d::date", q.To)
	}
	if q.From == "" && q.To == "" && q.Month != "" {
		add(" AND to_char(c.occurred_on,'YYYY-MM')=$%d", q.Month)
	}
	if q.State == "draft" || q.State == "posted" {
		add(" AND c.state=$%d", q.State)
	}
	if q.Account != "" {
		add(" AND c.account_id=$%d", q.Account)
	}
	if q.Payee != "" {
		add(" AND strpos(lower(c.payee),lower($%d))>0", q.Payee)
	}
	if q.Category != "" || q.Person != "" || q.State == "refunded" || q.State == "reversed" || q.State == "corrected" {
		where += " AND false"
	}
	return where, args, nil
}

func financeIncomeJSON() string {
	return `jsonb_build_object('resource','cash','id',c.id,'version',c.version,'occurred_on',c.occurred_on::text,'description',c.description,'payee',c.payee,'kind','income','event_kind','income','state',c.state,'money',jsonb_build_object('currency',c.original_currency,'exponent',c.original_exponent,'minor',c.original_minor::text,'original_status','known','base_minor',c.base_minor::text,'base_status',CASE WHEN c.base_minor IS NULL THEN 'rate_missing' WHEN c.original_currency<>'EUR' AND c.base_minor=0 THEN 'below_base_precision' ELSE 'known' END,'fx',CASE WHEN c.fx_numerator IS NULL THEN NULL ELSE jsonb_build_object('numerator',c.fx_numerator::text,'denominator',c.fx_denominator::text,'effective_on',c.fx_effective_on::text,'source',c.fx_source) END))`
}

func financeIncomeActions(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, row Record) error {
	rid := String(row["id"])
	stamp, err := financeResourceAccess(ctx, tx, id, scope, "cash_events", rid, "view")
	if err != nil {
		return err
	}
	origin, err := financeOriginProjection(ctx, tx, id, scope, "cash_events", rid)
	if err != nil {
		return err
	}
	row["access_version"], row["source_status"], row["available_actions"], row["status_badges"] = stamp, origin, []string{}, []string{}
	editable, err := financeLedgerCanEdit(ctx, tx, id, scope, "cash_events", rid)
	if err != nil {
		return err
	}
	if editable && row["state"] == "draft" {
		row["available_actions"] = []string{"income"}
	}
	return nil
}

func financeEntryJSON() string {
	return `CASE WHEN octet_length(e.description)::bigint+octet_length(e.payee)::bigint+coalesce(octet_length(f.fx_source),0)::bigint<=1048576 THEN jsonb_build_object('resource','entries','id',e.id,'version',e.version,'occurred_on',e.occurred_on::text,'description',e.description,'payee',e.payee,'kind',e.kind,'state',e.state,'money_mode',e.money_mode,'paid_by_membership_id',e.paid_by_membership_id,'money',jsonb_build_object('currency',coalesce(f.original_currency,'EUR'),'exponent',coalesce(f.original_exponent,2),'minor',coalesce(f.original_minor,e.amount_minor)::text,'original_status',CASE WHEN f.id IS NULL THEN 'legacy_unknown' ELSE f.original_state END,'base_minor',e.amount_minor::text,'base_status',CASE WHEN e.amount_minor IS NULL THEN 'rate_missing' WHEN e.money_mode='foreign_original' AND e.amount_minor=0 THEN 'below_base_precision' ELSE 'known' END,'fx',CASE WHEN f.fx_numerator IS NULL THEN NULL ELSE jsonb_build_object('numerator',f.fx_numerator::text,'denominator',f.fx_denominator::text,'effective_on',f.fx_effective_on::text,'source',f.fx_source) END),'original_status',CASE WHEN f.id IS NULL THEN 'legacy_unknown' ELSE f.original_state END) ELSE NULL END`
}

func financeEntryLines(ctx context.Context, tx pgx.Tx, household, entryID string, lock bool) ([]Record, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF l NOWAIT"
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT CASE WHEN octet_length(l.note)<=1048576 THEN jsonb_build_object('id',l.id,'version',l.version,'position',l.position,'category_id',l.category_id,'category_label',c.name,'original_minor',coalesce(o.original_minor,l.amount_minor)::text,'base_minor',l.amount_minor::text,'note',l.note) ELSE NULL END FROM expenses.entry_lines l JOIN expenses.categories c ON c.household_id=l.household_id AND c.id=l.category_id LEFT JOIN expenses.entry_original_lines o ON o.household_id=l.household_id AND o.line_id=l.id WHERE l.household_id=$1 AND l.entry_id=$2 ORDER BY l.position,l.id LIMIT 101`+suffix, []any{household, entryID}, 101)
	if err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, Invalid{"Buchung überschreitet die vollständige Kategoriegrenze"}
	}
	return rows, nil
}

func financeEntryActions(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, row Record) error {
	stamp, err := financeResourceAccess(ctx, tx, id, scope, "entries", String(row["id"]), "view")
	if err != nil {
		return err
	}
	row["access_version"], row["available_actions"], row["status_badges"] = stamp, []string{}, []string{}
	origin, err := financeOriginProjection(ctx, tx, id, scope, "entries", String(row["id"]))
	if err != nil {
		return err
	}
	row["source_status"] = origin
	canEdit, err := financeLedgerCanEdit(ctx, tx, id, scope, "entries", String(row["id"]))
	if err != nil {
		return err
	}
	if canEdit {
		if row["state"] == "draft" {
			row["available_actions"] = []string{"save", "post"}
		} else {
			row["available_actions"] = []string{"reverse", "correct", "refund"}
		}
	}
	relations, err := financeEntryRelations(ctx, tx, id, scope, String(row["id"]))
	if err != nil {
		return err
	}
	badges := []string{}
	for _, relation := range relations {
		if relation["original_entry_id"] == row["id"] {
			switch relation["relation"] {
			case "reversal_of":
				badges = append(badges, "storniert")
				row["available_actions"] = []string{}
			case "correction_of":
				badges = append(badges, "korrigiert")
			case "refund_of":
				badges = append(badges, "sichtbare Erstattung")
			}
		}
	}
	row["status_badges"] = badges
	return nil
}

func financeEntryDetail(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, rid string) (Record, error) {
	if _, err := financeResourceAccess(ctx, tx, id, scope, "entries", rid, "view"); err != nil {
		return nil, err
	}
	where, args, err := financeEntryWhere(scope, FinanceQuery{ID: rid})
	if err != nil {
		return nil, err
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT `+financeEntryJSON()+` FROM expenses.entries e LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id WHERE `+where, args, 1)
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = ErrNotFound
		}
		return nil, err
	}
	if err = financeEntryActions(ctx, tx, id, scope, rows[0]); err != nil {
		return nil, err
	}
	lines, err := financeEntryLines(ctx, tx, id.HouseholdID, rid, false)
	if err != nil {
		return nil, err
	}
	baseline := make([]Record, 0, len(lines))
	var originalSum, baseSum int64
	known := true
	for _, line := range lines {
		baseline = append(baseline, Record{"id": line["id"], "version": line["version"]})
		n, parseErr := strconv.ParseInt(String(line["original_minor"]), 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		originalSum += n
		if line["base_minor"] == nil {
			known = false
		} else {
			n, parseErr = strconv.ParseInt(String(line["base_minor"]), 10, 64)
			if parseErr != nil {
				return nil, parseErr
			}
			baseSum += n
		}
	}
	payment, split, err := financeEntryContextRead(ctx, tx, id, rid)
	if err != nil {
		return nil, err
	}
	money := rows[0]["money"].(map[string]any)
	original, err := strconv.ParseInt(String(money["minor"]), 10, 64)
	if err != nil {
		return nil, err
	}
	var remainingBase any
	if known && money["base_minor"] != nil {
		base, parseErr := strconv.ParseInt(String(money["base_minor"]), 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		remainingBase = strconv.FormatInt(base-baseSum, 10)
	}
	relations, err := financeEntryRelations(ctx, tx, id, scope, rid)
	if err != nil {
		return nil, err
	}
	access, err := financeAccessProjection(ctx, tx, id, scope, "entries", rid)
	if err != nil {
		return nil, err
	}
	return Record{"resource": "entries", "record": rows[0], "access": access, "payment": payment, "lines": lines, "baseline": baseline, "removed": []Record{}, "split": split, "remaining_original_minor": strconv.FormatInt(original-originalSum, 10), "remaining_base_minor": remainingBase, "relations": relations, "source_status": rows[0]["source_status"]}, nil
}

func financeIncomeDetail(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, rid string) (Record, error) {
	stamp, err := financeResourceAccess(ctx, tx, id, scope, "cash_events", rid, "view")
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object('resource','cash','record',jsonb_build_object('id',id,'version',version,'state',state,'event_kind',event_kind,'occurred_on',occurred_on::text,'description',description,'payee',payee,'money',jsonb_build_object('currency',original_currency,'exponent',original_exponent,'minor',original_minor::text,'base_minor',base_minor::text,'base_status',CASE WHEN base_minor IS NULL THEN 'rate_missing' WHEN original_currency<>'EUR' AND base_minor=0 THEN 'below_base_precision' ELSE 'known' END,'fx',CASE WHEN fx_numerator IS NULL THEN NULL ELSE jsonb_build_object('numerator',fx_numerator::text,'denominator',fx_denominator::text,'effective_on',fx_effective_on::text,'source',fx_source) END)),'payment',payment_context) FROM expenses.cash_events WHERE household_id=$1 AND id=$2 AND event_kind='income'`, id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	detail, err := decode(raw)
	if err != nil {
		return nil, err
	}
	record := detail["record"].(map[string]any)
	record["access_version"] = stamp
	origin, err := financeOriginProjection(ctx, tx, id, scope, "cash_events", rid)
	if err != nil {
		return nil, err
	}
	record["source_status"], detail["source_status"] = origin, origin
	record["available_actions"] = []string{}
	canEdit, err := financeLedgerCanEdit(ctx, tx, id, scope, "cash_events", rid)
	if err != nil {
		return nil, err
	}
	if canEdit && record["state"] == "draft" {
		record["available_actions"] = []string{"income"}
	}
	return detail, nil
}

func financeEntryContextRead(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string) (any, any, error) {
	var payment, split []byte
	err := tx.QueryRow(ctx, `SELECT CASE WHEN f.account_id IS NULL THEN NULL ELSE jsonb_build_object('account_id',f.account_id,'account_version',a.version,'native_minor',abs(f.payment_minor)::text,'fx',CASE WHEN f.payment_fx_numerator IS NULL THEN NULL ELSE jsonb_build_object('numerator',f.payment_fx_numerator::text,'denominator',f.payment_fx_denominator::text,'effective_on',f.payment_fx_on::text,'source',f.payment_fx_source) END,'difference_reviewed',f.payment_difference_reviewed) END FROM expenses.entry_finance f LEFT JOIN expenses.accounts a ON a.household_id=f.household_id AND a.id=f.account_id WHERE f.household_id=$1 AND f.entry_id=$2`, id.HouseholdID, rid).Scan(&payment)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object('method',method,'participants',basis) FROM expenses.entry_split_sets WHERE household_id=$1 AND entry_id=$2 AND active`, id.HouseholdID, rid).Scan(&split)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	var paymentValue, splitValue any
	if len(payment) > 0 {
		if err = json.Unmarshal(payment, &paymentValue); err != nil {
			return nil, nil, err
		}
	}
	if len(split) > 0 {
		if err = json.Unmarshal(split, &splitValue); err != nil {
			return nil, nil, err
		}
	}
	return paymentValue, splitValue, nil
}

func financeLedgerCanEdit(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string) (bool, error) {
	if !id.CanWrite() {
		return false, nil
	}
	predicate, auth, err := financeResourcePredicate(scope, kind, "edit", "editable", 3)
	if err != nil {
		return false, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+pgx.Identifier{"expenses", kind}.Sanitize()+" editable WHERE household_id=$1 AND id=$2 AND "+predicate+")", append([]any{id.HouseholdID, rid}, auth...)...).Scan(&allowed)
	return allowed, err
}

func financeEntryRelations(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, rid string) ([]Record, error) {
	predicate, auth, err := financeResourcePredicate(scope, "entry_relations", "view", "r", 3)
	if err != nil {
		return nil, err
	}
	return financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',r.id,'entry_id',r.entry_id,'original_entry_id',r.original_entry_id,'relation',r.relation,'reversal_entry_id',r.reversal_entry_id,'reviewed_original_version',r.reviewed_original_version) FROM expenses.entry_relations r WHERE r.household_id=$1 AND (r.entry_id=$2 OR r.original_entry_id=$2) AND `+predicate+` ORDER BY r.id LIMIT 1001`, append([]any{id.HouseholdID, rid}, auth...), 1000)
}
