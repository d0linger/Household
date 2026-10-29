package maintenance

import (
	"context"
	"errors"
	"household/internal/audit"
	"household/internal/database"
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
	if err = tx.QueryRow(ctx, "SELECT session_user").Scan(&role); err != nil {
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
	rows, err := tx.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=$1 AND tablename NOT IN('schema_migrations','fuel_types') AND NOT($1='fuel' AND tablename IN('module_operations','module_effects','legacy_operation_fences')) AND NOT($1='pantry' AND tablename IN('leftover_operations','foodfacts_operations')) ORDER BY tablename", mid)
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
	switch mid {
	case "mealplan":
		if _, err = tx.Exec(ctx, "DELETE FROM core.planning_purchase_requirements WHERE household_id=$1", hid); err != nil {
			return err
		}
	case "pantry":
		if _, err = tx.Exec(ctx, `UPDATE core.planning_purchase_requirements SET binding_id=NULL,binding_version=NULL,product_id=NULL,pantry_generation=NULL,unit_code=NULL,
 review_reason='pantry_purged',changed_by=$2 WHERE household_id=$1 AND product_id IS NOT NULL`, hid, actor); err != nil {
			return err
		}
	case "shopping":
		if _, err = tx.Exec(ctx, `UPDATE core.planning_purchase_requirements SET
 procurement_disposition=CASE WHEN item_id IS NOT NULL THEN 'missing' ELSE procurement_disposition END,
 review_reason=CASE WHEN item_id IS NOT NULL THEN 'shopping_purged' ELSE review_reason END,
 shopping_generation=NULL,list_id=NULL,item_id=NULL,item_version=NULL,item_fingerprint=NULL,pending_quantity=0,
 last_checkout_generation=NULL,last_checkout_id=NULL,last_checkout_item_id=NULL,changed_by=$2
 WHERE household_id=$1 AND (item_id IS NOT NULL OR last_checkout_id IS NOT NULL)`, hid, actor); err != nil {
			return err
		}
	}
	if mid == "mealplan" || mid == "pantry" {
		if mid == "mealplan" {
			if _, err = tx.Exec(ctx, "DELETE FROM core.planning_ingredient_bindings WHERE household_id=$1", hid); err != nil {
				return err
			}
		} else if _, err = tx.Exec(ctx, `UPDATE core.planning_ingredient_bindings SET product_id=NULL,pantry_generation=NULL,unit_code=NULL,changed_by=$2
 WHERE household_id=$1 AND product_id IS NOT NULL`, hid, actor); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE core.meal_reservations SET state='released',release_reason='module_purged' WHERE household_id=$1 AND state='active'", hid); err != nil {
			return err
		}
		// Keep minimal immutable operation identities; remove the detailed links
		// and quantities with the separate maintenance role after release.
		if _, err = tx.Exec(ctx, "DELETE FROM core.meal_reservation_allocations WHERE household_id=$1", hid); err != nil {
			return err
		}
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
	if mid == "fuel" {
		// The generation fence survives, but purged names, notes, OCR and other
		// historical business payloads must not remain in response receipts.
		if _, err = tx.Exec(ctx, `UPDATE fuel.module_operations SET result=NULL,result_sha256=NULL WHERE household_id=$1`, hid); err != nil {
			return err
		}
	}
	if mid == "pantry" {
		var version int
		var dirty bool
		if err = tx.QueryRow(ctx, "SELECT version,dirty FROM pantry.schema_migrations").Scan(&version, &dirty); err != nil {
			return err
		}
		if dirty || version > database.ExpectedVersion("pantry") {
			return errors.New("unsupported pantry schema for maintenance deletion")
		}
		if version >= 12 {
			// The exact owner/operator contract removes historical response text
			// while retaining minimal keys and completed cooking-source fences.
			if _, err = tx.Exec(ctx, "SELECT pantry.clear_extension_responses($1)", hid); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{"DELETE FROM core.alert_states WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.capture_jobs WHERE household_id=$1 AND module_id=$2", "UPDATE core.attachments SET state='purged' WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.search_documents WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.reminders WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.notifications WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.jobs WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.outbox_events WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.idempotency_records WHERE household_id=$1 AND (module_id=$2 OR target_generations ? $2)", "DELETE FROM core.import_batches WHERE household_id=$1 AND module_id=$2", "DELETE FROM core.resource_links WHERE household_id=$1 AND (source_module=$2 OR target_module=$2)", "DELETE FROM core.ingredient_product_bindings WHERE household_id=$1 AND ($2='recipes' OR $2='pantry')"} {
		if _, err = tx.Exec(ctx, query, hid, mid); err != nil {
			return err
		}
	}
	if mid == "expenses" {
		for _, table := range []string{"financial_subjects", "financial_policies"} {
			query := "UPDATE " + pgx.Identifier{"core", table}.Sanitize() + " SET retired_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND module_id=$2 AND module_generation=$3 AND retired_at IS NULL"
			if _, err = tx.Exec(ctx, query, hid, mid, generation); err != nil {
				return err
			}
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
