package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func pantryLeftoversRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Store) PantryLeftoversContext(ctx context.Context, id auth.Identity, kind, sourceID string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var generation int64
	if err := s.Pool.QueryRow(ctx, "SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry' AND state='enabled'", id.HouseholdID).Scan(&generation); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	tx, err := s.BeginMutation(ctx, id, "pantry", generation)
	if err != nil {
		return nil, err
	}
	defer pantryLeftoversRollback(tx)
	var today string
	if err = tx.QueryRow(ctx, "SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", id.HouseholdID).Scan(&today); err != nil {
		return nil, err
	}
	var recipesGeneration int64
	var recipesEnabled bool
	if err = tx.QueryRow(ctx, "SELECT data_generation,state='enabled' FROM core.module_states WHERE household_id=$1 AND module_id='recipes'", id.HouseholdID).Scan(&recipesGeneration, &recipesEnabled); err != nil {
		return nil, err
	}
	out := Record{"generation": generation, "today": today, "can_write": true, "recipes_enabled": recipesEnabled, "recipes_generation": recipesGeneration, "unit": "portion", "maximum_containers": 20}
	if kind == "" {
		kind = "manual"
	}
	if kind == "manual" {
		if sourceID != "" {
			return nil, Invalid{"Manuelle Reste enthalten keinen Rezeptverweis"}
		}
		out["source"] = Record{"kind": "manual", "actual_servings": "", "available": true}
		return out, nil
	}
	if !slices.Contains([]string{"recipe_version", "cooking"}, kind) || !value.ValidUUID(sourceID) {
		return nil, Invalid{"Aktuellen Rezept- oder Kochbezug auswählen"}
	}
	in := PantryLeftoversInput{SourceKind: kind, SourceID: strings.ToLower(sourceID), RecipesGeneration: recipesGeneration}
	source, err := pantryLeftoversCoreSource(ctx, tx, id, in)
	if err != nil {
		return nil, err
	}
	out["source"] = source
	if kind == "cooking" {
		var completed bool
		if err = tx.QueryRow(ctx, "SELECT pantry.leftover_source_used($1,$2,$3)", id.HouseholdID, recipesGeneration, in.SourceID).Scan(&completed); err != nil {
			return nil, err
		}
		out["already_recorded"] = completed
	}
	return out, nil
}

func (s *Store) planPantryLeftovers(ctx context.Context, tx pgx.Tx, id auth.Identity, in PantryLeftoversInput) (Record, PantryLeftoversInput, error) {
	in, total, err := normalizePantryLeftovers(in)
	if err != nil {
		return nil, in, err
	}
	source, err := pantryLeftoversCoreSource(ctx, tx, id, in)
	if err != nil {
		return nil, in, err
	}
	if in.SourceKind != "manual" {
		in.SourceVersionID = String(source["source_version_id"])
	}
	if in.SourceKind == "cooking" {
		var used bool
		if err = tx.QueryRow(ctx, "SELECT pantry.leftover_source_used($1,$2,$3)", id.HouseholdID, in.RecipesGeneration, in.SourceID).Scan(&used); err != nil {
			return nil, in, err
		}
		if used {
			return nil, in, Invalid{"Diese abgeschlossene Zubereitung hat bereits eine Resteeinlagerung. Vorhandene Chargen prüfen"}
		}
		if in.PreparedOn != String(source["cooked_on"]) {
			return nil, in, Invalid{"Zubereitungstag muss zum ausgewählten abgeschlossenen Kochvorgang passen"}
		}
		in.PreparedPortions, in.PortionsUnknown = String(source["actual_servings"]), false
		if in, total, err = normalizePantryLeftovers(in); err != nil {
			return nil, in, err
		}
	}
	var today string
	if err = tx.QueryRow(ctx, "SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", id.HouseholdID).Scan(&today); err != nil {
		return nil, in, err
	}
	if in.PreparedOn > today {
		return nil, in, Invalid{"Tatsächliche Zubereitung darf nicht in der Zukunft liegen"}
	}
	product := Record{"id": "", "name": in.Name, "unit": "portion", "new": true}
	if in.ProductID != "" {
		var name, unit string
		var version int64
		var archived, integers bool
		err = tx.QueryRow(ctx, `SELECT name,unit_code,version,archived_at IS NOT NULL,integer_quantities FROM pantry.products
 WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT`, id.HouseholdID, in.ProductID).Scan(&name, &unit, &version, &archived, &integers)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, in, ErrNotFound
		}
		if err != nil {
			return nil, in, shoppingContention(err)
		}
		if version != in.ProductVersion {
			return nil, in, ErrConflict
		}
		if archived || unit != "portion" {
			return nil, in, Invalid{"Aktives Portionsprodukt auswählen; Stück oder Gewicht werden nicht umgedeutet"}
		}
		for _, line := range in.Containers {
			q, _ := value.Decimal(line.Quantity)
			if _, err = pantryExactAmount(q, integers); err != nil {
				return nil, in, err
			}
		}
		product = Record{"id": in.ProductID, "name": name, "unit": unit, "version": strconv.FormatInt(version, 10), "new": false}
	}
	locations := map[string]Record{}
	order := make([]string, 0, len(in.Containers))
	for _, line := range in.Containers {
		order = append(order, line.LocationID)
	}
	slices.Sort(order)
	for _, lid := range slices.Compact(order) {
		var name string
		var version int64
		var archived bool
		err = tx.QueryRow(ctx, "SELECT name,version,archived_at IS NOT NULL FROM pantry.locations WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, lid).Scan(&name, &version, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, in, ErrNotFound
		}
		if err != nil {
			return nil, in, shoppingContention(err)
		}
		if archived {
			return nil, in, Invalid{"Reste benötigen einen aktiven Lagerort"}
		}
		locations[lid] = Record{"id": lid, "name": name, "version": strconv.FormatInt(version, 10)}
	}
	lines := make([]Record, 0, len(in.Containers))
	for _, line := range in.Containers {
		location := locations[line.LocationID]
		if Int(location["version"]) != line.LocationVersion {
			return nil, in, ErrConflict
		}
		if line.ReceivedOn > today {
			return nil, in, Invalid{"Tatsächliche Einlagerung darf nicht in der Zukunft liegen"}
		}
		lines = append(lines, Record{"line_id": line.ID, "label": line.Label, "quantity": line.Quantity, "location_id": line.LocationID, "location": location["name"], "received_on": line.ReceivedOn, "best_before_on": line.BestBeforeOn, "use_by_on": line.UseByOn})
	}
	in.Fingerprint = ""
	digest, err := pantryLeftoversDigest(in)
	if err != nil {
		return nil, in, err
	}
	in.Fingerprint = hex.EncodeToString(digest)
	return Record{"normalized": in, "fingerprint": in.Fingerprint, "source": source, "before": Record{"product": product, "locations": locations},
		"after":   Record{"name": in.Name, "prepared_on": in.PreparedOn, "total_portions": total, "prepared_portions": in.PreparedPortions, "portions_unknown": in.PortionsUnknown, "unit": "portion", "containers": lines},
		"effects": []string{"Restebehälter als neue Chargen einlagern", "Nur positive Portionszugänge buchen; keine Zutaten erneut verbrauchen", "Dauerhaften Zugangsbeleg speichern"}}, in, nil
}

func (s *Store) ReviewPantryLeftovers(ctx context.Context, id auth.Identity, input PantryLeftoversInput) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", input.Generation)
	if err != nil {
		return nil, err
	}
	defer pantryLeftoversRollback(tx)
	view, _, err := s.planPantryLeftovers(ctx, tx, id, input)
	return view, err
}

func (s *Store) PostPantryLeftovers(ctx context.Context, id auth.Identity, key, requestID string, input PantryLeftoversInput) (Record, error) {
	if !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Vorgangskennung verwenden"}
	}
	in, _, err := normalizePantryLeftovers(input)
	if err != nil {
		return nil, err
	}
	if in.Fingerprint == "" || in.SourceKind != "manual" && in.SourceVersionID == "" {
		return nil, Invalid{"Gesamte Resteeinlagerung zuerst aktuell prüfen"}
	}
	hash, err := pantryLeftoversDigest(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 20*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer pantryLeftoversRollback(tx)
	opID := value.UUID()
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT pantry.begin_leftovers($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", opID, strings.ToLower(key), in.Generation, hash,
		in.SourceKind, in.SourceID, in.RecipesGeneration, in.SourceVersionID, id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw)
	if err != nil {
		return nil, shoppingContention(err)
	}
	var begun struct {
		Replay bool   `json:"replay"`
		Result Record `json:"result"`
	}
	if err = json.Unmarshal(raw, &begun); err != nil {
		return nil, err
	}
	if begun.Replay {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return begun.Result, nil
	}
	view, normalized, err := s.planPantryLeftovers(ctx, tx, id, in)
	if err != nil {
		return nil, err
	}
	if normalized.Fingerprint != in.Fingerprint {
		return nil, ErrConflict
	}
	in = normalized
	product := in.ProductID
	if product == "" {
		err = tx.QueryRow(ctx, `INSERT INTO pantry.products(household_id,name,unit_code,integer_quantities,created_by)
 VALUES($1,$2,'portion',false,$3) RETURNING id`, id.HouseholdID, in.Name, id.UserID).Scan(&product)
		if err != nil {
			return nil, err
		}
		m, _ := s.Module("pantry")
		e, _ := m.Entity("products")
		if err = s.afterWrite(ctx, tx, id, m, e, Record{"id": product, "name": in.Name}, in.Generation); err != nil {
			return nil, err
		}
		if err = Audit(ctx, tx, id, "pantry", "products", product, "record_write", requestID); err != nil {
			return nil, err
		}
	}
	lines := make([]Record, 0, len(in.Containers))
	for _, line := range in.Containers {
		lot, movement := value.UUID(), value.UUID()
		location := view["before"].(Record)["locations"].(map[string]Record)[line.LocationID]
		if _, err = tx.Exec(ctx, `INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,best_before_on,use_by_on,notes,created_by)
 VALUES($1,$2,$3,$4,$5::date,nullif($6,'')::date,nullif($7,'')::date,$8,$9)`, lot, id.HouseholdID, product, line.LocationID, line.ReceivedOn, line.BestBeforeOn, line.UseByOn, line.Label, id.UserID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,correlation_id,note,created_by)
 VALUES($1,$2,$3,$4::numeric,'receive',$5,'Geprüfte Resteeinlagerung',$6)`, movement, id.HouseholdID, lot, line.Quantity, opID, id.UserID); err != nil {
			return nil, shoppingContention(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO pantry.prepared_lots(household_id,lot_id,operation_id,line_id,movement_id,prepared_on,initial_portions,prepared_portions,portions_unknown,label,location_id,received_on,best_before_on,use_by_on,source_kind,location_name,dish_name)
 VALUES($1,$2,$3,$4,$5,$6::date,$7::numeric,nullif($8,'')::numeric,$9,$10,$11,$12::date,nullif($13,'')::date,nullif($14,'')::date,$15,$16,$17)`,
			id.HouseholdID, lot, opID, line.ID, movement, in.PreparedOn, line.Quantity, in.PreparedPortions, in.PortionsUnknown, line.Label, line.LocationID, line.ReceivedOn, line.BestBeforeOn, line.UseByOn, in.SourceKind, location["name"], in.Name); err != nil {
			return nil, err
		}
		if err = pantryLeftoversCoreLink(ctx, tx, id, in, lot); err != nil {
			return nil, err
		}
		m, _ := s.Module("pantry")
		for _, item := range []struct{ entity, rid string }{{"lots", lot}, {"stock_movements", movement}} {
			e, _ := m.Entity(item.entity)
			var fact []byte
			if err = tx.QueryRow(ctx, "SELECT to_jsonb(r) FROM "+pgx.Identifier{"pantry", item.entity}.Sanitize()+" r WHERE household_id=$1 AND id=$2", id.HouseholdID, item.rid).Scan(&fact); err != nil {
				return nil, err
			}
			data, err := decode(fact)
			if err != nil {
				return nil, err
			}
			if err = s.afterWrite(ctx, tx, id, m, e, data, in.Generation); err != nil {
				return nil, err
			}
			if err = Audit(ctx, tx, id, "pantry", item.entity, item.rid, "record_write", requestID); err != nil {
				return nil, err
			}
		}
		lines = append(lines, Record{"line_id": line.ID, "lot_id": lot, "movement_id": movement, "label": line.Label, "quantity": line.Quantity, "location_id": line.LocationID, "location": location["name"]})
	}
	var recorded string
	if err = tx.QueryRow(ctx, "SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')").Scan(&recorded); err != nil {
		return nil, err
	}
	result := Record{"id": opID, "operation_id": opID, "request_key": strings.ToLower(key), "generation": in.Generation, "name": in.Name, "prepared_on": in.PreparedOn,
		"total_portions": view["after"].(Record)["total_portions"], "unit": "portion", "created_at": recorded, "lines": lines, "details_available": true}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 256<<10 {
		return nil, Invalid{"Restebeleg überschreitet die zulässige Größe"}
	}
	if _, err = tx.Exec(ctx, "SELECT pantry.finish_leftovers($1,$2::jsonb)", opID, encoded); err != nil {
		return nil, shoppingContention(err)
	}
	if err = Audit(ctx, tx, id, "pantry", "leftover_operations", opID, "post", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func (s *Store) pantryLeftoverStatus(ctx context.Context, id auth.Identity, key string, generation int64, abandon bool, requestID string) (Record, error) {
	if !value.ValidUUID(key) || generation < 1 {
		return nil, Invalid{"Ursprüngliche Vorgangskennung und aktuellen Vorratsstand verwenden"}
	}
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 10*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", generation)
	if err != nil {
		return nil, err
	}
	defer pantryLeftoversRollback(tx)
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT pantry.leftover_operation_status($1,$2,$3,$4,$5,$6)", strings.ToLower(key), generation, abandon, id.SessionID, id.AuthVersion, id.MembershipID).Scan(&raw)
	if err != nil {
		return nil, shoppingContention(err)
	}
	result, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) PantryLeftoverOperation(ctx context.Context, id auth.Identity, key string, generation int64) (Record, error) {
	return s.pantryLeftoverStatus(ctx, id, key, generation, false, "")
}

func (s *Store) AbandonPantryLeftoverOperation(ctx context.Context, id auth.Identity, key string, generation int64, requestID string) (Record, error) {
	return s.pantryLeftoverStatus(ctx, id, key, generation, true, requestID)
}
