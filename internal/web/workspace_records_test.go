package web

import (
	"net/url"
	"testing"

	"household/internal/module"
	"household/internal/store"
)

func TestWorkspaceRecordDestinations(t *testing.T) {
	const id = "5f0af549-79c0-4947-b6df-d97bf6970de0"
	const plan = "06a2343a-9e42-49e3-b1cb-fc25de34c514"
	for _, tc := range []struct{ mid, entity, want string }{
		{"shopping", "lists", "/m/shopping/lists/" + id},
		{"recipes", "recipes", "/m/recipes/recipes/" + id + "/workspace"},
		{"expenses", "entries", "/m/expenses/editor?entry=" + id},
		{"mealplan", "plans", "/m/mealplan/workspace/" + id},
	} {
		if got := workspaceRecordURL(tc.mid, module.Entity{ID: tc.entity}, store.Record{"id": id}); got != tc.want {
			t.Errorf("%s: %s want%s", tc.mid, got, tc.want)
		}
	}
	got := workspaceRecordURL("mealplan", module.Entity{ID: "meals"}, store.Record{"id": id, "plan_id": plan, "planned_on": "2026-11-11"})
	u, err := url.Parse(got)
	if err != nil || u.Path != "/m/mealplan/workspace/"+plan || u.Query().Get("meal") != id || u.Query().Get("week") != "2026-11-11" {
		t.Fatalf("meal loses plan/day/record: %s", got)
	}
	if got := workspaceRecordURL("mealplan", module.Entity{ID: "plans"}, store.Record{"id": "//outside.invalid"}); got != "/m/mealplan/plans" {
		t.Fatal("invalid identity became navigation", got)
	}
}
