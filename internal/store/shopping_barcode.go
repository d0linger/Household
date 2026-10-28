package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ShoppingBarcodeProduct struct {
	ID           string `json:"id"`
	Version      int64  `json:"version"`
	Name         string `json:"name"`
	Brand        string `json:"brand"`
	PackQuantity string `json:"pack_quantity"`
	PackUnit     string `json:"pack_unit"`
}

type ShoppingBarcodeView struct {
	Generation   int64                   `json:"generation"`
	ListVersion  int64                   `json:"list_version"`
	ListName     string                  `json:"list_name"`
	Code         string                  `json:"code"`
	AliasID      string                  `json:"alias_id"`
	AliasVersion int64                   `json:"alias_version"`
	Product      *ShoppingBarcodeProduct `json:"product"`
}

type ShoppingBarcodeInput struct {
	Generation     int64  `json:"generation"`
	ListVersion    int64  `json:"list_version"`
	Code           string `json:"code"`
	AliasID        string `json:"alias_id"`
	AliasVersion   int64  `json:"alias_version"`
	ProductID      string `json:"product_id"`
	ProductVersion int64  `json:"product_version"`
	Quantity       string `json:"quantity"`
}

func shoppingBarcodeCode(raw string) (string, error) {
	if len(raw) > 256 {
		return "", Invalid{"Barcode ist zu lang"}
	}
	code := strings.TrimSpace(raw)
	if len(code) < 1 || len(code) > 64 {
		return "", Invalid{"Barcode: 1 bis 64 sichtbare ASCII-Zeichen ohne Leerzeichen eingeben"}
	}
	for n := range len(code) {
		if code[n] < 33 || code[n] > 126 {
			return "", Invalid{"Barcode enthält Leerzeichen, Steuerzeichen oder nicht unterstützte Zeichen"}
		}
	}
	return code, nil
}

func shoppingBarcodeError(err error) error {
	var failure *pgconn.PgError
	if errors.As(err, &failure) && (failure.Code == "55P03" || failure.Code == "40P01" || failure.Code == "23505") {
		return ErrConflict
	}
	return err
}

// ShoppingBarcode uses one current-authority snapshot; an unknown code exposes
// no unrelated catalogue, receipt, stock or cross-module data.
func (s *Store) ShoppingBarcode(ctx context.Context, id auth.Identity, list, raw string) (ShoppingBarcodeView, error) {
	var out ShoppingBarcodeView
	if !value.ValidUUID(list) {
		return out, ErrNotFound
	}
	if raw != "" {
		var err error
		out.Code, err = shoppingBarcodeCode(raw)
		if err != nil {
			return out, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	var scope RepurchasePage
	if err = repurchaseScope(ctx, tx, id, &scope); err != nil {
		return out, err
	}
	out.Generation = scope.Generation
	if out.ListVersion, err = repurchaseList(ctx, tx, id, list, false); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, "SELECT name FROM shopping.lists WHERE household_id=$1 AND id=$2", id.HouseholdID, list).Scan(&out.ListName); err != nil {
		return out, err
	}
	if out.Code != "" {
		var product ShoppingBarcodeProduct
		err = tx.QueryRow(ctx, `SELECT b.id,b.version,p.id,p.version,p.name,p.brand,p.pack_quantity::text,p.pack_unit
 FROM shopping.barcodes b JOIN shopping.products p ON p.household_id=b.household_id AND p.id=b.product_id
 WHERE b.household_id=$1 AND b.code=$2`, id.HouseholdID, out.Code).Scan(&out.AliasID, &out.AliasVersion,
			&product.ID, &product.Version, &product.Name, &product.Brand, &product.PackQuantity, &product.PackUnit)
		if err == nil {
			out.Product = &product
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

// MutateShoppingBarcode is deliberately separate from scanning/lookup. Only
// these three compiled intents can assign a code, remove it, or add an unchecked
// item after the user has reviewed the actual product pack and quantity.
func (s *Store) MutateShoppingBarcode(ctx context.Context, id auth.Identity, list, intent, key, requestID string, in ShoppingBarcodeInput) (Record, error) {
	if intent != "map" && intent != "remove" && intent != "add" {
		return nil, ErrNotFound
	}
	if !value.ValidUUID(list) || !value.ValidUUID(key) || !value.ValidUUID(in.ProductID) || in.ProductVersion <= 0 || in.ListVersion <= 0 {
		return nil, Invalid{"Liste und Produkt erneut prüfen"}
	}
	code, err := shoppingBarcodeCode(in.Code)
	if err != nil {
		return nil, err
	}
	in.Code = code
	list, key, in.ProductID = strings.ToLower(list), strings.ToLower(key), strings.ToLower(in.ProductID)
	if intent == "map" {
		if in.AliasID != "" || in.AliasVersion != 0 || in.Quantity != "" {
			return nil, Invalid{"Neue Barcode-Zuordnung erneut prüfen"}
		}
	} else {
		if !value.ValidUUID(in.AliasID) || in.AliasVersion <= 0 {
			return nil, Invalid{"Lokale Zuordnung erneut laden"}
		}
		in.AliasID = strings.ToLower(in.AliasID)
		if intent == "add" {
			in.Quantity, err = normalizeRepurchaseQuantity(in.Quantity)
			if err != nil {
				return nil, err
			}
		} else if in.Quantity != "" {
			return nil, Invalid{"Entfernung ohne Mengenänderung bestätigen"}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "shopping", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	version, err := repurchaseList(ctx, tx, id, list, true)
	if err != nil {
		return nil, err
	}
	// Same-code operations serialize even when competing assignments name
	// different product rows. The unique index remains the final SQL authority.
	var acquired bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", "shopping-barcode/"+id.HouseholdID+"/"+code).Scan(&acquired); err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrConflict
	}
	var product ShoppingBarcodeProduct
	err = tx.QueryRow(ctx, `SELECT id,version,name,brand,pack_quantity::text,pack_unit FROM shopping.products WHERE household_id=$1 AND id=$2 FOR UPDATE NOWAIT`,
		id.HouseholdID, in.ProductID).Scan(&product.ID, &product.Version, &product.Name, &product.Brand, &product.PackQuantity, &product.PackUnit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingBarcodeError(err)
	}
	if version != in.ListVersion || product.Version != in.ProductVersion {
		return nil, ErrConflict
	}
	operation := "barcode/" + intent + "/shopping/lists/" + list
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	var previousHash, previous []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4 AND module_id='shopping' AND module_generation=$5`, id.HouseholdID, id.UserID, operation, key, in.Generation).Scan(&previousHash, &previous)
	if err == nil {
		if !bytes.Equal(previousHash, hash[:]) {
			return nil, ErrConflict
		}
		result, decodeErr := decode(previous)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err = shoppingBarcodeReplay(ctx, tx, id, list, intent, in, result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	result, err := s.writeShoppingBarcode(ctx, tx, id, list, intent, requestID, in, product)
	if err != nil {
		return nil, shoppingBarcodeError(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'shopping',$6,200,$7)`, id.HouseholdID, id.UserID, operation, key, hash[:], in.Generation, encoded); err != nil {
		return nil, err
	}
	return result, shoppingBarcodeError(tx.Commit(ctx))
}

func shoppingBarcodeReplay(ctx context.Context, tx pgx.Tx, id auth.Identity, list, intent string, in ShoppingBarcodeInput, result Record) error {
	var valid bool
	var err error
	if intent == "remove" {
		err = tx.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM shopping.barcodes WHERE household_id=$1 AND (id=$2 OR code=$3))", id.HouseholdID, in.AliasID, in.Code).Scan(&valid)
	} else {
		alias := in.AliasID
		if intent == "map" {
			alias = String(result["id"])
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shopping.barcodes WHERE household_id=$1 AND id=$2 AND code=$3 AND product_id=$4 AND version=$5)`,
			id.HouseholdID, alias, in.Code, in.ProductID, Int(result["alias_version"])).Scan(&valid)
		if err == nil && valid && intent == "add" {
			err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM shopping.items WHERE household_id=$1 AND id=$2 AND list_id=$3 AND product_id=$4)", id.HouseholdID, result["id"], list, in.ProductID).Scan(&valid)
		}
	}
	if err != nil {
		return err
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}

func (s *Store) writeShoppingBarcode(ctx context.Context, tx pgx.Tx, id auth.Identity, list, intent, requestID string, in ShoppingBarcodeInput, product ShoppingBarcodeProduct) (Record, error) {
	if intent == "map" {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM shopping.barcodes WHERE household_id=$1 AND code=$2)", id.HouseholdID, in.Code).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrConflict
		}
		var aliases int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM(SELECT id FROM shopping.barcodes
 WHERE household_id=$1 AND product_id=$2 LIMIT 100) bounded`, id.HouseholdID, in.ProductID).Scan(&aliases); err != nil {
			return nil, err
		}
		if aliases >= 100 {
			return nil, Invalid{"Für diese Produktvariante sind bereits 100 Barcodes zugeordnet. Eine bestehende Zuordnung ausdrücklich entfernen oder eine weitere Produktvariante verwenden."}
		}
		alias := value.UUID()
		if _, err := tx.Exec(ctx, "INSERT INTO shopping.barcodes(id,household_id,product_id,code,created_by) VALUES($1,$2,$3,$4,$5)", alias, id.HouseholdID, in.ProductID, in.Code, id.UserID); err != nil {
			return nil, err
		}
		return Record{"id": alias, "alias_version": int64(1), "product_id": in.ProductID}, nil
	}
	var alias string
	err := tx.QueryRow(ctx, `SELECT id FROM shopping.barcodes WHERE household_id=$1 AND id=$2 AND code=$3 AND product_id=$4 AND version=$5 FOR UPDATE NOWAIT`,
		id.HouseholdID, in.AliasID, in.Code, in.ProductID, in.AliasVersion).Scan(&alias)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if intent == "remove" {
		if _, err = tx.Exec(ctx, "DELETE FROM shopping.barcodes WHERE household_id=$1 AND id=$2 AND version=$3", id.HouseholdID, alias, in.AliasVersion); err != nil {
			return nil, err
		}
		return Record{"id": alias, "removed": true, "product_id": in.ProductID}, nil
	}
	var listed bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM shopping.items WHERE household_id=$1 AND list_id=$2 AND coalesce(replacement_product_id,product_id)=$3)", id.HouseholdID, list, in.ProductID).Scan(&listed); err != nil {
		return nil, err
	}
	if listed {
		return nil, ErrConflict
	}
	m, _ := s.Module("shopping")
	e, _ := m.Entity("items")
	data := Record{"list_id": list, "name": product.Name, "product_id": in.ProductID, "quantity": in.Quantity, "unit_code": "piece", "availability": "available", "group_name": "", "note": "", "sort_order": int64(0)}
	if err = s.validateDomain(ctx, tx, id, "shopping", "items", "", data, data); err != nil {
		return nil, err
	}
	item, err := save(ctx, tx, id, m, e, value.UUID(), data, true, 0)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, item, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "shopping", "items", String(item["id"]), "record_write", requestID); err != nil {
		return nil, err
	}
	return Record{"id": item["id"], "alias_version": in.AliasVersion, "product_id": in.ProductID, "list_id": list, "quantity": in.Quantity}, nil
}
