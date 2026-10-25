package store

import (
	"context"
	"errors"
	"household/internal/auth"
	"household/internal/value"
	"math/big"
	"unicode/utf8"

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
	if err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, list).Scan(&open); err != nil {
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
	if err = tx.QueryRow(ctx, "SELECT coalesce(sum(b.quantity),0)::text FROM pantry.lots l JOIN pantry.lot_balances b ON b.household_id=l.household_id AND b.lot_id=l.id WHERE l.household_id=$1 AND l.product_id=$2", id.HouseholdID, product["id"]).Scan(&balance); err != nil {
		return err
	}
	stock, err := stockDecimal(balance)
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
	generation, err := requireModule(ctx, tx, id, "shopping")
	if err != nil {
		return err
	}
	if plan["is_template"] == true {
		return Invalid{"Wochenvorlage zuerst als neue Arbeitswoche kopieren"}
	}
	start := String(data["week_start"])
	if start == "" {
		days, windowErr := planningWindow(String(plan["starts_on"]))
		if windowErr != nil {
			return windowErr
		}
		if String(plan["ends_on"]) > days[6] {
			return Invalid{"Bei längeren Plänen die zu übernehmende Woche ausdrücklich auswählen"}
		}
		start = days[0]
	}
	pid := String(plan["id"])
	week, err := lockPlanningWeek(ctx, tx, id, pid, start)
	if err != nil {
		return err
	}
	var size int64
	ids := make([]string, 0)
	for _, meal := range week.Meals {
		if meal.Kind != "recipe" || meal.Status != "open" && meal.Status != "planned" {
			continue
		}
		if meal.RequiresReview {
			return Invalid{"Mahlzeit „" + meal.Title + "“: Rezeptstand zuerst in der Wochenplanung prüfen"}
		}
		for _, component := range meal.Components {
			size += component.SnapshotBytes
			ids = append(ids, component.ID)
		}
	}
	if size > planningCopyBytes {
		return Invalid{"Rezeptstände der Einkaufswoche überschreiten 16 MiB"}
	}
	if err = lockPlanningComponents(ctx, tx, id, ids); err != nil {
		return err
	}
	list := String(data["list_id"])
	if !value.ValidUUID(list) {
		return Invalid{"Einkaufsliste auswählen"}
	}
	var open bool
	err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, list).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !open {
		return Invalid{"Eine offene Einkaufsliste auswählen"}
	}
	rows, err := tx.Query(ctx, `SELECT c.snapshot,c.servings::text,m.title FROM mealplan.components c JOIN mealplan.meals m ON m.household_id=c.household_id AND m.id=c.meal_id
 WHERE m.household_id=$1 AND m.plan_id=$2 AND c.id=ANY($3::uuid[]) AND m.kind='recipe' AND m.status IN('open','planned') ORDER BY m.planned_on,m.meal_slot,m.id,c.position,c.id`, id.HouseholdID, pid, ids)
	if err != nil {
		return err
	}
	pending := []Record{}
	for rows.Next() {
		var raw []byte
		var servings, title string
		if err = rows.Scan(&raw, &servings, &title); err != nil {
			rows.Close()
			return err
		}
		bundle, decodeErr := recipeBundle(raw)
		if decodeErr != nil {
			rows.Close()
			return decodeErr
		}
		for _, ingredient := range bundle.Ingredients {
			if len(pending) >= 500 {
				rows.Close()
				return Invalid{"Höchstens 500 Zutaten pro Einkaufsübernahme. Woche aufteilen."}
			}
			name := String(ingredient["name"])
			if name == "" || utf8.RuneCountInString(name) > 200 {
				rows.Close()
				return Invalid{"Zutatenname muss für den Einkauf 1 bis 200 Zeichen enthalten"}
			}
			quantity, quantityErr := planningQuantity(ingredient["quantity"], String(bundle.Recipe["base_servings"]), servings)
			if quantityErr != nil {
				rows.Close()
				return Invalid{"Mahlzeit „" + title + "“, Zutat „" + name + "“: " + quantityErr.Error()}
			}
			note := title
			for _, field := range []string{"quantity_note", "preparation_note"} {
				if text := String(ingredient[field]); text != "" {
					note += " · " + text
				}
			}
			pending = append(pending, Record{"list_id": list, "name": name, "quantity": quantity, "unit_code": ingredient["unit_code"], "note": note, "group_name": "Wochenplanung"})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return Invalid{"Keine Zutaten aus offenen oder geplanten Rezeptmahlzeiten in der gewählten Woche"}
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	for _, item := range pending {
		record, writeErr := save(ctx, tx, id, m, e, value.UUID(), item, true, 0)
		if writeErr != nil {
			return writeErr
		}
		if err = s.afterWrite(ctx, tx, id, m, e, record, generation); err != nil {
			return err
		}
	}
	return nil
}
