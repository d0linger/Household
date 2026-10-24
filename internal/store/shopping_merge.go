package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// mergeShoppingItems keeps the reviewed survivor. The action already owns its
// row; NOWAIT on subsequent locks makes opposite selections retry, not deadlock.
func (s *Store) mergeShoppingItems(ctx context.Context, tx pgx.Tx, id auth.Identity, survivor, data Record) ([]byte, error) {
	otherID := String(data["other_id"])
	if !value.ValidUUID(otherID) || otherID == String(survivor["id"]) {
		return nil, Invalid{"Zwei unterschiedliche Artikel auswählen"}
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM shopping.items t WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT", id.HouseholdID, otherID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	other, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(other["version"]) != Int(data["other_version"]) {
		return nil, ErrConflict
	}
	if err = shoppingMergeCompatible(survivor, other); err != nil {
		return nil, err
	}
	var working bool
	err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, survivor["list_id"]).Scan(&working)
	if err != nil {
		return nil, shoppingContention(err)
	}
	if !working {
		return nil, Invalid{"Nur Artikel einer offenen Einkaufsliste zusammenführen"}
	}
	var linked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.attachments WHERE household_id=$1 AND module_id='shopping' AND resource_type='items' AND resource_id=$2)
 OR EXISTS(SELECT 1 FROM core.resource_links WHERE household_id=$1 AND ((source_module='shopping' AND source_type='items' AND source_id=$2) OR (target_module='shopping' AND target_type='items' AND target_id=$2)))`, id.HouseholdID, otherID).Scan(&linked)
	if err != nil {
		return nil, err
	}
	if linked {
		return nil, Invalid{"Der zweite Artikel hat Dateien oder Verknüpfungen. Diese vor dem Zusammenführen gesondert prüfen"}
	}
	product := survivor["product_id"]
	if String(survivor["replacement_product_id"]) != "" {
		product = survivor["replacement_product_id"]
	}
	var pack, packUnit string
	err = tx.QueryRow(ctx, "SELECT pack_quantity::text,pack_unit FROM shopping.products WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, product).Scan(&pack, &packUnit)
	if err != nil {
		return nil, shoppingContention(err)
	}
	amount, err := shoppingMergedQuantity(String(survivor["quantity"]), String(survivor["unit_code"]), String(other["quantity"]), String(other["unit_code"]), pack, packUnit)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, "UPDATE shopping.items t SET quantity=$3,version=version+1,updated_at=now() WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, survivor["id"], amount).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM shopping.items WHERE household_id=$1 AND id=$2", id.HouseholdID, otherID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM core.search_documents WHERE household_id=$1 AND module_id='shopping' AND resource_type='items' AND resource_id=$2", id.HouseholdID, otherID); err != nil {
		return nil, err
	}
	result, err := decode(raw)
	if err != nil {
		return nil, err
	}
	// The outer action writes the survivor projection, audit and retry receipt.
	result["quantity"] = amount
	return json.Marshal(result)
}

func shoppingMergeCompatible(a, b Record) error {
	if String(a["id"]) != "" && String(a["id"]) == String(b["id"]) {
		return Invalid{"Zwei unterschiedliche Artikel auswählen"}
	}
	if a["checked_at"] != nil || b["checked_at"] != nil {
		return Invalid{"Erledigte Artikel zuerst wieder öffnen"}
	}
	if String(a["product_id"]) == "" || String(b["product_id"]) == "" {
		return Invalid{"Freie Artikel zuerst einer eindeutigen Produktvariante zuordnen"}
	}
	for _, field := range []string{"list_id", "product_id", "replacement_product_id", "availability", "assignee_id", "note", "group_name"} {
		if String(a[field]) != String(b[field]) {
			return Invalid{"Liste, Produkt, Ersatz, Verfügbarkeit, Zuständigkeit, Notiz und Gruppe müssen übereinstimmen. Unterschiede zuerst in den Details prüfen"}
		}
	}
	return nil
}

// PreviewShoppingMerge computes a non-binding proposal from read snapshots.
// The committing action checks all identities, versions and locks again.
func PreviewShoppingMerge(a, b Record, pack, packUnit string) (string, error) {
	if err := shoppingMergeCompatible(a, b); err != nil {
		return "", err
	}
	return shoppingMergedQuantity(String(a["quantity"]), String(a["unit_code"]), String(b["quantity"]), String(b["unit_code"]), pack, packUnit)
}

func shoppingMergedQuantity(a, aUnit, b, bUnit, pack, packUnit string) (string, error) {
	bad := Invalid{"Positive Mengen und kompatible Einheiten erforderlich; Ergebnis muss ohne Rundung mit sechs Nachkommastellen darstellbar sein"}
	x, err := value.Decimal(a)
	if err != nil || x.Sign() <= 0 {
		return "", bad
	}
	y, err := value.Decimal(b)
	if err != nil || y.Sign() <= 0 {
		return "", bad
	}
	p, err := value.Decimal(pack)
	if err != nil || p.Sign() <= 0 {
		return "", bad
	}
	// A piece counts one immutable catalog pack, never a guessed mass/volume.
	if bUnit == "piece" {
		y.Mul(y, p)
		bUnit = packUnit
	}
	target := aUnit
	if target == "piece" {
		target = packUnit
	}
	y, err = value.Convert(y, bUnit, target)
	if err != nil {
		return "", bad
	}
	if aUnit == "piece" {
		y.Quo(y, p)
	}
	total := new(big.Rat).Add(x, y)
	micro := new(big.Rat).Mul(total, big.NewRat(1000000, 1))
	if !micro.IsInt() {
		return "", bad
	}
	text := total.FloatString(6)
	if _, err = value.Decimal(text); err != nil {
		return "", Invalid{"Gesamtmenge überschreitet den zulässigen Wertebereich"}
	}
	return text, nil
}
