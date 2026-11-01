package store

import (
	"context"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/database"

	"github.com/jackc/pgx/v5"
)

// recordPreparedTransfer extends only the current prepared-food schema. Older
// clean Pantry installations retain their original transfer transaction.
func recordPreparedTransfer(ctx context.Context, tx pgx.Tx, id auth.Identity, source, target, correlation, quantity string, generation int64) error {
	var schemaRows, version int64
	var dirty bool
	if err := tx.QueryRow(ctx, "SELECT count(*),coalesce(max(version),0),coalesce(bool_or(dirty),false) FROM pantry.schema_migrations").Scan(&schemaRows, &version, &dirty); err != nil {
		return err
	}
	if schemaRows != 1 || dirty || version < 1 || version > int64(database.ExpectedVersion("pantry")) {
		return ErrConflict
	}
	if version < 12 {
		return nil
	}
	var recorded bool
	if err := tx.QueryRow(ctx, "SELECT pantry.record_prepared_transfer($1,$2,$3,$4::numeric,$5,$6,$7,$8)",
		source, target, correlation, quantity, generation, id.SessionID, id.AuthVersion, id.MembershipID).Scan(&recorded); err != nil {
		return shoppingContention(err)
	}
	if !recorded {
		return nil
	}
	return audit.Event(ctx, tx, "pantry", "prepared_transfers", target, "record_write", nil)
}
