package store

import (
	"context"
	"slices"
	"sort"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

const planningPurchaseDependencyLimit = 5000

type planningPurchaseLockIDs struct {
	plans, meals, components, products, bindings, requirements, lists, items []string
}

func planningPurchaseResourceIDs(scope planningPurchaseScope) planningPurchaseLockIDs {
	out := planningPurchaseLockIDs{}
	for _, plan := range scope.view.Plans {
		out.plans = append(out.plans, plan.ID)
	}
	for _, meal := range scope.view.Demand.Meals {
		out.meals = append(out.meals, meal.ID)
	}
	for _, line := range scope.view.Demand.Lines {
		out.components = append(out.components, line.ComponentID)
		out.bindings = append(out.bindings, String(line.Binding["id"]))
	}
	for _, product := range scope.view.Demand.Products {
		out.products = append(out.products, product.ID)
	}
	for _, r := range scope.view.Requirements {
		out.requirements = append(out.requirements, r.ID)
		out.lists, out.items = append(out.lists, r.ListID), append(out.items, r.ItemID)
	}
	for _, ids := range []*[]string{&out.plans, &out.meals, &out.components, &out.products, &out.bindings, &out.requirements, &out.lists, &out.items} {
		*ids = planningPurchaseUniqueIDs(*ids)
	}
	return out
}

func planningPurchaseUniqueIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func samePlanningPurchaseLocks(first, second planningPurchaseScope) bool {
	a, b := planningPurchaseResourceIDs(first), planningPurchaseResourceIDs(second)
	return slices.Equal(a.plans, b.plans) && slices.Equal(a.meals, b.meals) && slices.Equal(a.components, b.components) &&
		slices.Equal(a.products, b.products) && slices.Equal(a.bindings, b.bindings) && slices.Equal(a.requirements, b.requirements) &&
		slices.Equal(a.lists, b.lists) && slices.Equal(a.items, b.items)
}

func lockPlanningPurchaseIDs(ctx context.Context, tx pgx.Tx, id auth.Identity, table [2]string, ids []string, share bool) error {
	if len(ids) == 0 {
		return nil
	}
	mode := " FOR UPDATE NOWAIT"
	if share {
		mode = " FOR SHARE NOWAIT"
	}
	// Every relation is a compiled call-site constant; identifiers are quoted.
	tag, err := tx.Exec(ctx, "SELECT id FROM "+pgx.Identifier(table[:]).Sanitize()+" WHERE household_id=$1 AND id=ANY($2::uuid[]) ORDER BY id"+mode, id.HouseholdID, ids)
	if err != nil {
		return shoppingContention(err)
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return ErrConflict
	}
	return nil
}

func lockPlanningPurchaseResources(ctx context.Context, tx pgx.Tx, id auth.Identity, scope planningPurchaseScope, in PlanningPurchaseInput) error {
	ids := planningPurchaseResourceIDs(scope)
	for _, group := range []struct {
		table [2]string
		ids   []string
		share bool
	}{
		{[2]string{"mealplan", "plans"}, ids.plans, false},
		{[2]string{"mealplan", "meals"}, ids.meals, false},
		{[2]string{"mealplan", "components"}, ids.components, false},
		{[2]string{"pantry", "products"}, ids.products, false},
	} {
		if err := lockPlanningPurchaseIDs(ctx, tx, id, group.table, group.ids, group.share); err != nil {
			return err
		}
	}
	if err := lockPlanningPurchaseCoverage(ctx, tx, id, ids.products, in); err != nil {
		return err
	}
	creates := false
	for _, line := range scope.view.Lines {
		creates = creates || line.Action == "create"
	}
	if creates {
		if in.ListID == "" {
			return Invalid{"Für neue Einkaufspositionen eine offene, noch nicht abgeschlossene Liste bestätigen"}
		}
		ids.lists = planningPurchaseUniqueIDs(append(ids.lists, in.ListID))
	}
	for _, group := range []struct {
		table [2]string
		ids   []string
		share bool
	}{
		{[2]string{"core", "planning_ingredient_bindings"}, ids.bindings, true},
		{[2]string{"core", "planning_purchase_requirements"}, ids.requirements, false},
		{[2]string{"shopping", "lists"}, ids.lists, false},
		{[2]string{"shopping", "items"}, ids.items, false},
	} {
		if err := lockPlanningPurchaseIDs(ctx, tx, id, group.table, group.ids, group.share); err != nil {
			return err
		}
	}
	if creates {
		var open bool
		var version int64
		err := tx.QueryRow(ctx, `SELECT l.version,l.archived_at IS NULL AND NOT l.is_template AND NOT EXISTS(
 SELECT 1 FROM shopping.checkouts c WHERE c.household_id=l.household_id AND c.list_id=l.id)
 FROM shopping.lists l WHERE l.household_id=$1 AND l.id=$2`, id.HouseholdID, in.ListID).Scan(&version, &open)
		if err != nil {
			return err
		}
		if !open || version != in.ListVersion {
			return PlanningConflict{"Die Zielliste wurde geändert, archiviert oder bereits abgeschlossen. Eine aktuelle offene Liste bestätigen."}
		}
	}
	return nil
}

func lockPlanningPurchaseCoverage(ctx context.Context, tx pgx.Tx, id auth.Identity, products []string, in PlanningPurchaseInput) error {
	for _, dependency := range []struct {
		query string
		args  []any
	}{
		{`SELECT loc.id FROM pantry.locations loc WHERE loc.household_id=$1 AND EXISTS(
 SELECT 1 FROM pantry.lots l WHERE l.household_id=loc.household_id AND l.location_id=loc.id AND l.product_id=ANY($2::uuid[]))
 ORDER BY loc.id LIMIT 5001 FOR SHARE NOWAIT`, []any{id.HouseholdID, products}},
		{`SELECT r.id FROM core.meal_reservations r WHERE r.household_id=$1 AND r.mealplan_generation=$3 AND r.pantry_generation=$4 AND r.state='active'
 AND EXISTS(SELECT 1 FROM core.meal_reservation_allocations a WHERE a.household_id=r.household_id AND a.reservation_id=r.id AND a.product_id=ANY($2::uuid[]))
			ORDER BY r.id LIMIT 5001 FOR SHARE NOWAIT`, []any{id.HouseholdID, products, in.Generation, in.PantryGeneration}},
	} {
		tag, err := tx.Exec(ctx, dependency.query, dependency.args...)
		if err != nil {
			return shoppingContention(err)
		}
		if tag.RowsAffected() > planningPurchaseDependencyLimit {
			return Invalid{"Planungsumfang hat mehr als 5000 Lagerort- oder Reservierungsabhängigkeiten. Umfang vor der Übernahme verkleinern."}
		}
	}
	return nil
}
