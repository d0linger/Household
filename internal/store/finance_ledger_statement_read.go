package store

import (
	"context"
	"fmt"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func financeReadReconciliations(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	predicate, auth, err := financeResourcePredicate(scope, "reconciliations", "view", "r", 2)
	if err != nil {
		return nil, err
	}
	args := append([]any{id.HouseholdID}, auth...)
	where := "r.household_id=$1 AND " + predicate
	add := func(condition string, v any) { args = append(args, v); where += fmt.Sprintf(condition, len(args)) }
	if q.ID != "" {
		add(" AND r.id=$%d", q.ID)
	}
	if q.Account != "" {
		add(" AND r.account_id=$%d", q.Account)
	}
	if q.From != "" {
		add(" AND r.statement_on>=$%d::date", q.From)
	}
	if q.To != "" {
		add(" AND r.statement_on<=$%d::date", q.To)
	}
	page, err := financePageRead(q.Cursor)
	if err != nil {
		return nil, err
	}
	if q.ID == "" && page.ID != "" {
		if page.Date == "" {
			return nil, Invalid{"Abstimmungsseite erneut öffnen"}
		}
		args = append(args, page.Date, page.ID)
		where += fmt.Sprintf(" AND (r.statement_on,r.id)<($%d::date,$%d::uuid)", len(args)-1, len(args))
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('resource','reconciliations','id',r.id,'version',r.version,'account_id',r.account_id,'account_label',a.name,'cutoff',r.statement_on::text,'starts_on',r.starts_on::text,'opening_minor',r.opening_minor::text,'external_minor',r.statement_minor::text,'currency',r.currency_code,'exponent',r.currency_exponent,'computed_minor',r.computed_minor::text,'difference_minor',r.difference_minor::text,'state',r.state,'reopens_id',r.reopens_id,'reason',r.reopen_reason) FROM expenses.reconciliations r JOIN expenses.accounts a ON a.household_id=r.household_id AND a.id=r.account_id WHERE `+where+` ORDER BY r.statement_on DESC,r.id DESC LIMIT 51`, args, 51)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = financePageNext(String(rows[49]["cutoff"]), String(rows[49]["id"]))
	}
	for _, row := range rows {
		rid := String(row["id"])
		stamp, accessErr := financeResourceAccess(ctx, tx, id, scope, "reconciliations", rid, "view")
		if accessErr != nil {
			return nil, accessErr
		}
		row["access_version"], row["available_actions"] = stamp, []string{}
		editable, editErr := financeLedgerCanEdit(ctx, tx, id, scope, "reconciliations", rid)
		if editErr != nil {
			return nil, editErr
		}
		if editable && row["state"] == "confirmed" {
			row["available_actions"] = []string{"reopen"}
		}
	}
	var detail any
	if q.ID != "" {
		if len(rows) != 1 {
			return nil, ErrNotFound
		}
		itemPredicate, itemAuth, itemErr := financeResourcePredicate(scope, "reconciliation_items", "view", "i", 3)
		if itemErr != nil {
			return nil, itemErr
		}
		itemArgs := append([]any{id.HouseholdID, q.ID}, itemAuth...)
		itemWhere := "i.household_id=$1 AND i.reconciliation_id=$2 AND " + itemPredicate
		if page.ID != "" {
			itemArgs = append(itemArgs, page.ID)
			itemWhere += fmt.Sprintf(" AND i.id>$%d", len(itemArgs))
		}
		items, itemErr := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('id',i.id,'movement_id',i.movement_id,'amount_minor',i.amount_minor::text,'event_version',i.reviewed_event_version,'movement_version',i.reviewed_movement_version) FROM expenses.reconciliation_items i WHERE `+itemWhere+` ORDER BY i.id LIMIT 51`, itemArgs, 51)
		if itemErr != nil {
			return nil, itemErr
		}
		itemNext := ""
		if len(items) > 50 {
			items = items[:50]
			itemNext = financePageNext("", String(items[49]["id"]))
		}
		detail = Record{"resource": "reconciliations", "record": rows[0], "items": items, "next_cursor": itemNext, "adjustment_created": false}
	}
	return Record{"rows": rows, "next_cursor": next, "totals": Record{}, "detail": detail, "warnings": []string{"Abstimmungen enthalten keine automatische Ausgleichsbuchung."}}, nil
}
