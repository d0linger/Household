package store

import (
	"context"
	"encoding/json"
	"errors"
	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ConfirmCapture(ctx context.Context, id auth.Identity, captureID, requestID string, in Mutation) (string, string, string, error) {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
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
	if mid == "fuel" {
		return "", "", "", Invalid{"Fahrzeugbeleg über Fahrzeuge → Belegerkennung mit Quellenprüfung übernehmen"}
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
	if mid == "documents" {
		return "", "", "", Invalid{"Dokument im Dokumentenarbeitsbereich prüfen; das unveränderte Original wird dort als Dateiversion erhalten"}
	}
	if err = s.LockFinanceArtifact(ctx, tx, id, "capture", captureID, "edit"); err != nil {
		return "", "", "", err
	}
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
		if financialEntity(mid, entity) {
			if err = lockFinancialReplay(ctx, tx, id, entity, *result, financialEdit); err != nil {
				return "", "", "", err
			}
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
	captureOperation, err := s.PrepareFinanceArtifact(ctx, tx, id, "capture", captureID, []FinanceResourceRef{{Kind: "attachment", ID: attachment}})
	if err != nil {
		return "", "", "", err
	}
	rid := value.UUID()
	var financeOperation string
	var financeScope financialScope
	if financialEntity(mid, entity) {
		financeOperation, financeScope, err = prepareLegacyFinanceMutation(withFinanceOrigin(ctx, "capture", []FinanceResourceRef{{Kind: "capture", ID: captureID}}),
			tx, id, entity, rid, data)
		if err != nil {
			return "", "", "", err
		}
	}
	record, err := save(ctx, tx, id, m, e, rid, data, true, 0)
	if err != nil {
		return "", "", "", err
	}
	if err = s.afterWrite(ctx, tx, id, m, e, record, in.Generation); err != nil {
		return "", "", "", err
	}
	if financeOperation != "" {
		if err = completeFinanceOrigin(ctx, tx, id, financeScope, financeOperation); err != nil {
			return "", "", "", err
		}
	}
	// Compare and persist the approved input before domain-specific storage normalization.
	if _, err = tx.Exec(ctx, "UPDATE core.capture_jobs SET state='confirmed',confirmed_values=$2,confirmed_by=$3,confirmed_at=now(),result_resource_id=$4 WHERE id=$1", captureID, normalized, id.UserID, rid); err != nil {
		return "", "", "", err
	}
	attachmentSources := []FinanceResourceRef{{Kind: "capture", ID: captureID}}
	if financialEntity(mid, entity) {
		attachmentSources = append(attachmentSources, FinanceResourceRef{Kind: entity, ID: rid})
	}
	attachmentOperation, err := s.PrepareFinanceArtifact(ctx, tx, id, "attachment", attachment, attachmentSources)
	if err != nil {
		return "", "", "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.attachments SET resource_type=$2,resource_id=$3 WHERE id=$1", attachment, entity, rid); err != nil {
		return "", "", "", err
	}
	if err = s.CompleteFinanceArtifact(ctx, tx, id, attachmentOperation); err != nil {
		return "", "", "", err
	}
	if err = Audit(ctx, tx, id, mid, entity, rid, "capture_confirm", requestID); err != nil {
		return "", "", "", err
	}
	if err = s.CompleteFinanceArtifact(ctx, tx, id, captureOperation); err != nil {
		return "", "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", "", err
	}
	return rid, mid, entity, nil
}
