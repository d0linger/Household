//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

func shoppingExtensionOffline(t *testing.T, f *integrationFixture, id auth.Identity, enabled bool) {
	t.Helper()
	ctx := audit.WithRequest(audit.WithActor(f.ctx, id.UserID, id.HouseholdID), value.UUID())
	if _, err := audit.Exec(ctx, f.pool, `INSERT INTO core.user_preferences(household_id,user_id,offline_storage_enabled) VALUES($1,$2,$3)
 ON CONFLICT(household_id,user_id) DO UPDATE SET offline_storage_enabled=excluded.offline_storage_enabled`, id.HouseholdID, id.UserID, enabled); err != nil {
		t.Fatal(err)
	}
}

func TestShoppingExtensionsSnapshotCompleteExactAndPrivateProjection(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "<Complete> & Ä", "description": "LIST DESCRIPTION MUST NOT LEAK"})
	reader := shoppingTestMember(t, f, "snapshot-reader", "reader")
	gen := f.app.Store.States()["shopping"].Generation
	in := store.ShoppingSnapshotInput{Generation: gen, ListIDs: []string{store.String(list["id"])}}
	if _, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, reader, in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("snapshot without consent", err)
	}
	shoppingExtensionOffline(t, f, reader, true)
	product, _ := auditCreate(t, f, "shopping", "products", store.Record{"name": "Known product", "pack_quantity": "500", "pack_unit": "g", "note": "PRODUCT PRIVATE NOTE"})
	for n := 0; n < 121; n++ {
		data := store.Record{"list_id": list["id"], "name": "Item", "note": "disclosed item note", "quantity": "9007199254.740993", "unit_code": "piece", "product_id": product["id"], "assignee_id": reader.MembershipID}
		if n == 0 {
			data["quantity"] = nil
		}
		created, _ := auditCreate(t, f, "shopping", "items", data)
		if n == 0 {
			// Historical/restore fixture only: Shopping1's actual item unit column
			// is nullable, while today's public create form requires a unit. First
			// create a genuine valid domain row, then retain that legacy NULL under
			// the separate owner with normal audit triggers; no producer bypass is
			// claimed as a current application workflow.
			ctx := audit.WithRequest(audit.WithHousehold(audit.WithSystem(f.ctx, "operator"), f.identity.HouseholdID), value.UUID())
			tag, err := audit.Exec(ctx, f.owner, "UPDATE shopping.items SET unit_code=NULL,version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND quantity IS NULL", f.identity.HouseholdID, created["id"])
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatal("audited historical nullable-unit fixture", err)
			}
		}
	}
	out, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, reader, in)
	if err != nil || !out.Complete || out.Schema != 1 || len(out.Lists) != 1 || len(out.Lists[0].Items) != 121 || out.Totals.Items != 121 {
		t.Fatal("snapshot truncated to UI page", out.Totals, err)
	}
	if out.ExpiresAt.Sub(out.CapturedAt) != 7*24*time.Hour {
		t.Fatal("wrong fixed lifetime")
	}
	unknown, exact := 0, 0
	for _, item := range out.Lists[0].Items {
		if item.Quantity == nil {
			unknown++
			if item.Unit != nil {
				t.Fatal("historical unknown unit was fabricated into a default")
			}
		} else if *item.Quantity == "9007199254.740993" {
			exact++
		}
		if item.Note != "disclosed item note" || item.AssigneeLabel == nil || item.ProductLabel == nil {
			t.Fatal("allowed labels/notes missing")
		}
	}
	if unknown != 1 || exact != 120 {
		t.Fatal("unknown or exact quantity lost", unknown, exact)
	}
	raw, _ := json.Marshal(out)
	if len(raw) != out.Totals.UTF8Bytes {
		t.Fatal("byte count does not cover full response")
	}
	for _, secret := range []string{"LIST DESCRIPTION MUST NOT LEAK", "PRODUCT PRIVATE NOTE", reader.SessionID, `"assignee_id"`, `"image_id"`, `"currency"`, `"estimate_minor"`} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("forbidden snapshot field", secret)
		}
	}
	shoppingExtensionOffline(t, f, reader, false)
	if _, err = f.app.Store.ShoppingOfflineSnapshots(f.ctx, reader, in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("withdrawn offline consent accepted", err)
	}
}

func shoppingExtensionSeedItems(t *testing.T, f *integrationFixture, list string, count int, note string) {
	t.Helper()
	// Synthetic volume fixture through the actual App role and real audit/FKs;
	// production producer coverage is exercised by other tests, not claimed here.
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	for start := 0; start < count; start += 250 {
		n := min(250, count-start)
		_, err := audit.Exec(ctx, f.pool, `INSERT INTO shopping.items(household_id,list_id,name,note,created_by)
 SELECT $1,$2,'Bounded volume fixture',$3,$4 FROM generate_series(1,$5::integer)`, f.identity.HouseholdID, list, note, f.identity.UserID, n)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestShoppingExtensionsSnapshotRowListAndByteBudgets(t *testing.T) {
	f := newIntegrationFixture(t)
	shoppingExtensionOffline(t, f, f.identity, true)
	ids := []string{}
	for range 11 {
		list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Budget"})
		ids = append(ids, store.String(list["id"]))
	}
	gen := f.app.Store.States()["shopping"].Generation
	if out, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, f.identity, store.ShoppingSnapshotInput{Generation: gen, ListIDs: ids[:10]}); err != nil || len(out.Lists) != 10 {
		t.Fatal("ten empty lists", err)
	}
	for _, selection := range [][]string{ids, {}, {ids[0], ids[0]}, {value.UUID()}} {
		if _, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, f.identity, store.ShoppingSnapshotInput{Generation: gen, ListIDs: selection}); err == nil {
			t.Fatal("invalid full selection accepted", selection)
		}
	}
	shoppingExtensionSeedItems(t, f, ids[0], 1000, "")
	in := store.ShoppingSnapshotInput{Generation: gen, ListIDs: ids[:1]}
	if out, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, f.identity, in); err != nil || out.Totals.Items != 1000 {
		t.Fatal("1000 complete rows", out.Totals, err)
	}
	shoppingExtensionSeedItems(t, f, ids[0], 1, "")
	if out, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, f.identity, in); err == nil || out.Complete {
		t.Fatal("1001 returned partial success", err)
	}
	// An oversized restored note must be refused before arbitrary text is read.
	shoppingExtensionSeedItems(t, f, ids[1], 1, strings.Repeat("Ä", 2<<20))
	if out, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, f.identity, store.ShoppingSnapshotInput{Generation: gen, ListIDs: ids[1:2]}); err == nil || out.Complete {
		t.Fatal("oversized notes leaked partial success", err)
	}
}

func TestShoppingExtensionsCurrentSessionRoleAndGenerationFences(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Current authority"})
	reader := shoppingTestMember(t, f, "authority-reader", "reader")
	shoppingExtensionOffline(t, f, reader, true)
	gen := f.app.Store.States()["shopping"].Generation
	if _, err := f.app.Store.ShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), gen+1); !errors.Is(err, store.ErrConflict) {
		t.Fatal("wrong generation", err)
	}
	if _, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, reader, store.ShoppingSnapshotInput{Generation: gen + 1, ListIDs: []string{store.String(list["id"])}}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old snapshot generation", err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", reader.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.ShoppingExtensionsContext(f.ctx, reader); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked session context", err)
	}
	if _, err := f.app.Store.ShoppingOfflineSnapshots(f.ctx, reader, store.ShoppingSnapshotInput{Generation: gen, ListIDs: []string{store.String(list["id"])}}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked snapshot", err)
	}
	if _, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "0", Enabled: true}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked preference writer", err)
	}
}
