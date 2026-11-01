package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) financeSupportRead(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	switch q.Pane {
	case "overview":
		return financeOverview(ctx, tx, id, scope, q)
	case "reports":
		return financeReports(ctx, tx, id, scope, q)
	case "import":
		if q.Section == "receipts" {
			return s.financeReceiptView(ctx, tx, id, scope, q.ID)
		}
		q.Section = "bank_profiles"
	case "planning":
		if q.Section == "" {
			q.Section = "budgets"
		}
		if q.Section == "cashflow" {
			return financeCashflow(ctx, tx, id, scope, q)
		}
		if q.Section == "contributions" {
			return financeContributionView(ctx, tx, id, scope, q)
		}
	default:
		return nil, ErrNotFound
	}
	kinds := map[string]string{"filters": "saved_filters", "budgets": "category_budgets", "goals": "savings_goals",
		"recurrences": "recurring_templates", "rules": "categorization_rules", "bank_profiles": "bank_profiles"}
	kind := kinds[q.Section]
	if kind == "" {
		return nil, ErrNotFound
	}
	if kind == "saved_filters" {
		return financeSavedFilters(ctx, tx, id, scope, q)
	}
	items, next, err := financeSupportRows(ctx, tx, id, scope, kind, q.ID, q.Cursor)
	if err != nil {
		return nil, err
	}
	for _, row := range items {
		if err = financeEnrichSupport(ctx, tx, id, scope, kind, q, row); err != nil {
			return nil, err
		}
	}
	out := Record{"rows": items, "next_cursor": next, "totals": []Record{}, "warnings": []string{}}
	if q.ID != "" && len(items) == 1 {
		detail := Record{"resource": q.Section, "record": items[0], "access": items[0]["access"]}
		if kind == "savings_goals" {
			contributions, after, readErr := financeContributionRows(ctx, tx, id, scope, items[0], "")
			if readErr != nil {
				return nil, readErr
			}
			detail["contributions"], detail["contributions_next_cursor"] = contributions, after
		}
		out["detail"] = detail
	}
	return out, nil
}

func financeSupportRows(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid, cursor string) ([]Record, string, error) {
	if !financeSupportKind(kind) || rid != "" && !value.ValidUUID(rid) || cursor != "" && !value.ValidUUID(cursor) {
		return nil, "", Invalid{"Finanzauswahl erneut laden"}
	}
	predicate, args, err := financeResourcePredicate(scope, kind, "view", "t", 4)
	if err != nil {
		return nil, "", err
	}
	args = append([]any{id.HouseholdID, rid, cursor}, args...)
	rows, err := tx.Query(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{"expenses", kind}.Sanitize()+
		` t WHERE t.household_id=$1 AND ($2='' OR t.id=NULLIF($2,'')::uuid)
 AND ($3='' OR t.id>NULLIF($3,'')::uuid) AND `+predicate+" ORDER BY t.id LIMIT 51", args...)
	if err != nil {
		return nil, "", err
	}
	items := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, "", err
		}
		record, decodeErr := financeWireRecord(raw)
		if decodeErr != nil {
			rows.Close()
			return nil, "", decodeErr
		}
		items = append(items, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = String(items[49]["id"])
	}
	if rid != "" && len(items) == 0 {
		return nil, "", ErrNotFound
	}
	for _, row := range items {
		stamp, stampErr := financeResourceAccess(ctx, tx, id, scope, kind, String(row["id"]), "view")
		if stampErr != nil {
			return nil, "", stampErr
		}
		row["access_version"] = stamp
		row["currency"], row["exponent"] = row["currency_code"], row["currency_exponent"]
		if err = financeSupportCapabilities(ctx, tx, id, scope, kind, row); err != nil {
			return nil, "", err
		}
	}
	return items, next, nil
}

func financeSavedFilters(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if q.Cursor != "" && !value.ValidUUID(q.Cursor) {
		return nil, Invalid{"Ansichten erneut laden"}
	}
	rows, err := tx.Query(ctx, `SELECT id,name,version,criteria FROM expenses.saved_filters
 WHERE household_id=$1 AND owner_user_id=$2 AND ($3='' OR id>NULLIF($3,'')::uuid) ORDER BY id LIMIT 51`, id.HouseholdID, id.UserID, q.Cursor)
	if err != nil {
		return nil, err
	}
	items := []Record{}
	for rows.Next() {
		var rid, name string
		var version int64
		var raw []byte
		if err = rows.Scan(&rid, &name, &version, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, Record{"id": rid, "name": name, "version": version, "criteria_raw": json.RawMessage(raw)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = String(items[49]["id"])
	}
	for _, row := range items {
		raw, _ := row["criteria_raw"].(json.RawMessage)
		delete(row, "criteria_raw")
		var criteria FinanceFilter
		valid := decodeFinancePayload(raw, &criteria) == nil
		if valid {
			_, err = financeFilterReferences(ctx, tx, id, scope, criteria)
			valid = err == nil
		}
		if !valid && err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
			var invalid Invalid
			if !errors.As(err, &invalid) {
				return nil, err
			}
		}
		row["valid"] = valid
		// Name and filter ID are this user's own preference metadata. A revoked
		// criterion's financial IDs/labels/payload are omitted, never broadened.
		if valid {
			row["criteria"] = criteria
		}
		row["available_actions"] = []string{"delete"}
		if valid {
			row["available_actions"] = []string{"save", "delete"}
		}
		stamp, accessErr := financeResourceAccess(ctx, tx, id, scope, "saved_filters", String(row["id"]), "preference")
		if accessErr != nil {
			return nil, accessErr
		}
		row["access_version"] = stamp
	}
	return Record{"rows": items, "next_cursor": next, "warnings": []string{}}, nil
}

func financeSupportCapabilities(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind string, row Record) error {
	canEdit := false
	if id.CanWrite() {
		if err := tx.QueryRow(ctx, `SELECT core.finance_resource_visible($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,'edit')`,
			id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, scope.generation, kind, String(row["id"])).Scan(&canEdit); err != nil {
			return err
		}
	}
	actions := []string{}
	if kind == "categorization_rules" {
		actions = append(actions, "explain")
	}
	if canEdit {
		switch kind {
		case "goal_contributions":
			actions = append(actions, "reverse")
			if row["contribution_kind"] == "planned" {
				actions = append(actions, "save")
			}
			if row["contribution_kind"] == "reversal" {
				actions = []string{}
			}
		case "recurring_templates":
			actions = append(actions, "save")
			if row["active"] == true {
				actions = append(actions, "pause", "generate")
			}
		case "category_budgets", "bank_profiles":
			if row["archived_at"] == nil {
				actions = append(actions, "save", "archive")
			}
		case "savings_goals":
			actions = append(actions, "save")
			if row["state"] != "archived" {
				actions = append(actions, "archive")
			}
		case "categorization_rules":
			actions = append(actions, "save")
			if row["active"] == true {
				actions = append(actions, "archive")
			}
		}
	}
	row["available_actions"] = actions
	if kind == "category_budgets" || kind == "savings_goals" || kind == "recurring_templates" || kind == "categorization_rules" {
		access, err := financeAccessProjection(ctx, tx, id, scope, kind, String(row["id"]))
		if err != nil {
			return err
		}
		row["access"] = access
	}
	return nil
}

func financeContributionView(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, q FinanceQuery) (Record, error) {
	if q.ID != "" {
		row, err := financeSupportRecord(ctx, tx, id, scope, "goal_contributions", q.ID, false)
		if err != nil {
			return nil, err
		}
		goal, err := financeSupportRecord(ctx, tx, id, scope, "savings_goals", String(row["goal_id"]), false)
		if err != nil {
			return nil, err
		}
		row["currency"], row["exponent"] = goal["currency_code"], goal["currency_exponent"]
		if err = financeSupportCapabilities(ctx, tx, id, scope, "goal_contributions", row); err != nil {
			return nil, err
		}
		return Record{"rows": []Record{row}, "detail": Record{"resource": "contributions", "record": row}}, nil
	}
	if !value.ValidUUID(q.Goal) {
		return nil, Invalid{"Sparziel für die Beitragsauswahl ausdrücklich angeben"}
	}
	goal, err := financeSupportRecord(ctx, tx, id, scope, "savings_goals", q.Goal, false)
	if err != nil {
		return nil, err
	}
	rows, next, err := financeContributionRows(ctx, tx, id, scope, goal, q.Cursor)
	if err != nil {
		return nil, err
	}
	return Record{"rows": rows, "next_cursor": next}, nil
}

func financeContributionRows(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, goal Record, cursor string) ([]Record, string, error) {
	if cursor != "" && !value.ValidUUID(cursor) {
		return nil, "", Invalid{"Beitragsseite erneut laden"}
	}
	predicate, args, err := financeResourcePredicate(scope, "goal_contributions", "view", "t", 4)
	if err != nil {
		return nil, "", err
	}
	rows, err := financeSQLRecords(ctx, tx, `SELECT to_jsonb(t) FROM expenses.goal_contributions t WHERE t.household_id=$1 AND t.goal_id=$2
 AND ($3='' OR t.id>NULLIF($3,'')::uuid) AND `+predicate+` ORDER BY t.id LIMIT 51`, append([]any{id.HouseholdID, String(goal["id"]), cursor}, args...), 51)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = String(rows[49]["id"])
	}
	for _, row := range rows {
		row["currency"], row["exponent"] = goal["currency_code"], goal["currency_exponent"]
		row["amount_minor"] = String(row["amount_minor"])
		stamp, accessErr := financeResourceAccess(ctx, tx, id, scope, "goal_contributions", String(row["id"]), "view")
		if accessErr != nil {
			return nil, "", accessErr
		}
		row["access_version"] = stamp
		if err = financeSupportCapabilities(ctx, tx, id, scope, "goal_contributions", row); err != nil {
			return nil, "", err
		}
	}
	return rows, next, nil
}

func financeEnrichSupport(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind string, q FinanceQuery, row Record) error {
	switch kind {
	case "category_budgets":
		return financeBudgetValues(ctx, tx, id, scope, q.Month, row)
	case "savings_goals":
		return financeGoalValues(ctx, tx, id, scope, row)
	case "recurring_templates":
		return financeRecurrenceValues(ctx, tx, id, scope, row)
	}
	return nil
}

func financeExplainRules(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, in FinanceRuleExplainInput) (Record, error) {
	amount, err := finance.ParseMinor(in.AmountMinor)
	if err != nil || !financeText(in.Payee, 0, 100) {
		return nil, Invalid{"Empfänger und exakten Betrag prüfen"}
	}
	if _, ok := finance.CurrencyExponent(in.Currency); !ok {
		return nil, finance.ErrMoney
	}
	if in.AccountID != nil {
		if _, err = financeResourceAccess(ctx, tx, id, scope, "accounts", *in.AccountID, "view"); err != nil {
			return nil, err
		}
	}
	predicate, args, err := financeResourcePredicate(scope, "categorization_rules", "view", "t", 2)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(t) FROM expenses.categorization_rules t WHERE t.household_id=$1 AND t.active AND `+
		predicate+` ORDER BY t.priority,t.id LIMIT 1001`, append([]any{id.HouseholdID}, args...)...)
	if err != nil {
		return nil, err
	}
	candidates := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		r, e := financeWireRecord(raw)
		if e != nil {
			rows.Close()
			return nil, e
		}
		candidates = append(candidates, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(candidates) > 1000 {
		return nil, Invalid{"Regelmenge eingrenzen; Erklärung wird nicht gekürzt"}
	}
	matches := []Record{}
	for _, rule := range candidates {
		if rule["account_id"] != nil && (in.AccountID == nil || String(rule["account_id"]) != *in.AccountID) {
			continue
		}
		if rule["currency_code"] != nil && String(rule["currency_code"]) != in.Currency {
			continue
		}
		if rule["minimum_minor"] != nil {
			low, e := finance.ParseMinor(String(rule["minimum_minor"]))
			if e != nil {
				return nil, e
			}
			if amount < low {
				continue
			}
		}
		if rule["maximum_minor"] != nil {
			high, e := finance.ParseMinor(String(rule["maximum_minor"]))
			if e != nil {
				return nil, e
			}
			if amount > high {
				continue
			}
		}
		payee, pattern := strings.ToLower(in.Payee), strings.ToLower(String(rule["payee_pattern"]))
		matched := false
		switch String(rule["match_kind"]) {
		case "equals":
			matched = payee == pattern
		case "prefix":
			matched = strings.HasPrefix(payee, pattern)
		case "contains":
			matched = strings.Contains(payee, pattern)
		}
		if matched {
			matches = append(matches, Record{"id": rule["id"], "name": rule["name"], "priority": rule["priority"], "category_id": rule["category_id"], "reason": String(rule["match_kind"]) + ": " + String(rule["payee_pattern"]), "version": rule["version"]})
		}
	}
	var winner any
	if len(matches) > 0 {
		winner = matches[0]
	}
	impact := Record{"action": "rules.explain", "matches": matches, "winner": winner, "reason": "Niedrigste Prioritätszahl gewinnt, bei Gleichstand die UUID. Übernahme bleibt ausdrücklich geprüft."}
	return Record{"impact": impact, "read_only": true}, nil
}
