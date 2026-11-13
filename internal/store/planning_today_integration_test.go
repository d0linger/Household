//go:build integration

package store

import (
	"testing"

	"household/internal/value"
)

func TestTodayMealsCurrentDayVisibilityAndBounds(t *testing.T) {
	p := newNotificationPlanningFixture(t)
	f := p.f
	read := func() []PlanningTodayMeal {
		t.Helper()
		out, err := f.store.TodayMeals(f.ctx, f.identity, p.date, 7)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := read(); len(got) != 1 || got[0].ID != p.meal {
		t.Fatal("saved day meal missing", got)
	}
	reader := financeAuthorityMember(t, f, "reader")
	if got, err := f.store.TodayMeals(f.ctx, reader, p.date, 7); err != nil || len(got) != 1 {
		t.Fatal("reader lost shared meal read", got, err)
	}
	other := f.identity
	other.HouseholdID = value.UUID()
	if got, err := f.store.TodayMeals(f.ctx, other, p.date, 7); err != nil || len(got) != 0 {
		t.Fatal("household boundary", got, err)
	}
	if got, err := f.store.TodayMeals(f.ctx, f.identity, "2000-01-01", 7); err != nil || len(got) != 0 {
		t.Fatal("different day leaked", got, err)
	}
	for _, status := range []string{"open", "cancelled", "cooked", "purchased", "planned"} {
		if _, err := f.owner.Exec(f.ctx, `UPDATE mealplan.meals SET status=$3 WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, p.meal, status); err != nil {
			t.Fatal(err)
		}
		got := read()
		want := 1
		if status == "open" || status == "cancelled" {
			want = 0
		}
		if len(got) != want {
			t.Fatal("meal status visibility", status, got)
		}
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE mealplan.plans SET is_template=true WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, p.plan); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 0 {
		t.Fatal("template meal leaked", got)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE mealplan.plans SET is_template=false WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, p.plan); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO mealplan.meals(household_id,plan_id,planned_on,meal_slot,title,servings,created_by) VALUES($1,$2,$3,'breakfast','Bounded today fixture',1,$4)`, f.identity.HouseholdID, p.plan, p.date, f.identity.UserID); err != nil {
			t.Fatal(err)
		}
	}
	got := read()
	if len(got) != 7 || got[0].Slot != "breakfast" {
		t.Fatal("bounded slot order", got)
	}
	for _, meal := range got {
		if meal.ID == p.meal {
			t.Fatal("dinner sorted ahead of breakfast")
		}
	}
}
