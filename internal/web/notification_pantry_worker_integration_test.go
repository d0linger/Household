//go:build integration

package web

import (
	"fmt"
	"sort"
	"testing"

	"household/internal/store"
	"household/internal/worker"
)

func TestNotificationPantryWorkerCursorAndInAppEpisodes(t *testing.T) {
	f := newIntegrationFixture(t)
	const total = 55
	products := make([]string, 0, total)
	for n := range total {
		p, _ := auditCreate(t, f, "pantry", "products", store.Record{
			"name": fmt.Sprintf("Cursor product %02d", n), "unit_code": "kg", "low_stock_threshold": "2",
		})
		products = append(products, store.String(p["id"]))
	}
	sort.Strings(products)
	lastProduct := products[len(products)-1]
	location, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Cursor stock shelf"})
	lot, _ := auditCreate(t, f, "pantry", "lots", store.Record{
		"product_id": lastProduct, "location_id": location["id"], "received_on": "2026-01-01", "notes": "Cursor stock batch",
	})
	background := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	t.Cleanup(func() { _ = background.Close() })
	tick := func() {
		t.Helper()
		if err := background.Tick(f.ctx); err != nil {
			t.Fatal("actual worker tick", err)
		}
	}
	checkCount := func(want int) {
		t.Helper()
		var got int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notifications
 WHERE household_id=$1 AND user_id=$2 AND module_id='pantry' AND kind='low_stock'`,
			f.identity.HouseholdID, f.identity.UserID).Scan(&got); err != nil || got != want {
			t.Fatalf("in-app low-stock notices: got=%d want=%d err=%v", got, want, err)
		}
	}
	checkCursor := func(want string) {
		t.Helper()
		var after, state string
		var attempts int
		if err := f.pool.QueryRow(f.ctx, `SELECT payload->>'after',state,attempts FROM core.jobs
 WHERE household_id=$1 AND kind='pantry_low_stock_scan_v1'`, f.identity.HouseholdID).
			Scan(&after, &state, &attempts); err != nil || after != want || state != "pending" || attempts != 0 {
			t.Fatalf("durable cursor or generic-dispatch exclusion: after=%s state=%s attempts=%d err=%v", after, state, attempts, err)
		}
	}
	checkEpisode := func(active bool, episode int64) {
		t.Helper()
		var gotActive bool
		var gotEpisode int64
		if err := f.pool.QueryRow(f.ctx, `SELECT active,episode FROM core.alert_states
 WHERE household_id=$1 AND module_id='pantry' AND resource_id=$2 AND kind='low_stock'`,
			f.identity.HouseholdID, lastProduct).Scan(&gotActive, &gotEpisode); err != nil || gotActive != active || gotEpisode != episode {
			t.Fatalf("last-page stock episode: active=%t episode=%d err=%v", gotActive, gotEpisode, err)
		}
	}

	// Initial shortage still creates an in-app notice. The new external
	// channel's silent baseline must not suppress this established behavior.
	tick()
	checkCount(50)
	checkCursor(products[49])
	var firstPageRows int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.alert_states
 WHERE household_id=$1 AND module_id='pantry' AND kind='low_stock'`, f.identity.HouseholdID).
		Scan(&firstPageRows); err != nil || firstPageRows != 50 {
		t.Fatal("first tick exceeded its stock observation page", firstPageRows, err)
	}
	// A fresh worker has no in-memory continuation and must resume from SQL.
	if err := background.Close(); err != nil {
		t.Fatal(err)
	}
	background = &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	tick()
	checkCount(total)
	checkCursor("")
	checkEpisode(true, 1)

	// Physical stock receipt clears the episode only when its later page is
	// visited. Repeated low products in the earlier page do not alert again.
	auditCreate(t, f, "pantry", "stock_movements", store.Record{
		"lot_id": lot["id"], "reason": "receive", "quantity_delta": "3",
	})
	tick()
	checkCount(total)
	checkEpisode(true, 1)
	checkCursor(products[49])
	tick()
	checkCount(total)
	checkEpisode(false, 1)
	checkCursor("")

	auditCreate(t, f, "pantry", "stock_movements", store.Record{
		"lot_id": lot["id"], "reason": "consume", "quantity_delta": "-2",
	})
	tick()
	checkCount(total)
	tick()
	checkCount(total + 1)
	checkEpisode(true, 2)
	tick()
	tick()
	checkCount(total + 1)
	checkEpisode(true, 2)

	var external int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents
 WHERE household_id=$1 AND category='pantry'`, f.identity.HouseholdID).Scan(&external); err != nil || external != 0 {
		t.Fatal("in-app compatibility implicitly enrolled a device recipient", external, err)
	}
}
