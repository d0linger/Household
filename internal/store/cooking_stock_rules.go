package store

import (
	"context"
	"errors"
	"math/big"
	"slices"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func validateCookingStock(ctx context.Context, tx pgx.Tx, id auth.Identity, source Record, in CookingStockInput) ([]Record, map[string]Record, map[string]string, error) {
	rows := source["ingredients"].([]any)
	if len(rows) != len(in.Decisions) {
		return nil, nil, nil, Invalid{"Jede gespeicherte Quellzutat ausdrücklich verbrauchen oder überspringen"}
	}
	indexed := map[string]Record{}
	order := map[string]int{}
	for i, raw := range rows {
		row := raw.(map[string]any)
		indexed[String(row["id"])] = row
		order[String(row["id"])] = i
	}
	base, servings, err := cookingStockPortions(source)
	if err != nil {
		return nil, nil, nil, err
	}
	lots := map[string]Record{}
	for _, review := range in.Lots {
		lot, lockErr := lockPantryLot(ctx, tx, id, review.ID)
		if lockErr != nil {
			return nil, nil, nil, lockErr
		}
		if review.Version != Int(lot["version"]) || review.LocationVersion != Int(lot["location_version"]) || review.MovementCount != Int(lot["movement_count"]) || review.Balance != String(lot["stock"]) {
			return nil, nil, nil, PantryStockConflict{Message: "Charge oder Bestand wurde geändert. Alle tatsächlichen Mengen erneut prüfen.", Current: lot}
		}
		if lot["product_archived"] == true || lot["location_archived"] == true {
			return nil, nil, nil, Invalid{"Archivierte Produkte oder Lagerorte können nicht verbraucht werden"}
		}
		lots[review.ID] = lot
	}
	totals := map[string]*big.Rat{}
	decisions := []Record{}
	for _, d := range in.Decisions {
		row := indexed[d.IngredientID]
		if row == nil {
			return nil, nil, nil, Invalid{"Entscheidung gehört nicht zur abgeschlossenen Rezeptversion"}
		}
		decision := Record{"ingredient_id": d.IngredientID, "name": row["name"], "catalog_label": row["catalog_label"], "action": d.Action, "reason": d.Reason}
		if d.Action == "skip" {
			decisions = append(decisions, decision)
			continue
		}
		first := lots[d.Allocations[0].LotID]
		if first == nil || String(first["product_id"]) != d.ProductID || Int(first["product_version"]) != d.ProductVersion {
			return nil, nil, nil, ErrConflict
		}
		if d.Selection == "catalog" {
			if err = lockCookingStockBinding(ctx, tx, id, String(row["catalog_id"]), in, d); err != nil {
				return nil, nil, nil, err
			}
		}
		integers, _ := first["integer_quantities"].(bool)
		actual, suggested, amountErr := cookingStockActual(row, d, String(first["unit"]), integers, base, servings)
		if amountErr != nil {
			return nil, nil, nil, amountErr
		}
		allocations := []Record{}
		for _, a := range d.Allocations {
			lot := lots[a.LotID]
			if lot == nil || String(lot["product_id"]) != d.ProductID || Int(lot["product_version"]) != d.ProductVersion {
				return nil, nil, nil, ErrConflict
			}
			q, parseErr := value.Decimal(a.Quantity)
			if parseErr != nil {
				return nil, nil, nil, parseErr
			}
			if _, parseErr = pantryExactAmount(q, integers); parseErr != nil {
				return nil, nil, nil, parseErr
			}
			if totals[a.LotID] == nil {
				totals[a.LotID] = new(big.Rat)
			}
			totals[a.LotID].Add(totals[a.LotID], q)
			allocations = append(allocations, Record{"lot_id": a.LotID, "quantity": a.Quantity, "location": lot["location"], "lot_label": lot["label"]})
		}
		decision["product_id"], decision["product_name"], decision["unit"], decision["selection"] = d.ProductID, first["product_name"], first["unit"], d.Selection
		decision["actual"], decision["suggested"], decision["allocations"] = actual, suggested, allocations
		decisions = append(decisions, decision)
	}
	amounts := map[string]string{}
	for lid, q := range totals {
		integers, _ := lots[lid]["integer_quantities"].(bool)
		amount, amountErr := pantryExactAmount(q, integers)
		if amountErr != nil {
			return nil, nil, nil, amountErr
		}
		stock, parseErr := stockDecimal(String(lots[lid]["stock"]))
		if parseErr != nil {
			return nil, nil, nil, parseErr
		}
		if q.Cmp(stock) > 0 {
			return nil, nil, nil, PantryStockConflict{Message: "Die gemeinsame Entnahme mehrerer Zutaten überschreitet den Chargenbestand.", Current: lots[lid]}
		}
		amounts[lid] = amount
	}
	slices.SortFunc(decisions, func(a, b Record) int { return order[String(a["ingredient_id"])] - order[String(b["ingredient_id"])] })
	return decisions, lots, amounts, nil
}

func lockCookingStockBinding(ctx context.Context, tx pgx.Tx, id auth.Identity, catalog string, in CookingStockInput, d CookingStockDecision) error {
	if !value.ValidUUID(catalog) {
		return Invalid{"Quellzutat besitzt keine Katalogidentität; einmalige Auswahl begründen"}
	}
	var version int64
	var archived bool
	err := tx.QueryRow(ctx, "SELECT version,archived FROM recipes.ingredient_catalog WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, catalog).Scan(&version, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return shoppingContention(err)
	}
	if version != d.CatalogVersion || archived {
		return ErrConflict
	}
	var binding, product string
	var bindingVersion, target int64
	err = tx.QueryRow(ctx, `SELECT id,version,coalesce(product_id::text,''),coalesce(target_generation,0) FROM core.ingredient_product_bindings
 WHERE household_id=$1 AND source_generation=$2 AND catalog_id=$3 FOR SHARE NOWAIT`, id.HouseholdID, in.Generation, catalog).Scan(&binding, &bindingVersion, &product, &target)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return shoppingContention(err)
	}
	if binding != d.BindingID || bindingVersion != d.BindingVersion || product != d.ProductID || target != in.PantryGeneration {
		return ErrConflict
	}
	return nil
}
