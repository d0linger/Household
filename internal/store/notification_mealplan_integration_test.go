//go:build integration

package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/notification"
	"household/internal/value"
)

type notificationPlanningFixture struct {
	f                      *recipeExpansionFixture
	plan, meal, date, zone string
	at                     time.Time
}

func newNotificationPlanningFixture(t *testing.T) *notificationPlanningFixture {
	t.Helper()
	f := newRecipeExpansionFixture(t)
	var now time.Time
	var zone string
	if err := f.app.QueryRow(f.ctx, `SELECT clock_timestamp(),timezone FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&now, &zone); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(24 * time.Hour).Truncate(time.Minute).In(loc)
	date := at.Format(time.DateOnly)
	m, _ := f.store.Module("mealplan")
	e, _ := m.Entity("plans")
	plan, err := f.store.Write(f.ctx, f.identity, m, e, "", value.UUID(), value.UUID(), Mutation{Generation: f.store.States()["mealplan"].Generation,
		Data: energyRaw(Record{"name": "Explicit preparation fixture", "starts_on": date, "ends_on": at.AddDate(0, 0, 13).Format(time.DateOnly)})})
	if err != nil {
		t.Fatal("create actual plan", err)
	}
	pid := String(plan["id"])
	out, err := f.store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), PlanningInput{Generation: f.store.States()["mealplan"].Generation,
		PlanVersion: Int(plan["version"]), Action: "save", Week: date, Meal: PlanningMealInput{Title: "Explicit meal", PlannedOn: date, Slot: "dinner", Kind: "free", Status: "planned", Servings: "1"}})
	if err != nil {
		t.Fatal("create actual planned meal", err)
	}
	return &notificationPlanningFixture{f: f, plan: pid, meal: String(out["meal_id"]), date: date, zone: zone, at: at}
}

func (p *notificationPlanningFixture) input(t *testing.T) PlanningNotificationInput {
	t.Helper()
	out, err := p.f.store.PlanningNotificationSettings(p.f.ctx, p.f.identity, p.plan, p.meal)
	if err != nil {
		t.Fatal(err)
	}
	settings := out["settings"].(Record)
	return PlanningNotificationInput{Generation: Int(out["generation"]), PlanVersion: Int(out["plan_version"]), MealVersion: Int(out["meal_version"]),
		SettingsVersion: Int(settings["version"]), Timezone: String(out["timezone"]), Confirmed: true, Enabled: true, Responsible: p.f.identity.MembershipID,
		Steps: []PlanningNotificationStepInput{{ID: value.UUID(), Kind: "start", Label: "Explicit start", LocalDate: p.date, LocalTime: p.at.Format("15:04")}}}
}

func (p *notificationPlanningFixture) candidates(t *testing.T, at time.Time) []notification.Intent {
	t.Helper()
	tx, err := p.f.app.Begin(p.f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	out, err := planningNotificationIntents(p.f.ctx, tx, p.f.identity.HouseholdID, p.meal, at)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNotificationMealplanSettingsReplayAndSourceReconfirmation(t *testing.T) {
	p := newNotificationPlanningFixture(t)
	f := p.f
	reader := financeAuthorityMember(t, f, "reader")
	in := p.input(t)
	in.Responsible = reader.MembershipID
	if in.SettingsVersion != 0 {
		t.Fatal("new meal has implicit notification settings")
	}
	if _, err := f.store.SavePlanningNotificationSettings(f.ctx, reader, p.plan, p.meal, value.UUID(), value.UUID(), in); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader configured responsibility", err)
	}
	missing := in
	missing.Responsible = ""
	if _, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, value.UUID(), value.UUID(), missing); err == nil {
		t.Fatal("implicit recipient accepted")
	}
	key := value.UUID()
	first, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, key, value.UUID(), in)
	if err != nil || Int(first["settings"].(Record)["version"]) != 1 || Int(replay["settings"].(Record)["version"]) != 1 {
		t.Fatal("durable exact replay", first, replay, err)
	}
	other := in
	other.Responsible = f.identity.MembershipID
	if _, err = f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, key, value.UUID(), other); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	view, err := f.store.PlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal)
	if err != nil || Int(view["plan_version"]) != in.PlanVersion || Int(view["meal_version"]) != in.MealVersion {
		t.Fatal("settings changed unrelated revisions", view, err)
	}
	var audits int
	var safe bool
	err = f.app.QueryRow(f.ctx, `SELECT count(*),bool_and(changes ? 'notification_configuration_version'
 AND NOT(changes::text LIKE '%Explicit start%')) FROM core.audit_log WHERE module_id='mealplan' AND action LIKE 'notification_settings_%'`).Scan(&audits, &safe)
	if err != nil || audits != 1 || !safe {
		t.Fatal("content-free single settings audit", audits, safe, err)
	}
	if out := p.candidates(t, p.at); len(out) != 1 || out[0].UserID != reader.UserID || out[0].ExpiresAt.Sub(out[0].EventAt) != 4*time.Hour {
		t.Fatal("explicit recipient and expiry", out)
	}
	if out := p.candidates(t, p.at.Add(4*time.Hour)); len(out) != 0 {
		t.Fatal("expired source replay", out)
	}
	_, err = f.store.MutatePlanning(f.ctx, f.identity, p.plan, value.UUID(), value.UUID(), PlanningInput{Generation: in.Generation, PlanVersion: in.PlanVersion,
		Action: "save", MealID: p.meal, Version: in.MealVersion, Week: p.date, Meal: PlanningMealInput{Title: "Changed confirmed meal", PlannedOn: p.date, Slot: "dinner", Kind: "free", Status: "planned", Servings: "1"}})
	if err != nil {
		t.Fatal("actual meal edit", err)
	}
	if out := p.candidates(t, p.at); len(out) != 0 {
		t.Fatal("changed meal reused old confirmation", out)
	}
	view, err = f.store.PlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal)
	if err != nil || view["needs_confirmation"] != true {
		t.Fatal("stale source not shown", view, err)
	}
	confirmed := p.input(t)
	confirmed.Responsible = reader.MembershipID
	if _, err = f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, value.UUID(), value.UUID(), confirmed); err != nil {
		t.Fatal("explicit reconfirmation", err)
	}
	if out := p.candidates(t, p.at); len(out) != 1 || out[0].Revision != 2 {
		t.Fatal("reconfirmed future source unavailable", out)
	}
}

func TestNotificationMealplanClocksTemplateCopyAndDeletion(t *testing.T) {
	p := newNotificationPlanningFixture(t)
	f := p.f
	in := p.input(t)
	if _, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID)
	tx, err := audit.Begin(ctx, f.app)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	var before, after time.Time
	if err = tx.QueryRow(ctx, `SELECT notification_active_since FROM mealplan.plans WHERE id=$1`, p.plan).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE mealplan.plans SET notification_active_since='2000-01-01' WHERE id=$1`, p.plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT notification_active_since FROM mealplan.plans WHERE id=$1`, p.plan).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("owned activation clock forged", before, after, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE mealplan.notification_settings SET updated_at='2000-01-01' WHERE id=$1`, p.meal); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT updated_at FROM mealplan.notification_settings WHERE id=$1`, p.meal).Scan(&after); err != nil || after.Year() < 2026 {
		t.Fatal("owned confirmation clock forged", after, err)
	}
	rollback(tx)
	for _, template := range []bool{true, false} {
		if _, err = audit.Exec(ctx, f.app, `UPDATE mealplan.plans SET is_template=$2 WHERE id=$1`, p.plan, template); err != nil {
			t.Fatal(err)
		}
	}
	if out := p.candidates(t, p.at); len(out) != 0 {
		t.Fatal("template reactivation replayed old permission", out)
	}
	current := p.input(t)
	copyResult, err := f.store.MutatePlanning(f.ctx, f.identity, p.plan, value.UUID(), value.UUID(), PlanningInput{Generation: current.Generation, PlanVersion: current.PlanVersion,
		Action: "copy", Week: p.date, Name: "Independent copied plan", TargetDate: p.at.AddDate(0, 0, 14).Format(time.DateOnly)})
	if err != nil {
		t.Fatal("actual independent copy", err)
	}
	var copies int
	err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM mealplan.notification_settings n JOIN mealplan.meals m ON m.id=n.id WHERE m.plan_id=$1`, String(copyResult["plan_id"])).Scan(&copies)
	if err != nil || copies != 0 {
		t.Fatal("copy inherited notification authority", copies, err)
	}
	current = p.input(t)
	_, err = f.store.MutatePlanning(f.ctx, f.identity, p.plan, value.UUID(), value.UUID(), PlanningInput{Generation: current.Generation, PlanVersion: current.PlanVersion,
		Action: "delete", MealID: p.meal, Version: current.MealVersion, Week: p.date})
	if err != nil {
		t.Fatal("actual meal deletion", err)
	}
	err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM mealplan.notification_settings WHERE id=$1`, p.meal).Scan(&copies)
	if err != nil || copies != 0 || len(p.candidates(t, p.at)) != 0 {
		t.Fatal("deleted source retained authority", copies, err)
	}
}

func TestNotificationMealplanNoHistoryAndConcurrentScanner(t *testing.T) {
	p := newNotificationPlanningFixture(t)
	f := p.f
	in := p.input(t)
	old := in
	old.Steps = append([]PlanningNotificationStepInput(nil), in.Steps...)
	old.Steps[0].LocalDate = "2020-01-01"
	if _, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, value.UUID(), value.UUID(), old); err == nil {
		t.Fatal("elapsed confirmation accepted")
	}
	if _, err := f.store.SavePlanningNotificationSettings(f.ctx, f.identity, p.plan, p.meal, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	notificationChoreConsent(t, f, f.identity, "mealplan", false)
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() { _, err := f.store.StagePlanningNotifications(f.ctx, 1); results <- err })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("concurrent durable scanner", err)
		}
	}
	var jobs, intents int
	var clean bool
	err := f.app.QueryRow(f.ctx, `SELECT count(*),bool_and(last_error IS NULL),(SELECT count(*) FROM core.notification_intents WHERE category='mealplan')
 FROM core.jobs WHERE kind=$1`, PlanningNotificationScanJobKind).Scan(&jobs, &clean, &intents)
	if err != nil || jobs != 1 || !clean || intents != 0 {
		t.Fatal("future source staged or concurrent cursor damaged", jobs, clean, intents, err)
	}
}
