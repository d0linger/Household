package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func lockFoodFactsKey(ctx context.Context, tx pgx.Tx, id auth.Identity, key string) (bool, error) {
	var acquired bool
	err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", pantryFoodFactsNamespace+"/key/"+id.HouseholdID+"/"+id.UserID+"/"+key).Scan(&acquired)
	return acquired, err
}

func (s *Store) foodFactsReview(ctx context.Context, tx pgx.Tx, id auth.Identity, key string, in PantryFoodFactsInput) (PantryFoodFactsReview, error) {
	out := PantryFoodFactsReview{Input: in}
	configuration := s.foodFactsConfiguration()
	if configuration.State != "ready" || configuration.Provider != in.Provider {
		return out, Invalid{"Produktdienst ist deaktiviert oder nicht eingerichtet; lokale Erfassung bleibt möglich"}
	}
	local, err := readFoodFactsLocal(ctx, tx, id, in.Code)
	if err != nil {
		return out, err
	}
	if local != nil {
		return out, Invalid{"Dieser Code hat bereits eine lokale Zuordnung. Sie wird nicht überschrieben."}
	}
	cache, err := readFoodFactsCache(ctx, tx, id, in.Generation, in.Provider, in.Code)
	if err != nil {
		return out, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return out, err
	}
	if cache == nil || cache.State != "found" || !cache.ExpiresAt.After(now) || cache.FetchedAt.After(now) || hex.EncodeToString(cache.Fingerprint) != in.CacheFingerprint {
		return out, Invalid{"Frischen Produktvorschlag erneut laden und ausdrücklich prüfen"}
	}
	out.Source = cache.source()
	for _, field := range in.SelectedFields {
		if map[string]string{"name": cache.Name, "brand": cache.Brand, "pack_description": cache.Pack}[field] == "" {
			return out, Invalid{"Unbekanntes oder leeres Quellenfeld kann nicht übernommen werden"}
		}
	}
	if in.ProductID == "" {
		out.Product = Record{"id": "", "version": int64(0), "name": in.ProductName, "unit_code": in.Unit, "integer_quantities": in.IntegerQuantities}
		out.Effects = append(out.Effects, "Neues lokales Produkt mit ausdrücklich bestätigtem Namen und Basiseinheit")
	} else {
		var name, unit string
		var version int64
		var archived, integers bool
		err = tx.QueryRow(ctx, `SELECT name,unit_code,version,archived_at IS NOT NULL,integer_quantities
 FROM pantry.products WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT`, id.HouseholdID, in.ProductID).Scan(&name, &unit, &version, &archived, &integers)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, shoppingContention(err)
		}
		if archived || version != in.ProductVersion {
			return out, ErrConflict
		}
		out.Product = Record{"id": in.ProductID, "version": version, "name": name, "unit_code": unit, "integer_quantities": integers}
		out.Effects = append(out.Effects, "Bestehendes lokales Produkt bleibt unverändert")
	}
	if in.CreatePack {
		quantity, err := value.Decimal(in.PackQuantity)
		if err != nil {
			return out, Invalid{"Packungsmenge erneut prüfen"}
		}
		converted, err := value.Convert(quantity, in.PackUnit, String(out.Product["unit_code"]))
		if err != nil {
			return out, Invalid{"Packungseinheit ist nicht mit der lokalen Basiseinheit kompatibel"}
		}
		if _, err = pantryExactAmount(converted, out.Product["integer_quantities"] == true); err != nil {
			return out, err
		}
		out.Pack = Record{"label": in.PackLabel, "quantity": in.PackQuantity, "unit_code": in.PackUnit}
		out.Effects = append(out.Effects, "Neue Packung mit ausdrücklich eingegebenem Inhalt; keine Auslegung des externen Packungstextes")
	}
	out.Effects = append(out.Effects, "Neue lokale Barcode-Zuordnung und Herkunftsbeleg", "Keine Charge, kein Bestand und keine Haltbarkeit werden angelegt")
	hash, err := foodFactsHash([]any{pantryFoodFactsNamespace + "/review", id.HouseholdID, id.UserID, id.MembershipID,
		id.SessionID, id.Role, id.AuthVersion, key, in, out.Source, out.Product, out.Pack})
	if err != nil {
		return out, err
	}
	out.Fingerprint = hex.EncodeToString(hash)
	return out, nil
}

func (s *Store) ReviewPantryFoodFacts(ctx context.Context, id auth.Identity, key string, input PantryFoodFactsInput) (PantryFoodFactsReview, error) {
	if !value.ValidUUID(key) {
		return PantryFoodFactsReview{}, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	in, err := normalizePantryFoodFacts(input)
	if err != nil {
		return PantryFoodFactsReview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.beginFoodFacts(ctx, id, in.Generation)
	if err != nil {
		return PantryFoodFactsReview{}, err
	}
	defer foodFactsRollback(tx)
	result, err := s.foodFactsReview(ctx, tx, id, strings.ToLower(key), in)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

type foodFactsOperationRow struct {
	ID, State               string
	Generation              int64
	Request, Result, Digest []byte
}

func readFoodFactsOperation(ctx context.Context, tx pgx.Tx, id auth.Identity, key string) (*foodFactsOperationRow, error) {
	row := &foodFactsOperationRow{}
	err := tx.QueryRow(ctx, `SELECT id,generation,state,request_hash,result::text,result_hash
 FROM pantry.foodfacts_operations WHERE household_id=$1 AND created_by=$2 AND request_key=$3`,
		id.HouseholdID, id.UserID, key).Scan(&row.ID, &row.Generation, &row.State, &row.Request, &row.Result, &row.Digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

func foodFactsOperationReply(row *foodFactsOperationRow, generation int64) PantryFoodFactsOperation {
	out := PantryFoodFactsOperation{State: "unknown", Message: "Ausgang nicht bestätigt. Dieselbe Kennung behalten; fehlende Daten beweisen keinen Fehlschlag."}
	if row == nil || row.Generation != generation {
		return out
	}
	if row.State == "refused" && len(row.Request) == 0 && len(row.Result) == 0 && len(row.Digest) == 0 {
		out.State, out.Message = "refused", "Diese Vorgangskennung wurde dauerhaft gesperrt. Unter ihr kann keine spätere Übernahme stattfinden."
		return out
	}
	if row.State != "complete" || len(row.Request) != 32 || len(row.Result) == 0 || len(row.Result) > 16384 {
		return out
	}
	digest := sha256.Sum256(row.Result)
	if !bytes.Equal(digest[:], row.Digest) {
		return out
	}
	var receipt PantryFoodFactsReceipt
	if foodFactsDecode(row.Result, &receipt, 16384) != nil || receipt.OperationID != row.ID || receipt.Generation != generation ||
		!value.ValidUUID(receipt.ProductID) || !value.ValidUUID(receipt.BarcodeID) || !value.ValidUUID(receipt.ProvenanceID) ||
		receipt.PackID != "" && !value.ValidUUID(receipt.PackID) || !validFoodFactsSource(receipt.Source) {
		return out
	}
	accepted, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt)
	fetched, _ := time.Parse(time.RFC3339Nano, receipt.Source.FetchedAt)
	expires, _ := time.Parse(time.RFC3339Nano, receipt.Source.ExpiresAt)
	if err != nil || accepted.Before(fetched) || !accepted.Before(expires) || len(receipt.SelectedFields) < 1 || len(receipt.SelectedFields) > 3 {
		return out
	}
	out.State, out.Message, out.Receipt = "complete", "Historischer Übernahmebeleg. Aktuelle Produkt- und Bestandsdaten gesondert neu laden.", &receipt
	return out
}

func (s *Store) PostPantryFoodFacts(ctx context.Context, id auth.Identity, key, requestID string, input PantryFoodFactsInput, fingerprint string) (PantryFoodFactsReceipt, error) {
	if !value.ValidUUID(key) || !foodFactsHex(fingerprint) {
		return PantryFoodFactsReceipt{}, Invalid{"Signierte aktuelle Gesamtprüfung mit stabilem Vorgangsschlüssel erforderlich"}
	}
	key = strings.ToLower(key)
	in, err := normalizePantryFoodFacts(input)
	if err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.beginFoodFacts(ctx, id, in.Generation)
	if err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	defer foodFactsRollback(tx)
	locked, err := lockFoodFactsKey(ctx, tx, id, key)
	if err != nil || !locked {
		if err == nil {
			err = ErrConflict
		}
		return PantryFoodFactsReceipt{}, err
	}
	requestHash, err := foodFactsRequestHash(id, key, in)
	if err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	prior, err := readFoodFactsOperation(ctx, tx, id, key)
	if err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	if prior != nil {
		if prior.Generation != in.Generation || !bytes.Equal(prior.Request, requestHash) {
			return PantryFoodFactsReceipt{}, ErrConflict
		}
		status := foodFactsOperationReply(prior, in.Generation)
		if status.State != "complete" || status.Receipt == nil {
			return PantryFoodFactsReceipt{}, Invalid{"Vorgang ist nicht neu ausführbar. Bestehende Kennung über die Vorgangsabfrage klären."}
		}
		return *status.Receipt, tx.Commit(ctx)
	}
	review, err := s.foodFactsReview(ctx, tx, id, key, in)
	if err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	if review.Fingerprint != strings.ToLower(fingerprint) {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	productID, packID := in.ProductID, ""
	if productID == "" {
		productID = value.UUID()
	}
	if in.CreatePack {
		packID = value.UUID()
	}
	var accepted time.Time
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&accepted); err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	receipt := PantryFoodFactsReceipt{OperationID: value.UUID(), Generation: in.Generation, AcceptedAt: accepted.UTC().Format(time.RFC3339Nano),
		ProductID: productID, PackID: packID, BarcodeID: value.UUID(), ProvenanceID: value.UUID(), Source: review.Source,
		SelectedFields: append([]string(nil), in.SelectedFields...), Message: "Übernahmebeleg für Produktzuordnung und Herkunft gespeichert. Aktuelle Produktdaten gesondert neu laden. Es wurde kein Bestand gebucht."}
	resultJSON, err := json.Marshal(receipt)
	if err != nil || len(resultJSON) > 16384 {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	// Deferred SQL completion guards validate the effects inserted below before
	// commit. The receipt's hash uses PostgreSQL's own JSONB text representation.
	var storedResult, storedDigest []byte
	var storedID, storedHousehold, storedActor, storedKey, storedState string
	var storedGeneration int64
	var storedRequest []byte
	err = tx.QueryRow(ctx, `INSERT INTO pantry.foodfacts_operations(id,household_id,generation,created_by,request_key,state,request_hash,result,result_hash)
 VALUES($1,$2,$3,$4,$5,'complete',$6,$7::jsonb,sha256(convert_to(($7::jsonb)::text,'UTF8')))
 RETURNING id,household_id,generation,created_by,request_key,state,request_hash,result::text,result_hash`, receipt.OperationID, id.HouseholdID, in.Generation, id.UserID, key, requestHash, resultJSON).
		Scan(&storedID, &storedHousehold, &storedGeneration, &storedActor, &storedKey, &storedState, &storedRequest, &storedResult, &storedDigest)
	if err != nil {
		return PantryFoodFactsReceipt{}, shoppingContention(err)
	}
	if storedID != receipt.OperationID || storedHousehold != id.HouseholdID || storedGeneration != in.Generation ||
		storedActor != id.UserID || storedKey != key || storedState != "complete" || !bytes.Equal(storedRequest, requestHash) {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	stored := &foodFactsOperationRow{ID: storedID, Generation: storedGeneration, State: storedState, Request: storedRequest, Result: storedResult, Digest: storedDigest}
	reply := foodFactsOperationReply(stored, in.Generation)
	if reply.State != "complete" || reply.Receipt == nil {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	actualJSON, err := json.Marshal(reply.Receipt)
	if err != nil || !bytes.Equal(actualJSON, resultJSON) {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	module, ok := s.Module("pantry")
	if !ok {
		return PantryFoodFactsReceipt{}, ErrNotFound
	}
	productEntity, _ := module.Entity("products")
	if in.ProductID == "" {
		product, err := save(ctx, tx, id, module, productEntity, productID, Record{"name": in.ProductName, "unit_code": in.Unit, "integer_quantities": in.IntegerQuantities}, true, 0)
		if err != nil {
			return PantryFoodFactsReceipt{}, shoppingContention(err)
		}
		if String(product["id"]) != productID || String(product["household_id"]) != id.HouseholdID || String(product["name"]) != in.ProductName ||
			String(product["unit_code"]) != in.Unit || product["integer_quantities"] != in.IntegerQuantities {
			return PantryFoodFactsReceipt{}, ErrConflict
		}
		if err = s.afterWrite(ctx, tx, id, module, productEntity, product, in.Generation); err != nil {
			return PantryFoodFactsReceipt{}, err
		}
		if err = Audit(ctx, tx, id, "pantry", "products", productID, "record_write", requestID); err != nil {
			return PantryFoodFactsReceipt{}, err
		}
	}
	if in.CreatePack {
		data := Record{"product_id": productID, "label": in.PackLabel, "quantity": in.PackQuantity, "unit_code": in.PackUnit, "disabled": false}
		if err = validatePantryPack(ctx, tx, id, "", data); err != nil {
			return PantryFoodFactsReceipt{}, err
		}
		entity, _ := module.Entity("packs")
		pack, err := save(ctx, tx, id, module, entity, packID, data, true, 0)
		if err != nil {
			return PantryFoodFactsReceipt{}, shoppingContention(err)
		}
		if String(pack["id"]) != packID || String(pack["product_id"]) != productID || String(pack["household_id"]) != id.HouseholdID ||
			String(pack["label"]) != in.PackLabel || String(pack["quantity"]) != in.PackQuantity || String(pack["unit_code"]) != in.PackUnit || pack["disabled"] != false {
			return PantryFoodFactsReceipt{}, ErrConflict
		}
		if err = s.afterWrite(ctx, tx, id, module, entity, pack, in.Generation); err != nil {
			return PantryFoodFactsReceipt{}, err
		}
		if err = Audit(ctx, tx, id, "pantry", "packs", packID, "record_write", requestID); err != nil {
			return PantryFoodFactsReceipt{}, err
		}
	}
	barcodeData := Record{"product_id": productID, "code": in.Code}
	if packID != "" {
		barcodeData["pack_id"] = packID
	}
	if err = validatePantryBarcode(ctx, tx, id, "", barcodeData, barcodeData); err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	barcodeEntity, _ := module.Entity("barcodes")
	barcode, err := save(ctx, tx, id, module, barcodeEntity, receipt.BarcodeID, barcodeData, true, 0)
	if err != nil {
		return PantryFoodFactsReceipt{}, shoppingContention(err)
	}
	if String(barcode["id"]) != receipt.BarcodeID || String(barcode["product_id"]) != productID || String(barcode["household_id"]) != id.HouseholdID ||
		String(barcode["code"]) != in.Code || String(barcode["pack_id"]) != packID {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	if err = s.afterWrite(ctx, tx, id, module, barcodeEntity, barcode, in.Generation); err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	if err = Audit(ctx, tx, id, "pantry", "barcodes", receipt.BarcodeID, "record_write", requestID); err != nil {
		return PantryFoodFactsReceipt{}, err
	}
	adopted := Record{"product_name": review.Product["name"], "unit": review.Product["unit_code"], "integer_quantities": review.Product["integer_quantities"],
		"pack_label": in.PackLabel, "pack_quantity": in.PackQuantity, "pack_unit": in.PackUnit}
	provenance, err := json.Marshal(Record{"source": review.Source, "adopted": adopted})
	if err != nil || len(provenance) > 8192 {
		return PantryFoodFactsReceipt{}, ErrConflict
	}
	var storedProvenance string
	var exactProvenance bool
	err = tx.QueryRow(ctx, `INSERT INTO pantry.foodfacts_provenance(id,household_id,generation,created_by,operation_id,product_id,pack_id,barcode_id,provider,code,source_snapshot,selected_fields)
 VALUES($1,$2,$3,$4,$5,$6,nullif($7,'')::uuid,$8,$9,$10,$11::jsonb,$12)
 RETURNING id,household_id=$2 AND generation=$3 AND created_by=$4 AND operation_id=$5 AND product_id=$6
 AND coalesce(pack_id::text,'')=$7 AND barcode_id=$8 AND provider=$9 AND code=$10 AND source_snapshot=$11::jsonb AND selected_fields=$12::text[]`,
		receipt.ProvenanceID, id.HouseholdID, in.Generation, id.UserID, receipt.OperationID, productID, packID, receipt.BarcodeID, in.Provider, in.Code, provenance, in.SelectedFields).Scan(&storedProvenance, &exactProvenance)
	if err != nil || storedProvenance != receipt.ProvenanceID || !exactProvenance {
		if err == nil {
			err = ErrConflict
		}
		return PantryFoodFactsReceipt{}, shoppingContention(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return PantryFoodFactsReceipt{}, shoppingContention(err)
	}
	return receipt, nil
}

func (s *Store) PantryFoodFactsOperationStatus(ctx context.Context, id auth.Identity, key string, generation int64) (PantryFoodFactsOperation, error) {
	if !value.ValidUUID(key) {
		return PantryFoodFactsOperation{}, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.beginFoodFacts(ctx, id, generation)
	if err != nil {
		return PantryFoodFactsOperation{}, err
	}
	defer foodFactsRollback(tx)
	key = strings.ToLower(key)
	locked, err := lockFoodFactsKey(ctx, tx, id, key)
	if err != nil {
		return PantryFoodFactsOperation{}, err
	}
	if !locked {
		return PantryFoodFactsOperation{State: "unknown", Message: "Vorgang ist möglicherweise noch in Bearbeitung. Kennung behalten und später erneut prüfen."}, nil
	}
	row, err := readFoodFactsOperation(ctx, tx, id, key)
	if err != nil {
		return PantryFoodFactsOperation{}, err
	}
	return foodFactsOperationReply(row, generation), tx.Commit(ctx)
}

func (s *Store) AbandonPantryFoodFacts(ctx context.Context, id auth.Identity, key string, generation int64, requestID string) (PantryFoodFactsOperation, error) {
	if !value.ValidUUID(key) {
		return PantryFoodFactsOperation{}, Invalid{"Stabilen Vorgangsschlüssel verwenden"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.beginFoodFacts(ctx, id, generation)
	if err != nil {
		return PantryFoodFactsOperation{}, err
	}
	defer foodFactsRollback(tx)
	key = strings.ToLower(key)
	locked, err := lockFoodFactsKey(ctx, tx, id, key)
	if err != nil || !locked {
		if err == nil {
			err = ErrConflict
		}
		return PantryFoodFactsOperation{}, err
	}
	prior, err := readFoodFactsOperation(ctx, tx, id, key)
	if err != nil {
		return PantryFoodFactsOperation{}, err
	}
	if prior != nil {
		return foodFactsOperationReply(prior, generation), tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO pantry.foodfacts_operations(household_id,generation,created_by,request_key,state)
 VALUES($1,$2,$3,$4,'refused')`, id.HouseholdID, generation, id.UserID, key)
	if err != nil {
		return PantryFoodFactsOperation{}, shoppingContention(err)
	}
	return PantryFoodFactsOperation{State: "refused", Message: "Vorgangskennung dauerhaft gesperrt. Eine verspätete Übernahme unter dieser Kennung ist ausgeschlossen."}, tx.Commit(ctx)
}

func (s *Store) PantryFoodFactsProvenance(ctx context.Context, id auth.Identity, product string, generation int64) (PantryFoodFactsProvenanceView, error) {
	empty := PantryFoodFactsProvenanceView{}
	if !value.ValidUUID(product) {
		return empty, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, currentGeneration, err := s.foodFactsReadScope(ctx, id)
	if err != nil {
		return empty, err
	}
	defer foodFactsRollback(tx)
	if currentGeneration != generation {
		return empty, ErrConflict
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pantry.products WHERE household_id=$1 AND id=$2)", id.HouseholdID, product).Scan(&exists); err != nil {
		return empty, err
	}
	if !exists {
		return empty, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT source_snapshot->'source' FROM pantry.foodfacts_provenance
 WHERE household_id=$1 AND generation=$2 AND product_id=$3 ORDER BY created_at DESC,id DESC LIMIT 21`, id.HouseholdID, generation, product)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	result := PantryFoodFactsProvenanceView{Sources: make([]PantryFoodFactsSource, 0, 20)}
	for rows.Next() {
		if len(result.Sources) >= 20 {
			result.More = true
			break
		}
		var raw []byte
		var source PantryFoodFactsSource
		if err = rows.Scan(&raw); err != nil {
			return empty, err
		}
		if foodFactsDecode(raw, &source, 8192) != nil || !validFoodFactsSource(source) {
			return empty, ErrConflict
		}
		result.Sources = append(result.Sources, source)
	}
	if err = rows.Err(); err != nil {
		return empty, err
	}
	rows.Close()
	return result, tx.Commit(ctx)
}
