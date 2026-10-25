//go:build integration

package web

import (
	"os"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
)

func TestIntegrationMigrationRepeatAndAuditRepair(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("fresh installation is not ready", err)
	}
	var records int
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM shopping.items)+(SELECT count(*) FROM recipes.recipes)
 +(SELECT count(*) FROM pantry.lots)+(SELECT count(*) FROM expenses.entries)`).Scan(&records); err != nil || records != 0 {
		t.Fatal("fresh fixture retained runtime domain data", records, err)
	}
	_, lot := pantryFixture(t, f, "migration repeat contract")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "10.250001", "reason": "receive"})
	started := time.Now()
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal("explicit repeat migration", err)
	}
	fixtureTiming(t, "explicit_migration_repeat", started)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("repeat migration is not ready", err)
	}
	var balance string
	var movements int64
	if err := f.pool.QueryRow(f.ctx, "SELECT quantity::text,movement_count FROM pantry.lot_balances WHERE lot_id=$1", lot["id"]).Scan(&balance, &movements); err != nil || balance != "10.250001" || movements != 1 {
		t.Fatal("repeat migration rewrote or duplicated stock", balance, movements, err)
	}
	if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER household_audit_change ON shopping.lists"); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("readiness accepted a missing audit trigger")
	}
	if _, err := f.pool.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err == nil {
		t.Fatal("runtime role could repair privileged audit metadata")
	}
	started = time.Now()
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal("owner repeat did not repair the missing trigger", err)
	}
	fixtureTiming(t, "explicit_migration_repair", started)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("repaired installation is not ready", err)
	}
	list, requestID := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Audited after migration repair"})
	var actor string
	if err := f.pool.QueryRow(f.ctx, "SELECT actor_user_id FROM core.audit_log WHERE resource_id=$1 AND request_id=$2 AND action='CREATE'", list["id"], requestID).Scan(&actor); err != nil || actor != f.identity.UserID {
		t.Fatal("repaired trigger did not audit the authenticated actor", actor, err)
	}
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO core.audit_log(action,outcome,request_id) VALUES('forged','success',gen_random_uuid())"); err == nil {
		t.Fatal("repeat migration weakened immutable audit privileges")
	}
}
