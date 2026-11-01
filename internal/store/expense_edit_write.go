package store

import (
	"context"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) writeExpenseEdit(ctx context.Context, tx pgx.Tx, id auth.Identity, draft expenseedit.Draft, changes expenseedit.LineChanges, requestID string) (Record, error) {
	m, _ := s.Module("expenses")
	entryEntity, _ := m.Entity("entries")
	lineEntity, _ := m.Entity("entry_lines")
	entryID := draft.EntryID
	creating := entryID == ""
	if creating {
		entryID = value.UUID()
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if !creating {
		var advanced bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2)`, id.HouseholdID, entryID).Scan(&advanced); err != nil {
			return nil, err
		}
		if advanced {
			return nil, Invalid{"Buchung mit Finanzdetails im Finanzarbeitsbereich gemeinsam bearbeiten"}
		}
	}
	origin, sources := financeOriginFor(ctx, nil)
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "entries", entryID, origin, sources)
	if err != nil {
		return nil, err
	}
	var payer any
	if draft.Header.PaidByMembershipID != "" {
		payer = draft.Header.PaidByMembershipID
	}
	_, err = save(ctx, tx, id, m, entryEntity, entryID, Record{
		"occurred_on": draft.Header.OccurredOn, "description": draft.Header.Description, "payee": draft.Header.Payee,
		"amount_minor": draft.Header.AmountMinor, "kind": draft.Header.Kind, "paid_by_membership_id": payer,
	}, creating, draft.Version)
	if err != nil {
		return nil, err
	}
	deleted := make([]string, 0, len(changes.Delete))
	for _, line := range changes.Delete {
		tag, err := tx.Exec(ctx, "DELETE FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2 AND id=$3 AND version=$4", id.HouseholdID, entryID, line.ID, line.Version)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrConflict
		}
		deleted = append(deleted, line.ID)
		if err = Audit(ctx, tx, id, "expenses", "entry_lines", line.ID, "record_delete", requestID); err != nil {
			return nil, err
		}
	}
	if len(deleted) > 0 {
		if err = cleanupExpenseEditLines(ctx, tx, id, deleted); err != nil {
			return nil, err
		}
	}
	for _, line := range changes.Save {
		lineID := line.ID
		adding := lineID == ""
		if adding {
			lineID = value.UUID()
		}
		row, err := save(ctx, tx, id, m, lineEntity, lineID, Record{
			"entry_id": entryID, "category_id": line.CategoryID, "amount_minor": line.AmountMinor, "note": line.Note, "position": line.Position,
		}, adding, line.Version)
		if err != nil {
			return nil, err
		}
		if err = s.afterWrite(ctx, tx, id, m, lineEntity, row, draft.Generation); err != nil {
			return nil, err
		}
		if err = Audit(ctx, tx, id, "expenses", "entry_lines", lineID, "record_write", requestID); err != nil {
			return nil, err
		}
	}
	if draft.Action == "post" {
		if err = financeLockDebts(ctx, tx, id.HouseholdID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2", id.HouseholdID, entryID); err != nil {
			return nil, err
		}
	}
	var raw []byte
	if err = tx.QueryRow(ctx, "SELECT to_jsonb(e) FROM expenses.entries e WHERE household_id=$1 AND id=$2", id.HouseholdID, entryID).Scan(&raw); err != nil {
		return nil, err
	}
	entry, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(ctx, tx, id, m, entryEntity, entry, draft.Generation); err != nil {
		return nil, err
	}
	action := "record_write"
	if draft.Action == "post" {
		action = "post"
	}
	if err = Audit(ctx, tx, id, "expenses", "entries", entryID, action, requestID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return entry, nil
}

func cleanupExpenseEditLines(ctx context.Context, tx pgx.Tx, id auth.Identity, deleted []string) error {
	// Each affected set is the explicitly removed, locked and bounded child set.
	for _, query := range []string{
		"DELETE FROM core.search_documents WHERE household_id=$1 AND module_id='expenses' AND resource_type='entry_lines' AND resource_id=ANY($2::uuid[])",
		"UPDATE core.reminders SET state='cancelled',cancelled_at=now() WHERE household_id=$1 AND module_id='expenses' AND resource_type='entry_lines' AND resource_id=ANY($2::uuid[]) AND state='pending'",
		"UPDATE core.attachments SET state='orphaned' WHERE household_id=$1 AND module_id='expenses' AND resource_type='entry_lines' AND resource_id=ANY($2::uuid[]) AND state='ready'",
		`INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at)
 SELECT storage_key,household_id,id,now()+interval '7 days' FROM core.attachments WHERE household_id=$1 AND module_id='expenses'
 AND resource_type='entry_lines' AND resource_id=ANY($2::uuid[]) AND state='orphaned' ON CONFLICT DO NOTHING`,
		`INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at)
 SELECT d.storage_key,a.household_id,a.id,now()+interval '7 days' FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id
 WHERE a.household_id=$1 AND a.module_id='expenses' AND a.resource_type='entry_lines' AND a.resource_id=ANY($2::uuid[]) AND a.state='orphaned' ON CONFLICT DO NOTHING`,
		`DELETE FROM core.resource_links WHERE household_id=$1 AND ((source_module='expenses' AND source_type='entry_lines' AND source_id=ANY($2::uuid[]))
 OR (target_module='expenses' AND target_type='entry_lines' AND target_id=ANY($2::uuid[])))`,
	} {
		if _, err := tx.Exec(ctx, query, id.HouseholdID, deleted); err != nil {
			return err
		}
	}
	return nil
}
