package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

func financeBudgetValues(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, month string, budget Record) error {
	start, err := time.Parse(time.DateOnly, String(budget["starts_on"]))
	if err != nil {
		return err
	}
	target, err := time.Parse("2006-01", month)
	if err != nil {
		return Invalid{"Budgetmonat auswählen"}
	}
	budget["currency"], budget["exponent"] = "EUR", 2
	budget["scope_label"] = "Verbrauch und Übertrag aktuell sichtbarer Buchungen"
	budget["scope_basis"] = "current_authorized_bookings"
	if target.Before(start) || budget["ends_on"] != nil && target.Format(time.DateOnly) > String(budget["ends_on"]) {
		budget["in_period"], budget["spent_minor"], budget["carry_minor"], budget["available_minor"] = false, nil, nil, nil
		return nil
	}
	budget["in_period"] = true
	var selection FinanceBudgetScope
	raw, err := json.Marshal(budget["scope"])
	if err != nil {
		return err
	}
	if err = decodeFinancePayload(raw, &selection); err != nil {
		return err
	}
	if _, err = financeFilterReferences(ctx, tx, id, scope, FinanceFilter{Account: selection.Account, Person: selection.Person, Category: String(budget["category_id"])}); err != nil {
		return err
	}
	count := (target.Year()-start.Year())*12 + int(target.Month()-start.Month()) + 1
	q := FinanceQuery{Pane: "overview", Month: month, Category: String(budget["category_id"]), Account: selection.Account, Person: selection.Person}
	where, args, err := financeExpenseSelection(scope, q)
	if err != nil {
		return err
	}
	if count > 120 {
		var spent string
		if err = tx.QueryRow(ctx, `SELECT coalesce(sum(`+financeConsumptionExpression()+`),0)::text FROM expenses.entries t WHERE t.state='posted' AND `+where, args...).Scan(&spent); err != nil {
			return err
		}
		budget["spent_minor"], budget["carry_minor"], budget["available_minor"], budget["scope_complete"] = spent, nil, nil, false
		budget["reason"] = "Übertrag umfasst mehr als120Monate; einen ausdrücklich geprüften neuen Budgetbeginn verwenden"
		return nil
	}
	// These trusted date replacements are bounded to120months above. The public
	// report window remains two years; carry is one grouped authorized snapshot.
	args[1] = start.Format(time.DateOnly)
	rows, err := tx.Query(ctx, `SELECT to_char(t.occurred_on,'YYYY-MM'),coalesce(sum(`+financeConsumptionExpression()+`),0)::text
 FROM expenses.entries t WHERE t.state='posted' AND `+where+` GROUP BY to_char(t.occurred_on,'YYYY-MM')`, args...)
	if err != nil {
		return err
	}
	spends := map[string]*big.Int{}
	for rows.Next() {
		var key, amount string
		if err = rows.Scan(&key, &amount); err != nil {
			rows.Close()
			return err
		}
		n, ok := new(big.Int).SetString(amount, 10)
		if !ok {
			rows.Close()
			return finance.ErrMoney
		}
		spends[key] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	monthly, ok := new(big.Int).SetString(String(budget["monthly_minor"]), 10)
	if !ok {
		return finance.ErrMoney
	}
	carry, ok := new(big.Int).SetString(String(budget["initial_carry_minor"]), 10)
	if !ok {
		return finance.ErrMoney
	}
	for date := start; !date.After(target); date = date.AddDate(0, 1, 0) {
		spent := spends[date.Format("2006-01")]
		if spent == nil {
			spent = new(big.Int)
		}
		available := new(big.Int).Sub(new(big.Int).Add(carry, monthly), spent)
		if date.Equal(target) {
			budget["spent_minor"], budget["carry_minor"], budget["available_minor"], budget["scope_complete"] = spent.String(), carry.String(), available.String(), true
			return nil
		}
		switch String(budget["carry_rule"]) {
		case "none":
			carry = new(big.Int)
		case "positive_only":
			carry = available
			if carry.Sign() < 0 {
				carry = new(big.Int)
			}
		case "all":
			carry = available
		default:
			return Invalid{"Unbekannte gespeicherte Übertragsregel"}
		}
	}
	return nil
}

func financeGoalValues(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, goal Record) error {
	zone, err := time.LoadLocation(scope.timezone)
	if err != nil {
		return err
	}
	today := scope.readAt.In(zone).Format(time.DateOnly)
	predicate, args, err := financeResourcePredicate(scope, "goal_contributions", "view", "t", 4)
	if err != nil {
		return err
	}
	args = append([]any{id.HouseholdID, String(goal["id"]), today}, args...)
	var actual, planned string
	err = tx.QueryRow(ctx, `SELECT coalesce(sum(CASE
 WHEN t.contribution_kind IN('manual_recorded','ledger_recorded') AND t.occurred_on<=$3::date THEN t.amount_minor::numeric
 WHEN t.contribution_kind='reversal' AND original.contribution_kind IN('manual_recorded','ledger_recorded') AND t.occurred_on<=$3::date THEN -t.amount_minor::numeric ELSE 0 END),0)::text,
 coalesce(sum(CASE WHEN t.contribution_kind='planned' THEN t.amount_minor::numeric
 WHEN t.contribution_kind='reversal' AND original.contribution_kind='planned' THEN -t.amount_minor::numeric ELSE 0 END),0)::text
 FROM expenses.goal_contributions t LEFT JOIN expenses.goal_contributions original
 ON original.household_id=t.household_id AND original.id=t.reverses_contribution_id
 WHERE t.household_id=$1 AND t.goal_id=$2 AND `+predicate, args...).Scan(&actual, &planned)
	if err != nil {
		return err
	}
	target, ok := new(big.Int).SetString(String(goal["target_minor"]), 10)
	if !ok {
		return finance.ErrMoney
	}
	recorded, ok := new(big.Int).SetString(actual, 10)
	if !ok {
		return finance.ErrMoney
	}
	goal["recorded_minor"], goal["planned_minor"], goal["remaining_minor"] = actual, planned, new(big.Int).Sub(target, recorded).String()
	goal["progress_label"] = "Aktuell sichtbare bestätigte Beiträge; kein Kontostand"
	goal["progress_as_of"] = today
	return nil
}

func financeContributionCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "reverse" {
		var in FinanceContributionReverse
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		if c.Version != 0 || !finance.CivilDate(in.OccurredOn) || !financeText(in.Note, 0, 1000) {
			return nil, Invalid{"Ausgleich des Beitrags als neue geprüfte Tatsache erfassen"}
		}
		original, err := financeSupportRecord(ctx, tx, id, scope, "goal_contributions", in.OriginalID, true)
		if err != nil {
			return nil, err
		}
		if Int(original["version"]) != in.OriginalVersion || original["contribution_kind"] == "reversal" {
			return nil, ErrConflict
		}
		if in.OccurredOn < String(original["occurred_on"]) {
			return nil, Invalid{"Gegenbeitrag darf nicht vor dem ursprünglichen Beitrag liegen"}
		}
		var reversed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.goal_contributions WHERE household_id=$1 AND reverses_contribution_id=$2)`, id.HouseholdID, in.OriginalID).Scan(&reversed); err != nil {
			return nil, err
		}
		if reversed {
			return nil, ErrConflict
		}
		return financeSaveSupport(withFinanceOrigin(ctx, "reversal", []FinanceResourceRef{{Kind: "goal_contributions", ID: in.OriginalID}}), tx, id, scope, c, "goal_contributions",
			[]string{"goal_id", "occurred_on", "amount_minor", "contribution_kind", "event_id", "movement_id", "reverses_contribution_id", "note"},
			[]any{String(original["goal_id"]), in.OccurredOn, Int(original["amount_minor"]), "reversal", original["event_id"], original["movement_id"], in.OriginalID, in.Note},
			[]FinanceResourceRef{{Kind: "goal_contributions", ID: in.OriginalID}}, in)
	}
	var in FinanceContributionInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	amount, err := finance.ParseMinor(in.AmountMinor)
	if err != nil || amount <= 0 || !finance.CivilDate(in.OccurredOn) || !financeText(in.Note, 0, 1000) ||
		in.ContributionKind != "planned" && in.ContributionKind != "manual_recorded" && in.ContributionKind != "ledger_recorded" {
		return nil, Invalid{"Beitragsart, Datum und positiven Zielbetrag prüfen"}
	}
	goal, err := financeSupportRecord(ctx, tx, id, scope, "savings_goals", in.GoalID, true)
	if err != nil {
		return nil, err
	}
	if Int(goal["version"]) != in.GoalVersion || goal["state"] != "active" {
		return nil, ErrConflict
	}
	refs := []FinanceResourceRef{{Kind: "savings_goals", ID: in.GoalID}}
	if c.Version > 0 {
		old, readErr := financeSupportRecord(ctx, tx, id, scope, "goal_contributions", c.ID, true)
		if readErr != nil {
			return nil, readErr
		}
		if old["contribution_kind"] != "planned" || in.ContributionKind != "planned" {
			return nil, Invalid{"Tatsächliche Beiträge bleiben unverändert; einen verknüpften Gegenbeitrag erfassen"}
		}
	}
	if in.ContributionKind == "ledger_recorded" {
		if in.EventID == nil || in.MovementID == nil {
			return nil, Invalid{"Tatsächlichen Zahlungseingang ausdrücklich auswählen"}
		}
		if _, err = financeResourceAccess(ctx, tx, id, scope, "account_movements", *in.MovementID, "edit"); err != nil {
			return nil, err
		}
		var event, account, code, state string
		var exponent int
		var native int64
		err = tx.QueryRow(ctx, `SELECT m.event_id,m.account_id,m.currency_code,m.currency_exponent,m.amount_minor,e.state
 FROM expenses.account_movements m JOIN expenses.cash_events e ON e.household_id=m.household_id AND e.id=m.event_id
 WHERE m.household_id=$1 AND m.id=$2 FOR UPDATE OF m NOWAIT`, id.HouseholdID, *in.MovementID).Scan(&event, &account, &code, &exponent, &native, &state)
		if err != nil {
			return nil, financeNoRows(err)
		}
		if event != *in.EventID || state != "posted" || native <= 0 || code != String(goal["currency_code"]) || int64(exponent) != Int(goal["currency_exponent"]) || goal["account_id"] != nil && account != String(goal["account_id"]) {
			return nil, Invalid{"Beitrag und Ziel müssen zum bestätigten Eingang und seiner Währung passen"}
		}
		var used string
		err = tx.QueryRow(ctx, `SELECT coalesce(sum(CASE WHEN contribution_kind='ledger_recorded' THEN amount_minor::numeric WHEN contribution_kind='reversal' THEN -amount_minor::numeric ELSE 0 END),0)::text
 FROM expenses.goal_contributions WHERE household_id=$1 AND movement_id=$2`, id.HouseholdID, *in.MovementID).Scan(&used)
		if err != nil {
			return nil, err
		}
		claimed, ok := new(big.Int).SetString(used, 10)
		if !ok {
			return nil, finance.ErrMoney
		}
		if new(big.Int).Add(claimed, big.NewInt(amount)).Cmp(big.NewInt(native)) > 0 {
			return nil, ErrConflict
		}
		refs = append(refs, FinanceResourceRef{Kind: "account_movements", ID: *in.MovementID}, FinanceResourceRef{Kind: "cash_events", ID: *in.EventID})
	} else if in.EventID != nil || in.MovementID != nil {
		return nil, Invalid{"Geplante/manuelle Beiträge behaupten keine automatische Kontozahlung"}
	}
	return financeSaveSupport(ctx, tx, id, scope, c, "goal_contributions",
		[]string{"goal_id", "occurred_on", "amount_minor", "contribution_kind", "event_id", "movement_id", "note"},
		[]any{in.GoalID, in.OccurredOn, amount, in.ContributionKind, in.EventID, in.MovementID, in.Note}, refs, in)
}

func financeInt(raw string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, errors.New("invalid exact financial aggregate")
	}
	return n, nil
}
