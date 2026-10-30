package store

import (
	"context"
	"encoding/json"
	"errors"
	"household/internal/audit"
	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// ConfirmImport commits every row together; retrying a confirmed batch is harmless.
func (s *Store) ConfirmImport(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, batchID, requestID string, generation int64) error {
	if m.ID == "fuel" {
		return Invalid{"Fahrzeugdaten über Fahrzeuge → Import mit Zeilenvorschau und dauerhaftem Vorgangsschlüssel übernehmen"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if !value.ValidUUID(batchID) {
		return ErrNotFound
	}
	tx, err := s.BeginMutation(ctx, id, m.ID, generation)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var state string
	var purpose string
	var failures int
	err = tx.QueryRow(ctx, "SELECT state,error_count,purpose FROM core.import_batches WHERE id=$1 AND household_id=$2 AND module_id=$3 AND entity=$4 AND module_generation=$5 FOR UPDATE", batchID, id.HouseholdID, m.ID, e.ID, generation).Scan(&state, &failures, &purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if purpose != "flat_csv" {
		return ErrNotFound
	}
	if state == "confirmed" {
		return nil
	}
	if state != "preview" || failures != 0 {
		return ErrConflict
	}
	rows, err := tx.Query(ctx, "SELECT row_number,normalized_values FROM core.import_rows WHERE batch_id=$1 ORDER BY row_number", batchID)
	if err != nil {
		return err
	}
	type item struct {
		number int
		data   map[string]json.RawMessage
	}
	items := []item{}
	for rows.Next() {
		var x item
		var raw []byte
		if err = rows.Scan(&x.number, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &x.data); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range items {
		data, err := e.Normalize(x.data, false)
		if err != nil {
			return Invalid{err.Error()}
		}
		if err = s.validateDomain(ctx, tx, id, m.ID, e.ID, "", data, data); err != nil {
			return err
		}
		record, err := save(ctx, tx, id, m, e, value.UUID(), data, true, 0)
		if err != nil {
			return err
		}
		if err = s.afterWrite(ctx, tx, id, m, e, record, generation); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE core.import_rows SET state='imported',result_resource_id=$3 WHERE batch_id=$1 AND row_number=$2", batchID, x.number, record["id"]); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE core.import_batches SET state='confirmed',confirmed_by=$2,confirmed_at=now() WHERE id=$1", batchID, id.UserID); err != nil {
		return err
	}
	if err = Audit(ctx, tx, id, m.ID, e.ID, "", "import_confirm", requestID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
