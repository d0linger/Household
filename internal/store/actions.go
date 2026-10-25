package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) Action(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, rid, action, key, requestID string, in Mutation) (Record, error) {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if !value.ValidUUID(rid) || !value.ValidUUID(key) {
		return nil, Invalid{"gültige Vorgangs-ID erforderlich"}
	}
	tx, err := s.BeginMutation(ctx, id, m.ID, in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	targets := map[string]int64{}
	for _, target := range ActionTargets(m.ID, e.ID, action) {
		generation, targetErr := requireModule(ctx, tx, id, target)
		if targetErr != nil {
			return nil, targetErr
		}
		if in.TargetGenerations[target] != generation {
			return nil, ErrConflict
		}
		targets[target] = generation
	}
	op := "action/" + m.ID + "/" + e.ID + "/" + rid + "/" + action
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var raw []byte
	table := pgx.Identifier{m.ID, e.ID}.Sanitize()
	lock := " FOR UPDATE"
	if e.Immutable {
		lock = ""
	} // Immutable rows cannot be changed by the runtime role; the household barrier blocks maintenance purge.
	err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+table+" t WHERE household_id=$1 AND id=$2"+lock, id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(record["version"]) != in.Version {
		return nil, ErrConflict
	}
	data := Record{}
	for k, v := range in.Data {
		var val any
		d := json.NewDecoder(strings.NewReader(string(v)))
		d.UseNumber()
		if err = d.Decode(&val); err != nil {
			return nil, Invalid{"ungültige Aktionsdaten"}
		}
		data[k] = val
	}
	switch m.ID + "/" + e.ID + "/" + action {
	case "shopping/items/merge":
		raw, err = s.mergeShoppingItems(ctx, tx, id, record, data)
	case "shopping/lists/clone":
		raw, err = s.copyShoppingList(ctx, tx, id, record, data, in.Generation)
	case "shopping/lists/activity":
		raw, err = claimShoppingList(ctx, tx, id, record, data)
	case "shopping/lists/archive":
		archived := data["archived"] == true || data["archived"] == "true"
		if data["archived"] != true && data["archived"] != false && data["archived"] != "true" && data["archived"] != "false" {
			return nil, Invalid{"Zielzustand erforderlich"}
		}
		err = tx.QueryRow(ctx, "UPDATE shopping.lists t SET archived_at=CASE WHEN $3 THEN now() END,shopper_id=NULL,shopper_until=NULL,updated_at=now(),version=version+1 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, rid, archived).Scan(&raw)
	case "shopping/items/check":
		var open bool
		if err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR SHARE", id.HouseholdID, record["list_id"]).Scan(&open); err != nil {
			return nil, err
		}
		if !open {
			return nil, Invalid{"Archivierte Liste oder Vorlage zuerst als neue Einkaufsliste kopieren"}
		}
		checked, ok := data["checked"].(bool)
		if !ok {
			return nil, Invalid{"Zielzustand erforderlich"}
		}
		if checked && String(record["availability"]) == "unavailable" {
			return nil, Invalid{"Artikel ist als nicht verfügbar markiert. Verfügbarkeit oder Ersatz zuerst bearbeiten"}
		}
		err = tx.QueryRow(ctx, "UPDATE shopping.items t SET checked_at=CASE WHEN $3 THEN now() END,checked_by=CASE WHEN $3 THEN $4::uuid END,updated_at=now(),version=version+1 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, rid, checked, id.UserID).Scan(&raw)
		if err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO shopping.item_events(household_id,item_id,action,actor_user_id) VALUES($1,$2,$3,$4)", id.HouseholdID, rid, map[bool]string{true: "check", false: "uncheck"}[checked], id.UserID)
		}
	case "expenses/entries/post":
		if String(record["state"]) != "draft" {
			return nil, ErrConflict
		}
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2", id.HouseholdID, rid).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			if !value.ValidUUID(String(data["category_id"])) {
				return nil, Invalid{"Kategorie oder vollständige Aufteilung erforderlich"}
			}
			_, err = tx.Exec(ctx, "INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by) VALUES($1,$2,$3,$4,$5)", id.HouseholdID, rid, data["category_id"], record["amount_minor"], id.UserID)
			if err != nil {
				return nil, err
			}
		}
		err = tx.QueryRow(ctx, "UPDATE expenses.entries t SET state='posted',posted_at=now(),updated_at=now(),version=version+1 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, rid).Scan(&raw)
	case "expenses/entries/reverse":
		if String(record["state"]) != "posted" {
			return nil, Invalid{"nur gebuchte Einträge stornieren"}
		}
		newID := value.UUID()
		_, err = tx.Exec(ctx, "INSERT INTO expenses.entries(id,household_id,occurred_on,payee,description,amount_minor,kind,reverses_entry_id,created_by) VALUES($1,$2,current_date,$3,$4,$5,'reversal',$6,$7)", newID, id.HouseholdID, record["payee"], "Storno: "+String(record["description"]), -Int(record["amount_minor"]), rid, id.UserID)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,note,position,created_by) SELECT household_id,$2,category_id,-amount_minor,note,position,$3 FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$4", id.HouseholdID, newID, id.UserID, rid)
		if err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, "UPDATE expenses.entries t SET state='posted',posted_at=now() WHERE id=$1 RETURNING to_jsonb(t)", newID).Scan(&raw)
	case "chores/task_occurrences/complete":
		if String(record["state"]) != "open" {
			return nil, ErrConflict
		}
		err = tx.QueryRow(ctx, "UPDATE chores.task_occurrences t SET state='completed',completed_at=now(),completed_by=$3,updated_at=now(),version=version+1 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, rid, id.UserID).Scan(&raw)
		if err != nil {
			return nil, err
		}
		var nextRaw []byte
		err = tx.QueryRow(ctx, `INSERT INTO chores.task_occurrences AS t(household_id,template_id,occurrence_key,due_at,assignee_id,created_by)
		 SELECT o.household_id,o.template_id,((o.due_at AT TIME ZONE t.timezone)+make_interval(days=>t.repeat_days))::text,((o.due_at AT TIME ZONE t.timezone)+make_interval(days=>t.repeat_days)) AT TIME ZONE t.timezone,o.assignee_id,$3
		 FROM chores.task_occurrences o JOIN chores.task_templates t ON t.id=o.template_id AND t.household_id=o.household_id WHERE o.household_id=$1 AND o.id=$2 AND t.active AND t.repeat_days>0 ON CONFLICT(template_id,occurrence_key) DO NOTHING RETURNING to_jsonb(t)`, id.HouseholdID, rid, id.UserID).Scan(&nextRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		} else if err == nil {
			var next Record
			next, err = decode(nextRaw)
			if err == nil {
				err = s.afterWrite(ctx, tx, id, m, e, next, in.Generation)
			}
		}
	case "recipes/recipes/shopping":
		err = s.recipeToShopping(ctx, tx, id, record, data)
		if err == nil {
			raw = []byte(`{"message":"Zutaten zur Einkaufsliste hinzugefügt"}`)
		}
	case "recipes/recipes/tags":
		ids, ok := data["tag_ids"].([]any)
		if !ok && data["tag_ids"] != nil {
			return nil, Invalid{"Tags als Liste angeben"}
		}
		if len(ids) > 50 {
			return nil, Invalid{"höchstens 50 Tags"}
		}
		if _, err = tx.Exec(ctx, "DELETE FROM recipes.recipe_tags WHERE household_id=$1 AND recipe_id=$2", id.HouseholdID, rid); err != nil {
			return nil, err
		}
		for _, tag := range ids {
			if !value.ValidUUID(String(tag)) {
				return nil, Invalid{"ungültiger Tag"}
			}
			if _, err = tx.Exec(ctx, "INSERT INTO recipes.recipe_tags(household_id,recipe_id,tag_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", id.HouseholdID, rid, tag); err != nil {
				return nil, err
			}
		}
		err = tx.QueryRow(ctx, "UPDATE recipes.recipes t SET version=version+1,updated_at=now() WHERE id=$1 RETURNING to_jsonb(t)", rid).Scan(&raw)
	case "pantry/products/shopping":
		err = s.pantryToShopping(ctx, tx, id, record, data)
		if err == nil {
			raw = []byte(`{"message":"Einkaufsvorschlag übernommen"}`)
		}
	case "mealplan/meals/recipe":
		return nil, Invalid{"Rezeptstände und Komponenten ausdrücklich in der Wochenplanung prüfen; die alte Einzelverknüpfung wird nicht mehr gespeichert"}
	case "mealplan/plans/shopping":
		err = s.mealsToShopping(ctx, tx, id, record, data)
		if err == nil {
			raw = []byte(`{"message":"Zutaten geplanter Rezepte übernommen"}`)
		}
	case "fuel/fillups/expense":
		var result Record
		result, err = s.fuelToExpense(ctx, tx, id, record, data)
		if err == nil {
			raw, err = json.Marshal(result)
		}
	case "pantry/lots/transfer":
		err = s.transferStock(ctx, tx, id, record, data)
		if err == nil {
			raw = []byte(`{"message":"Bestand umgelagert"}`)
		}
	case "pantry/stock_movements/reverse":
		if String(record["reason"]) == "transfer" {
			return nil, Invalid{"Umlagerungen durch eine vollständige Gegen-Umlagerung korrigieren"}
		}
		if _, err = tx.Exec(ctx, "SELECT id FROM pantry.lots WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, record["lot_id"]); err != nil {
			return nil, err
		}
		var balance string
		if err = tx.QueryRow(ctx, "SELECT coalesce((SELECT quantity FROM pantry.lot_balances WHERE household_id=$1 AND lot_id=$2),0)::text", id.HouseholdID, record["lot_id"]).Scan(&balance); err != nil {
			return nil, err
		}
		stock, _ := stockDecimal(balance)
		original, _ := value.Decimal(String(record["quantity_delta"]))
		if stock == nil || original == nil || new(big.Rat).Sub(stock, original).Sign() < 0 {
			return nil, Invalid{"Korrektur würde negativen Bestand erzeugen"}
		}
		err = tx.QueryRow(ctx, "INSERT INTO pantry.stock_movements AS t(household_id,lot_id,quantity_delta,reason,reverses_movement_id,note,created_by) VALUES($1,$2,-$3::numeric,'reversal',$4,'Ausgleich',$5) RETURNING to_jsonb(t)", id.HouseholdID, record["lot_id"], String(record["quantity_delta"]), rid, id.UserID).Scan(&raw)
	default:
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	result, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if result["id"] != nil {
		if err = s.afterWrite(ctx, tx, id, m, e, result, in.Generation); err != nil {
			return nil, err
		}
	}
	if err = Audit(ctx, tx, id, m.ID, e.ID, rid, action, requestID); err != nil {
		return nil, err
	}
	targetJSON, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload,target_generations) VALUES($1,$2,$3,$4,$5,$6,$7,200,$8,$9)", id.HouseholdID, id.UserID, op, key, hash[:], m.ID, in.Generation, raw, targetJSON); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// ActionTargets is the compiled contract for destination generation binding.
func ActionTargets(mid, entity, action string) []string {
	switch mid + "/" + entity + "/" + action {
	case "recipes/recipes/shopping", "pantry/products/shopping":
		return []string{"shopping"}
	case "fuel/fillups/expense":
		return []string{"expenses"}
	case "mealplan/plans/shopping":
		return []string{"shopping"}
	default:
		return nil
	}
}

func requireModule(ctx context.Context, tx pgx.Tx, id auth.Identity, mid string) (int64, error) {
	var state string
	var generation int64
	err := tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2 FOR SHARE", id.HouseholdID, mid).Scan(&state, &generation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "enabled" {
		return 0, ErrNotFound
	}
	return generation, err
}

func (s *Store) recipeToShopping(ctx context.Context, tx pgx.Tx, id auth.Identity, recipe, data Record) error {
	generation, err := requireModule(ctx, tx, id, "shopping")
	if err != nil {
		return err
	}
	listID := String(data["list_id"])
	if !value.ValidUUID(listID) {
		return Invalid{"Einkaufsliste auswählen"}
	}
	var open bool
	if err = tx.QueryRow(ctx, "SELECT archived_at IS NULL AND NOT is_template FROM shopping.lists WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, listID).Scan(&open); err != nil {
		return err
	}
	if !open {
		return Invalid{"Einkaufsliste ist archiviert"}
	}
	servings, err := value.Decimal(String(data["servings"]))
	if err != nil || servings.Sign() <= 0 || servings.Cmp(big.NewRat(1000, 1)) > 0 {
		return Invalid{"gültige Portionenzahl erforderlich"}
	}
	base, err := value.Decimal(String(recipe["base_servings"]))
	if err != nil {
		return err
	}
	factor := new(big.Rat).Quo(servings, base)
	rows, err := tx.Query(ctx, "SELECT id,name,quantity::text,unit_code,preparation_note FROM recipes.ingredients WHERE household_id=$1 AND recipe_id=$2 ORDER BY position,id", id.HouseholdID, recipe["id"])
	if err != nil {
		return err
	}
	type ingredient struct {
		ID       string
		Name     string
		Quantity *string
		Unit     *string
		Note     string
	}
	ingredients := []ingredient{}
	for rows.Next() {
		var x ingredient
		if err = rows.Scan(&x.ID, &x.Name, &x.Quantity, &x.Unit, &x.Note); err != nil {
			rows.Close()
			return err
		}
		ingredients = append(ingredients, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ingredients) == 0 {
		return Invalid{"Rezept enthält noch keine Zutaten"}
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	selected := map[string]bool{}
	if ids, exists := data["ingredient_ids"]; exists {
		list, ok := ids.([]any)
		if !ok || len(list) == 0 {
			return Invalid{"Mindestens eine Zutat auswählen"}
		}
		for _, id := range list {
			selected[String(id)] = true
		}
		valid := map[string]bool{}
		for _, x := range ingredients {
			valid[x.ID] = true
		}
		for id := range selected {
			if !valid[id] {
				return Invalid{"Zutat gehört nicht mehr zu diesem Rezept; Auswahl neu laden"}
			}
		}
	}
	for _, x := range ingredients {
		if len(selected) > 0 && !selected[x.ID] {
			continue
		}
		var quantity any
		var unit any = x.Unit
		if x.Quantity != nil {
			q, err := value.Decimal(*x.Quantity)
			if err != nil {
				return err
			}
			scaled := new(big.Rat).Mul(q, factor)
			if overrides, ok := data["unit_overrides"].(map[string]any); ok && overrides[x.ID] != nil && x.Unit != nil {
				target := String(overrides[x.ID])
				scaled, err = value.Convert(scaled, *x.Unit, target)
				if err != nil {
					return Invalid{"Einheiten dieser Zutat sind nicht kompatibel"}
				}
				unit = target
			}
			quantity = scaled.FloatString(6)
		}
		r, err := save(ctx, tx, id, m, e, value.UUID(), Record{"list_id": listID, "name": x.Name, "quantity": quantity, "unit_code": unit, "note": x.Note, "group_name": "Aus Rezept: " + String(recipe["title"])}, true, 0)
		if err != nil {
			return err
		}
		if err = s.afterWrite(ctx, tx, id, m, e, r, generation); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) fuelToExpense(ctx context.Context, tx pgx.Tx, id auth.Identity, fill, data Record) (Record, error) {
	generation, err := requireModule(ctx, tx, id, "expenses")
	if err != nil {
		return nil, err
	}
	var existing string
	err = tx.QueryRow(ctx, "SELECT target_id FROM core.resource_links WHERE household_id=$1 AND source_module='fuel' AND source_id=$2 AND relation='fuel_expense'", id.HouseholdID, fill["id"]).Scan(&existing)
	if err == nil {
		return Record{"message": "Bereits verknüpft", "expense_id": existing}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	category := String(data["category_id"])
	if !value.ValidUUID(category) {
		return nil, Invalid{"Ausgabenkategorie auswählen"}
	}
	newID := value.UUID()
	m, _ := s.Module("expenses")
	e, _ := m.Entity("entries")
	r, err := save(ctx, tx, id, m, e, newID, Record{"occurred_on": fill["occurred_on"], "description": "Tankvorgang: " + String(fill["station"]), "payee": fill["station"], "amount_minor": Int(fill["total_minor"]), "kind": "expense"}, true, 0)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by) VALUES($1,$2,$3,$4,$5)", id.HouseholdID, newID, category, fill["total_minor"], id.UserID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "UPDATE expenses.entries SET state='posted',posted_at=now() WHERE id=$1", newID); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation) VALUES($1,'fuel','fillups',$2,'expenses','entries',$3,'fuel_expense')", id.HouseholdID, fill["id"], newID)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, r, generation); err != nil {
		return nil, err
	}
	return Record{"message": "Ausgabe einmalig verknüpft", "expense_id": newID}, nil
}

func (s *Store) transferStock(ctx context.Context, tx pgx.Tx, id auth.Identity, lot, data Record) error {
	location := String(data["location_id"])
	if !value.ValidUUID(location) || location == String(lot["location_id"]) {
		return Invalid{"anderen Lagerort auswählen"}
	}
	quantity, err := value.Decimal(String(data["quantity"]))
	if err != nil || quantity.Sign() <= 0 {
		return Invalid{"positive Menge erforderlich"}
	}
	var balance string
	if err = tx.QueryRow(ctx, "SELECT coalesce((SELECT quantity FROM pantry.lot_balances WHERE household_id=$1 AND lot_id=$2),0)::text", id.HouseholdID, lot["id"]).Scan(&balance); err != nil {
		return err
	}
	stock, err := stockDecimal(balance)
	if err != nil || quantity.Cmp(stock) > 0 {
		return Invalid{"Bestand reicht für die Umlagerung nicht aus"}
	}
	var integerOnly bool
	if err = tx.QueryRow(ctx, "SELECT integer_quantities FROM pantry.products WHERE household_id=$1 AND id=$2", id.HouseholdID, lot["product_id"]).Scan(&integerOnly); err != nil {
		return err
	}
	if integerOnly && !quantity.IsInt() {
		return Invalid{"nur ganze Stückzahlen umlagern"}
	}
	newID, correlation := value.UUID(), value.UUID()
	var newLotRaw []byte
	err = tx.QueryRow(ctx, "INSERT INTO pantry.lots AS t(id,household_id,product_id,location_id,received_on,best_before_on,use_by_on,opened_on,notes,created_by) SELECT $3,household_id,product_id,$4,received_on,best_before_on,use_by_on,opened_on,notes,$5 FROM pantry.lots WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(t)", id.HouseholdID, lot["id"], newID, location, id.UserID).Scan(&newLotRaw)
	if err != nil {
		return err
	}
	newLot, err := decode(newLotRaw)
	if err != nil {
		return err
	}
	m, _ := s.Module("pantry")
	e, _ := m.Entity("lots")
	var generation int64
	if err = tx.QueryRow(ctx, "SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry'", id.HouseholdID).Scan(&generation); err != nil {
		return err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, newLot, generation); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,correlation_id,created_by) VALUES($1,$2,-$3::numeric,'transfer',$4,$5),($1,$6,$3::numeric,'transfer',$4,$5)", id.HouseholdID, lot["id"], quantity.FloatString(6), correlation, id.UserID, newID)
	return err
}
