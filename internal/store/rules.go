package store

import (
	"context"
	"errors"
	"math/big"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) validateDomain(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid string, data, changed Record) error {
	bad := func(text string) error { return Invalid{text} }
	quantity := func(key string) *big.Rat {
		r, err := value.Decimal(String(data[key]))
		if err != nil {
			return new(big.Rat)
		}
		return r
	}
	switch mid + "/" + entity {
	case "shopping/items":
		var archived bool
		if err := tx.QueryRow(ctx, "SELECT archived_at IS NOT NULL FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR SHARE", id.HouseholdID, data["list_id"]).Scan(&archived); err != nil {
			return err
		}
		if archived {
			return bad("Archivierte Liste zuerst wieder öffnen")
		}
		return validateShoppingItem(ctx, tx, id, rid, data, changed)
	case "shopping/products":
		return validateShoppingProduct(ctx, tx, id, rid, data)
	case "shopping/price_observations":
		return validateShoppingUnit(ctx, tx, id, String(data["product_id"]), String(data["unit_code"]))
	case "recipes/ingredients", "recipes/steps":
		if _, err := tx.Exec(ctx, "SELECT id FROM recipes.recipes WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, data["recipe_id"]); err != nil {
			return err
		}
		if entity == "steps" {
			if Int(data["timer_seconds"]) == 0 {
				changed["timer_seconds"] = nil
			}
			if String(data["instruction"]) == "" {
				return bad("Zubereitungsschritt erforderlich")
			}
		}
	case "fuel/odometer_series":
		if quantity("initial_reading").Sign() < 0 {
			return bad("Anfangsstand darf nicht negativ sein")
		}
		if rid != "" {
			var violates bool
			err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM fuel.fillups WHERE household_id=$1 AND odometer_series_id=$2 AND (odometer<$3::numeric OR occurred_on<$4::date OR occurred_on>nullif($5,'')::date OR vehicle_id<>$6::uuid))", id.HouseholdID, rid, String(data["initial_reading"]), String(data["started_on"]), String(data["ended_on"]), String(data["vehicle_id"])).Scan(&violates)
			if err != nil {
				return err
			}
			if violates {
				return bad("Änderung widerspricht vorhandenen Tankvorgängen")
			}
		}
	case "fuel/fillups":
		var initial, previous, next string
		var validDate bool
		err := tx.QueryRow(ctx, "SELECT initial_reading::text,$4::date>=started_on AND (ended_on IS NULL OR $4::date<=ended_on) FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=$2 AND id=$3 FOR UPDATE", id.HouseholdID, data["vehicle_id"], data["odometer_series_id"], data["occurred_on"]).Scan(&initial, &validDate)
		if errors.Is(err, pgx.ErrNoRows) {
			return bad("Kilometerstandsreihe gehört nicht zum Fahrzeug")
		}
		if err != nil {
			return err
		}
		if !validDate {
			return bad("Tankvorgang liegt außerhalb der Kilometerstandsreihe")
		}
		base, _ := value.Decimal(initial)
		reading := quantity("odometer")
		if reading.Cmp(base) < 0 {
			return bad("Kilometerstand liegt unter dem Anfangsstand")
		}
		err = tx.QueryRow(ctx, `SELECT coalesce(max(odometer) FILTER(WHERE occurred_on<$4::date),0)::text,coalesce(min(odometer) FILTER(WHERE occurred_on>$4::date),999999999999)::text FROM fuel.fillups WHERE household_id=$1 AND odometer_series_id=$2 AND id<>coalesce(nullif($3,'')::uuid,'00000000-0000-0000-0000-000000000000')`, id.HouseholdID, data["odometer_series_id"], rid, data["occurred_on"]).Scan(&previous, &next)
		if err != nil {
			return err
		}
		lo, _ := value.Decimal(previous)
		hi, _ := value.Decimal(next)
		if reading.Cmp(lo) < 0 || reading.Cmp(hi) > 0 {
			return bad("Kilometerstand widerspricht der Fahrzeughistorie")
		}
	case "pantry/stock_movements":
		var current string
		var integers bool
		err := tx.QueryRow(ctx, "SELECT p.integer_quantities FROM pantry.lots l JOIN pantry.products p ON p.id=l.product_id AND p.household_id=l.household_id WHERE l.household_id=$1 AND l.id=$2 FOR UPDATE OF l", id.HouseholdID, data["lot_id"]).Scan(&integers)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, "SELECT coalesce(sum(quantity_delta),0)::text FROM pantry.stock_movements WHERE household_id=$1 AND lot_id=$2", id.HouseholdID, data["lot_id"]).Scan(&current); err != nil {
			return err
		}
		delta := quantity("quantity_delta")
		if delta.Sign() == 0 || integers && !delta.IsInt() {
			return bad("Gültige Bestandsänderung erforderlich")
		}
		reason := String(data["reason"])
		if reason == "receive" && delta.Sign() < 0 || (reason == "consume" || reason == "waste") && delta.Sign() > 0 {
			return bad("Vorzeichen passt nicht zur Bestandsbewegung")
		}
		stock, parseErr := value.Decimal(current)
		if parseErr != nil {
			return Invalid{"Bestand überschreitet den zulässigen Wertebereich"}
		}
		if new(big.Rat).Add(stock, delta).Sign() < 0 {
			return bad("Bestand würde negativ; Vorgang prüfen")
		}
	case "pantry/products":
		if quantity("low_stock_threshold").Sign() < 0 {
			return bad("Mindestbestand darf nicht negativ sein")
		}
		if rid != "" {
			if _, exists := changed["unit_code"]; exists {
				var old string
				var count int
				err := tx.QueryRow(ctx, "SELECT unit_code,(SELECT count(*) FROM pantry.lots l JOIN pantry.stock_movements sm ON sm.lot_id=l.id WHERE l.product_id=p.id) FROM pantry.products p WHERE p.household_id=$1 AND p.id=$2", id.HouseholdID, rid).Scan(&old, &count)
				if err != nil {
					return err
				}
				if count > 0 && old != String(data["unit_code"]) {
					return bad("Einheit mit vorhandenem Bestand nicht ändern")
				}
			}
		}
	case "pantry/lots":
		if rid != "" {
			var oldProduct, oldLocation string
			var movements bool
			err := tx.QueryRow(ctx, "SELECT product_id,location_id,EXISTS(SELECT 1 FROM pantry.stock_movements WHERE lot_id=l.id) FROM pantry.lots l WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&oldProduct, &oldLocation, &movements)
			if err != nil {
				return err
			}
			if movements && (oldProduct != String(data["product_id"]) || oldLocation != String(data["location_id"])) {
				return bad("Produkt und Lagerort einer Charge mit Bewegungshistorie sind fest; Umlagerung verwenden")
			}
		}
	case "expenses/entries":
		if String(data["state"]) == "posted" {
			return bad("Gebuchte Ausgabe über Storno korrigieren")
		}
		amount := Int(data["amount_minor"])
		kind := String(data["kind"])
		if kind == "expense" && amount <= 0 || kind == "refund" && amount >= 0 {
			return bad("Ausgabe positiv, Erstattung negativ eingeben")
		}
	case "expenses/entry_lines":
		if rid != "" {
			var original string
			if err := tx.QueryRow(ctx, "SELECT entry_id FROM expenses.entry_lines WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&original); err != nil {
				return err
			}
			if original != String(data["entry_id"]) {
				return bad("Zuordnung einer Aufteilung ist unveränderlich")
			}
		}
		var status string
		var amount int64
		err := tx.QueryRow(ctx, "SELECT state,amount_minor FROM expenses.entries WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, data["entry_id"]).Scan(&status, &amount)
		if err != nil {
			return err
		}
		if status == "posted" {
			return bad("Gebuchte Aufteilung ist unveränderlich")
		}
		if amount > 0 && Int(data["amount_minor"]) < 0 || amount < 0 && Int(data["amount_minor"]) > 0 {
			return bad("Teilbetrag muss das Vorzeichen der Ausgabe haben")
		}
	case "chores/task_templates":
		if Int(data["repeat_days"]) == 0 {
			changed["repeat_days"] = nil
		}
	case "meters/readings":
		var initial, previous string
		var rollover *string
		var installed time.Time
		var removed *time.Time
		err := tx.QueryRow(ctx, "SELECT initial_reading::text,rollover_value::text,installed_on,removed_on FROM meters.devices WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, data["device_id"]).Scan(&initial, &rollover, &installed, &removed)
		if err != nil {
			return err
		}
		measured, err := time.Parse(time.RFC3339, String(data["measured_at"]))
		if err != nil {
			return bad("Ungültiger Ablesezeitpunkt")
		}
		if measured.Before(installed) || removed != nil && measured.After(removed.Add(24*time.Hour)) {
			return bad("Ablesung liegt außerhalb der Gerätelaufzeit")
		}
		reading := quantity("value")
		base, _ := value.Decimal(initial)
		if reading.Sign() < 0 || rollover == nil && reading.Cmp(base) < 0 {
			return bad("Ablesung liegt unter dem Anfangsstand")
		}
		if err = tx.QueryRow(ctx, "SELECT coalesce(max(value),$4::numeric)::text FROM meters.readings WHERE household_id=$1 AND device_id=$2 AND measured_at<$3::timestamptz AND id<>coalesce(nullif($5,'')::uuid,'00000000-0000-0000-0000-000000000000')", id.HouseholdID, data["device_id"], data["measured_at"], initial, rid).Scan(&previous); err != nil {
			return err
		}
		prev, _ := value.Decimal(previous)
		if rollover == nil && reading.Cmp(prev) < 0 {
			return bad("Ablesung widerspricht vorherigen Werten")
		}
		if rollover == nil {
			var next *string
			if err = tx.QueryRow(ctx, "SELECT min(value)::text FROM meters.readings WHERE household_id=$1 AND device_id=$2 AND measured_at>$3::timestamptz AND id<>coalesce(nullif($4,'')::uuid,'00000000-0000-0000-0000-000000000000')", id.HouseholdID, data["device_id"], data["measured_at"], rid).Scan(&next); err != nil {
				return err
			}
			if next != nil {
				upper, err := value.Decimal(*next)
				if err != nil {
					return err
				}
				if reading.Cmp(upper) > 0 {
					return bad("Ablesung übersteigt einen späteren Wert")
				}
			}
		}
		if rollover != nil {
			max, _ := value.Decimal(*rollover)
			if reading.Cmp(max) >= 0 {
				return bad("Wert liegt außerhalb des Zählerbereichs")
			}
		}
	case "meters/devices":
		if quantity("initial_reading").Sign() < 0 {
			return bad("Anfangsstand darf nicht negativ sein")
		}
		if rid != "" {
			var violates bool
			err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM meters.readings WHERE household_id=$1 AND device_id=$2 AND (measured_at::date<$3::date OR measured_at::date>nullif($4,'')::date OR (nullif($5,'')::numeric IS NULL AND value<$6::numeric) OR value>=nullif($5,'')::numeric))", id.HouseholdID, rid, String(data["installed_on"]), String(data["removed_on"]), String(data["rollover_value"]), String(data["initial_reading"])).Scan(&violates)
			if err != nil {
				return err
			}
			if violates {
				return bad("Geräteänderung widerspricht vorhandenen Ablesungen")
			}
			var history bool
			var meter string
			var rollover *string
			err = tx.QueryRow(ctx, "SELECT meter_id,rollover_value::text,EXISTS(SELECT 1 FROM meters.readings WHERE device_id=d.id) FROM meters.devices d WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&meter, &rollover, &history)
			if err != nil {
				return err
			}
			oldRoll := ""
			if rollover != nil {
				oldRoll = *rollover
			}
			if history && (meter != String(data["meter_id"]) || oldRoll != String(data["rollover_value"])) {
				return bad("Zählerzuordnung und Überlaufbereich mit Historie nicht ändern")
			}
		}
	case "meters/meters":
		if rid != "" {
			var old string
			var used bool
			err := tx.QueryRow(ctx, "SELECT unit_code,EXISTS(SELECT 1 FROM meters.devices WHERE meter_id=m.id) FROM meters.meters m WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&old, &used)
			if err != nil {
				return err
			}
			if used && old != String(data["unit_code"]) {
				return bad("Einheit mit vorhandenen Geräten nicht ändern")
			}
		}
	case "mealplan/plans":
		if rid != "" {
			var violates bool
			err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND planned_on NOT BETWEEN $3::date AND $4::date)", id.HouseholdID, rid, data["starts_on"], data["ends_on"]).Scan(&violates)
			if err != nil {
				return err
			}
			if violates {
				return bad("Planzeitraum würde vorhandene Mahlzeiten ausschließen")
			}
		}
	case "mealplan/meals":
		var valid bool
		err := tx.QueryRow(ctx, "SELECT $3::date BETWEEN starts_on AND ends_on FROM mealplan.plans WHERE household_id=$1 AND id=$2 FOR SHARE", id.HouseholdID, data["plan_id"], data["planned_on"]).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return bad("Mahlzeit liegt außerhalb des Plans")
		}
	}
	return nil
}
