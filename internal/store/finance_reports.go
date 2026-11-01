package store

import (
	"context"
	"fmt"
	"time"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

// financeExpenseSelection is reused for overview, category budgets and exports.
// Category selection narrows consumption to its lines; person selection means
// bookings with participation, not an invented person/category allocation matrix.
func financeExpenseSelection(scope financialScope, q FinanceQuery) (string, []any, error) {
	from, to, err := financeDateWindow(q)
	if err != nil {
		return "", nil, err
	}
	args := []any{scope.householdID, from, to, q.Account, q.Person, q.Category, financeLike(q.Payee), q.State}
	predicate, policyArgs, err := financeResourcePredicate(scope, "entries", "view", "t", 9)
	if err != nil {
		return "", nil, err
	}
	args = append(args, policyArgs...)
	where := `t.household_id=$1 AND t.occurred_on BETWEEN $2::date AND $3::date
 AND ($4='' OR EXISTS(SELECT 1 FROM expenses.entry_finance f WHERE f.household_id=t.household_id AND f.entry_id=t.id AND f.account_id=NULLIF($4,'')::uuid))
 AND ($5='' OR t.paid_by_membership_id=NULLIF($5,'')::uuid OR EXISTS(SELECT 1 FROM expenses.entry_person_shares p
 WHERE p.household_id=t.household_id AND p.entry_id=t.id AND p.membership_id=NULLIF($5,'')::uuid))
 AND ($6='' OR EXISTS(SELECT 1 FROM expenses.entry_lines l WHERE l.household_id=t.household_id AND l.entry_id=t.id AND l.category_id=NULLIF($6,'')::uuid))
 AND t.payee ILIKE $7 ESCAPE '\' AND $8::text IS NOT NULL AND ` + predicate
	if q.State == "draft" || q.State == "posted" {
		where += " AND t.state=$8"
	} else if q.State != "" {
		relation := map[string]string{"refunded": "refund_of", "reversed": "reversal_of", "corrected": "correction_of"}[q.State]
		if relation == "" {
			return "", nil, Invalid{"Buchungszustand erneut auswählen"}
		}
		related, relationArgs, relationErr := financeResourcePredicate(scope, "entry_relations", "view", "er", len(args)+1)
		if relationErr != nil {
			return "", nil, relationErr
		}
		args = append(args, relationArgs...)
		args = append(args, relation)
		where += fmt.Sprintf(` AND EXISTS(SELECT 1 FROM expenses.entry_relations er WHERE er.household_id=t.household_id
 AND er.original_entry_id=t.id AND er.relation=$%d AND %s)`, len(args), related)
	}
	return where, args, nil
}

func financeDateWindow(q FinanceQuery) (string, string, error) {
	month, err := time.Parse("2006-01", q.Month)
	if err != nil {
		return "", "", Invalid{"Monat auswählen"}
	}
	from, to := q.From, q.To
	if from == "" {
		from = month.Format(time.DateOnly)
	}
	if to == "" {
		to = month.AddDate(0, 1, -1).Format(time.DateOnly)
	}
	start, startErr := time.Parse(time.DateOnly, from)
	end, endErr := time.Parse(time.DateOnly, to)
	if startErr != nil || endErr != nil || start.After(end) || end.After(start.AddDate(2, 0, 0)) {
		return "", "", Invalid{"Einen Zeitraum von höchstens zwei Jahren auswählen"}
	}
	return from, to, nil
}

func financeConsumptionExpression() string {
	return `CASE WHEN $6='' THEN t.amount_minor::numeric ELSE
 (SELECT sum(selected.amount_minor::numeric) FROM expenses.entry_lines selected
 WHERE selected.household_id=t.household_id AND selected.entry_id=t.id AND selected.category_id=NULLIF($6,'')::uuid) END`
}

func financeOverview(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if _, err := financeFilterReferences(ctx, tx, id, scope, FinanceFilter{From: q.From, To: q.To, Account: q.Account, Person: q.Person, Category: q.Category, Payee: q.Payee, State: q.State}); err != nil {
		return nil, err
	}
	where, args, err := financeExpenseSelection(scope, q)
	if err != nil {
		return nil, err
	}
	var posted, draft string
	var unknown int64
	err = tx.QueryRow(ctx, `SELECT
 coalesce(sum(`+financeConsumptionExpression()+`) FILTER(WHERE t.state='posted'),0)::text,
 coalesce(sum(`+financeConsumptionExpression()+`) FILTER(WHERE t.state='draft' AND t.amount_minor IS NOT NULL),0)::text,
 count(*) FILTER(WHERE t.state='draft' AND t.amount_minor IS NULL)
 FROM expenses.entries t WHERE `+where, args...).Scan(&posted, &draft, &unknown)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT l.category_id,c.name,sum(l.amount_minor::numeric)::text
 FROM expenses.entries t JOIN expenses.entry_lines l ON l.household_id=t.household_id AND l.entry_id=t.id
 JOIN expenses.categories c ON c.household_id=l.household_id AND c.id=l.category_id
 WHERE t.state='posted' AND ($6='' OR l.category_id=NULLIF($6,'')::uuid) AND `+where+
		` GROUP BY l.category_id,c.name ORDER BY abs(sum(l.amount_minor::numeric)) DESC,l.category_id LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	categories := []Record{}
	for rows.Next() {
		var category, label, minor string
		if err = rows.Scan(&category, &label, &minor); err != nil {
			rows.Close()
			return nil, err
		}
		categories = append(categories, Record{"category_id": category, "label": label, "amount_minor": minor, "currency": "EUR", "exponent": 2})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return Record{"rows": categories, "totals": Record{"posted_consumption_minor": posted, "draft_known_minor": draft, "draft_unknown_count": unknown},
		"warnings": []string{"Summen berücksichtigen aktuell sichtbare Buchungen. Der Personenfilter umfasst alle Buchungen mit Beteiligung dieser Person; Kategorieanteile beziehen sich jeweils auf die ganze Buchung."}}, nil
}

func financeReports(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if q.Section == "ledger" {
		return financeLedgerRead(ctx, tx, id, scope, q)
	}
	where, args, err := financeExpenseSelection(scope, q)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT to_char(t.occurred_on,'YYYY-MM'),sum(`+financeConsumptionExpression()+`)::text
 FROM expenses.entries t WHERE t.state='posted' AND `+where+
		` GROUP BY to_char(t.occurred_on,'YYYY-MM') ORDER BY to_char(t.occurred_on,'YYYY-MM')`, args...)
	if err != nil {
		return nil, err
	}
	items := []Record{}
	for rows.Next() {
		var month, minor string
		if err = rows.Scan(&month, &minor); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, Record{"month": month, "amount_minor": minor, "currency": "EUR", "exponent": 2})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	summary, err := financeOverview(ctx, tx, id, scope, q)
	if err != nil {
		return nil, err
	}
	return Record{"rows": items, "categories": summary["rows"], "totals": summary["totals"], "warnings": summary["warnings"]}, nil
}
