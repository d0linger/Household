package store

import (
	"context"
	"errors"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func validatePantryPack(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, data Record) error {
	label := String(data["label"])
	if label == "" || utf8.RuneCountInString(label) > 100 {
		return Invalid{"Packungsbezeichnung: 1 bis 100 Zeichen erforderlich"}
	}
	var base string
	err := tx.QueryRow(ctx, "SELECT unit_code FROM pantry.products WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, data["product_id"]).Scan(&base)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return shoppingContention(err)
	}
	amount, err := value.Decimal(String(data["quantity"]))
	if err != nil || amount.Sign() <= 0 {
		return Invalid{"Positiven Packungsinhalt eingeben"}
	}
	converted, err := value.Convert(amount, String(data["unit_code"]), base)
	if err != nil {
		return Invalid{"Packungsinhalt muss zur Basiseinheit des Produkts passen"}
	}
	if _, err = pantryExactAmount(converted, false); err != nil {
		return err
	}
	if rid == "" {
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM pantry.packs WHERE household_id=$1 AND product_id=$2", id.HouseholdID, data["product_id"]).Scan(&count); err != nil {
			return err
		}
		if count >= 50 {
			return Invalid{"Höchstens 50 Packungen pro Produkt; ungenutzte Packungen zuerst prüfen"}
		}
	} else {
		var original string
		if err = tx.QueryRow(ctx, "SELECT product_id FROM pantry.packs WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&original); err != nil {
			return err
		}
		if original != String(data["product_id"]) {
			return Invalid{"Das Produkt einer Packung ist fest. Für ein anderes Produkt eine neue Packung anlegen."}
		}
	}
	return nil
}

func validatePantryBarcode(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, data, changed Record) error {
	code, err := pantryBarcode(String(data["code"]))
	if err != nil {
		return err
	}
	changed["code"] = code
	if _, err = tx.Exec(ctx, "SELECT id FROM pantry.products WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, data["product_id"]); err != nil {
		return shoppingContention(err)
	}
	if pack := String(data["pack_id"]); pack != "" {
		var matches bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pantry.packs WHERE household_id=$1 AND id=$2 AND product_id=$3)", id.HouseholdID, pack, data["product_id"]).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return Invalid{"Die ausgewählte Packung gehört nicht zu diesem Produkt"}
		}
	}
	var label string
	err = tx.QueryRow(ctx, `SELECT p.name FROM pantry.barcodes b JOIN pantry.products p ON p.household_id=b.household_id AND p.id=b.product_id
 WHERE b.household_id=$1 AND b.code=$2 AND b.id<>coalesce(nullif($3,'')::uuid,'00000000-0000-0000-0000-000000000000')`, id.HouseholdID, code, rid).Scan(&label)
	if err == nil {
		return Invalid{"Dieser Barcode ist bereits dem Produkt „" + label + "“ zugeordnet. Bestehende Zuordnung prüfen."}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM pantry.barcodes WHERE household_id=$1 AND product_id=$2 AND id<>coalesce(nullif($3,'')::uuid,'00000000-0000-0000-0000-000000000000')", id.HouseholdID, data["product_id"], rid).Scan(&count); err != nil {
		return err
	}
	if count >= 100 {
		return Invalid{"Höchstens 100 Barcodes pro Produkt"}
	}
	return nil
}

func validatePantryOpening(data, changed Record) error {
	source, note := String(data["after_open_source"]), String(data["after_open_note"])
	if utf8.RuneCountInString(note) > 500 {
		return Invalid{"Quelle des Öffnungshinweises: höchstens 500 Zeichen"}
	}
	if data["after_open_days"] == nil {
		if source != "" || note != "" {
			return Invalid{"Zum Öffnungshinweis eine Tageszahl angeben oder Herkunft und Quellennotiz ausdrücklich leeren"}
		}
	} else if Int(data["after_open_days"]) > 3660 || source != "user" && source != "manufacturer" {
		return Invalid{"Öffnungshinweis: 0 bis 3660 Tage und Herkunft auswählen; leer bedeutet unbekannt"}
	}
	// Generic optional select/text fields accept null; these columns retain an
	// explicit empty string for unknown provenance, while duration remains NULL.
	if _, exists := changed["after_open_source"]; exists {
		changed["after_open_source"] = source
	}
	if _, exists := changed["after_open_note"]; exists {
		changed["after_open_note"] = note
	}
	return nil
}
