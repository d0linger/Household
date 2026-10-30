//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

// These cases supplement the accepted module campaign. They use the real
// migration/HTTP/store contracts and deliberately remain serial: each fixture
// owns and resets its explicitly guarded disposable database.
func TestPlanningAcceptanceLinkedMoveSwapAndNoEffects(t *testing.T) {
	for _, tc := range []struct {
		name, subject, action, date, slot string
		valid                             bool
	}{
		{"source move remains before target", "source", "move", "2026-10-22", "dinner", true},
		{"target move remains after source", "target", "move", "2026-10-25", "lunch", true},
		{"source move crosses target", "source", "move", "2026-10-25", "dinner", false},
		{"target move crosses source", "target", "move", "2026-10-22", "lunch", false},
		{"source swap remains before target", "source", "swap", "2026-10-22", "dinner", true},
		{"target swap remains after source", "target", "swap", "2026-10-25", "dinner", true},
		{"source swap crosses target", "source", "swap", "2026-10-25", "dinner", false},
		{"target swap crosses source", "target", "swap", "2026-10-22", "dinner", false},
		{"linked source and target cannot exchange chronology", "source", "linked_swap", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, pid, sourceMeal, target := plannedLeftoverFixture(t)
			peer := ""
			if tc.action == "swap" {
				in := planningInput(t, f, pid)
				in.Action = "save"
				in.Meal = store.PlanningMealInput{Title: "Independent swap peer", PlannedOn: tc.date,
					Slot: tc.slot, Kind: "free", Status: "planned", Servings: "1"}
				peer = store.String(planningMutation(t, f, pid, in)["meal_id"])
			}
			source := plannedLeftoverReviewSource(t, f, pid, sourceMeal)
			allocation := plannedLeftoverAllocation(t, f, pid, target, source, "2")
			if _, err := f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), allocation); err != nil {
				t.Fatal("create reviewed forecast", err)
			}
			mid := sourceMeal
			if tc.subject == "target" {
				mid = target
			}
			meal := participantCurrent(t, f, pid, mid)
			in := planningInput(t, f, pid)
			in.MealID, in.Version, in.Action = mid, meal.Version, tc.action
			if tc.action == "move" {
				in.TargetDate, in.TargetSlot = tc.date, tc.slot
			} else {
				if tc.action == "linked_swap" {
					peer = target
					in.Action = "swap"
				}
				other := participantCurrent(t, f, pid, peer)
				in.OtherID, in.OtherVersion = peer, other.Version
			}
			before := acceptancePlanningSnapshot(t, f, pid)
			physical := planningExpansionStockSnapshot(t, f)
			_, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in)
			if !tc.valid {
				if !errors.Is(err, store.ErrConflict) {
					t.Fatalf("crossed reviewed chronology must conflict, got %v", err)
				}
				if acceptancePlanningSnapshot(t, f, pid) != before {
					t.Fatal("rejected linked move/swap changed plan, both meals, forecasts, receipts or domain effects")
				}
				return
			}
			if err != nil {
				t.Fatal("valid linked move/swap", err)
			}
			changed := participantCurrent(t, f, pid, mid)
			if changed.PlannedOn != tc.date || changed.Slot != tc.slot {
				t.Fatal("valid movement did not persist the reviewed destination")
			}
			if tc.action == "swap" {
				other := participantCurrent(t, f, pid, peer)
				if other.PlannedOn != meal.PlannedOn || other.Slot != meal.Slot {
					t.Fatal("valid swap failed to exchange both dates and slots")
				}
			}
			view := plannedLeftoverView(t, f, pid)
			sources, allocations := view["sources"].([]store.Record), view["allocations"].([]store.Record)
			if len(sources) != 1 || len(allocations) != 1 || allocations[0]["current"] != false ||
				sources[0]["allocated_servings"] != "2.000000" || sources[0]["free_servings"] != "2.000000" {
				t.Fatal("movement silently refreshed review, discarded stale capacity or changed the exact forecast")
			}
			if tc.subject == "source" {
				if sources[0]["current"] != false {
					t.Fatal("source movement retained a false current source review")
				}
				source = plannedLeftoverReviewSource(t, f, pid, sourceMeal)
				if plannedLeftoverView(t, f, pid)["allocations"].([]store.Record)[0]["current"] != false {
					t.Fatal("source review silently refreshed target review")
				}
			} else {
				source = sources[0]
				if source["current"] != true {
					t.Fatal("target movement invalidated an unchanged source")
				}
			}
			review := plannedLeftoverAllocation(t, f, pid, target, source, "2")
			review.Action = "allocation_review"
			review.AllocationID, review.AllocationVersion = store.String(allocations[0]["id"]), store.Int(allocations[0]["version"])
			if _, err = f.app.Store.MutatePlanningLeftovers(f.ctx, f.identity, pid, value.UUID(), value.UUID(), review); err != nil {
				t.Fatal("explicit review of moved target", err)
			}
			view = plannedLeftoverView(t, f, pid)
			if view["allocations"].([]store.Record)[0]["current"] != true ||
				view["sources"].([]store.Record)[0]["free_servings"] != "2.000000" || planningExpansionStockSnapshot(t, f) != physical {
				t.Fatal("explicit review changed capacity or wrote physical inventory/reservations/cooking/purchases")
			}
		})
	}
}

func TestPlanningAcceptanceExistingDemandAndRealOwnReservations(t *testing.T) {
	for _, tc := range []struct {
		name           string
		unreservedMeal bool
		secondCoverage string
	}{
		{"own reservation is not subtracted twice", false, "sufficient"},
		{"unreserved existing demand still spends the residual pool", true, "shortage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, pid, day, product, lot, _, rid := planningExpansionFixture(t)
			auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "2", "reason": "receive"})
			in := planningExpansionAutoplanInput(t, f, pid, day, rid)
			existing := acceptanceSaveRecipeMeal(t, f, pid, day, "dinner", in.Candidates[0].SourceVersionID)
			acceptanceReviewExistingDemand(t, f, pid, existing, product)
			if tc.unreservedMeal {
				unreserved := acceptanceSaveRecipeMeal(t, f, pid, day, "snack", in.Candidates[0].SourceVersionID)
				acceptanceReviewExistingDemand(t, f, pid, unreserved, product)
			}
			reservation := reservationReview(t, f, pid, existing, store.String(lot["id"]), "1")
			lease, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, existing, value.UUID(), value.UUID(), reservation)
			if err != nil || !value.ValidUUID(store.String(lease["id"])) {
				t.Fatal("real reviewed own reservation was not committed", err)
			}
			var actualQuantity string
			var allocations int
			if err = f.pool.QueryRow(f.ctx, `SELECT count(*),sum(a.quantity)::text FROM core.meal_reservation_allocations a
 JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
 WHERE r.household_id=$1 AND r.meal_id=$2 AND r.id=$3 AND r.state='active' AND r.expires_at>clock_timestamp()`,
				f.identity.HouseholdID, existing, lease["id"]).Scan(&allocations, &actualQuantity); err != nil || allocations != 1 || actualQuantity != "1.000000" {
				t.Fatal("required actual one-kilogram active reservation evidence differs", err)
			}
			in.PlanningExtensionCommand = planningExpansionCommand(t, f, pid, day)
			physical := planningExpansionStockSnapshot(t, f)
			signed, preview := acceptanceSignedPreview(t, f, pid, in)
			if len(preview.Slots) != 2 || preview.Slots[0].Coverage != "sufficient" || preview.Slots[1].Coverage != tc.secondCoverage ||
				!preview.StockReview.ExistingDemandComplete || preview.StockReview.UnknownExistingLines != 0 {
				coverage := make([]string, 0, len(preview.Slots))
				for _, slot := range preview.Slots {
					coverage = append(coverage, slot.Coverage)
				}
				t.Fatalf("actual existing-demand/own-reservation query did not conserve the joint free pool: coverage=%v existing_complete=%t unknown_existing=%d",
					coverage, preview.StockReview.ExistingDemandComplete, preview.StockReview.UnknownExistingLines)
			}
			if planningExpansionStockSnapshot(t, f) != physical {
				t.Fatal("read-only signed preview changed the actual lease or physical stock")
			}
			key := value.UUID()
			status, raw := acceptancePlanningPost(t, f, "/api/v1/planning/"+pid+"/autoplan/apply", key, signed)
			var applied struct {
				CreatedMealIDs []string `json:"created_meal_ids"`
			}
			if status != http.StatusOK || json.Unmarshal(raw, &applied) != nil || len(applied.CreatedMealIDs) != 2 {
				t.Fatalf("reviewed actual joint proposal apply: status %d", status)
			}
			var count int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2", f.identity.HouseholdID, pid).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 3
			if tc.unreservedMeal {
				want++
			}
			if count != want || planningExpansionStockSnapshot(t, f) != physical || acceptanceAutoplanReceiptCount(t, f, pid, key) != 1 {
				t.Fatal("apply replaced existing demand, changed stock/reservations or missed its sole receipt")
			}
			if _, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, existing); err != nil {
				t.Fatal("existing reserved meal disappeared", err)
			}
		})
	}
}

func TestPlanningAcceptanceSignedDependencyAndAuthorityChanges(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		wantStatus   int
	}{
		{"saved weekly settings changed", "settings", http.StatusConflict},
		{"effective selected price changed", "price", http.StatusConflict},
		{"reviewed Shopping binding changed", "binding", http.StatusConflict},
		{"selected immutable source was deleted", "source", http.StatusConflict},
		{"current writer became Reader", "role", http.StatusForbidden},
		{"current membership became inactive", "membership", http.StatusUnauthorized},
		{"issuing session was revoked", "session", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, pid, day, _, _, catalog, rid := planningExpansionFixture(t)
			// Preserve a separate current administrator when the controlled Owner
			// fixture changes the issuing account's role or membership below.
			if tc.change == "role" || tc.change == "membership" {
				shoppingTestMember(t, f, "acceptance-other-admin", "admin")
			}
			_, price := acceptanceBindPrice(t, f, catalog, day)
			in := planningExpansionAutoplanInput(t, f, pid, day, rid)
			in.Rules.Cost = 100
			signed, preview := acceptanceSignedPreview(t, f, pid, in)
			if len(preview.Slots) != 2 || preview.Slots[0].CostMinor != "200" || preview.Slots[0].UnknownCostCount != 0 {
				t.Fatal("authentic preview must first contain the actual selected two-euro price")
			}
			switch tc.change {
			case "settings":
				target := "1000"
				_, err := f.app.Store.MutatePlanningBudget(f.ctx, f.identity, pid, value.UUID(), value.UUID(), store.PlanningBudgetInput{
					PlanningExtensionCommand: planningExpansionCommand(t, f, pid, day), BudgetMinor: &target,
					ManualEstimates: map[string]store.PlanningManualEstimate{}, Rules: store.PlanningAutoplanRules{Cost: 100},
				})
				if err != nil {
					t.Fatal("actual saved settings mutation", err)
				}
			case "price":
				m, _ := f.app.Store.Module("shopping")
				e, _ := m.Entity("price_observations")
				if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(price["id"]), value.UUID(), value.UUID(),
					auditMutation(t, f, "shopping", store.Record{"price_minor": "3.00"}, store.Int(price["version"]))); err != nil {
					t.Fatal("actual selected price mutation", err)
				}
			case "binding":
				other, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Explicit alternative price product", "pack_unit": "kg", "pack_quantity": "1"})
				acceptanceSetPriceBinding(t, f, store.String(catalog["id"]), other)
			case "source":
				m, _ := f.app.Store.Module("recipes")
				e, _ := m.Entity("recipes")
				var version int64
				if err := f.pool.QueryRow(f.ctx, "SELECT version FROM recipes.recipes WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, rid).Scan(&version); err != nil {
					t.Fatal(err)
				}
				if err := f.app.Store.Delete(f.ctx, f.identity, m, e, rid, version, in.RecipesGeneration, value.UUID()); err != nil {
					t.Fatal("actual selected recipe deletion", err)
				}
			case "role":
				changed, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, f.identity.MembershipID)
				if err != nil || changed.RowsAffected() != 1 {
					t.Fatal("controlled actual membership-role change", err)
				}
			case "membership":
				changed, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET inactive_at=clock_timestamp() WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, f.identity.MembershipID)
				if err != nil || changed.RowsAffected() != 1 {
					t.Fatal("controlled actual membership deactivation", err)
				}
			case "session":
				changed, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND user_id=$2", f.identity.SessionID, f.identity.UserID)
				if err != nil || changed.RowsAffected() != 1 {
					t.Fatal("controlled actual issuing-session revocation", err)
				}
			}
			// Intentional dependency/authentication changes precede the baseline.
			// The old authentic signed input is sent unchanged with a NEW key.
			before := acceptancePlanningSnapshot(t, f, pid)
			key := value.UUID()
			if status, _ := acceptancePlanningPost(t, f, "/api/v1/planning/"+pid+"/autoplan/apply", key, signed); status != tc.wantStatus {
				t.Fatalf("stale authentic review status %d, want %d", status, tc.wantStatus)
			}
			if acceptancePlanningSnapshot(t, f, pid) != before || acceptanceAutoplanReceiptCount(t, f, pid, key) != 0 {
				t.Fatal("denied authentic review changed meals, components, receipts, forecasts or physical side effects")
			}
		})
	}
}

type acceptanceAutoplanPreview struct {
	ReviewFingerprint string `json:"review_fingerprint"`
	ReviewProof       string `json:"review_proof"`
	ReviewExpiresAt   int64  `json:"review_expires_at"`
	Slots             []struct {
		Coverage         string `json:"coverage"`
		CostMinor        string `json:"cost_minor"`
		UnknownCostCount int    `json:"unknown_cost_count"`
	} `json:"slots"`
	StockReview struct {
		ExistingDemandComplete bool `json:"existing_demand_complete"`
		UnknownExistingLines   int  `json:"unknown_existing_lines"`
	} `json:"stock_review"`
}

func acceptancePlanningPost(t *testing.T, f *integrationFixture, path, key string, input any) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, raw, err := f.request(http.MethodPost, path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(body),
		map[string]string{"Idempotency-Key": key})
	if err != nil {
		t.Fatal("actual planning HTTP request failed", err)
	}
	return response.StatusCode, raw
}

func acceptanceSignedPreview(t *testing.T, f *integrationFixture, pid string, in store.PlanningAutoplanInput) (store.PlanningAutoplanInput, acceptanceAutoplanPreview) {
	t.Helper()
	status, raw := acceptancePlanningPost(t, f, "/api/v1/planning/"+pid+"/autoplan/preview", value.UUID(), in)
	var preview acceptanceAutoplanPreview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.ReviewFingerprint) != 64 ||
		preview.ReviewProof == "" || preview.ReviewExpiresAt <= 0 {
		t.Fatalf("real signed HTTP preview unavailable: status %d", status)
	}
	in.ReviewFingerprint, in.ReviewProof, in.ReviewExpiresAt = preview.ReviewFingerprint, preview.ReviewProof, preview.ReviewExpiresAt
	return in, preview
}

func acceptanceSaveRecipeMeal(t *testing.T, f *integrationFixture, pid, day, slot, version string) string {
	t.Helper()
	base := planningExpansionCommand(t, f, pid, day)
	result, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), store.PlanningInput{
		Generation: base.Generation, PlanVersion: base.PlanVersion, Action: "save",
		Meal: store.PlanningMealInput{Title: "Existing actual kilogram demand", PlannedOn: day, Slot: slot,
			Kind: "recipe", Status: "planned", Servings: "1"},
		Components: []store.PlanningComponentInput{{SourceVersionID: version,
			SourceGeneration: f.app.Store.States()["recipes"].Generation, Servings: "1"}},
	})
	if err != nil || !value.ValidUUID(store.String(result["meal_id"])) {
		t.Fatal("save actual existing recipe demand", err)
	}
	return store.String(result["meal_id"])
}

func acceptanceReviewExistingDemand(t *testing.T, f *integrationFixture, pid, mid string, product store.Record) {
	t.Helper()
	meal := participantCurrent(t, f, pid, mid)
	if len(meal.Components) != 1 {
		t.Fatal("existing-demand fixture must have exactly one frozen component")
	}
	// A catalogue suggestion does not authorize existing frozen ingredient
	// demand. Use the same explicit source-hash/CAS review as a real user.
	bindPlanningIngredients(t, f, pid, mid, meal.Components[0].ID, product)
	demand, err := f.app.Store.PlanningDemand(f.ctx, f.identity, pid, []string{mid})
	if err != nil {
		t.Fatal("read actually reviewed existing demand", err)
	}
	if len(demand.Issues) != 0 || len(demand.Lines) != 1 || len(demand.Products) != 1 {
		t.Fatalf("reviewed one-kilogram fixture scope: issues=%d lines=%d products=%d", len(demand.Issues), len(demand.Lines), len(demand.Products))
	}
	line, grouped := demand.Lines[0], demand.Products[0]
	if line.Reason != "" || line.ProductID != store.String(product["id"]) || line.Demand == nil || line.Demand.Rational != "1" ||
		line.Binding["assigned"] != true || line.Binding["target_current"] != true || grouped == nil || grouped.ID != line.ProductID ||
		grouped.Unit != "kg" || !grouped.Complete || grouped.Demand.Rational != "1" {
		t.Fatal("explicit frozen ingredient review must produce assigned current exact one-kilogram demand")
	}
}

func acceptanceSetPriceBinding(t *testing.T, f *integrationFixture, catalog string, product store.Record) {
	t.Helper()
	var catalogVersion, bindingVersion int64
	var bindingID string
	if err := f.pool.QueryRow(f.ctx, `SELECT c.version,b.id,b.version FROM recipes.ingredient_catalog c
 JOIN core.ingredient_product_bindings b ON b.household_id=c.household_id AND b.catalog_id=c.id
 WHERE c.household_id=$1 AND c.id=$2`, f.identity.HouseholdID, catalog).Scan(&catalogVersion, &bindingID, &bindingVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.SaveRecipeShoppingBinding(f.ctx, f.identity, catalog, value.UUID(), value.UUID(), store.RecipePriceBindingInput{
		Generation: f.app.Store.States()["recipes"].Generation, ShoppingGeneration: f.app.Store.States()["shopping"].Generation,
		CatalogVersion: catalogVersion, BindingID: bindingID, Version: bindingVersion,
		ProductID: store.String(product["id"]), ProductVersion: store.Int(product["version"]),
	}); err != nil {
		t.Fatal("actual reviewed Shopping price binding", err)
	}
}

func acceptanceBindPrice(t *testing.T, f *integrationFixture, catalog store.Record, day string) (store.Record, store.Record) {
	t.Helper()
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Acceptance current price product", "pack_unit": "kg", "pack_quantity": "1"})
	shop, _ := auditCreate(t, f, "shopping", "stores", store.Record{"name": "Acceptance price source"})
	price, _ := auditCreate(t, f, "shopping", "price_observations", store.Record{"product_id": product["id"], "store_id": shop["id"],
		"observed_on": day, "quantity": "1", "unit_code": "kg", "price_minor": "2.00"})
	acceptanceSetPriceBinding(t, f, store.String(catalog["id"]), product)
	return product, price
}

func acceptanceAutoplanReceiptCount(t *testing.T, f *integrationFixture, pid, key string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.idempotency_records
 WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4`, f.identity.HouseholdID, f.identity.UserID,
		"mealplan/extension/autoplan/"+pid, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Excludes session sliding/rate-limit/security logging, which may legitimately
// change during denied HTTP authentication. Includes complete actual plan rows,
// forecast/settings rows and the business projections the rejected write must
// leave untouched. The exact HH/plan predicates never inspect another tenant.
func acceptancePlanningSnapshot(t *testing.T, f *integrationFixture, pid string) string {
	t.Helper()
	var result string
	if err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'plan',(SELECT to_jsonb(p) FROM mealplan.plans p WHERE p.household_id=$1 AND p.id=$2),
 'meals',(SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.id),'[]'::jsonb) FROM mealplan.meals m WHERE m.household_id=$1 AND m.plan_id=$2),
 'components',(SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id),'[]'::jsonb) FROM mealplan.components c JOIN mealplan.meals m ON m.household_id=c.household_id AND m.id=c.meal_id WHERE m.household_id=$1 AND m.plan_id=$2),
 'sources',(SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]'::jsonb) FROM mealplan.leftover_sources s WHERE s.household_id=$1 AND s.plan_id=$2),
 'allocations',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id),'[]'::jsonb) FROM mealplan.leftover_allocations a WHERE a.household_id=$1 AND a.plan_id=$2),
 'settings',(SELECT coalesce(jsonb_agg(to_jsonb(w) ORDER BY w.week_start),'[]'::jsonb) FROM mealplan.weekly_settings w WHERE w.household_id=$1 AND w.plan_id=$2),
 'movements',(SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]'::jsonb) FROM pantry.stock_movements s WHERE s.household_id=$1),
 'balances',(SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY b.lot_id),'[]'::jsonb) FROM pantry.lot_balances b WHERE b.household_id=$1),
 'reservations',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]'::jsonb) FROM core.meal_reservations r WHERE r.household_id=$1),
 'reservation_allocations',(SELECT count(*) FROM core.meal_reservation_allocations WHERE household_id=$1),
 'cooking',(SELECT count(*) FROM recipes.cooking_records WHERE household_id=$1),
 'shopping',(SELECT count(*) FROM shopping.items WHERE household_id=$1),
 'receipts',(SELECT count(*) FROM core.idempotency_records WHERE household_id=$1),
 'outbox',(SELECT count(*) FROM core.outbox_events WHERE household_id=$1 AND module_id='mealplan'),
 'search',(SELECT count(*) FROM core.search_documents WHERE household_id=$1 AND module_id='mealplan'),
 'audit',(SELECT count(*) FROM core.audit_log WHERE household_id=$1 AND module_id='mealplan'))::text`, f.identity.HouseholdID, pid).Scan(&result); err != nil {
		t.Fatal("read exact bounded fixture effects", err)
	}
	return result
}
