package store

import (
	"context"
	"errors"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func validateShoppingItem(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, data, changed Record) error {
	availability := String(data["availability"])
	replacement, product := String(data["replacement_product_id"]), String(data["product_id"])
	if (availability == "substituted") != (replacement != "") || replacement != "" && (product == "" || replacement == product) {
		return Invalid{"Für Ersatz ein anderes Produkt wählen; bei verfügbar/nicht verfügbar das Ersatzprodukt leeren"}
	}
	if assignment, changedAssignment := changed["assignee_id"]; changedAssignment && String(assignment) != "" {
		var unchanged bool
		if rid != "" {
			if err := tx.QueryRow(ctx, "SELECT coalesce(assignee_id=$3::uuid,false) FROM shopping.items WHERE household_id=$1 AND id=$2", id.HouseholdID, rid, assignment).Scan(&unchanged); err != nil {
				return err
			}
		}
		if !unchanged {
			var member string
			err := tx.QueryRow(ctx, `SELECT m.id FROM core.memberships m JOIN core.users u ON u.id=m.user_id
		 WHERE m.household_id=$1 AND m.id=$2 AND m.inactive_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF m,u`, id.HouseholdID, assignment).Scan(&member)
			if errors.Is(err, pgx.ErrNoRows) {
				return Invalid{"Zuständige Person ist nicht aktiv in diesem Haushalt"}
			}
			if err != nil {
				return err
			}
		}
	}
	for _, selected := range []string{product, replacement} {
		if selected != "" {
			if err := validateShoppingUnit(ctx, tx, id, selected, String(data["unit_code"])); err != nil {
				return err
			}
		}
	}
	return nil
}

// Piece quantities count the chosen catalog pack. Other units must share its
// physical dimension; a density is never guessed from the product's name.
func validateShoppingUnit(ctx context.Context, tx pgx.Tx, id auth.Identity, product, unit string) error {
	var compatible bool
	err := tx.QueryRow(ctx, `SELECT $3 IN('','piece') OR u.dimension=(SELECT dimension FROM core.units WHERE code=$3)
	 FROM shopping.products p JOIN core.units u ON u.code=p.pack_unit WHERE p.household_id=$1 AND p.id=$2 FOR SHARE OF p`, id.HouseholdID, product, unit).Scan(&compatible)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invalid{"Produktvariante ist in diesem Haushalt nicht verfügbar"}
	}
	if err != nil {
		return err
	}
	if !compatible {
		return Invalid{"Einheit passt nicht zum Packungsinhalt des Produkts"}
	}
	return nil
}

func validateShoppingProduct(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, data Record) error {
	if rid == "" {
		return nil
	}
	var changed, used bool
	err := tx.QueryRow(ctx, `SELECT (pack_quantity<>$3::numeric OR pack_unit<>$4),
	 EXISTS(SELECT 1 FROM shopping.items WHERE household_id=$1 AND (product_id=$2 OR replacement_product_id=$2))
	 OR EXISTS(SELECT 1 FROM shopping.price_observations WHERE household_id=$1 AND product_id=$2)
	 OR EXISTS(SELECT 1 FROM shopping.barcodes WHERE household_id=$1 AND product_id=$2)
	 FROM shopping.products WHERE household_id=$1 AND id=$2`, id.HouseholdID, rid, data["pack_quantity"], data["pack_unit"]).Scan(&changed, &used)
	if err != nil {
		return err
	}
	if changed && used {
		return Invalid{"Packungsinhalt wird bereits verwendet. Für eine andere Packung eine neue Produktvariante anlegen"}
	}
	return nil
}

func claimShoppingList(ctx context.Context, tx pgx.Tx, id auth.Identity, list Record, data Record) ([]byte, error) {
	if list["is_template"] == true {
		return nil, Invalid{"Vorlage zuerst als neue Einkaufsliste kopieren"}
	}
	if list["archived_at"] != nil {
		return nil, Invalid{"Archivierte Liste zuerst wieder öffnen"}
	}
	active, ok := data["active"].(bool)
	if !ok {
		return nil, Invalid{"Einkaufsstatus erforderlich"}
	}
	var member string
	if err := tx.QueryRow(ctx, "SELECT id FROM core.memberships WHERE household_id=$1 AND user_id=$2 AND inactive_at IS NULL", id.HouseholdID, id.UserID).Scan(&member); err != nil {
		return nil, err
	}
	var holder *string
	if err := tx.QueryRow(ctx, `SELECT (SELECT m.id FROM core.memberships m JOIN core.users u ON u.id=m.user_id
	 WHERE m.household_id=l.household_id AND m.id=l.shopper_id AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND l.shopper_until>now())
	 FROM shopping.lists l WHERE l.household_id=$1 AND l.id=$2`, id.HouseholdID, list["id"]).Scan(&holder); err != nil {
		return nil, err
	}
	if holder != nil && *holder != member && (active || id.Role != "admin") {
		return nil, Invalid{"Eine andere Person kauft gerade ein. Nach Ablauf erneut versuchen oder freigeben lassen"}
	}
	var raw []byte
	err := tx.QueryRow(ctx, `UPDATE shopping.lists t SET shopper_id=CASE WHEN $3 THEN $4::uuid END,
	 shopper_until=CASE WHEN $3 THEN now()+interval '30 minutes' END,version=version+1,updated_at=now()
	 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)`, id.HouseholdID, list["id"], active, member).Scan(&raw)
	return raw, err
}
