//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func bindPlanningIngredients(t *testing.T, f *integrationFixture, pid, mid, cid string, product store.Record) {
	t.Helper()
	var ids []string
	if err := f.pool.QueryRow(f.ctx, "SELECT ARRAY(SELECT item->>'id' FROM mealplan.components c,jsonb_array_elements(c.snapshot->'ingredients') item WHERE c.id=$1)", cid).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	for _, ingredient := range ids {
		in := planningBindingReview(t, f, pid, mid, cid, ingredient, product)
		if _, err := f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, value.UUID(), value.UUID(), in); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanningDemandWholeMealAggregationClaimsAndLotScope(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	view, err := f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Outcome != "incomplete" || len(view.Lines) != 2 || len(view.Products) != 0 || view.ShoppingCoverage != "unlinked" {
		t.Fatal("unconfirmed mappings became coverage", view, err)
	}
	if view.Lines[0].ComponentPosition != 0 || view.Lines[1].ComponentPosition != 0 || view.Lines[0].IngredientPosition != 0 || view.Lines[1].IngredientPosition != 1 {
		t.Fatal("frozen ingredient/component order missing for whole-scope allocation", view.Lines)
	}
	var mappings int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.planning_ingredient_bindings").Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal("preview mutated source mappings", mappings, err)
	}
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || len(view.Products) != 1 || view.Outcome != "shortage" {
		t.Fatal("grouped demand", view, err)
	}
	g := view.Products[0]
	if g.Demand.Rational != "6/5" || g.AvailableForSelection != "1.000000" || g.Shortage == nil || g.Shortage.Rational != "1/5" || !g.Bookable {
		t.Fatal("shared stock subtracted independently per ingredient", g)
	}
	var lotID string
	if err = f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lotID); err != nil {
		t.Fatal(err)
	}
	lot := store.Record{"id": lotID}
	if _, err = reserveFixtureLot(t, f, pid, mid, product, lot, "0.6"); err != nil {
		t.Fatal(err)
	}
	otherPlan, otherMeal := reservationOtherMeal(t, f, mid)
	if _, err = reserveFixtureLot(t, f, otherPlan, otherMeal, product, lot, "0.3"); err != nil {
		t.Fatal(err)
	}
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Products[0].Own != "0.600000" || view.Products[0].Other != "0.300000" || view.Products[0].Free != "0.100000" || view.Products[0].AvailableForSelection != "0.700000" || view.Products[0].Shortage.Rational != "1/2" {
		t.Fatal("own claim counted twice or other meal ignored", view, err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "-0.75", "reason": "consume"})
	location, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Independent surplus"})
	extra, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": location["id"], "received_on": "2026-01-01", "notes": "Surplus does not hide undercoverage"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": extra["id"], "quantity_delta": "1", "reason": "receive"})
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Products[0].Physical != "1.250000" || view.Products[0].Undercovered != "0.650000" || view.Products[0].AvailableForSelection != "1.000000" || view.Products[0].Shortage.Rational != "1/5" {
		t.Fatal("product aggregate hid lot deficit or used aggregate claim subtraction", view, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.locations SET archived_at=clock_timestamp() WHERE id=$1", location["id"]); err != nil {
		t.Fatal(err)
	}
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil {
		t.Fatal(err)
	}
	available, ok := new(big.Rat).SetString(view.Products[0].AvailableForSelection)
	if !ok || available.Sign() != 0 || view.Products[0].Physical != "1.250000" || view.Products[0].Eligible != "0.250000" || view.Products[0].Shortage.Rational != "6/5" {
		t.Fatal("archived surplus became available", view.Products, err)
	}
}

func TestPlanningDemandLargeExactShortageIsNotBookable(t *testing.T) {
	ingredients := make([]store.RecipeRow, 2)
	for i := range ingredients {
		ingredients[i] = store.RecipeRow{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Large bounded source", "quantity": "600000000000", "unit_code": "kg"})}
	}
	f, pid, mid, cid, _, _ := planningBindingRecipeFixture(t, ingredients)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Large exact planning demand", "unit_code": "kg"})
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	view, err := f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || len(view.Products) != 1 {
		t.Fatal("large demand preview", view, err)
	}
	got := view.Products[0]
	if got.Demand.Rational != "1200000000000" || got.Shortage == nil || got.Shortage.Rational != "1200000000000" || got.Shortage.Approximate || got.Bookable {
		t.Fatalf("large exact preview bypasses booking range: %+v", got)
	}
}

func TestPlanningDemandHTTPAndRealModulePurges(t *testing.T) {
	f, pid, mid, cid, ingredient, product := planningBindingFixture(t)
	in := planningBindingReview(t, f, pid, mid, cid, ingredient, product)
	path := "/api/v1/planning/" + pid + "/meals/" + mid + "/components/" + cid + "/ingredients/" + ingredient + "/binding"
	raw, _ := json.Marshal(in)
	response, _, err := f.request(http.MethodPost, path, f.token, "invalid", "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("plan mapping bypassed CSRF", response, err)
	}
	response, body, err := f.request(http.MethodPost, path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("actual plan mapping HTTP", response, string(body), err)
	}
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	reservationPurgeModule(t, f, "recipes")
	view, err := f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Outcome != "shortage" || view.Products[0].Demand.Rational != "6/5" {
		t.Fatal("confirmed plan mapping depends on deleted Recipe catalogue", view, err)
	}
	reservationPurgeModule(t, f, "pantry")
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Outcome != "incomplete" || len(view.Products) != 0 || view.Lines[0].Binding["assigned"] != false || store.Int(view.Lines[0].Binding["version"]) < 2 {
		t.Fatal("actual target purge retained product or erased mapping identity", view, err)
	}
	if _, err = f.app.Store.SetPlanningIngredientBinding(f.ctx, f.identity, pid, mid, cid, ingredient, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old target generation revived a mapping", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningDemand(f.ctx, foreign, pid, []string{mid}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign source exposed", err)
	}
	for _, selected := range [][]string{nil, {mid, mid}, {value.UUID()}, make([]string, 43)} {
		if _, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, selected); err == nil {
			t.Fatal("invalid whole-meal selection accepted", selected)
		}
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE household_id=$1 AND user_id=$2", f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	response, body, err = f.request(http.MethodGet, "/api/v1/planning/"+pid+"/demand/preview?meal="+mid, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || strings.Contains(string(body), store.String(product["id"])) {
		t.Fatal("reader preview failed or exposed purged target", response, string(body), err)
	}
	response, body, err = f.request(http.MethodGet, "/m/mealplan/demand/"+pid+"?meal="+mid, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || bytes.Contains(body, []byte("id=\"demand-mapping\"")) {
		t.Fatal("reader page contains mapping controls", response, err)
	}
	response, _, err = f.request(http.MethodPost, path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("reader changed plan mapping", response, err)
	}
}

func TestPlanningDemandUnknownChangedAndDisabledMappings(t *testing.T) {
	ingredients := []store.RecipeRow{
		{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Known source", "quantity": "1", "unit_code": "kg"})},
		{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Unknown amount", "quantity": nil, "unit_code": "kg"})},
		{Key: value.UUID(), Data: recipeData(t, store.Record{"name": "Incompatible volume", "quantity": "1", "unit_code": "l"})},
	}
	f, pid, mid, cid, _, _ := planningBindingRecipeFixture(t, ingredients)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Unused mutable base unit", "unit_code": "kg"})
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	view, err := f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || len(view.Products) != 1 || view.Outcome != "incomplete" || view.Products[0].Complete || view.Products[0].Shortage != nil || view.Products[0].Demand.Rational != "1" || view.Lines[1].Reason != "quantity_unknown" || view.Lines[2].Reason != "unit_incompatible" {
		t.Fatal("unknown/incompatible quantities became zero shortage", view, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET unit_code='g' WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Lines[0].Reason != "product_unit_changed" || view.Products[0].Shortage != nil {
		t.Fatal("changed base unit reused a stale confirmation", view, err)
	}
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil || view.Products[0].Demand.Rational != "1000" || view.Lines[0].Reason != "" {
		t.Fatal("explicit unit reconfirmation did not convert known demand", view, err)
	}
	state := f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	view, err = f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	raw, marshalErr := json.Marshal(view)
	if err != nil || marshalErr != nil || view.Outcome != "incomplete" || len(view.Products) != 0 || view.Lines[0].Binding["assigned"] != true || view.Lines[0].Binding["target_current"] != false || strings.Contains(string(raw), store.String(product["id"])) || strings.Contains(string(raw), store.String(product["name"])) {
		t.Fatal("disabled target leaked or confirmation identity lost", view, err, marshalErr)
	}
	state = f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	reservationPurgeModule(t, f, "mealplan")
	var remaining int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.planning_ingredient_bindings WHERE component_id=$1", cid).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("actual source purge retained ingredient payload", remaining, err)
	}
}
