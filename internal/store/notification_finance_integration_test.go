//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func notificationFinanceOptIn(t *testing.T, f *recipeExpansionFixture, id auth.Identity) NotificationPreference {
	t.Helper()
	p, err := f.store.SaveNotificationPreference(f.ctx, id, NotificationPreference{Category: "expenses", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}})
	if err != nil {
		t.Fatal("explicit finance consent", err)
	}
	return p
}

func notificationFinancePage(f *recipeExpansionFixture, id auth.Identity) (int, error) {
	ctx := audit.WithActor(f.ctx, id.UserID, id.HouseholdID)
	tx, err := f.app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return 0, err
	}
	n, err := f.store.StageFinanceNotificationPage(ctx, tx, id, 10)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

func notificationFinanceCycle(t *testing.T, f *recipeExpansionFixture, id auth.Identity) int {
	t.Helper()
	// Advance only the disposable worker retry clock, never a business deadline.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2 AND dedupe_key LIKE $3`, id.HouseholdID, FinanceNotificationScanJobKind, "finance-scan-v1/"+id.HouseholdID+"/"+id.UserID+"/%"); err != nil {
		t.Fatal(err)
	}
	for pages := 0; pages < 32; pages++ {
		n, err := notificationFinancePage(f, id)
		if err != nil {
			t.Fatal("finance notification page", err)
		}
		if n == 0 {
			return pages
		}
	}
	t.Fatal("finance cursor did not finish within 32 bounded pages")
	return 0
}

func notificationFinanceIntents(t *testing.T, f *recipeExpansionFixture, id auth.Identity, resource string) []notification.Intent {
	t.Helper()
	rows, err := f.owner.Query(f.ctx, `SELECT schema_version,household_id::text,user_id::text,module_id,module_generation,resource_type,resource_id::text,
 source_revision,occurrence,category,event_at,not_before,expires_at FROM core.notification_intents
 WHERE household_id=$1 AND user_id=$2 AND category='expenses' AND ($3='' OR resource_id=nullif($3,'')::uuid) ORDER BY event_at,occurrence`, id.HouseholdID, id.UserID, resource)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []notification.Intent
	for rows.Next() {
		var i notification.Intent
		if err = rows.Scan(&i.Schema, &i.HouseholdID, &i.UserID, &i.Module, &i.Generation, &i.ResourceType, &i.ResourceID, &i.Revision, &i.Occurrence, &i.Category, &i.EventAt, &i.NotBefore, &i.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		if err = i.Validate(); err != nil {
			t.Fatal("invalid persisted contract", err)
		}
		result = append(result, i)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func notificationFinanceCount(t *testing.T, f *recipeExpansionFixture, id auth.Identity, resource string, want int) {
	t.Helper()
	if got := len(notificationFinanceIntents(t, f, id, resource)); got != want {
		t.Fatalf("intent count = %d; want %d", got, want)
	}
}

func notificationFinanceAuthority(t *testing.T, f *recipeExpansionFixture, id auth.Identity, i notification.Intent, want bool) {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	cutoff, err := f.store.FinanceNotificationAuthority(f.ctx, tx, id, i)
	if err != nil || (!cutoff.IsZero()) != want {
		t.Fatal("current finance audience mismatch", want, err)
	}
}

func notificationFinancePost(t *testing.T, f *recipeExpansionFixture, category, minor, on string) (string, int64) {
	t.Helper()
	rid, in := value.UUID(), financeNativeEntry(category, minor)
	in.OccurredOn = on
	r := financeNativeApply(t, f, "entries", "post", rid, 0, in)
	return rid, Int(r["saved_version"])
}

func notificationFinanceRefund(t *testing.T, f *recipeExpansionFixture, category, original string, version int64, minor, on string) {
	t.Helper()
	in := financeNativeEntry(category, "-"+minor)
	in.Kind, in.OccurredOn = "refund", on
	financeNativeApply(t, f, "entries", "refund", value.UUID(), 0, Record{"original_entry_id": original, "original_version": version, "entry": in})
}

func notificationFinanceEpoch(t *testing.T, f *recipeExpansionFixture) int64 {
	t.Helper()
	var epoch int64
	if err := f.owner.QueryRow(f.ctx, `SELECT coalesce((SELECT epoch FROM core.notification_financial_epochs WHERE household_id=$1),0)`, f.identity.HouseholdID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	return epoch
}

func notificationFinanceObservation(t *testing.T, f *recipeExpansionFixture, id auth.Identity, budget string, threshold int, want string, episode int64) {
	t.Helper()
	var state string
	var actual int64
	if err := f.owner.QueryRow(f.ctx, `SELECT state,episode FROM expenses.notification_budget_observations WHERE household_id=$1 AND user_id=$2 AND budget_id=$3 AND threshold=$4`, id.HouseholdID, id.UserID, budget, threshold).Scan(&state, &actual); err != nil || state != want || actual != episode {
		t.Fatal("budget observation", state, actual, "want", want, episode, err)
	}
}

func TestNotificationFinanceBudgetEpisodesAndExactCarry(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.households SET timezone='UTC' WHERE id=$1`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	notificationFinanceOptIn(t, f, f.identity)
	now := time.Now().UTC()
	today, month := now.Format(time.DateOnly), now.Format("2006-01")
	category, budget := financeNativeCategory(t, f), value.UUID()
	in := FinanceBudgetInput{Name: "Exact boundary budget", CategoryID: category, StartsOn: month + "-01", MonthlyMinor: "10000", CarryRule: "none", InitialCarryMinor: "0"}
	receipt := financeNativeApply(t, f, "budgets", "save", budget, 0, in)
	epoch := notificationFinanceEpoch(t, f)
	notificationFinanceCycle(t, f, f.identity)
	original, version := notificationFinancePost(t, f, category, "7999", today)
	notificationFinanceCycle(t, f, f.identity)
	notificationFinanceCount(t, f, f.identity, budget, 0)
	notificationFinancePost(t, f, category, "1", today)
	notificationFinanceCycle(t, f, f.identity)
	notificationFinanceCount(t, f, f.identity, budget, 1)
	notificationFinancePost(t, f, category, "2000", today)
	notificationFinanceCycle(t, f, f.identity)
	notificationFinanceCount(t, f, f.identity, budget, 2)
	if notificationFinanceEpoch(t, f) != epoch {
		t.Fatal("ordinary ledger posting reset financial permission epoch")
	}
	for _, i := range notificationFinanceIntents(t, f, f.identity, budget) {
		notificationFinanceAuthority(t, f, f.identity, i, true)
	}
	t.Run("strict hysteresis and append-only refunds", func(t *testing.T) {
		notificationFinanceRefund(t, f, category, original, version, "501", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceObservation(t, f, f.identity, budget, 100, "healthy", 1)
		notificationFinancePost(t, f, category, "501", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, budget, 3)
		notificationFinanceRefund(t, f, category, original, version, "2500", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceObservation(t, f, f.identity, budget, 80, "crossed", 1)
		notificationFinanceRefund(t, f, category, original, version, "100", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceObservation(t, f, f.identity, budget, 80, "healthy", 1)
		notificationFinancePost(t, f, category, "600", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, budget, 4)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, budget, 4)
	})
	t.Run("configuration and month reset preserve episode without replay", func(t *testing.T) {
		old := notificationFinanceIntents(t, f, f.identity, budget)
		in.Name = "Reviewed updated budget"
		financeNativeApply(t, f, "budgets", "save", budget, Int(receipt["saved_version"]), in)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceObservation(t, f, f.identity, budget, 80, "baseline", 2)
		notificationFinanceCount(t, f, f.identity, budget, 4)
		for _, i := range old {
			notificationFinanceAuthority(t, f, f.identity, i, false)
		}
		// Model a persisted preceding-month worker snapshot; ledger dates stay real.
		if _, err := f.owner.Exec(f.ctx, `UPDATE expenses.notification_budget_observations SET month=$4::date WHERE household_id=$1 AND user_id=$2 AND budget_id=$3`, f.identity.HouseholdID, f.identity.UserID, budget, now.AddDate(0, -1, 0).Format("2006-01")+"-01"); err != nil {
			t.Fatal(err)
		}
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, budget, 4)
		notificationFinanceObservation(t, f, f.identity, budget, 80, "baseline", 2)
	})
	t.Run("initially over budget is silent until healthy and crossed again", func(t *testing.T) {
		cat, b := financeNativeCategory(t, f), value.UUID()
		e, v := notificationFinancePost(t, f, cat, "1000", today)
		financeNativeApply(t, f, "budgets", "save", b, 0, FinanceBudgetInput{Name: "No old overspend", CategoryID: cat, StartsOn: month + "-01", MonthlyMinor: "1000", CarryRule: "none", InitialCarryMinor: "0"})
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, b, 0)
		notificationFinanceObservation(t, f, f.identity, b, 80, "baseline", 0)
		notificationFinanceRefund(t, f, cat, e, v, "300", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinancePost(t, f, cat, "100", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, b, 1)
	})
	t.Run("signed actual carry determines exact threshold", func(t *testing.T) {
		cat, b := financeNativeCategory(t, f), value.UUID()
		previous := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
		notificationFinancePost(t, f, cat, "1600", previous.Format(time.DateOnly))
		financeNativeApply(t, f, "budgets", "save", b, 0, FinanceBudgetInput{Name: "Carry threshold", CategoryID: cat, StartsOn: previous.Format(time.DateOnly), MonthlyMinor: "1000", CarryRule: "all", InitialCarryMinor: "100"})
		notificationFinanceCycle(t, f, f.identity)
		notificationFinancePost(t, f, cat, "399", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, b, 0)
		notificationFinancePost(t, f, cat, "1", today)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, b, 1)
		row := financeSupportDetail(t, f, "budgets", b, month)
		if row["carry_minor"] != "-500" || row["spent_minor"] != "400" {
			t.Fatal("actual carry basis differs", row)
		}
	})
}

func notificationFinanceAccess(t *testing.T, f *recipeExpansionFixture, kind, rid string, grants []financeGrantInput) {
	t.Helper()
	if kind != "accounts" && kind != "category_budgets" {
		t.Fatal("unsupported fixture permission target")
	}
	for attempt := 0; attempt < 2; attempt++ {
		id := f.identity
		generation := f.store.States()["expenses"].Generation
		var raw []byte
		// Ask the same authenticated authority contract used by financial details,
		// explicitly without the metadata-only administrator recovery projection.
		err := f.app.QueryRow(f.ctx, `SELECT core.finance_access_projection($1,$2,$3,$4,$5,$6,$7,$8,false)`, id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, generation, kind, rid).Scan(&raw)
		if err != nil {
			t.Fatal("current access projection", err)
		}
		var info Record
		if err = json.Unmarshal(raw, &info); err != nil {
			t.Fatal(err)
		}
		var version int64
		if err = f.owner.QueryRow(f.ctx, `SELECT version FROM `+pgx.Identifier{"expenses", kind}.Sanitize()+` WHERE household_id=$1 AND id=$2`, id.HouseholdID, rid).Scan(&version); err != nil {
			t.Fatal(err)
		}
		action := "change"
		payload := Record{"target_kind": kind, "target_id": rid, "policy_version": info["policy_version"], "access_mode": "selected", "responsible_membership_id": id.MembershipID, "grants": grants}
		if info["can_assign"] == true {
			action = "assign"
			payload = Record{"target_kind": kind, "target_id": rid, "policy_version": info["policy_version"], "responsible_membership_id": id.MembershipID}
		}
		raw, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		c := FinanceCommand{Resource: "access", Action: action, ID: rid, Version: version, Generation: generation, AccessVersion: String(info["access_version"]), Payload: raw}
		key := value.UUID()
		p, err := f.store.FinancePreview(f.ctx, id, key, value.UUID(), c)
		if err != nil {
			t.Fatal("review actual permission change", action, err)
		}
		c.ReviewHash = String(p["impact_hash"])
		if _, err = f.store.FinanceApply(f.ctx, id, key, value.UUID(), c); err != nil {
			t.Fatal("apply actual permission change", action, err)
		}
		if action == "change" {
			return
		}
	}
	t.Fatal("permission change did not finish after explicit self assignment")
}

func TestNotificationFinancePagingAndPermissionBaselines(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.households SET timezone='UTC' WHERE id=$1`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	notificationFinanceOptIn(t, f, f.identity)
	today, month := time.Now().UTC().Format(time.DateOnly), time.Now().UTC().Format("2006-01")
	category := financeNativeCategory(t, f)
	for n := 0; n < 13; n++ {
		financeNativeApply(t, f, "budgets", "save", value.UUID(), 0, FinanceBudgetInput{Name: fmt.Sprintf("Paged budget %02d", n), CategoryID: category, StartsOn: month + "-01", MonthlyMinor: "1000", CarryRule: "none", InitialCarryMinor: "0"})
	}
	if pages := notificationFinanceCycle(t, f, f.identity); pages != 3 {
		t.Fatal("13 budgets did not use payments plus two bounded budget pages", pages)
	}
	var observations int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.notification_budget_observations WHERE household_id=$1 AND user_id=$2`, f.identity.HouseholdID, f.identity.UserID).Scan(&observations); err != nil || observations != 26 {
		t.Fatal("cursor missed budgets beyond first page", observations, err)
	}
	notificationFinancePost(t, f, category, "800", today)
	t.Run("concurrent pages serialize without duplicate episodes", func(t *testing.T) {
		if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2`, f.identity.HouseholdID, FinanceNotificationScanJobKind); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		start := make(chan struct{})
		for n := 0; n < 2; n++ {
			wg.Go(func() { <-start; _, err := notificationFinancePage(f, f.identity); errs <- err })
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			var pg *pgconn.PgError
			if err != nil && (!errors.As(err, &pg) || pg.Code != "40001") {
				t.Fatal("unexpected concurrent page error", err)
			}
		}
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, "", 13)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, "", 13)
	})
	t.Run("new visibility silently rebaselines old spend and revoke denies content", func(t *testing.T) {
		reader := financeAuthorityMember(t, f, "reader")
		notificationFinanceOptIn(t, f, reader)
		cat, budget := financeNativeCategory(t, f), value.UUID()
		account := financeAuthorityInitialAccount(t, f, f.identity, []FinanceInitialGrant{{MembershipID: f.identity.MembershipID, Permission: "edit"}})
		entry, input := value.UUID(), financeNativeEntry(cat, "1000")
		input.OccurredOn = today
		input.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: 1, NativeMinor: "1000"}
		financeNativeApply(t, f, "entries", "post", entry, 0, input)
		financeNativeApply(t, f, "budgets", "save", budget, 0, FinanceBudgetInput{Name: "Current authorized scope only", CategoryID: cat, StartsOn: month + "-01", MonthlyMinor: "1000", CarryRule: "none", InitialCarryMinor: "0"})
		notificationFinanceCycle(t, f, reader)
		notificationFinanceObservation(t, f, reader, budget, 80, "healthy", 0)
		before := notificationFinanceEpoch(t, f)
		notificationFinanceAccess(t, f, "accounts", account, []financeGrantInput{{MembershipID: f.identity.MembershipID, Permission: "edit"}, {MembershipID: reader.MembershipID, Permission: "view"}})
		if notificationFinanceEpoch(t, f) <= before {
			t.Fatal("grant-only change failed to advance epoch")
		}
		notificationFinanceCycle(t, f, reader)
		notificationFinanceCount(t, f, reader, budget, 0)
		notificationFinanceObservation(t, f, reader, budget, 80, "baseline", 0)
		if _, err := f.store.FinanceView(f.ctx, reader, FinanceQuery{Pane: "entries", ID: entry}); err != nil {
			t.Fatal("reviewed view grant not usable", err)
		}
		// A separate shared budget crosses freshly for this reader, then becomes private.
		visibleCat, visibleBudget := financeNativeCategory(t, f), value.UUID()
		financeNativeApply(t, f, "budgets", "save", visibleBudget, 0, FinanceBudgetInput{Name: "Private-budget-content-sentinel", CategoryID: visibleCat, StartsOn: month + "-01", MonthlyMinor: "1000", CarryRule: "none", InitialCarryMinor: "0"})
		notificationFinanceCycle(t, f, reader)
		notificationFinancePost(t, f, visibleCat, "800", today)
		notificationFinanceCycle(t, f, reader)
		intents := notificationFinanceIntents(t, f, reader, visibleBudget)
		if len(intents) != 1 {
			t.Fatal("missing fresh reader budget crossing", len(intents))
		}
		notificationFinanceAuthority(t, f, reader, intents[0], true)
		notificationFinanceAccess(t, f, "category_budgets", visibleBudget, []financeGrantInput{{MembershipID: f.identity.MembershipID, Permission: "edit"}})
		notificationFinanceAuthority(t, f, reader, intents[0], false)
		out, err := f.store.FinanceView(f.ctx, reader, FinanceQuery{Pane: "planning", Section: "budgets", ID: visibleBudget, Month: month})
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
			t.Fatal("denied budget detail returned", err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "Private-budget-content-sentinel") {
			t.Fatal("denied financial content escaped")
		}
	})
}

// notificationFinanceHistoricalReminder models elapsed source clocks only in the
// isolated owner fixture. It is not evidence of real elapsed time or OS delivery.
// The entry and confirmation must already exist through reviewed application APIs.
func notificationFinanceHistoricalReminder(t *testing.T, f *recipeExpansionFixture, entry string, before time.Time) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	var isolated bool
	if err = tx.QueryRow(f.ctx, `SELECT current_database()='household_test' AND session_user='household_owner' AND current_user='household_owner'`).Scan(&isolated); err != nil || !isolated {
		t.Fatal("historical clock helper requires isolated fixture owner", err)
	}
	const facts = `SELECT jsonb_build_object('entry',to_jsonb(e)-'updated_at','reminder',to_jsonb(r)-ARRAY['created_at','updated_at','confirmed_at','responsible_since','completed_at'],
 'origin',to_jsonb(o)-'row_hash')::text FROM expenses.entries e JOIN expenses.notification_payment_reminders r ON r.household_id=e.household_id AND r.id=e.id
 JOIN core.finance_origin_resources o ON o.household_id=e.household_id AND o.resource_type='entries' AND o.resource_id=e.id AND o.retired_at IS NULL
 WHERE e.household_id=$1 AND e.id=$2 AND r.state='confirmed'`
	var original, after string
	if err = tx.QueryRow(f.ctx, facts, f.identity.HouseholdID, entry).Scan(&original); err != nil {
		t.Fatal("require existing real confirmed source", err)
	}
	var enabled bool
	if err = tx.QueryRow(f.ctx, `SELECT tgenabled='O' FROM pg_trigger WHERE tgrelid='expenses.notification_payment_reminders'::regclass AND tgname='notification_payment_guard'`).Scan(&enabled); err != nil || !enabled {
		t.Fatal("source trigger not enabled before clock fixture", err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE expenses.notification_payment_reminders DISABLE TRIGGER notification_payment_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE expenses.notification_payment_reminders SET created_at=$3,updated_at=$3,confirmed_at=$3,responsible_since=$3 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, entry, before); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE expenses.notification_payment_reminders ENABLE TRIGGER notification_payment_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE expenses.entries SET updated_at=$3 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, entry, before); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE core.finance_origin_resources o SET row_hash=sha256(convert_to(to_jsonb(e)::text,'UTF8')) FROM expenses.entries e
 WHERE e.household_id=$1 AND e.id=$2 AND o.household_id=e.household_id AND o.resource_type='entries' AND o.resource_id=e.id AND o.retired_at IS NULL`, f.identity.HouseholdID, entry); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(f.ctx, facts, f.identity.HouseholdID, entry).Scan(&after); err != nil || original != after {
		t.Fatal("clock fixture changed business, version or origin facts", err)
	}
	if err = tx.QueryRow(f.ctx, `SELECT t.tgenabled='O' AND o.row_hash=sha256(convert_to(to_jsonb(e)::text,'UTF8'))
 FROM pg_trigger t,expenses.entries e JOIN core.finance_origin_resources o ON o.household_id=e.household_id AND o.resource_type='entries' AND o.resource_id=e.id AND o.retired_at IS NULL
 WHERE t.tgrelid='expenses.notification_payment_reminders'::regclass AND t.tgname='notification_payment_guard' AND e.household_id=$1 AND e.id=$2`, f.identity.HouseholdID, entry).Scan(&enabled); err != nil || !enabled {
		t.Fatal("fixture did not restore trigger/hash", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func notificationFinanceCalendar(t *testing.T, f *recipeExpansionFixture) time.Time {
	t.Helper()
	var now time.Time
	if err := f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	zone := fmt.Sprintf("Etc/GMT%+d", now.UTC().Hour()-12)
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.households SET timezone=$2 WHERE id=$1`, f.identity.HouseholdID, zone); err != nil {
		t.Fatal(err)
	}
	local := now.In(loc)
	event := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, loc)
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.module_states SET changed_at=$2 WHERE household_id=$1 AND module_id='expenses'`, f.identity.HouseholdID, event.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.notification_financial_epochs SET updated_at=$2 WHERE household_id=$1`, f.identity.HouseholdID, event.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestNotificationFinancePaymentDatesAndCurrentAuthority(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	event := notificationFinanceCalendar(t, f)
	pref := notificationFinanceOptIn(t, f, f.identity)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.notification_preferences SET enabled_since=$3 WHERE household_id=$1 AND user_id=$2 AND category='expenses'`, f.identity.HouseholdID, f.identity.UserID, event.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	category := financeNativeCategory(t, f)
	makeReminder := func(t *testing.T, days int, historical bool) (string, FinanceEntryInput) {
		t.Helper()
		entry, input := value.UUID(), financeNativeEntry(category, "1250")
		input.OccurredOn = event.Format(time.DateOnly)
		financeNativeApply(t, f, "entries", "save", entry, 0, input)
		in := notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID)
		in.DueOn = event.AddDate(0, 0, days).Format(time.DateOnly)
		notificationFinanceReminderSave(t, f, entry, in)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID))
		if historical {
			notificationFinanceHistoricalReminder(t, f, entry, event.Add(-time.Hour))
		}
		return entry, input
	}
	t.Run("due and seven-day stages retain original event and deadline", func(t *testing.T) {
		for _, days := range []int{0, 7} {
			entry, _ := makeReminder(t, days, true)
			notificationFinanceCycle(t, f, f.identity)
			intents := notificationFinanceIntents(t, f, f.identity, entry)
			if len(intents) != 1 {
				t.Fatal("missing confirmed date stage", days, len(intents))
			}
			i := intents[0]
			if !i.EventAt.Equal(event) || !i.NotBefore.Equal(event) || !i.ExpiresAt.Equal(event.Add(24*time.Hour)) || i.Occurrence != financePaymentOccurrence(event.AddDate(0, 0, days).Format(time.DateOnly), days) {
				t.Fatal("date stage moved to polling time", i.Occurrence)
			}
			notificationFinanceAuthority(t, f, f.identity, i, true)
			notificationFinanceCycle(t, f, f.identity)
			notificationFinanceCount(t, f, f.identity, entry, 1)
			notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "complete", f.identity.MembershipID))
			notificationFinanceAuthority(t, f, f.identity, i, false)
		}
	})
	t.Run("late confirmation does not replay an elapsed stage", func(t *testing.T) {
		entry, _ := makeReminder(t, 0, false)
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, entry, 0)
	})
	t.Run("source edits and reassignment fence the original recipient", func(t *testing.T) {
		entry, input := makeReminder(t, 0, true)
		notificationFinanceCycle(t, f, f.identity)
		intents := notificationFinanceIntents(t, f, f.identity, entry)
		if len(intents) != 1 {
			t.Fatal("missing source intent")
		}
		source := notificationFinanceReminderSource(t, f, f.identity, entry)
		input.Description = "Reviewed source change"
		input.Lines[0].Version = 1
		input.Baseline = []expenseedit.LineVersion{{ID: input.Lines[0].ID, Version: 1}}
		financeNativeApply(t, f, "entries", "save", entry, source.EntryVersion, input)
		notificationFinanceAuthority(t, f, f.identity, intents[0], false)
		other := financeAuthorityMember(t, f, "reader")
		reassign := notificationFinanceReminderInput(t, f, entry, "save", other.MembershipID)
		reassign.ResponsibleMemberID = other.MembershipID
		notificationFinanceReminderSave(t, f, entry, reassign)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", other.MembershipID))
		notificationFinanceAuthority(t, f, f.identity, intents[0], false)
		notificationFinanceOptIn(t, f, other)
		notificationFinanceCycle(t, f, other)
		notificationFinanceCount(t, f, other, entry, 0)
	})
	t.Run("real account payment cancels source eligibility without a paid flag", func(t *testing.T) {
		account, accountVersion := financeNativeAccount(t, f, "EUR", 2)
		entry, input := value.UUID(), financeNativeEntry(category, "1250")
		input.OccurredOn = event.Format(time.DateOnly)
		input.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: accountVersion, NativeMinor: "1250"}
		financeNativeApply(t, f, "entries", "save", entry, 0, input)
		in := notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID)
		in.DueOn = event.Format(time.DateOnly)
		notificationFinanceReminderSave(t, f, entry, in)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID))
		notificationFinanceHistoricalReminder(t, f, entry, event.Add(-time.Hour))
		notificationFinanceCycle(t, f, f.identity)
		intents := notificationFinanceIntents(t, f, f.identity, entry)
		if len(intents) != 1 {
			t.Fatal("draft payment plan was mistaken for real payment", len(intents))
		}
		source := notificationFinanceReminderSource(t, f, f.identity, entry)
		input.Lines[0].Version = 1
		input.Baseline = []expenseedit.LineVersion{{ID: input.Lines[0].ID, Version: 1}}
		financeNativeApply(t, f, "entries", "post", entry, source.EntryVersion, input)
		if !notificationFinanceReminderSource(t, f, f.identity, entry).VerifiedPaid {
			t.Fatal("real posted account movement was not recognized")
		}
		notificationFinanceAuthority(t, f, f.identity, intents[0], false)
		wrongGeneration := intents[0]
		wrongGeneration.Generation++
		notificationFinanceAuthority(t, f, f.identity, wrongGeneration, false)
	})
	t.Run("renewed consent does not backfill even a still fresh source", func(t *testing.T) {
		entry, _ := makeReminder(t, 0, true)
		pref.Enabled = false
		var err error
		pref, err = f.store.SaveNotificationPreference(f.ctx, f.identity, pref)
		if err != nil {
			t.Fatal(err)
		}
		pref.Enabled = true
		if _, err = f.store.SaveNotificationPreference(f.ctx, f.identity, pref); err != nil {
			t.Fatal(err)
		}
		notificationFinanceCycle(t, f, f.identity)
		notificationFinanceCount(t, f, f.identity, entry, 0)
	})
}
