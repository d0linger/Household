package maintenance

import (
	"context"
	"errors"
	"household/internal/audit"
	"household/internal/modules"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Purge(ctx context.Context, pool *pgxpool.Pool, requestID string) error {
	if !value.ValidUUID(requestID) {
		return errors.New("purge requires the exact request UUID")
	}
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), requestID)
	tx, err := audit.Begin(ctx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var role string
	if err = tx.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil {
		return err
	}
	if role != "household_owner" {
		return errors.New("purge requires the separate maintenance role")
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households FOR UPDATE"); err != nil {
		return err
	}
	var mid, hid, actor string
	var generation int64
	err = tx.QueryRow(ctx, `SELECT p.module_id,p.household_id,p.requested_by,p.expected_generation FROM core.module_purge_requests p JOIN core.memberships m ON m.household_id=p.household_id AND m.user_id=p.requested_by JOIN core.users u ON u.id=m.user_id JOIN core.module_states s ON s.household_id=p.household_id AND s.module_id=p.module_id WHERE p.id=$1 AND p.state='pending' AND p.expires_at>now() AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password AND s.state='disabled' AND s.data_generation=p.expected_generation AND EXISTS(SELECT 1 FROM core.backup_runs b WHERE b.state='complete' AND b.started_at>=s.changed_at AND b.started_at>now()-interval '7 days' AND b.verified_at>now()-interval '7 days' AND (b.id::text=p.backup_reference OR b.snapshot_reference=p.backup_reference)) FOR UPDATE OF p,s`, requestID).Scan(&mid, &hid, &actor, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("purge request is expired, invalid, unverified, or module is active")
	}
	if err != nil {
		return err
	}
	ctx = audit.WithHousehold(ctx, hid)
	if err = audit.Bind(ctx, tx); err != nil {
		return err
	}
	known := false
	for _, m := range modules.Registry() {
		if m.ID == mid {
			known = true
		}
	}
	if !known {
		return errors.New("module is not compiled into this release")
	}
	rows, err := tx.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=$1 AND tablename NOT IN('schema_migrations','fuel_types') ORDER BY tablename", mid)
	if err != nil {
		return err
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, pgx.Identifier{mid, table}.Sanitize())
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return errors.New("module has no purgeable tables")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.file_deletions(storage_key,household_id,attachment_id) SELECT storage_key,household_id,id FROM core.attachments WHERE household_id=$1 AND module_id=$2 UNION ALL SELECT d.storage_key,a.household_id,a.id FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.household_id=$1 AND a.module_id=$2 ON CONFLICT DO NOTHING", hid, mid); err != nil {
		return err
	}
	// security: schema is the compiled registry and table names come from pg_catalog.
	query := "TRUNCATE "
	for i, table := range tables {
		if i > 0 {
			query += ","
		}
		query += table
	}
	if _, err = tx.Exec(ctx, query); err != nil {
		return err
	}
	for _, query := range []string{"DELETE FROM core.alert_states WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.capture_jobs WHERE household_id=$1 AND module_id=$2", "UPDATE core.attachments SET state='purged' WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.search_documents WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.reminders WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.notifications WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.jobs WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.outbox_events WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.idempotency_records WHERE household_id=$1 AND (module_id=$2 OR target_generations ? $2)", "DELETE FROM core.import_batches WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.resource_links WHERE household_id=$1 AND (source_module=$2 OR target_module=$2)"} {
		if _, err = tx.Exec(ctx, query, hid, mid); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE core.module_states SET data_generation=data_generation+1,state_version=state_version+1,changed_at=now() WHERE household_id=$1 AND module_id=$2 AND data_generation=$3", hid, mid, generation); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.households SET module_revision=module_revision+1 WHERE id=$1", hid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE core.module_purge_requests SET state='completed',finished_at=now() WHERE id=$1", requestID); err != nil {
		return err
	}
	if err = audit.Event(ctx, tx, mid, "module", "", "module_purged", map[string]any{"requested_by": actor}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
