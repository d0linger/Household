package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

const FinanceNotificationScanJobKind = "finance_notification_scan_v1"

var errFinanceNotificationHidden = errors.New("financial notification source is not visible")

type financeNotificationContext struct {
	scope                    financialScope
	epoch, membershipVersion int64
	activated                time.Time
	permissionChanged        *time.Time
}

func readFinanceNotificationContext(ctx context.Context, tx pgx.Tx, id auth.Identity) (financeNotificationContext, error) {
	var out financeNotificationContext
	if id.SessionID == "" {
		return out, ErrForbidden
	}
	var err error
	out.scope, err = readFinancialScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT m.version,s.changed_at,coalesce(e.epoch,0),e.updated_at
 FROM core.memberships m JOIN core.module_states s ON s.household_id=m.household_id AND s.module_id='expenses'
 LEFT JOIN core.notification_financial_epochs e ON e.household_id=m.household_id
 WHERE m.household_id=$1 AND m.id=$2 AND m.user_id=$3 AND m.inactive_at IS NULL AND s.state='enabled' AND s.data_generation=$4`, id.HouseholdID, id.MembershipID, id.UserID, out.scope.generation).
		Scan(&out.membershipVersion, &out.activated, &out.epoch, &out.permissionChanged)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

type financeNotificationBudget struct {
	version          int64
	stamp            []byte
	spent, allowance *big.Int
	eligible         bool
}

// Read through the existing row predicate before exposing a budget or adding
// any ledger values. The existing calculation defines this user's exact totals.
func readFinanceNotificationBudget(ctx context.Context, tx pgx.Tx, id auth.Identity, c financeNotificationContext, budgetID, month string) (financeNotificationBudget, error) {
	var out financeNotificationBudget
	predicate, args, err := financeResourcePredicate(c.scope, "category_budgets", "view", "t", 3)
	if err != nil {
		return out, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT to_jsonb(t) FROM expenses.category_budgets t WHERE t.household_id=$1 AND t.id=$2 AND `+predicate,
		append([]any{id.HouseholdID, budgetID}, args...)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errFinanceNotificationHidden
	}
	if err != nil {
		return out, err
	}
	budget, err := financeWireRecord(raw)
	if err != nil {
		return out, err
	}
	out.version = Int(budget["version"])
	var selection FinanceBudgetScope
	raw, err = json.Marshal(budget["scope"])
	if err != nil {
		return out, err
	}
	if err = decodeFinancePayload(raw, &selection); err != nil {
		return out, err
	}
	var accountVersion, personVersion, categoryVersion int64
	var accountActive, personActive, categoryActive bool
	err = tx.QueryRow(ctx, `SELECT coalesce(a.version,0),coalesce(m.version,0),coalesce(cat.version,0),
 $2='' OR(a.id IS NOT NULL AND a.archived_at IS NULL),$3='' OR(m.id IS NOT NULL AND m.inactive_at IS NULL),
 cat.id IS NOT NULL AND cat.archived_at IS NULL
 FROM core.households h LEFT JOIN expenses.accounts a ON a.household_id=h.id AND a.id=nullif($2,'')::uuid
 LEFT JOIN core.memberships m ON m.household_id=h.id AND m.id=nullif($3,'')::uuid
 LEFT JOIN expenses.categories cat ON cat.household_id=h.id AND cat.id=$4 WHERE h.id=$1`, id.HouseholdID, selection.Account, selection.Person, String(budget["category_id"])).
		Scan(&accountVersion, &personVersion, &categoryVersion, &accountActive, &personActive, &categoryActive)
	if err != nil {
		return out, err
	}
	encoded, err := json.Marshal([]any{out.version, c.scope.timezone, month, accountVersion, personVersion, categoryVersion})
	if err != nil {
		return out, err
	}
	stamp := sha256.Sum256(encoded)
	out.stamp = stamp[:]
	if budget["archived_at"] != nil || !accountActive || !personActive || !categoryActive {
		return out, nil
	}
	if err = financeBudgetValues(ctx, tx, id, c.scope, month, budget); err != nil {
		return out, err
	}
	if budget["in_period"] != true || budget["scope_complete"] != true {
		return out, nil
	}
	spent, ok := new(big.Int).SetString(String(budget["spent_minor"]), 10)
	if !ok {
		return out, ErrConflict
	}
	monthly, ok := new(big.Int).SetString(String(budget["monthly_minor"]), 10)
	if !ok {
		return out, ErrConflict
	}
	carry, ok := new(big.Int).SetString(String(budget["carry_minor"]), 10)
	if !ok {
		return out, ErrConflict
	}
	out.spent = spent
	out.allowance = new(big.Int).Add(monthly, carry)
	// The sender's transaction is READ COMMITTED. Reject a mixed configuration
	// if it changed while the authorized aggregate was being calculated.
	var stable bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.category_budgets b
 LEFT JOIN expenses.accounts a ON a.household_id=b.household_id AND a.id=nullif($4,'')::uuid
 LEFT JOIN core.memberships m ON m.household_id=b.household_id AND m.id=nullif($6,'')::uuid
 JOIN expenses.categories cat ON cat.household_id=b.household_id AND cat.id=b.category_id
 WHERE b.household_id=$1 AND b.id=$2 AND b.version=$3 AND b.archived_at IS NULL
 AND($4='' OR(a.version=$5 AND a.archived_at IS NULL)) AND($6='' OR(m.version=$7 AND m.inactive_at IS NULL))
 AND cat.version=$8 AND cat.archived_at IS NULL)`, id.HouseholdID, budgetID, out.version, selection.Account, accountVersion, selection.Person, personVersion, categoryVersion).Scan(&stable)
	if err != nil {
		return out, err
	}
	out.eligible = stable && out.allowance.Sign() > 0
	return out, nil
}

func financeBudgetCrossed(b financeNotificationBudget, threshold int) bool {
	return b.eligible && b.spent != nil && b.allowance != nil &&
		new(big.Int).Mul(b.spent, big.NewInt(100)).Cmp(new(big.Int).Mul(b.allowance, big.NewInt(int64(threshold)))) >= 0
}

func financeBudgetRearmed(b financeNotificationBudget, threshold int) bool {
	return b.eligible && b.spent != nil && b.allowance != nil &&
		new(big.Int).Mul(b.spent, big.NewInt(100)).Cmp(new(big.Int).Mul(b.allowance, big.NewInt(int64(threshold-5)))) < 0
}

func financeBudgetOccurrence(month string, threshold int, episode int64) string {
	return "budget/" + month + "/threshold-" + strconv.Itoa(threshold) + "/episode-" + strconv.FormatInt(episode, 10)
}

func financePaymentOccurrence(day string, stage int) string {
	return "payment/" + day + "/stage-" + strconv.Itoa(stage)
}

func financePaymentIntents(id auth.Identity, s financePaymentReminderSource, now time.Time) ([]notification.Intent, error) {
	if !s.Eligible || s.ConfirmedAt == nil {
		return nil, nil
	}
	var out []notification.Intent
	for _, stage := range []int{7, 0} {
		event, err := notification.CalendarStage(s.DueOn, s.Timezone, "09:00", stage)
		if err != nil {
			return nil, err
		}
		if event.After(now) || !event.Add(24*time.Hour).After(now) || s.ConfirmedAt.After(event) || s.ResponsibleSince.After(event) ||
			s.SourceUpdated.After(event) || s.ModuleActivation.After(event) || s.PermissionChangedAt != nil && s.PermissionChangedAt.After(event) {
			continue
		}
		out = append(out, notification.Intent{Schema: notification.Schema, HouseholdID: id.HouseholdID, UserID: id.UserID, Module: "expenses", Generation: s.ModuleGeneration,
			ResourceType: "payment_reminders", ResourceID: s.EntryID, Revision: s.Version, Occurrence: financePaymentOccurrence(s.DueOn, stage), Category: "expenses",
			EventAt: event, NotBefore: event, ExpiresAt: event.Add(24 * time.Hour)})
	}
	return out, nil
}

// FinanceNotificationAuthority requires an actual current session for this
// recipient. A generic notification membership check never authorizes finance.
func (s *Store) FinanceNotificationAuthority(ctx context.Context, tx pgx.Tx, id auth.Identity, i notification.Intent) (time.Time, error) {
	if i.Validate() != nil || i.Category != "expenses" || i.HouseholdID != id.HouseholdID || i.UserID != id.UserID {
		return time.Time{}, nil
	}
	c, err := readFinanceNotificationContext(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if c.scope.generation != i.Generation || c.activated.After(i.EventAt) {
		return time.Time{}, nil
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	var cutoff time.Time
	switch i.ResourceType {
	case "payment_reminders":
		source, e := readFinancePaymentReminderSource(ctx, tx, id, i.ResourceID)
		if errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
			return time.Time{}, nil
		}
		if e != nil {
			return time.Time{}, e
		}
		intents, e := financePaymentIntents(id, source, now)
		if e != nil {
			return time.Time{}, e
		}
		for _, candidate := range intents {
			if candidate.Revision == i.Revision && candidate.Occurrence == i.Occurrence && candidate.EventAt.Equal(i.EventAt) && candidate.NotBefore.Equal(i.NotBefore) && candidate.ExpiresAt.Equal(i.ExpiresAt) {
				cutoff = i.EventAt
				break
			}
		}
	case "budget_thresholds":
		parts := strings.Split(i.Occurrence, "/")
		if len(parts) != 4 || parts[0] != "budget" {
			return time.Time{}, nil
		}
		threshold, e := strconv.Atoi(strings.TrimPrefix(parts[2], "threshold-"))
		if e != nil || threshold != 80 && threshold != 100 {
			return time.Time{}, nil
		}
		episode, e := strconv.ParseInt(strings.TrimPrefix(parts[3], "episode-"), 10, 64)
		if e != nil || episode < 1 || i.Occurrence != financeBudgetOccurrence(parts[1], threshold, episode) {
			return time.Time{}, nil
		}
		zone, e := time.LoadLocation(c.scope.timezone)
		if e != nil {
			return time.Time{}, e
		}
		if now.In(zone).Format("2006-01") != parts[1] {
			return time.Time{}, nil
		}
		budget, e := readFinanceNotificationBudget(ctx, tx, id, c, i.ResourceID, parts[1])
		// Only the direct visibility-filtered missing row is a clean denial.
		// Existing budget calculation may map a PostgreSQL exception; propagate
		// it so callers roll back rather than write in an aborted transaction.
		if errors.Is(e, errFinanceNotificationHidden) {
			return time.Time{}, nil
		}
		if e != nil {
			return time.Time{}, e
		}
		if budget.version != i.Revision || !financeBudgetCrossed(budget, threshold) {
			return time.Time{}, nil
		}
		var detected time.Time
		err = tx.QueryRow(ctx, `SELECT detected_at,consent_cutoff FROM expenses.notification_budget_observations
 WHERE household_id=$1 AND user_id=$2 AND budget_id=$3 AND threshold=$4 AND module_generation=$5 AND module_activated_at=$6
 AND month=$7::date AND budget_version=$8 AND permission_epoch=$9 AND membership_version=$10 AND auth_version=$11 AND scope_stamp=$12
 AND state='crossed' AND episode=$13 AND detected_at IS NOT NULL AND consent_cutoff IS NOT NULL`, id.HouseholdID, id.UserID, i.ResourceID, threshold, i.Generation, c.activated,
			parts[1]+"-01", budget.version, c.epoch, c.membershipVersion, id.AuthVersion, budget.stamp, episode).Scan(&detected, &cutoff)
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		if err != nil {
			return time.Time{}, err
		}
		if !detected.Equal(i.EventAt) || !detected.Equal(i.NotBefore) || !detected.Add(24*time.Hour).Equal(i.ExpiresAt) || cutoff.After(detected) {
			return time.Time{}, nil
		}
	default:
		return time.Time{}, nil
	}
	// Under READ COMMITTED, do not combine a calculation with a permission change
	// that committed between statements. This takes no epoch-before-policy lock.
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT coalesce((SELECT epoch FROM core.notification_financial_epochs WHERE household_id=$1),0)`, id.HouseholdID).Scan(&epoch); err != nil {
		return time.Time{}, err
	}
	if epoch != c.epoch {
		return time.Time{}, nil
	}
	return cutoff, nil
}
