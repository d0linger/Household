package store

import (
	"context"
	"household/internal/auth"
	"household/internal/value"
	"math/big"

	"github.com/jackc/pgx/v5"
)

func (s *Store) pantryToShopping(ctx context.Context, tx pgx.Tx, id auth.Identity, product, data Record) error {
	generation, err := requireModule(ctx, tx, id, "shopping")
	if err != nil {
		return err
	}
	list := String(data["list_id"])
	if !value.ValidUUID(list) {
		return Invalid{"Einkaufsliste auswählen"}
	}
	var open bool
	if err = tx.QueryRow(ctx, "SELECT archived_at IS NULL FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, list).Scan(&open); err != nil {
		return err
	}
	if !open {
		return Invalid{"Einkaufsliste ist archiviert"}
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM shopping.items WHERE household_id=$1 AND list_id=$2 AND lower(name)=lower($3) AND checked_at IS NULL)", id.HouseholdID, list, product["name"]).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return Invalid{"Produkt steht bereits offen auf dieser Liste"}
	}
	var balance string
	if err = tx.QueryRow(ctx, "SELECT coalesce(sum(sm.quantity_delta),0)::text FROM pantry.lots l JOIN pantry.stock_movements sm ON sm.household_id=l.household_id AND sm.lot_id=l.id WHERE l.household_id=$1 AND l.product_id=$2", id.HouseholdID, product["id"]).Scan(&balance); err != nil {
		return err
	}
	stock, err := value.Decimal(balance)
	if err != nil {
		return err
	}
	minimum, err := value.Decimal(String(product["low_stock_threshold"]))
	if err != nil {
		return Invalid{"Mindestbestand festlegen"}
	}
	quantity := new(big.Rat).Sub(minimum, stock)
	if override := String(data["quantity"]); override != "" {
		quantity, err = value.Decimal(override)
		if err != nil {
			return Invalid{"Menge prüfen"}
		}
	}
	if quantity.Sign() <= 0 {
		return Invalid{"Kein Fehlbestand; bei Bedarf eine positive Einkaufsmenge angeben"}
	}
	if integer, _ := product["integer_quantities"].(bool); integer && !quantity.IsInt() {
		return Invalid{"Ganze Einkaufsmenge erforderlich"}
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	record, err := save(ctx, tx, id, m, e, value.UUID(), Record{"list_id": list, "name": product["name"], "quantity": quantity.FloatString(6), "unit_code": product["unit_code"], "group_name": "Vorrat ergänzen"}, true, 0)
	if err != nil {
		return err
	}
	return s.afterWrite(ctx, tx, id, m, e, record, generation)
}
func (s *Store) mealsToShopping(ctx context.Context, tx pgx.Tx, id auth.Identity, plan, data Record) error {
	if _, err := requireModule(ctx, tx, id, "recipes"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(r),m.servings::text FROM mealplan.meals m JOIN core.resource_links l ON l.household_id=m.household_id AND l.source_module='mealplan' AND l.source_type='meals' AND l.source_id=m.id AND l.relation='meal_recipe' JOIN recipes.recipes r ON r.household_id=m.household_id AND r.id=l.target_id WHERE m.household_id=$1 AND m.plan_id=$2 ORDER BY m.planned_on,m.id FOR SHARE OF r,m,l`, id.HouseholdID, plan["id"])
	if err != nil {
		return err
	}
	type meal struct {
		recipe   Record
		servings string
	}
	items := []meal{}
	for rows.Next() {
		var raw []byte
		var x meal
		if err = rows.Scan(&raw, &x.servings); err != nil {
			rows.Close()
			return err
		}
		x.recipe, err = decode(raw)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return Invalid{"Zuerst Mahlzeiten mit Rezepten verknüpfen"}
	}
	for _, x := range items {
		if err = s.recipeToShopping(ctx, tx, id, x.recipe, Record{"list_id": data["list_id"], "servings": x.servings}); err != nil {
			return err
		}
	}
	return nil
}
