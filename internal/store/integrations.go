package store

import (
	"context"
	"errors"
	"household/internal/auth"
	"household/internal/value"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func (s *Store) pantryToShopping(ctx context.Context, tx pgx.Tx, id auth.Identity, product, data Record) (Record, error) {
	generation, err := requireModule(ctx, tx, id, "shopping")
	if err != nil {
		return nil, err
	}
	if product["archived_at"] != nil {
		return nil, Invalid{"Archiviertes Produkt zuerst prüfen; kein neuer Nachkauf"}
	}
	packID := String(data["pack_id"])
	reviewed := data["reviewed"] == true
	if packID != "" {
		if !reviewed || !value.ValidUUID(packID) {
			return nil, Invalid{"Packung ausdrücklich in der Nachkaufvorschau prüfen"}
		}
		var version int64
		var disabled bool
		err = tx.QueryRow(ctx, `SELECT version,disabled FROM pantry.packs WHERE household_id=$1 AND product_id=$2 AND id=$3
 FOR SHARE NOWAIT`, id.HouseholdID, product["id"], packID).Scan(&version, &disabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		if err != nil {
			return nil, shoppingContention(err)
		}
		if disabled || version != Int(data["pack_version"]) {
			return nil, ErrConflict
		}
	}
	list := String(data["list_id"])
	if !value.ValidUUID(list) {
		return nil, Invalid{"Einkaufsliste auswählen"}
	}
	var open bool
	err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, list).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if !open {
		return nil, Invalid{"Eine offene Einkaufsliste auswählen; archivierte Listen und Vorlagen sind ausgeschlossen"}
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM shopping.items WHERE household_id=$1 AND list_id=$2 AND lower(name)=lower($3) AND checked_at IS NULL)", id.HouseholdID, list, product["name"]).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, Invalid{"Produkt steht bereits offen auf dieser Liste"}
	}
	var view PantryReplenishmentView
	var quantity string
	if override := String(data["quantity"]); !reviewed && override != "" {
		// A deliberately entered legacy quantity need not be a usable automatic
		// suggestion (for example, an old fractional minimum on whole pieces).
		amount, parseErr := value.Decimal(override)
		if parseErr != nil {
			return nil, Invalid{"Menge prüfen"}
		}
		quantity, err = pantryExactAmount(amount, product["integer_quantities"] == true)
		if err != nil {
			return nil, err
		}
	} else {
		// Action already owns the product FOR UPDATE lock. Every journal writer
		// needs its conflicting product share lock. A new statement sees writers
		// that committed before that lock was acquired.
		view, err = readPantryReplenishment(ctx, tx, id, String(product["id"]), packID)
		if err != nil {
			return nil, err
		}
		quantity = view.Suggestion.Quantity
		if reviewed {
			if String(data["balance"]) != view.Balance || String(data["movement_count"]) != view.MovementCount ||
				Int(data["pack_version"]) != view.PackVersion || String(data["quantity"]) != quantity {
				return nil, ErrConflict
			}
			if !view.Suggestion.Suggested {
				return nil, Invalid{"Aus diesem geprüften Stand ergibt sich kein Nachkauf"}
			}
		}
	}
	if quantity == "0" {
		return nil, Invalid{"Kein Fehlbestand; bei Bedarf eine positive Einkaufsmenge ausdrücklich angeben"}
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	record, err := save(ctx, tx, id, m, e, value.UUID(), Record{"list_id": list, "name": product["name"], "quantity": quantity, "unit_code": product["unit_code"], "group_name": "Vorrat ergänzen"}, true, 0)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, record, generation); err != nil {
		return nil, err
	}
	return Record{"message": "Einkaufsvorschlag übernommen", "item_id": record["id"], "list_id": list,
		"quantity": quantity, "unit_code": product["unit_code"], "pack_count": view.Suggestion.PackCount}, nil
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
