package store

import (
	"context"
	"encoding/json"
	"errors"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ConfirmCapture(ctx context.Context, id auth.Identity, captureID, requestID string, in Mutation) (string, string, string, error) {
	if !value.ValidUUID(captureID) {
		return "", "", "", ErrNotFound
	}
	var mid, entity string
	err := s.Pool.QueryRow(ctx, "SELECT module_id,target_schema FROM core.capture_jobs WHERE household_id=$1 AND id=$2", id.HouseholdID, captureID).Scan(&mid, &entity)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	if err != nil {
		return "", "", "", err
	}
	m, ok := s.Module(mid)
	e, exists := m.Entity(entity)
	if !ok || !exists {
		return "", "", "", ErrNotFound
	}
	data, err := e.Normalize(in.Data, false)
	if err != nil {
		return "", "", "", Invalid{err.Error()}
	}
	normalized, err := json.Marshal(data)
	if err != nil {
		return "", "", "", err
	}
	tx, err := s.BeginMutation(ctx, id, mid, in.Generation)
	if err != nil {
		return "", "", "", err
	}
	defer rollback(tx)
	var state, attachment string
	var result *string
	var same bool
	err = tx.QueryRow(ctx, "SELECT state,result_resource_id,source_attachment_id,coalesce(confirmed_values=$4::jsonb,false) FROM core.capture_jobs WHERE household_id=$1 AND id=$2 AND module_generation=$3 AND expires_at>now() FOR UPDATE", id.HouseholdID, captureID, in.Generation, normalized).Scan(&state, &result, &attachment, &same)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	if err != nil {
		return "", "", "", err
	}
	if state == "confirmed" && result != nil {
		if !same {
			return "", "", "", ErrConflict
		}
		return *result, mid, entity, nil
	}
	if state != "review" {
		return "", "", "", ErrConflict
	}
	var sourceType, sourceID string
	err = tx.QueryRow(ctx, "SELECT resource_type,resource_id FROM core.attachments WHERE id=$1 AND household_id=$2 AND module_id=$3 AND module_generation=$4 AND state='ready'", attachment, id.HouseholdID, mid, in.Generation).Scan(&sourceType, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrConflict
	}
	if err != nil {
		return "", "", "", err
	}
	if err = s.LockResource(ctx, tx, id, mid, sourceType, sourceID); err != nil {
		return "", "", "", err
	}
	var live string
	err = tx.QueryRow(ctx, "SELECT id FROM core.attachments WHERE id=$1 AND household_id=$2 AND module_id=$3 AND module_generation=$4 AND state='ready' AND resource_type=$5 AND resource_id=$6 FOR UPDATE", attachment, id.HouseholdID, mid, in.Generation, sourceType, sourceID).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrConflict
	}
	if err != nil {
		return "", "", "", err
	}
	if err = s.validateDomain(ctx, tx, id, mid, entity, "", data, data); err != nil {
		return "", "", "", err
	}
	record, err := save(ctx, tx, id, m, e, value.UUID(), data, true, 0)
	if err != nil {
		return "", "", "", err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, record, in.Generation); err != nil {
		return "", "", "", err
	}
	rid := String(record["id"])
	// Compare and persist the approved input before domain-specific storage normalization.
	if _, err = tx.Exec(ctx, "UPDATE core.capture_jobs SET state='confirmed',confirmed_values=$2,confirmed_by=$3,confirmed_at=now(),result_resource_id=$4 WHERE id=$1", captureID, normalized, id.UserID, rid); err != nil {
		return "", "", "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.attachments SET resource_type=$2,resource_id=$3 WHERE id=$1", attachment, entity, rid); err != nil {
		return "", "", "", err
	}
	if err = Audit(ctx, tx, id, mid, entity, rid, "capture_confirm", requestID); err != nil {
		return "", "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", "", err
	}
	return rid, mid, entity, nil
}
