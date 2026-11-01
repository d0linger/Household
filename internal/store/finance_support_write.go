package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) financeSupportCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Resource != "filters" && (c.Resource != "rules" || c.Action != "explain") && !id.CanWrite() {
		return nil, ErrForbidden
	}
	switch c.Resource {
	case "filters":
		return financeFilterCommand(ctx, tx, id, scope, c)
	case "budgets":
		return financeBudgetCommand(ctx, tx, id, scope, c)
	case "goals":
		return financeGoalCommand(ctx, tx, id, scope, c)
	case "rules":
		return financeRuleCommand(ctx, tx, id, scope, c)
	case "bank_profiles":
		return financeProfileCommand(ctx, tx, id, scope, c)
	case "contributions":
		return financeContributionCommand(ctx, tx, id, scope, c)
	case "recurrences":
		return s.financeRecurrenceCommand(ctx, tx, id, scope, c)
	case "receipts":
		return s.financeReceiptCommand(ctx, tx, id, scope, c)
	default:
		return nil, ErrNotFound
	}
}

func financeFilterCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "delete" {
		if err := decodeFinancePayload(c.Payload, &struct{}{}); err != nil {
			return nil, err
		}
		if _, err := financeSupportRecord(ctx, tx, id, scope, "saved_filters", c.ID, true); err != nil {
			return nil, err
		}
		op, err := prepareFinanceOrigin(ctx, tx, id, scope, "saved_filters", c.ID, "preference", nil)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM expenses.saved_filters WHERE household_id=$1 AND id=$2 AND owner_user_id=$3 AND version=$4`, id.HouseholdID, c.ID, id.UserID, c.Version)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrConflict
		}
		if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
			return nil, err
		}
		return Record{"id": c.ID, "version": c.Version + 1, "impact": Record{"action": "filter_deleted", "id": c.ID, "version": c.Version}}, nil
	}
	var in FinanceFilterInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if !financeText(in.Name, 1, 100) {
		return nil, Invalid{"Ansicht eindeutig benennen"}
	}
	refs, err := financeFilterReferences(ctx, tx, id, scope, in.Criteria)
	if err != nil {
		return nil, err
	}
	criteria, err := json.Marshal(in.Criteria)
	if err != nil {
		return nil, err
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "saved_filters", []string{"owner_user_id", "name", "criteria", "criteria_version"},
		[]any{id.UserID, in.Name, criteria, 1}, refs, in)
}

func financeBudgetCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "archive" {
		return financeArchiveSupport(ctx, tx, id, scope, c, "category_budgets", "archived_at", "clock_timestamp()")
	}
	var in FinanceBudgetInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	monthly, err := finance.ParseMinor(in.MonthlyMinor)
	initial, initialErr := finance.ParseMinor(in.InitialCarryMinor)
	if err != nil || initialErr != nil || monthly < 0 || !financeText(in.Name, 1, 100) ||
		!financeMonthBoundary(in.StartsOn) || in.EndsOn != nil && (!financeMonthBoundary(*in.EndsOn) || *in.EndsOn < in.StartsOn) ||
		!slices.Contains([]string{"none", "positive_only", "all"}, in.CarryRule) || in.CarryRule == "none" && initial != 0 {
		return nil, Invalid{"Budget, Monatsgrenzen und ausdrücklichen Übertrag prüfen"}
	}
	if err = financeCategory(ctx, tx, id.HouseholdID, in.CategoryID); err != nil {
		return nil, err
	}
	refs, err := financeFilterReferences(ctx, tx, id, scope, FinanceFilter{Account: in.Scope.Account, Person: in.Scope.Person})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in.Scope)
	if err != nil {
		return nil, err
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "category_budgets",
		[]string{"name", "category_id", "starts_on", "ends_on", "monthly_minor", "carry_rule", "initial_carry_minor", "scope"},
		[]any{in.Name, in.CategoryID, in.StartsOn, in.EndsOn, monthly, in.CarryRule, initial, raw}, refs, in)
}

func financeGoalCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "archive" {
		return financeArchiveSupport(ctx, tx, id, scope, c, "savings_goals", "state", "'archived'")
	}
	var in FinanceGoalInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	target, err := finance.ParseMinor(in.TargetMinor)
	exponent, supported := finance.CurrencyExponent(in.Currency)
	if err != nil || target <= 0 || !supported || exponent != in.Exponent || !financeText(in.Name, 1, 100) ||
		in.TargetOn != nil && !value.Date(*in.TargetOn) || !slices.Contains([]string{"active", "completed", "archived"}, in.State) ||
		(in.PlannedPeriod == nil) != (in.PlannedPeriodMinor == nil) {
		return nil, Invalid{"Sparziel, Währung, Termin und Beitragsplan prüfen"}
	}
	var planned any
	if in.PlannedPeriod != nil {
		amount, amountErr := finance.ParseMinor(*in.PlannedPeriodMinor)
		if amountErr != nil || amount < 0 || !slices.Contains([]string{"week", "month"}, *in.PlannedPeriod) {
			return nil, Invalid{"Geplanten Beitrag ausdrücklich pro Woche oder Monat angeben"}
		}
		planned = amount
	}
	refs := []FinanceResourceRef{}
	if in.AccountID != nil {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: *in.AccountID})
	}
	if c.Version > 0 {
		old, oldErr := financeSupportRecord(ctx, tx, id, scope, "savings_goals", c.ID, true)
		if oldErr != nil {
			return nil, oldErr
		}
		if String(old["currency_code"]) != in.Currency || Int(old["currency_exponent"]) != int64(in.Exponent) {
			return nil, Invalid{"Die Währung eines gespeicherten Sparziels bleibt unverändert"}
		}
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "savings_goals",
		[]string{"name", "currency_code", "currency_exponent", "target_minor", "target_on", "account_id", "planned_period_minor", "planned_period", "state"},
		[]any{in.Name, in.Currency, in.Exponent, target, in.TargetOn, in.AccountID, planned, in.PlannedPeriod, in.State}, refs, in)
}

func financeRuleCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "archive" {
		return financeArchiveSupport(ctx, tx, id, scope, c, "categorization_rules", "active", "false")
	}
	if c.Action == "explain" {
		var in FinanceRuleExplainInput
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		return financeExplainRules(ctx, tx, id, scope, in)
	}
	var in FinanceRuleInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if !financeText(in.Name, 1, 100) || in.Priority < 0 || in.Priority > 1000000 ||
		!slices.Contains([]string{"equals", "prefix", "contains"}, in.MatchKind) || !financeText(in.PayeePattern, 1, 100) {
		return nil, Invalid{"Regelname, Priorität und wörtliche Empfängerbedingung prüfen"}
	}
	if in.Currency != nil {
		if _, supported := finance.CurrencyExponent(*in.Currency); !supported {
			return nil, finance.ErrMoney
		}
	}
	if (in.MinimumMinor != nil || in.MaximumMinor != nil) && in.Currency == nil {
		return nil, Invalid{"Für Betragsgrenzen ausdrücklich eine Regelwährung auswählen"}
	}
	min, max, err := financeOptionalBounds(in.MinimumMinor, in.MaximumMinor)
	if err != nil {
		return nil, err
	}
	if err = financeCategory(ctx, tx, id.HouseholdID, in.CategoryID); err != nil {
		return nil, err
	}
	refs := []FinanceResourceRef{}
	if in.AccountID != nil {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: *in.AccountID})
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "categorization_rules",
		[]string{"name", "priority", "active", "match_kind", "payee_pattern", "currency_code", "minimum_minor", "maximum_minor", "category_id", "account_id"},
		[]any{in.Name, in.Priority, in.Active, in.MatchKind, in.PayeePattern, in.Currency, min, max, in.CategoryID, in.AccountID}, refs, in)
}

func financeProfileCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "archive" {
		return financeArchiveSupport(ctx, tx, id, scope, c, "bank_profiles", "archived_at", "clock_timestamp()")
	}
	var in FinanceBankProfileInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if err := validateFinanceBankProfile(in); err != nil {
		return nil, err
	}
	fields, err := json.Marshal(in.FieldMap)
	if err != nil {
		return nil, err
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "bank_profiles",
		[]string{"owner_user_id", "name", "account_id", "delimiter", "encoding", "date_layout", "decimal_separator", "group_separator", "amount_mode", "field_map", "profile_format"},
		[]any{id.UserID, in.Name, in.AccountID, in.Delimiter, in.Encoding, in.DateLayout, in.DecimalSeparator, in.GroupSeparator, in.AmountMode, fields, 1},
		[]FinanceResourceRef{{Kind: "accounts", ID: in.AccountID}}, in)
}

// financeSaveSupport accepts only compiled relation/column lists from the closed
// command functions above; client keys never become SQL identifiers.
func financeSaveSupport(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand,
	kind string, columns []string, values []any, refs []FinanceResourceRef, reviewed any, finalize ...func(Record) error) (Record, error) {
	if !financeSupportKind(kind) || len(columns) != len(values) {
		return nil, errors.New("invalid compiled finance write")
	}
	intent := "edit"
	if kind == "saved_filters" {
		intent = "view"
	}
	stamp, err := financeProspectiveAccess(ctx, tx, id, scope, refs, intent)
	if err != nil {
		return nil, err
	}
	if c.Version > 0 {
		old, readErr := financeSupportRecord(ctx, tx, id, scope, kind, c.ID, true)
		if readErr != nil {
			return nil, readErr
		}
		if Int(old["version"]) != c.Version {
			return nil, ErrConflict
		}
	}
	origin, sources := financeOriginFor(ctx, refs)
	if kind == "saved_filters" {
		origin = "preference"
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, kind, c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	args := []any{c.ID, id.HouseholdID, id.UserID}
	quoted, placeholders, assignments := []string{}, []string{}, []string{}
	for index, column := range columns {
		quoted = append(quoted, pgx.Identifier{column}.Sanitize())
		placeholders = append(placeholders, fmt.Sprintf("$%d", index+4))
		assignments = append(assignments, fmt.Sprintf("%s=$%d", pgx.Identifier{column}.Sanitize(), index+4))
	}
	args = append(args, values...)
	table := pgx.Identifier{"expenses", kind}.Sanitize()
	query := "INSERT INTO " + table + " AS t (id,household_id,created_by,changed_by," + strings.Join(quoted, ",") +
		") VALUES($1,$2,$3,$3," + strings.Join(placeholders, ",") + ") RETURNING to_jsonb(t)"
	if c.Version > 0 {
		args = append(args, c.Version)
		query = "UPDATE " + table + " AS t SET " + strings.Join(assignments, ",") +
			",changed_by=$3,updated_at=clock_timestamp(),version=version+1 WHERE id=$1 AND household_id=$2 AND version=$" +
			fmt.Sprint(len(args)) + " RETURNING to_jsonb(t)"
	}
	var raw []byte
	if err = tx.QueryRow(ctx, query, args...).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	} else if err != nil {
		return nil, financeAuthorityError(err)
	}
	record, err := financeWireRecord(raw)
	if err != nil {
		return nil, err
	}
	if len(finalize) > 1 {
		return nil, errors.New("multiple compiled finance finalizers")
	}
	if len(finalize) == 1 {
		if err = finalize[0](record); err != nil {
			return nil, err
		}
		if err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+table+" t WHERE household_id=$1 AND id=$2", id.HouseholdID, c.ID).Scan(&raw); err != nil {
			return nil, err
		}
		record, err = financeWireRecord(raw)
		if err != nil {
			return nil, err
		}
	}
	if err = financeProjectSupport(ctx, tx, id, scope, kind, record); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return Record{"id": c.ID, "resource_type": kind, "version": Int(record["version"]), "record": record,
		"impact": Record{"action": c.Resource + "." + c.Action, "id": c.ID, "prior_version": c.Version, "values": reviewed, "source_stamp": stamp}}, nil
}

func financeArchiveSupport(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand, kind, column, expression string) (Record, error) {
	if err := decodeFinancePayload(c.Payload, &struct{}{}); err != nil {
		return nil, err
	}
	old, err := financeSupportRecord(ctx, tx, id, scope, kind, c.ID, true)
	if err != nil {
		return nil, err
	}
	if c.Version <= 0 || Int(old["version"]) != c.Version {
		return nil, ErrConflict
	}
	origin, sources := financeOriginFor(ctx, nil)
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, kind, c.ID, origin, sources)
	if err != nil {
		return nil, err
	}
	// security: kind/column/expression are fixed literals at the four calls above.
	var version int64
	err = tx.QueryRow(ctx, "UPDATE "+pgx.Identifier{"expenses", kind}.Sanitize()+" SET "+pgx.Identifier{column}.Sanitize()+"="+expression+
		",changed_by=$3,updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2 AND version=$4 RETURNING version",
		id.HouseholdID, c.ID, id.UserID, c.Version).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	current, err := financeSupportRecord(ctx, tx, id, scope, kind, c.ID, false)
	if err != nil {
		return nil, err
	}
	if err = financeProjectSupport(ctx, tx, id, scope, kind, current); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return Record{"id": c.ID, "version": version, "resource_type": kind, "impact": Record{"action": c.Resource + ".archive", "id": c.ID, "prior_version": c.Version}}, nil
}

func financeSupportRecord(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string, lock bool) (Record, error) {
	if !financeSupportKind(kind) || !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	intent := "view"
	if lock {
		intent = "edit"
		if kind == "saved_filters" {
			intent = "preference"
		}
	}
	stamp, err := financeResourceAccess(ctx, tx, id, scope, kind, rid, intent)
	if err != nil {
		return nil, err
	}
	suffix := ""
	if lock {
		suffix = " FOR UPDATE NOWAIT"
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{"expenses", kind}.Sanitize()+" t WHERE household_id=$1 AND id=$2"+suffix, id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	record, err := financeWireRecord(raw)
	if err != nil {
		return nil, err
	}
	record["access_version"] = stamp
	return record, nil
}

func financeSupportKind(kind string) bool {
	return slices.Contains([]string{"saved_filters", "category_budgets", "savings_goals", "goal_contributions", "recurring_templates", "recurring_occurrences", "categorization_rules", "bank_profiles", "receipt_reviews", "receipt_review_lines"}, kind)
}

func financeWireRecord(raw []byte) (Record, error) {
	r, err := decode(raw)
	if err != nil {
		return nil, err
	}
	for key, v := range r {
		if v != nil && (strings.HasSuffix(key, "_minor") || strings.HasSuffix(key, "_numerator") || strings.HasSuffix(key, "_denominator")) {
			r[key] = String(v)
		}
	}
	return r, nil
}

func financeCategory(ctx context.Context, tx pgx.Tx, household, category string) error {
	if !value.ValidUUID(category) {
		return Invalid{"Gültige Kategorie auswählen"}
	}
	var found string
	err := tx.QueryRow(ctx, `SELECT id FROM expenses.categories WHERE household_id=$1 AND id=$2 AND archived_at IS NULL FOR KEY SHARE NOWAIT`, household, category).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invalid{"Eine aktive Kategorie dieses Haushalts auswählen"}
	}
	return err
}

func financeFilterReferences(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, filter FinanceFilter) ([]FinanceResourceRef, error) {
	q := FinanceQuery{Pane: "entries", From: filter.From, To: filter.To, Account: filter.Account, Person: filter.Person, Category: filter.Category, Payee: filter.Payee, State: filter.State}
	if err := validateFinanceQuery(q); err != nil {
		return nil, err
	}
	refs := []FinanceResourceRef{}
	if filter.Account != "" {
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: filter.Account})
		if _, err := financeResourceAccess(ctx, tx, id, scope, "accounts", filter.Account, "view"); err != nil {
			return nil, err
		}
	}
	if filter.Category != "" {
		var category string
		err := tx.QueryRow(ctx, `SELECT id FROM expenses.categories WHERE household_id=$1 AND id=$2`, id.HouseholdID, filter.Category).Scan(&category)
		if err != nil {
			return nil, financeNoRows(err)
		}
	}
	if filter.Person != "" {
		var found string
		err := tx.QueryRow(ctx, `SELECT m.id FROM core.memberships m WHERE m.household_id=$1 AND m.id=$2`, id.HouseholdID, filter.Person).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, Invalid{"Eine Person dieses Haushalts auswählen"}
		}
		if err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func financeOptionalBounds(minimum, maximum *string) (any, any, error) {
	var min, max any
	var low, high int64
	var err error
	if minimum != nil {
		low, err = finance.ParseMinor(*minimum)
		if err != nil {
			return nil, nil, err
		}
		min = low
	}
	if maximum != nil {
		high, err = finance.ParseMinor(*maximum)
		if err != nil {
			return nil, nil, err
		}
		max = high
	}
	if minimum != nil && maximum != nil && low > high {
		return nil, nil, Invalid{"Untere Betragsgrenze darf die obere nicht überschreiten"}
	}
	return min, max, nil
}

func financeMonthBoundary(date string) bool {
	return value.Date(date) && strings.HasSuffix(date, "-01")
}
