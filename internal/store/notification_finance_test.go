package store

import (
	"math/big"
	"testing"
	"time"

	"household/internal/auth"
)

func TestNotificationFinanceBudgetExactThresholdAndHysteresis(t *testing.T) {
	for _, tc := range []struct {
		spent, allowance string
		threshold        int
		crossed, rearmed bool
	}{
		{"7999", "10000", 80, false, false}, {"8000", "10000", 80, true, false},
		{"7500", "10000", 80, false, false}, {"7499", "10000", 80, false, true},
		{"9999", "10000", 100, false, false}, {"10000", "10000", 100, true, false},
		{"9500", "10000", 100, false, false}, {"9499", "10000", 100, false, true},
		{"-200", "10000", 80, false, true}, {"80000000000000000000000001", "100000000000000000000000000", 80, true, false},
	} {
		t.Run(tc.spent+"/"+tc.allowance, func(t *testing.T) {
			spent, _ := new(big.Int).SetString(tc.spent, 10)
			allowance, _ := new(big.Int).SetString(tc.allowance, 10)
			b := financeNotificationBudget{eligible: true, spent: spent, allowance: allowance}
			if financeBudgetCrossed(b, tc.threshold) != tc.crossed || financeBudgetRearmed(b, tc.threshold) != tc.rearmed {
				t.Fatal("incorrect exact boundary")
			}
			if spent.String() != tc.spent || allowance.String() != tc.allowance {
				t.Fatal("comparison mutated accounting values")
			}
		})
	}
	if financeBudgetCrossed(financeNotificationBudget{}, 80) || financeBudgetRearmed(financeNotificationBudget{}, 80) {
		t.Fatal("unknown allowance became a percentage")
	}
}

func TestNotificationFinanceBudgetSilentBaselineAndEpisodes(t *testing.T) {
	base := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	budget := func(spent int64) financeNotificationBudget {
		return financeNotificationBudget{eligible: true, spent: big.NewInt(spent), allowance: big.NewInt(100)}
	}
	o := nextFinanceBudgetObservation(financeBudgetObservation{}, false, budget(90), 80, base)
	if o.state != "baseline" || o.detected != nil || o.episode != 0 {
		t.Fatal("initial overspend sent history")
	}
	o = nextFinanceBudgetObservation(o, true, budget(78), 80, base.Add(time.Minute))
	if o.state != "baseline" {
		t.Fatal("baseline rearmed above hysteresis boundary")
	}
	healthy := base.Add(2 * time.Minute)
	o = nextFinanceBudgetObservation(o, true, budget(74), 80, healthy)
	if o.state != "healthy" || o.healthy == nil || !o.healthy.Equal(healthy) {
		t.Fatal("healthy observation missing")
	}
	crossing := base.Add(3 * time.Minute)
	o = nextFinanceBudgetObservation(o, true, budget(80), 80, crossing)
	if o.state != "crossed" || o.episode != 1 || o.cutoff == nil || !o.cutoff.Equal(healthy) || !o.detected.Equal(crossing) {
		t.Fatal("crossing lost its prior consent boundary")
	}
	o = nextFinanceBudgetObservation(o, true, budget(79), 80, base.Add(4*time.Minute))
	o = nextFinanceBudgetObservation(o, true, budget(81), 80, base.Add(5*time.Minute))
	if o.episode != 1 || !o.detected.Equal(crossing) {
		t.Fatal("hysteresis bounce manufactured another occurrence")
	}
	o = nextFinanceBudgetObservation(o, true, budget(70), 80, base.Add(6*time.Minute))
	o = nextFinanceBudgetObservation(o, true, budget(81), 80, base.Add(7*time.Minute))
	if o.episode != 2 {
		t.Fatal("new healthy episode did not rearm")
	}
	o = nextFinanceBudgetObservation(o, false, budget(110), 80, base.Add(8*time.Minute))
	if o.state != "baseline" || o.episode != 2 || o.cutoff != nil {
		t.Fatal("permission/config change replayed historical spending")
	}
	o = nextFinanceBudgetObservation(o, true, financeNotificationBudget{}, 80, base.Add(9*time.Minute))
	if o.state != "ineligible" || o.healthy != nil || o.detected != nil {
		t.Fatal("incomplete data retained delivery authority")
	}
}

func TestNotificationFinancePaymentStagesAndNoHistory(t *testing.T) {
	id := auth.Identity{HouseholdID: "10000000-0000-4000-8000-000000000001", UserID: "10000000-0000-4000-8000-000000000002"}
	event := time.Date(2026, 3, 29, 7, 0, 0, 0, time.UTC) //09:00 Vienna, spring DST day.
	before := event.Add(-8 * 24 * time.Hour)
	source := financePaymentReminderSource{EntryID: "10000000-0000-4000-8000-000000000003", DueOn: "2026-03-29", Timezone: "Europe/Vienna",
		Version: 2, ModuleGeneration: 1, ConfirmedAt: &before, ResponsibleSince: before, SourceUpdated: before, ModuleActivation: before, Eligible: true}
	intents, err := financePaymentIntents(id, source, event)
	if err != nil || len(intents) != 1 || intents[0].Validate() != nil || !intents[0].EventAt.Equal(event) || !intents[0].ExpiresAt.Equal(event.Add(24*time.Hour)) {
		t.Fatal("day stage or fixed freshness differs", intents, err)
	}
	for _, name := range []string{"confirmation", "assignment", "source", "permission", "module", "completed"} {
		t.Run(name, func(t *testing.T) {
			changed := source
			late := event.Add(time.Second)
			switch name {
			case "confirmation":
				changed.ConfirmedAt = &late
			case "assignment":
				changed.ResponsibleSince = late
			case "source":
				changed.SourceUpdated = late
			case "permission":
				changed.PermissionChangedAt = &late
			case "module":
				changed.ModuleActivation = late
			case "completed":
				changed.Eligible = false
			}
			got, e := financePaymentIntents(id, changed, event.Add(time.Minute))
			if e != nil || len(got) != 0 {
				t.Fatal("stale/unconfirmed source replayed", got, e)
			}
		})
	}
	if got, err := financePaymentIntents(id, source, event.Add(24*time.Hour)); err != nil || len(got) != 0 {
		t.Fatal("expired date survived freshness", got, err)
	}
}
