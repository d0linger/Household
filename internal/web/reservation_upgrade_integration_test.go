//go:build integration

package web

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReservationPopulatedPriorUpgrade(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := resetIntegrationSchemas(f.ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	for _, schema := range database.Schemas {
		version := uint(database.ExpectedVersion(schema))
		switch schema {
		case "core":
			version = 18
		case "pantry":
			version = 6
		case "mealplan":
			version = 2
		}
		migratePriorAuditSchema(t, f, schema, version)
	}
	hh, user, product, location, lot, plan, meal := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
	tx, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO core.households(id,name) VALUES($1,'Prior reservation release')", []any{hh}},
		{"INSERT INTO core.users(id,username,display_name,password_hash) VALUES($1,'prior-reservation','Prior member','fixture-no-login')", []any{user}},
		{"INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", []any{hh, user}},
		{"INSERT INTO pantry.products(id,household_id,name,unit_code,low_stock_threshold,target_stock,created_by) VALUES($1,$2,'Prior target','kg',1,3,$3)", []any{product, hh, user}},
		{"INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES($1,$2,'Prior shelf',$3)", []any{location, hh, user}},
		{"INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by) VALUES($1,$2,$3,$4,'2026-10-25','Prior lot',$5)", []any{lot, hh, product, location, user}},
		{"INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,0.750001,'receive',$3)", []any{hh, lot, user}},
		{"INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES($1,$2,'Prior meal plan','2026-10-25','2026-10-31',$3)", []any{plan, hh, user}},
		{"INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,notes,created_by) VALUES($1,$2,$3,'2026-10-26','Prior meal','Preserve private source text',$4)", []any{meal, hh, plan, user}},
	} {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var stock, notes string
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1),
 (SELECT notes FROM mealplan.meals WHERE id=$2),(SELECT count(*) FROM core.meal_reservations)+(SELECT count(*) FROM core.meal_reservation_allocations)`, lot, meal).Scan(&stock, &notes, &count); err != nil || stock != "0.750001" || notes != "Preserve private source text" || count != 0 {
		t.Fatal("upgrade changed prior stock/source or synthesized claims", stock, notes, count, err)
	}
}

func TestReservationDirectModuleChangeCannotInvertHouseholdLock(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	rid, err := reserveFixtureLot(t, f, pid, mid, product, lot, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(f.ctx) }()
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	_, err = f.pool.Exec(ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID)
	cancel()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55P03" {
		t.Fatal("direct module update waited in inverse lock order", err)
	}
	var state string
	if err = f.pool.QueryRow(f.ctx, "SELECT state FROM core.meal_reservations WHERE id=$1", rid).Scan(&state); err != nil || state != "active" {
		t.Fatal("failed module update partially released a lease", state, err)
	}
	if err = blocker.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT state FROM core.meal_reservations WHERE id=$1", rid).Scan(&state); err != nil || state != "released" {
		t.Fatal("direct disable/reenable revived a claim", state, err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningReservation(f.ctx, foreign, pid, mid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("module lifecycle exposed another household", err)
	}
}

func TestReservationMutationIsolationIsExplicit(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
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
	var inherited string
	if err = pool.QueryRow(f.ctx, "SHOW default_transaction_isolation").Scan(&inherited); err != nil || inherited != "repeatable read" {
		t.Fatal("isolation fixture did not install the alternative default", inherited, err)
	}
	isolated := store.New(pool, f.app.Store.Modules)
	in := reservationReview(t, f, pid, mid, store.String(lot["id"]), "0.5")
	if _, err = isolated.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("application inherited an unsafe connection isolation default", err)
	}
	otherPlan, otherMeal := reservationOtherMeal(t, f, mid)
	tx, err := pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	key, root := value.UUID(), value.UUID()
	hash := sha256.Sum256([]byte(key))
	if _, err = tx.Exec(f.ctx, reservationRootInsert, root, f.identity.HouseholdID, in.Generation, otherPlan, otherMeal, in.PantryGeneration, f.identity.UserID, key, hash[:], 1); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 VALUES($1,$2,$3,$4,'kg',0.2)`, f.identity.HouseholdID, root, product["id"], lot["id"])
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "25000" {
		t.Fatal("direct repeatable-read allocation bypassed the fresh-snapshot contract", err)
	}
}
