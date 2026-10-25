package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const pantryBatchDraftJSON = `to_jsonb(d)-'payload'||jsonb_build_object('lines',d.payload->'lines','location_id',coalesce(d.location_id::text,''),'expired',d.expires_at<=clock_timestamp())`

func pantryBatchDraftInput(record Record) (PantryBatchDraftInput, error) {
	in := PantryBatchDraftInput{Generation: Int(record["generation"]), Version: Int(record["version"]), Kind: String(record["kind"]), Title: String(record["title"]), LocationID: String(record["location_id"])}
	raw, err := json.Marshal(record["lines"])
	if err != nil {
		return in, err
	}
	if err = json.Unmarshal(raw, &in.Lines); err != nil {
		return in, err
	}
	return normalizePantryBatchDraft(in)
}

func pantryBatchFingerprint(in PantryBatchDraftInput) (string, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func readPantryBatchDraft(ctx context.Context, q pantryQuerier, id auth.Identity, draft string, lock bool) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	if !value.ValidUUID(draft) {
		return nil, ErrNotFound
	}
	query := "SELECT " + pantryBatchDraftJSON + ` FROM pantry.stock_batch_drafts d JOIN core.module_states m ON m.household_id=d.household_id AND m.module_id='pantry' AND m.state='enabled' AND m.data_generation=d.generation
 WHERE d.household_id=$1 AND d.owner_id=$2 AND d.id=$3`
	if lock {
		query += " FOR UPDATE OF d NOWAIT"
	}
	var raw []byte
	err := q.QueryRow(ctx, query, id.HouseholdID, id.UserID, draft).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	record, err := decode(raw)
	if err != nil {
		return nil, err
	}
	in, err := pantryBatchDraftInput(record)
	if err != nil {
		return nil, err
	}
	record["fingerprint"], err = pantryBatchFingerprint(in)
	return record, err
}

func (s *Store) PantryBatchDraft(ctx context.Context, id auth.Identity, draft string) (Record, error) {
	return readPantryBatchDraft(ctx, s.Pool, id, draft, false)
}

func (s *Store) PantryBatchDrafts(ctx context.Context, id auth.Identity) ([]Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT jsonb_build_object('id',d.id,'kind',d.kind,'title',d.title,'version',d.version,'updated_at',d.updated_at,'expires_at',d.expires_at,'line_count',jsonb_array_length(d.payload->'lines'))
 FROM pantry.stock_batch_drafts d JOIN core.module_states m ON m.household_id=d.household_id AND m.module_id='pantry' AND m.state='enabled' AND m.data_generation=d.generation
 WHERE d.household_id=$1 AND d.owner_id=$2 AND d.state='active' AND d.expires_at>clock_timestamp() ORDER BY d.created_at DESC,d.id DESC LIMIT 11`, id.HouseholdID, id.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := cookingRecords(rows)
	if err != nil {
		return nil, err
	}
	if len(records) > 10 {
		return nil, Invalid{"Entwurfsgrenze überschritten; gespeicherte Entwürfe vor weiteren Änderungen prüfen"}
	}
	return records, nil
}
