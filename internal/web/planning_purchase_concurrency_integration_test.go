//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlanningPurchaseSyncOneConnectionExplicitIsolationAndLateMultiItemRollback(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	var lotID string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lotID); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "quantity_delta": "-1", "reason": "consume"})
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Genuine multi-item rollback"})
	config, err := pgxpool.ParseConfig(os.Getenv("HOUSEHOLD_TEST_APP_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	pool, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	isolated := store.New(pool, f.app.Store.Modules)
	var inherited string
	if err = pool.QueryRow(f.ctx, "SHOW default_transaction_isolation").Scan(&inherited); err != nil || inherited != "repeatable read" {
		t.Fatal("alternate isolation fixture not installed", inherited, err)
	}
	in, view := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	if !view.Ready || len(view.Lines) != 2 || view.Lines[0].Action != "create" || view.Lines[1].Action != "create" {
		t.Fatal("fixture must produce two actual item creates", view)
	}
	var before int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.outbox_events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION core.reject_planning_sync_fixture() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
 BEGIN RAISE EXCEPTION 'forced planning sync late failure' USING ERRCODE='23514';END $$;
 CREATE CONSTRAINT TRIGGER zz_reject_planning_sync_fixture AFTER INSERT ON core.planning_purchase_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.reject_planning_sync_fixture()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.owner.Exec(f.ctx, "DROP TRIGGER IF EXISTS zz_reject_planning_sync_fixture ON core.planning_purchase_operations;DROP FUNCTION IF EXISTS core.reject_planning_sync_fixture()")
	}()
	key := value.UUID()
	_, err = isolated.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.Message != "forced planning sync late failure" {
		t.Fatal("actual deferred late failure not reached with one connection", err)
	}
	var items, requirements, proofs, receipts, outbox int
	err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM shopping.items),(SELECT count(*) FROM core.planning_purchase_requirements),
 (SELECT count(*) FROM core.planning_purchase_operations),(SELECT count(*) FROM shopping.planning_purchase_receipts),(SELECT count(*) FROM core.outbox_events)`).Scan(&items, &requirements, &proofs, &receipts, &outbox)
	if err != nil || items != 0 || requirements != 0 || proofs != 0 || receipts != 0 || outbox != before {
		t.Fatal("late failure retained partial item/requirement/evidence/projection", items, requirements, proofs, receipts, outbox, before, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DROP TRIGGER zz_reject_planning_sync_fixture ON core.planning_purchase_operations;DROP FUNCTION core.reject_planning_sync_fixture()"); err != nil {
		t.Fatal(err)
	}
	result, err := isolated.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal("failed request did not leave same key reusable / inherited unsafe isolation", err)
	}
	assertPurchaseCounts(t, result, 2, 2, 0, 0)
	if _, err = isolated.PlanningPurchases(f.ctx, f.identity, nil); err != nil {
		t.Fatal("one connection read leaked or opened nested pool connection", err)
	}
}

func TestPlanningPurchaseSyncContendedCoverageDependenciesFailWithoutEffects(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	var lotID, locationID string
	if err := f.pool.QueryRow(f.ctx, "SELECT id,location_id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lotID, &locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveFixtureLot(t, f, pid, mid, product, store.Record{"id": lotID}, "0.2"); err != nil {
		t.Fatal(err)
	}
	var reservationID string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM core.meal_reservations WHERE state='active'").Scan(&reservationID); err != nil {
		t.Fatal(err)
	}
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Contended scope destination"})
	in, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	for _, tc := range []struct{ name, query, id string }{
		{"location archive writer", "SELECT id FROM pantry.locations WHERE id=$1 FOR UPDATE", locationID},
		{"reservation release writer", "SELECT id FROM core.meal_reservations WHERE id=$1 FOR UPDATE", reservationID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(f.ctx) }()
			if _, err = tx.Exec(f.ctx, tc.query, tc.id); err != nil {
				t.Fatal(err)
			}
			_, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in)
			var conflict store.PlanningConflict
			if !errors.As(err, &conflict) && !errors.Is(err, store.ErrConflict) {
				t.Fatal("contested dependency did not fail as review conflict", err)
			}
		})
	}
	var effects int
	if err := f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.planning_purchase_operations)+(SELECT count(*) FROM shopping.items)").Scan(&effects); err != nil || effects != 0 {
		t.Fatal("contested dependency left partial effects", effects, err)
	}
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("unchanged reviewed scope did not recover after lock release", err)
	}
}

func TestPlanningPurchaseOppositeOrderContentionLeavesNoPartialCheckoutOrSync(t *testing.T) {
	f, ids, _, _, _, listID, checkout := planningCheckoutFixture(t, "skip", "", false)
	in, view := reviewedPurchaseInput(t, f, nil, "")
	if !view.Ready {
		t.Fatal("fixture must allow an unchanged planning sync", view)
	}
	checkoutKey, syncKey := value.UUID(), value.UUID()
	for _, tc := range []struct {
		name, query, id, path, key string
		input                      any
	}{
		{"checkout versus planning requirement lock", "SELECT id FROM core.planning_purchase_requirements WHERE id=$1 FOR UPDATE", ids[0],
			"/api/v1/shopping/lists/" + listID + "/checkout", checkoutKey, checkout},
		{"sync versus checkout list lock", "SELECT id FROM shopping.lists WHERE id=$1 FOR UPDATE", listID,
			"/api/v1/planning-purchases/sync", syncKey, in},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(f.ctx) }()
			if _, err = tx.Exec(f.ctx, tc.query, tc.id); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			response, raw, err := f.request(http.MethodPost, tc.path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": tc.key})
			if err != nil || response.StatusCode != http.StatusConflict {
				t.Fatal("opposite writer resource must return an authenticated HTTP review conflict", string(raw), err)
			}
		})
	}
	var checkouts, failedProofs int
	var open, managed bool
	err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM shopping.checkouts),
 (SELECT count(*) FROM core.planning_purchase_operations WHERE request_key=ANY($1::uuid[])),
 (SELECT archived_at IS NULL FROM shopping.lists WHERE id=$2),
 (SELECT active AND pending_quantity=0.2 AND procurement_disposition='managed' FROM core.planning_purchase_requirements WHERE id=$3)`,
		[]string{checkoutKey, syncKey}, listID, ids[0]).Scan(&checkouts, &failedProofs, &open, &managed)
	if err != nil || checkouts != 0 || failedProofs != 0 || !open || !managed {
		t.Fatal("contention left a partial checkout, closed list or planning proof", checkouts, failedProofs, open, managed, err)
	}
	if _, err = f.app.Store.CheckoutShopping(f.ctx, f.identity, listID, checkoutKey, value.UUID(), checkout); err != nil {
		t.Fatal("unchanged checkout did not recover after planning lock release", err)
	}
}
