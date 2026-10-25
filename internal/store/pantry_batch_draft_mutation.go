package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PantryBatchDiscardInput struct {
	Generation int64 `json:"generation"`
	Version    int64 `json:"version"`
}

func pantryDraftPrior(ctx context.Context, tx pgx.Tx, id auth.Identity, op, key string, input any) ([]byte, Record, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256(raw)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.HouseholdID+id.UserID+op+key); err != nil {
		return nil, nil, err
	}
	var priorHash, reply []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &reply)
	if errors.Is(err, pgx.ErrNoRows) {
		return hash[:], nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(hash[:], priorHash) {
		return nil, nil, ErrConflict
	}
	prior, err := decode(reply)
	return hash[:], prior, err
}

func pantryDraftReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity, op, key string, generation int64, hash []byte, result Record) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'pantry',$6,200,$7)`, id.HouseholdID, id.UserID, op, key, hash, generation, raw)
	return err
}

func (s *Store) SavePantryBatchDraft(ctx context.Context, id auth.Identity, draft, key, requestID string, input PantryBatchDraftInput) (Record, error) {
	if !value.ValidUUID(draft) || !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Entwurfs- und Vorgangskennung erforderlich"}
	}
	in, err := normalizePantryBatchDraft(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 20*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	op := "pantry/batch-draft/" + draft + "/save"
	hash, prior, err := pantryDraftPrior(ctx, tx, id, op, key, in)
	if err != nil || prior != nil {
		return prior, err
	}
	current, err := readPantryBatchDraft(ctx, tx, id, draft, true)
	creating := errors.Is(err, ErrNotFound) && in.Version == 0
	if err != nil && !creating {
		return nil, err
	}
	if !creating && (Int(current["version"]) != in.Version || current["state"] != "active") {
		return nil, ErrConflict
	}
	if !creating && (current["kind"] != in.Kind || (String(current["location_id"]) != "" && String(current["location_id"]) != in.LocationID)) {
		return nil, Invalid{"Art und bereits festgelegter Inventurort sind fest. Einen neuen unabhängigen Entwurf anlegen."}
	}
	payload, err := json.Marshal(Record{"lines": in.Lines})
	if err != nil {
		return nil, err
	}
	if creating {
		_, err = tx.Exec(ctx, `INSERT INTO pantry.stock_batch_drafts(id,household_id,owner_id,generation,kind,title,location_id,payload)
 VALUES($1,$2,$3,$4,$5,$6,nullif($7,'')::uuid,$8)`, draft, id.HouseholdID, id.UserID, in.Generation, in.Kind, in.Title, in.LocationID, payload)
	} else {
		_, err = tx.Exec(ctx, `UPDATE pantry.stock_batch_drafts SET title=$4,location_id=nullif($5,'')::uuid,payload=$6
 WHERE household_id=$1 AND owner_id=$2 AND id=$3`, id.HouseholdID, id.UserID, draft, in.Title, in.LocationID, payload)
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	result, err := readPantryBatchDraft(ctx, tx, id, draft, false)
	if err != nil {
		return nil, err
	}
	if err = pantryDraftReceipt(ctx, tx, id, op, key, in.Generation, hash, result); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "pantry", "stock_batch_drafts", draft, "record_write", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func (s *Store) DiscardPantryBatchDraft(ctx context.Context, id auth.Identity, draft, key, requestID string, in PantryBatchDiscardInput) (Record, error) {
	if !value.ValidUUID(draft) || !value.ValidUUID(key) || in.Version < 1 || in.Generation < 1 {
		return nil, Invalid{"Geprüften Entwurfs- und Modulstand übermitteln"}
	}
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 20*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	op := "pantry/batch-draft/" + draft + "/discard"
	hash, prior, err := pantryDraftPrior(ctx, tx, id, op, key, in)
	if err != nil || prior != nil {
		return prior, err
	}
	current, err := readPantryBatchDraft(ctx, tx, id, draft, true)
	if err != nil {
		return nil, err
	}
	if Int(current["version"]) != in.Version || current["state"] != "active" {
		return nil, ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE pantry.stock_batch_drafts SET state='discarded' WHERE household_id=$1 AND owner_id=$2 AND id=$3", id.HouseholdID, id.UserID, draft); err != nil {
		return nil, shoppingContention(err)
	}
	result, err := readPantryBatchDraft(ctx, tx, id, draft, false)
	if err != nil {
		return nil, err
	}
	if err = pantryDraftReceipt(ctx, tx, id, op, key, in.Generation, hash, result); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "pantry", "stock_batch_drafts", draft, "record_write", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}
