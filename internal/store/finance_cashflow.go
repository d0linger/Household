package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

type financeCashflowDay struct {
	confirmed, estimated *big.Int
	opening              bool
}

func financeCashflow(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if q.Person != "" || q.Category != "" || q.Payee != "" || q.State != "" {
		return nil, Invalid{"Liquidität verwendet Konto und Zeitraum. Andere Buchungsfilter ausdrücklich zurücksetzen."}
	}
	zone, err := time.LoadLocation(scope.timezone)
	if err != nil {
		return nil, err
	}
	today := scope.readAt.In(zone).Format(time.DateOnly)
	from := q.From
	if from == "" {
		from = today
	}
	start, err := time.Parse(time.DateOnly, from)
	if err != nil || !finance.CivilDate(from) {
		return nil, Invalid{"Gültigen Vorschauanfang wählen"}
	}
	to := q.To
	if to == "" {
		to = start.AddDate(0, 0, 89).Format(time.DateOnly)
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil || !finance.CivilDate(to) || end.Before(start) || end.After(start.AddDate(0, 0, 364)) {
		return nil, Invalid{"Liquiditätsvorschau auf höchstens365Tage begrenzen"}
	}
	accounts, err := financeLedgerRead(ctx, tx, id, scope, FinanceQuery{Pane: "accounts", ID: q.Account})
	if err != nil {
		return nil, err
	}
	if String(accounts["next_cursor"]) != "" {
		return nil, Invalid{"Bei mehr als50sichtbaren Konten ein Konto ausdrücklich auswählen"}
	}
	items, _ := accounts["rows"].([]Record)
	byAccount := map[string]Record{}
	changes := map[string]map[string]*financeCashflowDay{}
	balances := map[string]*big.Int{}
	known := map[string]bool{}
	for _, account := range items {
		accountID := String(account["id"])
		byAccount[accountID] = account
		changes[accountID] = map[string]*financeCashflowDay{}
		balances[accountID] = new(big.Int)
		predicate, args, policyErr := financeResourcePredicate(scope, "account_movements", "view", "m", 4)
		if policyErr != nil {
			return nil, policyErr
		}
		var opening bool
		var amount string
		err = tx.QueryRow(ctx, `SELECT coalesce(bool_or(e.event_kind='opening'),false),coalesce(sum(m.amount_minor::numeric),0)::text
 FROM expenses.account_movements m JOIN expenses.cash_events e ON e.household_id=m.household_id AND e.id=m.event_id
 WHERE m.household_id=$1 AND m.account_id=$2 AND e.state='posted' AND e.occurred_on<$3::date AND `+predicate,
			append([]any{id.HouseholdID, accountID, from}, args...)...).Scan(&opening, &amount)
		if err != nil {
			return nil, err
		}
		balance, parseErr := financeInt(amount)
		if parseErr != nil {
			return nil, parseErr
		}
		balances[accountID] = balance
		known[accountID] = opening
	}
	add := func(account, date, minor string, estimated, opening bool) error {
		if byAccount[account] == nil {
			return nil
		}
		amount, parseErr := financeInt(minor)
		if parseErr != nil {
			return parseErr
		}
		day := changes[account][date]
		if day == nil {
			day = &financeCashflowDay{confirmed: new(big.Int), estimated: new(big.Int)}
			changes[account][date] = day
		}
		if estimated {
			day.estimated.Add(day.estimated, amount)
		} else {
			day.confirmed.Add(day.confirmed, amount)
			day.opening = day.opening || opening
		}
		return nil
	}
	predicate, args, err := financeResourcePredicate(scope, "account_movements", "view", "m", 5)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT m.account_id,e.occurred_on::text,sum(m.amount_minor::numeric)::text,bool_or(e.event_kind='opening')
 FROM expenses.account_movements m JOIN expenses.cash_events e ON e.household_id=m.household_id AND e.id=m.event_id
 WHERE m.household_id=$1 AND e.state='posted' AND e.occurred_on BETWEEN $2::date AND $3::date AND ($4='' OR m.account_id=NULLIF($4,'')::uuid) AND `+
		predicate+` GROUP BY m.account_id,e.occurred_on ORDER BY e.occurred_on,m.account_id`, append([]any{id.HouseholdID, from, to, q.Account}, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var account, date, minor string
		var opening bool
		if err = rows.Scan(&account, &date, &minor, &opening); err != nil {
			rows.Close()
			return nil, err
		}
		if err = add(account, date, minor, false, opening); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Draft payment facts are estimates. They never join the confirmed movement
	// sum, and generated occurrences below are omitted from template estimates.
	for _, kind := range []string{"entries", "cash_events"} {
		predicate, args, err = financeResourcePredicate(scope, kind, "view", "t", 5)
		if err != nil {
			return nil, err
		}
		statement := `SELECT f.account_id,t.occurred_on::text,CASE WHEN t.kind='expense' THEN -abs(f.payment_minor)::numeric ELSE abs(f.payment_minor)::numeric END::text
 FROM expenses.entries t JOIN expenses.entry_finance f ON f.household_id=t.household_id AND f.entry_id=t.id
 WHERE t.household_id=$1 AND t.state='draft' AND f.account_id IS NOT NULL AND f.payment_minor IS NOT NULL
 AND t.occurred_on BETWEEN $2::date AND $3::date AND ($4='' OR f.account_id=NULLIF($4,'')::uuid) AND ` + predicate
		if kind == "cash_events" {
			statement = `SELECT t.account_id,t.occurred_on::text,(t.payment_context->>'native_minor')
 FROM expenses.cash_events t WHERE t.household_id=$1 AND t.event_kind='income' AND t.state='draft' AND t.account_id IS NOT NULL
 AND t.payment_context->>'native_minor' IS NOT NULL AND t.occurred_on BETWEEN $2::date AND $3::date AND ($4='' OR t.account_id=NULLIF($4,'')::uuid) AND ` + predicate
		}
		rows, err = tx.Query(ctx, statement, append([]any{id.HouseholdID, from, to, q.Account}, args...)...)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			count++
			if count > 10000 {
				rows.Close()
				return nil, Invalid{"Zu viele offene Zahlungsentwürfe für eine vollständige Vorschau"}
			}
			var account, date, minor string
			if err = rows.Scan(&account, &date, &minor); err != nil {
				rows.Close()
				return nil, err
			}
			if err = add(account, date, minor, true, false); err != nil {
				rows.Close()
				return nil, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	warnings := []string{"Bestätigte Zahlungen, offene Entwürfe und Vorlagen sind getrennte Tatsachen. Die Projektion verwendet nur aktuell sichtbare Buchungen; sie ist kein Banksaldo.", "Ohne bekannten Anfangsstand bleibt der projizierte Saldo unbekannt. Geplante Fremdwährungszahlungen behalten ihre tatsächliche Kontowährung."}
	predicate, args, err = financeResourcePredicate(scope, "recurring_templates", "view", "t", 3)
	if err != nil {
		return nil, err
	}
	templates, err := financeSQLRecords(ctx, tx, `SELECT to_jsonb(t) FROM expenses.recurring_templates t WHERE t.household_id=$1 AND t.active
 AND ($2='' OR t.account_id=NULLIF($2,'')::uuid OR t.template->'payment'->>'account_id'=$2) AND `+predicate+` ORDER BY t.id LIMIT 1001`, append([]any{id.HouseholdID, q.Account}, args...), 1001)
	if err != nil {
		return nil, err
	}
	if len(templates) > 1000 {
		return nil, Invalid{"Vorlagenauswahl für die vollständige Liquiditätsvorschau eingrenzen"}
	}
	for _, template := range templates {
		active, _ := template["active"].(bool)
		if !active {
			continue
		}
		raw, marshalErr := json.Marshal(template["template"])
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payment *FinancePaymentInput
		direction := int64(-1)
		if template["template_kind"] == "income" {
			var draft FinanceIncomeInput
			if err = decodeFinancePayload(raw, &draft); err != nil {
				return nil, err
			}
			payment = draft.Payment
			direction = 1
		} else {
			var draft FinanceEntryInput
			if err = decodeFinancePayload(raw, &draft); err != nil {
				return nil, err
			}
			payment = draft.Payment
			if draft.Kind == "refund" {
				direction = 1
			}
		}
		if payment == nil {
			continue
		}
		account := payment.AccountID
		if template["account_id"] != nil && String(template["account_id"]) != account {
			return nil, Invalid{"Vorlagenkonto widerspricht der gespeicherten tatsächlichen Zahlung"}
		}
		if byAccount[account] == nil {
			continue
		}
		if archived, _ := byAccount[account]["archived"].(bool); archived {
			warnings = append(warnings, "Eine sichtbare Vorlage verweist auf ein archiviertes Konto und wird nicht als künftige Zahlung angesetzt.")
			continue
		}
		native, parseErr := finance.ParseMinor(payment.NativeMinor)
		if parseErr != nil {
			return nil, parseErr
		}
		last, readErr := financeRecurrenceState(ctx, tx, id.HouseholdID, String(template["id"]))
		if readErr != nil {
			return nil, readErr
		}
		if before := start.AddDate(0, 0, -1).Format(time.DateOnly); last < before {
			last = before
		}
		through := to
		if template["ends_on"] != nil && String(template["ends_on"]) < through {
			through = String(template["ends_on"])
		}
		for range 366 {
			on, nextErr := financeNextRecurrence(String(template["anchor_on"]), last, through, String(template["repeat_unit"]), int(Int(template["repeat_interval"])), String(template["monthday_policy"]))
			if nextErr != nil {
				return nil, nextErr
			}
			if on == "" {
				break
			}
			if err = add(account, on, fmt.Sprint(direction*native), true, false); err != nil {
				return nil, err
			}
			last = on
		}
	}
	out := []Record{}
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		civil := date.Format(time.DateOnly)
		for _, account := range items {
			accountID := String(account["id"])
			day := changes[accountID][civil]
			if day == nil {
				day = &financeCashflowDay{confirmed: new(big.Int), estimated: new(big.Int)}
			}
			if day.opening {
				known[accountID] = true
				balances[accountID] = new(big.Int)
			}
			balances[accountID].Add(balances[accountID], day.confirmed)
			balances[accountID].Add(balances[accountID], day.estimated)
			var projected any
			if known[accountID] {
				projected = balances[accountID].String()
			}
			out = append(out, Record{"date": civil, "account_id": accountID, "label": account["name"], "currency": account["currency"], "exponent": account["exponent"],
				"confirmed_minor": day.confirmed.String(), "estimated_minor": day.estimated.String(), "projected_minor": projected, "opening_known": known[accountID], "scope_label": "Projizierter Saldo sichtbarer Buchungen"})
			if len(out) > 5000 {
				return nil, Invalid{"Vorschau auf ein Konto oder einen kürzeren Zeitraum begrenzen; es wird nichts gekürzt"}
			}
		}
	}
	return Record{"rows": out, "totals": []Record{}, "warnings": warnings, "from": from, "to": to}, nil
}
