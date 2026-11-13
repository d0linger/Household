//go:build integration

package web

import (
	"errors"
	"testing"

	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
)

func TestShoppingExtensionsPurgeClearsOnlyOwnConsentAndOldGeneration(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Purge watched list"})
	gen := f.app.Store.States()["shopping"].Generation
	shoppingExtensionWatch(t, f, f.identity, store.String(list["id"]), "0", true)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET settings=settings||jsonb_build_object('unrelated',jsonb_build_object('retained',true))
 WHERE household_id=$1 AND module_id='shopping'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	// Synthetic encrypted-row fixture only, proving Core's independent lifetime;
	// not a claim that this invalid envelope was registered or sent to a vendor.
	device := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.push_subscriptions(id,household_id,user_id,endpoint_hash,encrypted_subscription,encryption_key_id,encryption_format)
 VALUES($1::uuid,$2::uuid,$3::uuid,sha256(convert_to($1::uuid::text,'UTF8')),decode('01','hex'),'synthetic-do-not-send',1)`, device, f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, f.app.Store.States()["shopping"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	backup, request := value.UUID(), value.UUID()
	// Synthetic backup eligibility ONLY. Real encrypted backup/restore remains
	// a separate required guarded operator acceptance, never inferred from this.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference)
 VALUES($1,'complete',clock_timestamp(),clock_timestamp(),'SYNTHETIC Shopping purge eligibility','not-a-real-backup',$2)`, backup, "synthetic-shopping-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference)
 VALUES($1,$2,'shopping',$3,$4,$5)`, request, f.identity.HouseholdID, gen, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.pool, request); err == nil {
		t.Fatal("App role performed owner purge")
	}
	if err := maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal(err)
	}
	var generation int64
	var removed, unrelated bool
	var devices int
	if err := f.owner.QueryRow(f.ctx, `SELECT data_generation,NOT(settings ? 'shopping_notify_v1'),settings->'unrelated'=jsonb_build_object('retained',true),
 (SELECT count(*) FROM core.push_subscriptions WHERE id=$2) FROM core.module_states WHERE household_id=$1 AND module_id='shopping'`, f.identity.HouseholdID, device).Scan(&generation, &removed, &unrelated, &devices); err != nil || generation != gen+1 || !removed || !unrelated || devices != 1 {
		t.Fatal("purge ownership/generation scope", generation, removed, unrelated, devices, err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("unchanged SQL/Ready contract", err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "shopping", true, f.app.Store.States()["shopping"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	fresh, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "New generation"})
	if _, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, f.identity, store.String(fresh["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "0", Enabled: true}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old generation rebuilt consent", err)
	}
	view := shoppingExtensionWatch(t, f, f.identity, store.String(fresh["id"]), "0", true)
	if view.Context.Generation != gen+1 || view.Revision != "1" {
		t.Fatal("fresh explicit consent failed", view)
	}
}
