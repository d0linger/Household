package store

import (
	"context"
	"fmt"
	"math/big"
	"slices"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

type financeMemberBalance struct {
	member, label, currency string
	exponent                int
	amount                  *big.Int
}

func financeLockDebts(ctx context.Context, tx pgx.Tx, household string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, household+"/finance/debts")
	return err
}

func financeReadPeople(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	pairs, balances, err := financeDebtProjection(ctx, tx, id, scope)
	if err != nil {
		return nil, err
	}
	if q.Person != "" {
		balances = slices.DeleteFunc(balances, func(row Record) bool { return row["membership_id"] != q.Person })
		pairs = slices.DeleteFunc(pairs, func(row Record) bool { return row["debtor"] != q.Person && row["creditor"] != q.Person })
	}
	predicate, auth, err := financeResourcePredicate(scope, "cash_events", "view", "c", 2)
	if err != nil {
		return nil, err
	}
	args := append([]any{id.HouseholdID}, auth...)
	where := "c.household_id=$1 AND c.state='posted' AND c.event_kind='settlement' AND " + predicate
	add := func(condition string, v any) { args = append(args, v); where += fmt.Sprintf(condition, len(args)) }
	if q.Person != "" {
		args = append(args, q.Person)
		where += fmt.Sprintf(" AND (s.from_membership_id=$%d OR s.to_membership_id=$%d)", len(args), len(args))
	}
	if q.ID != "" {
		add(" AND c.id=$%d", q.ID)
	}
	if q.From != "" {
		add(" AND c.occurred_on>=$%d::date", q.From)
	}
	if q.To != "" {
		add(" AND c.occurred_on<=$%d::date", q.To)
	}
	page, err := financePageRead(q.Cursor)
	if err != nil {
		return nil, err
	}
	if page.ID != "" {
		if page.Date == "" {
			return nil, Invalid{"Ausgleichshistorie erneut öffnen"}
		}
		args = append(args, page.Date, page.ID)
		where += fmt.Sprintf(" AND (c.occurred_on,c.id)<($%d::date,$%d::uuid)", len(args)-1, len(args))
	}
	history, err := financeSQLRecords(ctx, tx, `SELECT jsonb_build_object('resource','cash','id',c.id,'version',c.version,'event_kind','settlement','state',c.state,'occurred_on',c.occurred_on::text,'description',c.description,'debtor',s.from_membership_id,'creditor',s.to_membership_id,'currency',s.currency_code,'exponent',s.currency_exponent,'amount_minor',s.amount_minor::text,'available_actions','[]'::jsonb) FROM expenses.cash_events c JOIN expenses.settlement_details s ON s.household_id=c.household_id AND s.event_id=c.id WHERE `+where+` ORDER BY c.occurred_on DESC,c.id DESC LIMIT 51`, args, 51)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(history) > 50 {
		history = history[:50]
		next = financePageNext(String(history[49]["occurred_on"]), String(history[49]["id"]))
	}
	detail := Record{"pairs": pairs, "settlements": history, "next_cursor": next}
	if q.ID != "" {
		if len(history) != 1 {
			return nil, ErrNotFound
		}
		detail["resource"], detail["record"] = "cash", history[0]
	}
	return Record{"rows": balances, "totals": Record{}, "detail": detail, "next_cursor": "", "warnings": []string{"Forderungen beruhen auf aktuell sichtbaren Buchungen und Ausgleichen und werden je Originalwährung geführt. Unbekannte Aufteilungen werden nicht erfunden; die Ausgleichshistorie verändert keine ursprüngliche Ausgabe."}}, nil
}

func financeDebtProjection(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope) ([]Record, []Record, error) {
	predicate, auth, err := financeResourcePredicate(scope, "entries", "view", "e", 2)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT e.id,e.version,s.currency_code,s.currency_exponent,s.total_minor,s.payer_membership_id,p.membership_id,p.share_minor,
 coalesce(pu.display_name,pu.username,'Gespeichertes Mitglied'),coalesce(ou.display_name,ou.username,'Gespeichertes Mitglied')
 FROM expenses.entries e JOIN expenses.entry_split_sets s ON s.household_id=e.household_id AND s.entry_id=e.id AND s.active
 JOIN expenses.entry_person_shares p ON p.household_id=s.household_id AND p.split_set_id=s.id
 JOIN core.memberships pm ON pm.household_id=p.household_id AND pm.id=p.membership_id JOIN core.users pu ON pu.id=pm.user_id
 JOIN core.memberships om ON om.household_id=s.household_id AND om.id=s.payer_membership_id JOIN core.users ou ON ou.id=om.user_id
 WHERE e.household_id=$1 AND e.state='posted' AND `+predicate+` ORDER BY e.id,p.membership_id LIMIT 10001`, append([]any{id.HouseholdID}, auth...)...)
	if err != nil {
		return nil, nil, err
	}
	balances := map[string]*financeMemberBalance{}
	sources := map[string]map[string]int64{}
	cashSources := map[string][]Record{}
	entrySeen := map[string]bool{}
	add := func(member, label, currency string, exponent int, amount int64) {
		key := currency + "/" + member
		if balances[key] == nil {
			balances[key] = &financeMemberBalance{member: member, label: label, currency: currency, exponent: exponent, amount: new(big.Int)}
		}
		balances[key].amount.Add(balances[key].amount, big.NewInt(amount))
	}
	count := 0
	for rows.Next() {
		count++
		if count > 10000 {
			rows.Close()
			return nil, nil, Invalid{"Personenauswertung überschreitet die vollständige Quellgrenze"}
		}
		var entry, currency, payer, member, memberLabel, payerLabel string
		var version, total, share int64
		var exponent int
		if err = rows.Scan(&entry, &version, &currency, &exponent, &total, &payer, &member, &share, &memberLabel, &payerLabel); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if !entrySeen[entry] {
			entrySeen[entry] = true
			add(payer, payerLabel, currency, exponent, total)
			if sources[currency] == nil {
				sources[currency] = map[string]int64{}
			}
			sources[currency][entry] = version
		}
		add(member, memberLabel, currency, exponent, -share)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	cashPredicate, cashAuth, err := financeResourcePredicate(scope, "cash_events", "view", "c", 2)
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `SELECT c.id,c.version,s.from_membership_id,s.to_membership_id,s.currency_code,s.currency_exponent,s.amount_minor,fu.display_name,tu.display_name FROM expenses.settlement_details s JOIN expenses.cash_events c ON c.household_id=s.household_id AND c.id=s.event_id JOIN core.memberships fm ON fm.household_id=s.household_id AND fm.id=s.from_membership_id JOIN core.users fu ON fu.id=fm.user_id JOIN core.memberships tm ON tm.household_id=s.household_id AND tm.id=s.to_membership_id JOIN core.users tu ON tu.id=tm.user_id WHERE c.household_id=$1 AND c.state='posted' AND `+cashPredicate+` ORDER BY c.id LIMIT 10001`, append([]any{id.HouseholdID}, cashAuth...)...)
	if err != nil {
		return nil, nil, err
	}
	count = 0
	for rows.Next() {
		count++
		if count > 10000 {
			rows.Close()
			return nil, nil, Invalid{"Ausgleichshistorie überschreitet die vollständige Quellgrenze"}
		}
		var eventID, from, to, currency, fromLabel, toLabel string
		var exponent int
		var version, amount int64
		if err = rows.Scan(&eventID, &version, &from, &to, &currency, &exponent, &amount, &fromLabel, &toLabel); err != nil {
			rows.Close()
			return nil, nil, err
		}
		add(from, fromLabel, currency, exponent, amount)
		add(to, toLabel, currency, exponent, -amount)
		cashSources[currency] = append(cashSources[currency], Record{"event_id": eventID, "version": version})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(balances))
	for key := range balances {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	output := []Record{}
	creditors, debtors := map[string][]*financeMemberBalance{}, map[string][]*financeMemberBalance{}
	for _, key := range keys {
		b := balances[key]
		output = append(output, Record{"membership_id": b.member, "label": b.label, "currency": b.currency, "exponent": b.exponent, "balance_minor": b.amount.String()})
		copyBalance := *b
		copyBalance.amount = new(big.Int).Set(b.amount)
		if b.amount.Sign() > 0 {
			creditors[b.currency] = append(creditors[b.currency], &copyBalance)
		} else if b.amount.Sign() < 0 {
			copyBalance.amount.Neg(copyBalance.amount)
			debtors[b.currency] = append(debtors[b.currency], &copyBalance)
		}
	}
	currencies := []string{}
	for currency := range sources {
		currencies = append(currencies, currency)
	}
	slices.Sort(currencies)
	pairs := []Record{}
	for _, currency := range currencies {
		ids := []string{}
		for rid := range sources[currency] {
			ids = append(ids, rid)
		}
		slices.Sort(ids)
		if len(ids)+len(cashSources[currency]) > 1000 {
			return nil, nil, Invalid{"Ausgleichsbasis überschreitet die vollständige Herkunftsgrenze"}
		}
		basis := make([]FinanceSourceVersion, 0, len(ids))
		if cashSources[currency] == nil {
			cashSources[currency] = []Record{}
		}
		for _, rid := range ids {
			basis = append(basis, FinanceSourceVersion{EntryID: rid, Version: sources[currency][rid]})
		}
		i, j := 0, 0
		for i < len(debtors[currency]) && j < len(creditors[currency]) {
			d, c := debtors[currency][i], creditors[currency][j]
			amount := new(big.Int).Set(d.amount)
			if c.amount.Cmp(amount) < 0 {
				amount.Set(c.amount)
			}
			pairs = append(pairs, Record{"debtor": d.member, "debtor_label": d.label, "creditor": c.member, "creditor_label": c.label, "currency": currency, "exponent": d.exponent, "debt_minor": amount.String(), "reviewed_sources": basis, "cash_sources": cashSources[currency]})
			d.amount.Sub(d.amount, amount)
			c.amount.Sub(c.amount, amount)
			if d.amount.Sign() == 0 {
				i++
			}
			if c.amount.Sign() == 0 {
				j++
			}
		}
	}
	return pairs, output, nil
}
