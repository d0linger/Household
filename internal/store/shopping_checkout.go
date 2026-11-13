package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CheckoutInput binds every explicit decision to the reviewed source snapshot.
// Amounts are decimal strings; an empty price means unknown, never zero.
type CheckoutInput struct {
	Generation       int64          `json:"generation"`
	PantryGeneration int64          `json:"pantry_generation"`
	Fingerprint      string         `json:"fingerprint"`
	ReceiptID        string         `json:"receipt_id"`
	ReceiptTotal     string         `json:"receipt_total"`
	ReceivedOn       string         `json:"received_on"`
	Lines            []CheckoutLine `json:"lines"`
}

type CheckoutLine struct {
	ItemID          string `json:"item_id"`
	Decision        string `json:"decision"`
	Quantity        string `json:"quantity"`
	Unit            string `json:"unit"`
	Price           string `json:"price"`
	ProductID       string `json:"product_id"`
	ProductVersion  int64  `json:"product_version"`
	LocationID      string `json:"location_id"`
	LocationVersion int64  `json:"location_version"`
	BestBefore      string `json:"best_before"`
	UseBy           string `json:"use_by"`
}

const checkoutSourceQuery = `SELECT to_jsonb(i)||jsonb_build_object('quantity',i.quantity::text,
 'product',CASE WHEN p.id IS NOT NULL THEN to_jsonb(p)||jsonb_build_object('pack_quantity',p.pack_quantity::text) END,
 'price_observation',CASE WHEN price.id IS NOT NULL THEN to_jsonb(price)||jsonb_build_object('quantity',price.quantity::text,'price_minor',price.price_minor::text) END,
 'estimate_minor',CASE WHEN i.availability<>'unavailable' THEN round(i.quantity*
 (CASE WHEN i.unit_code='piece' THEN p.pack_quantity*pack.factor_to_base ELSE unit.factor_to_base END)*price.price_minor/
 (price.quantity*CASE WHEN price.unit_code='piece' THEN p.pack_quantity*pack.factor_to_base ELSE price.factor_to_base END))::text END)
 FROM shopping.items i JOIN shopping.lists l ON l.household_id=i.household_id AND l.id=i.list_id
 JOIN core.households h ON h.id=i.household_id
 LEFT JOIN shopping.products p ON p.household_id=i.household_id AND p.id=coalesce(i.replacement_product_id,i.product_id)
 LEFT JOIN core.units pack ON pack.code=p.pack_unit LEFT JOIN core.units unit ON unit.code=i.unit_code
 LEFT JOIN LATERAL (SELECT pr.*,pu.factor_to_base FROM shopping.price_observations pr JOIN core.units pu ON pu.code=pr.unit_code
 WHERE pr.household_id=i.household_id AND pr.product_id=p.id AND pr.store_id=l.store_id
 AND pr.observed_on<=(now() AT TIME ZONE h.timezone)::date AND (pr.unit_code='piece' OR pu.dimension=pack.dimension)
 AND (i.unit_code='piece' OR unit.dimension=pack.dimension)
 ORDER BY pr.observed_on DESC,pr.created_at DESC,pr.id DESC LIMIT 1) price ON true
 WHERE i.household_id=$1 AND i.list_id=$2 ORDER BY i.id LIMIT 501`

func checkoutFingerprint(source Record) (string, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func checkoutSource(ctx context.Context, tx pgx.Tx, id auth.Identity, list Record) (Record, error) {
	rows, err := tx.Query(ctx, checkoutSourceQuery, id.HouseholdID, list["id"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		item, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > 500 {
		return nil, Invalid{"Für einen Abschluss höchstens 500 Positionen verwenden; große Listen vorher aufteilen"}
	}
	return Record{"list": list, "items": items}, nil
}

func checkoutList(ctx context.Context, tx pgx.Tx, id auth.Identity, listID, lock string) (Record, error) {
	if !value.ValidUUID(listID) {
		return nil, ErrNotFound
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(l) FROM shopping.lists l WHERE household_id=$1 AND id=$2"+lock, id.HouseholdID, listID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func readCheckout(ctx context.Context, tx pgx.Tx, id auth.Identity, listID string) (Record, []byte, error) {
	var raw, hash []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(c)-'request_hash',request_hash FROM shopping.checkouts c WHERE household_id=$1 AND list_id=$2", id.HouseholdID, listID).Scan(&raw, &hash)
	if err != nil {
		return nil, nil, err
	}
	r, err := decode(raw)
	return r, hash, err
}

func (s *Store) ShoppingCheckoutPreview(ctx context.Context, id auth.Identity, listID string) (Record, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	list, err := checkoutList(ctx, tx, id, listID, "")
	if err != nil {
		return nil, err
	}
	completed, _, err := readCheckout(ctx, tx, id, listID)
	if err == nil {
		return Record{"completed": completed}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if list["archived_at"] != nil || list["is_template"] == true {
		return nil, Invalid{"Nur eine offene Einkaufsliste abschließen"}
	}
	source, err := checkoutSource(ctx, tx, id, list)
	if err != nil {
		return nil, err
	}
	fingerprint, err := checkoutFingerprint(source)
	if err != nil {
		return nil, err
	}
	source["fingerprint"] = fingerprint
	var today string
	if err = tx.QueryRow(ctx, "SELECT (now() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", id.HouseholdID).Scan(&today); err != nil {
		return nil, err
	}
	source["today"] = today
	return source, nil
}

func (s *Store) CheckoutShopping(ctx context.Context, id auth.Identity, listID, key, requestID string, in CheckoutInput) (Record, error) {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if !value.ValidUUID(key) || len(in.Lines) == 0 || len(in.Lines) > 500 || !value.Date(in.ReceivedOn) {
		return nil, Invalid{"Vorgang, Einkaufsdatum und 1 bis 500 geprüfte Positionen erforderlich"}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	tx, err := s.BeginMutation(ctx, id, "shopping", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	list, err := checkoutList(ctx, tx, id, listID, " FOR UPDATE")
	if err != nil {
		return nil, err
	}
	prior, priorHash, err := readCheckout(ctx, tx, id, listID)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, Invalid{"Diese Liste wurde bereits abgeschlossen. Abschluss öffnen; für einen neuen Einkauf die Liste kopieren"}
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if list["archived_at"] != nil || list["is_template"] == true {
		return nil, Invalid{"Nur eine offene Einkaufsliste abschließen"}
	}
	var today string
	if err = tx.QueryRow(ctx, "SELECT (now() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", id.HouseholdID).Scan(&today); err != nil {
		return nil, err
	}
	if in.ReceivedOn > today {
		return nil, Invalid{"Ein abgeschlossener Einkauf darf nicht in der Zukunft liegen"}
	}
	// Existing item edits take child then parent locks. Never wait for a child
	// while holding the list: return a review conflict instead of a lock cycle.
	if _, err = tx.Exec(ctx, "SELECT id FROM shopping.items WHERE household_id=$1 AND list_id=$2 ORDER BY id FOR UPDATE NOWAIT", id.HouseholdID, listID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return nil, ErrConflict
		}
		return nil, err
	}
	source, err := checkoutSource(ctx, tx, id, list)
	if err != nil {
		return nil, err
	}
	fingerprint, err := checkoutFingerprint(source)
	if err != nil {
		return nil, err
	}
	if fingerprint != in.Fingerprint {
		return nil, Invalid{"Liste, Produkt oder Preis wurde geändert. Entwurf behalten und den aktuellen Vergleich erneut prüfen"}
	}
	items := source["items"].([]Record)
	if len(items) != len(in.Lines) {
		return nil, Invalid{"Für jede Position genau eine Entscheidung treffen"}
	}
	decisions := map[string]CheckoutLine{}
	stock := false
	for _, line := range in.Lines {
		if _, duplicate := decisions[line.ItemID]; duplicate || !value.ValidUUID(line.ItemID) {
			return nil, Invalid{"Position fehlt oder wurde mehrfach angegeben"}
		}
		decisions[line.ItemID] = line
		stock = stock || line.Decision == "store"
	}
	if stock {
		generation, targetErr := requireModule(ctx, tx, id, "pantry")
		if targetErr != nil {
			return nil, targetErr
		}
		if generation != in.PantryGeneration {
			return nil, ErrConflict
		}
	}
	var receipt any
	if in.ReceiptID != "" {
		if !value.ValidUUID(in.ReceiptID) {
			return nil, Invalid{"Beleg auswählen"}
		}
		var receiptRaw []byte
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'name',original_name,'sha256',encode(sha256,'hex'))
 FROM core.attachments WHERE household_id=$1 AND id=$2 AND module_id='shopping' AND module_generation=$3
 AND resource_type='lists' AND resource_id=$4 AND state='ready' FOR SHARE`, id.HouseholdID, in.ReceiptID, in.Generation, listID).Scan(&receiptRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, Invalid{"Beleg gehört nicht zu dieser Liste oder ist nicht mehr verfügbar"}
		}
		if err != nil {
			return nil, err
		}
		receipt, err = decode(receiptRaw)
		if err != nil {
			return nil, err
		}
	}
	total, err := checkoutPrice(in.ReceiptTotal)
	if err != nil {
		return nil, err
	}
	actualSum, plannedSum := new(big.Int), new(big.Int)
	unknownActual, unknownPlan := 0, 0
	results := []Record{}
	checkoutID := value.UUID()
	for _, item := range items {
		line, exists := decisions[String(item["id"])]
		if !exists {
			return nil, Invalid{"Position fehlt im geprüften Abschluss"}
		}
		result, lineErr := s.checkoutLine(ctx, tx, id, item, line, in, checkoutID)
		if lineErr != nil {
			var invalid Invalid
			if errors.As(lineErr, &invalid) {
				// A quoted item parameter keeps its boundary unambiguous when
				// the HTTP layer localizes this trusted, wrapped validation error.
				return nil, Invalid{fmt.Errorf("artikel %q: %w", String(item["name"]), invalid).Error()}
			}
			return nil, fmt.Errorf("%s: %w", String(item["name"]), lineErr)
		}
		results = append(results, result)
		if item["availability"] != "unavailable" {
			if amount, ok := new(big.Int).SetString(String(item["estimate_minor"]), 10); ok {
				plannedSum.Add(plannedSum, amount)
			} else {
				unknownPlan++
			}
		}
		if line.Decision != "skip" {
			if amount, ok := new(big.Int).SetString(String(result["actual_minor"]), 10); ok {
				actualSum.Add(actualSum, amount)
			} else {
				unknownActual++
			}
		}
	}
	snapshot := Record{"source": source, "lines": results, "receipt": receipt, "receipt_total_minor": total,
		"received_on": in.ReceivedOn, "planned_known_minor": plannedSum.String(), "planned_unknown": unknownPlan,
		"actual_known_minor": actualSum.String(), "actual_unknown": unknownActual, "pantry_generation": in.PantryGeneration}
	if total != nil && unknownActual == 0 {
		snapshot["unallocated_minor"] = new(big.Int).Sub(big.NewInt(*total), actualSum).String()
	}
	snapshotRaw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO shopping.checkouts(id,household_id,list_id,request_hash,item_count,snapshot,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)", checkoutID, id.HouseholdID, listID, hash[:], len(items), snapshotRaw, id.UserID)
	if err != nil {
		return nil, err
	}
	if err = recordPlanningCheckout(ctx, tx, id, checkoutID, in, results, hash[:]); err != nil {
		return nil, err
	}
	var listRaw []byte
	err = tx.QueryRow(ctx, `UPDATE shopping.lists l SET archived_at=now(),shopper_id=NULL,shopper_until=NULL,version=version+1,updated_at=now()
 WHERE household_id=$1 AND id=$2 RETURNING to_jsonb(l)`, id.HouseholdID, listID).Scan(&listRaw)
	if err != nil {
		return nil, err
	}
	updated, err := decode(listRaw)
	if err != nil {
		return nil, err
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("lists")
	if err = s.afterWrite(ctx, tx, id, m, e, updated, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "shopping", "lists", listID, "complete", requestID); err != nil {
		return nil, err
	}
	result, _, err := readCheckout(ctx, tx, id, listID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func checkoutPrice(s string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	n, err := value.Minor(s)
	if err != nil || n < 0 {
		return nil, Invalid{"Preis als nichtnegativen EUR-Betrag mit höchstens zwei Nachkommastellen angeben; leer bedeutet unbekannt"}
	}
	return &n, nil
}

func checkoutQuantity(quantity, unit, target string, integer bool) (string, error) {
	q, err := value.Decimal(quantity)
	if err != nil || q.Sign() <= 0 {
		return "", Invalid{"Tatsächlich gekaufte Menge muss positiv sein"}
	}
	q, err = value.Convert(q, unit, target)
	if err != nil {
		return "", Invalid{"Tatsächliche Einheit passt nicht zur Basiseinheit des Vorratsprodukts"}
	}
	exact := q.FloatString(6)
	bounded, err := value.Decimal(exact)
	if err != nil || bounded.Cmp(q) != 0 || integer && !q.IsInt() {
		return "", Invalid{"Basismenge ist zu groß, nicht exakt mit sechs Nachkommastellen darstellbar oder muss ganzzahlig sein"}
	}
	return exact, nil
}

func (s *Store) checkoutLine(ctx context.Context, tx pgx.Tx, id auth.Identity, item Record, line CheckoutLine, in CheckoutInput, checkoutID string) (Record, error) {
	purchased := item["checked_at"] != nil && item["availability"] != "unavailable"
	if !purchased && line.Decision != "skip" || purchased && line.Decision != "store" && line.Decision != "no_stock" {
		return nil, Invalid{"Erledigte Artikel einlagern oder ausdrücklich ohne Vorrat abschließen; offene Artikel ausdrücklich auslassen"}
	}
	result := Record{"item_id": line.ItemID, "name": item["name"], "decision": line.Decision}
	if line.Decision == "skip" {
		if line.Quantity != "" || line.Unit != "" || line.Price != "" || line.ProductID != "" || line.LocationID != "" || line.BestBefore != "" || line.UseBy != "" {
			return nil, Invalid{"Ausgelassene Position darf keine gekauften Mengen oder Ziele enthalten"}
		}
		return result, nil
	}
	price, err := checkoutPrice(line.Price)
	if err != nil {
		return nil, err
	}
	if price != nil {
		result["actual_minor"] = String(*price)
	}
	quantity, err := checkoutQuantity(line.Quantity, line.Unit, line.Unit, false)
	if err != nil {
		return nil, err
	}
	result["quantity"], result["unit"] = quantity, line.Unit
	if line.Decision == "no_stock" {
		if line.ProductID != "" || line.LocationID != "" || line.BestBefore != "" || line.UseBy != "" {
			return nil, Invalid{"Ohne Einlagerung keine Vorratsziele oder Chargendaten angeben"}
		}
		return result, nil
	}
	if !value.ValidUUID(line.ProductID) || !value.ValidUUID(line.LocationID) {
		return nil, Invalid{"Vorratsprodukt und Lagerort ausdrücklich auswählen"}
	}
	for _, date := range []string{line.BestBefore, line.UseBy} {
		if date != "" && !value.Date(date) {
			return nil, Invalid{"Haltbarkeitsdatum prüfen"}
		}
	}
	var unit, productName, locationName string
	var integer bool
	var productVersion, locationVersion int64
	err = tx.QueryRow(ctx, `SELECT p.unit_code,p.name,p.integer_quantities,p.version,l.name,l.version
 FROM pantry.products p JOIN pantry.locations l ON l.household_id=p.household_id
 WHERE p.household_id=$1 AND p.id=$2 AND l.id=$3 AND p.archived_at IS NULL AND l.archived_at IS NULL
 FOR SHARE OF p,l NOWAIT`, id.HouseholdID, line.ProductID, line.LocationID).Scan(&unit, &productName, &integer, &productVersion, &locationName, &locationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Invalid{"Vorratsprodukt oder Lagerort ist nicht mehr aktiv"}
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return nil, ErrConflict
		}
		return nil, err
	}
	if productVersion != line.ProductVersion || locationVersion != line.LocationVersion {
		return nil, Invalid{"Vorratsprodukt oder Lagerort wurde geändert; Auswahl erneut prüfen"}
	}
	base, err := checkoutQuantity(line.Quantity, line.Unit, unit, integer)
	if err != nil {
		return nil, err
	}
	m, _ := s.Module("pantry")
	lots, _ := m.Entity("lots")
	data := Record{"product_id": line.ProductID, "location_id": line.LocationID, "received_on": in.ReceivedOn,
		"notes": "Einkauf: " + strings.TrimSpace(String(item["name"]))}
	if line.BestBefore != "" {
		data["best_before_on"] = line.BestBefore
	}
	if line.UseBy != "" {
		data["use_by_on"] = line.UseBy
	}
	lot, err := save(ctx, tx, id, m, lots, value.UUID(), data, true, 0)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, lots, lot, in.PantryGeneration); err != nil {
		return nil, err
	}
	movements, _ := m.Entity("stock_movements")
	var movementRaw []byte
	err = tx.QueryRow(ctx, `INSERT INTO pantry.stock_movements AS movement(id,household_id,lot_id,quantity_delta,reason,note,created_by,correlation_id)
 VALUES($1,$2,$3,$4::numeric,'receive','Geprüfter Einkaufsabschluss',$5,$6) RETURNING to_jsonb(movement)`,
		value.UUID(), id.HouseholdID, lot["id"], base, id.UserID, checkoutID).Scan(&movementRaw)
	if err != nil {
		return nil, err
	}
	movement, err := decode(movementRaw)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, movements, movement, in.PantryGeneration); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation)
 VALUES($1,'shopping','checkouts',$2,'pantry','lots',$3,'shopping_stock')`, id.HouseholdID, checkoutID, lot["id"])
	if err != nil {
		return nil, err
	}
	result["stock"] = Record{"product_id": line.ProductID, "product_name": productName, "location_id": line.LocationID,
		"location_name": locationName, "quantity": base, "unit": unit, "lot_id": lot["id"], "movement_id": movement["id"], "best_before": line.BestBefore, "use_by": line.UseBy}
	return result, nil
}
