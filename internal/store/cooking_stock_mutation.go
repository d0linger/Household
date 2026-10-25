package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PostCookingStock(ctx context.Context, id auth.Identity, record, key, requestID string, input CookingStockInput) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(record) || !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Koch- und Vorgangskennung erforderlich"}
	}
	in, err := normalizeCookingStock(input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Record string            `json:"record"`
		Input  CookingStockInput `json:"input"`
	}{record, in})
	if err != nil {
		return nil, err
	}
	if len(encoded) > 1<<20 {
		return nil, Invalid{"Buchungsdaten überschreiten 1 MiB"}
	}
	hash := sha256.Sum256(encoded)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "recipes", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	// The source lock excludes actor and target generation: every authorized
	// editor competes for the same once-only source, including after target purge.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+"cooking-stock/"+record); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+id.UserID+"cooking-stock-key/"+key); err != nil {
		return nil, err
	}
	var state string
	var generation int64
	err = tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id='pantry' FOR SHARE", id.HouseholdID).Scan(&state, &generation)
	if err != nil {
		return nil, err
	}
	proof, priorHash, err := readCookingStockProof(ctx, tx, id, record, in.Generation)
	if err != nil {
		return nil, err
	}
	if proof != nil {
		if !bytes.Equal(hash[:], priorHash) {
			return nil, ErrConflict
		}
		return visibleCookingStockReceipt(ctx, tx, id, proof, state == "enabled", generation)
	}
	var priorKey bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM core.cooking_stock_postings WHERE household_id=$1 AND created_by=$2 AND request_key=$3)", id.HouseholdID, id.UserID, key).Scan(&priorKey); err != nil {
		return nil, err
	}
	if priorKey {
		return nil, ErrConflict
	}
	if state != "enabled" {
		return nil, ErrNotFound
	}
	if generation != in.PantryGeneration {
		return nil, ErrConflict
	}
	source, err := readCookingStockSource(ctx, tx, id, record)
	if err != nil {
		return nil, err
	}
	if String(source["source_version_id"]) != in.SourceVersionID {
		return nil, ErrConflict
	}
	decisions, lots, amounts, err := validateCookingStock(ctx, tx, id, source, in)
	if err != nil {
		return nil, err
	}
	posting := value.UUID()
	mode := "stock"
	if len(amounts) == 0 {
		mode = "no_stock"
	}
	var at time.Time
	err = tx.QueryRow(ctx, `INSERT INTO core.cooking_stock_postings(id,household_id,recipes_generation,cooking_record_id,source_version_id,pantry_generation,created_by,request_key,request_hash,decision_count,movement_count,mode)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at`, posting, id.HouseholdID, in.Generation, record, in.SourceVersionID, in.PantryGeneration, id.UserID, key, hash[:], len(decisions), len(amounts), mode).Scan(&at)
	if err != nil {
		return nil, shoppingContention(err)
	}
	// Keep only needed, immutable provenance in Pantry's durable receipt.
	delete(source, "ingredients")
	result := Record{"id": posting, "source": source, "decisions": decisions, "mode": mode, "created_at": at.UTC().Format(time.RFC3339Nano)}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(snapshot) > 2<<20 {
		return nil, Invalid{"Buchungsbeleg überschreitet die zulässige Größe"}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO pantry.cooking_stock_receipts(id,household_id,snapshot) VALUES($1,$2,$3)", posting, id.HouseholdID, snapshot); err != nil {
		return nil, err
	}
	for _, review := range in.Lots {
		if err = s.writeCookingStockLot(ctx, tx, id, posting, review.ID, amounts[review.ID], lots[review.ID], hash[:], in.PantryGeneration, requestID); err != nil {
			return nil, err
		}
	}
	proof = Record{"id": posting, "mode": mode, "created_at": result["created_at"], "pantry_generation": in.PantryGeneration}
	result, err = visibleCookingStockReceipt(ctx, tx, id, proof, true, in.PantryGeneration)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func (s *Store) writeCookingStockLot(ctx context.Context, tx pgx.Tx, id auth.Identity, posting, lid, amount string, lot Record, hash []byte, generation int64, requestID string) error {
	var movement string
	err := tx.QueryRow(ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,note,created_by,correlation_id)
 VALUES($1,$2,-$3::numeric,'consume','Geprüfter Verbrauch nach Kochen',$4,$5) RETURNING id`, id.HouseholdID, lid, amount, id.UserID, posting).Scan(&movement)
	if err != nil {
		return shoppingContention(err)
	}
	after, err := readPantryLot(ctx, tx, id, lid)
	if err != nil {
		return err
	}
	oid := value.UUID()
	operation := Record{"id": oid, "kind": "consume", "lot_id": lid, "movement_id": movement, "reverses_movement_id": "", "quantity": amount, "unit": lot["unit"],
		"product_name": lot["product_name"], "location": lot["location"], "lot_label": lot["label"], "stock_after": after["stock"], "movement_count": after["movement_count"], "cooking_posting_id": posting, "pack": nil}
	var at time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return err
	}
	operation["recorded_at"] = at.UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pantry.stock_operations(id,household_id,lot_id,movement_id,kind,request_key,request_hash,snapshot,created_by,created_at)
 VALUES($1,$2,$3,$4,'consume',$5,$6,$7,$8,$9)`, oid, id.HouseholdID, lid, movement, value.UUID(), hash, raw, id.UserID, at); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pantry.cooking_stock_movements(id,household_id,posting_id,lot_id,operation_id,quantity,unit_code) VALUES($1,$2,$3,$4,$5,$6::numeric,$7)`, movement, id.HouseholdID, posting, lid, oid, amount, lot["unit"]); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, "SELECT to_jsonb(m) FROM pantry.stock_movements m WHERE household_id=$1 AND id=$2", id.HouseholdID, movement).Scan(&raw); err != nil {
		return err
	}
	row, err := decode(raw)
	if err != nil {
		return err
	}
	m, _ := s.Module("pantry")
	e, _ := m.Entity("stock_movements")
	if err = s.afterWrite(ctx, tx, id, m, e, row, generation); err != nil {
		return err
	}
	return Audit(ctx, tx, id, "pantry", "stock_movements", movement, "record_write", requestID)
}
