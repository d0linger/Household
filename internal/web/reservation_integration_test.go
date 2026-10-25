//go:build integration

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func reservationReview(t *testing.T, f *integrationFixture, pid, mid, lid, quantity string) store.ReservationInput {
	t.Helper()
	view, err := f.app.Store.PlanningReservation(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	source := view["source"].(store.Record)
	state, err := f.app.Store.PlanningReservationLot(f.ctx, f.identity, pid, mid, lid)
	if err != nil {
		t.Fatal(err)
	}
	lot, coverage := state["lot"].(store.Record), state["coverage"].(store.ReservationCoverage)
	in := store.ReservationInput{Generation: store.Int(source["generation"]), PantryGeneration: store.Int(source["pantry_generation"]),
		SourceBasis: store.String(source["source_basis"]), ExpiresAt: store.String(source["suggested_expiry"]),
		Allocations: []store.ReservationAllocation{{LotID: lid, ProductID: store.String(lot["product_id"]), ProductVersion: store.Int(lot["product_version"]),
			LotVersion: store.Int(lot["version"]), LocationVersion: store.Int(lot["location_version"]), MovementCount: store.Int(lot["movement_count"]),
			Balance: coverage.Physical, OtherReserved: coverage.Other, Quantity: quantity, Unit: store.String(lot["unit"])}}}
	if view["reservation"] != nil {
		current := view["reservation"].(store.Record)
		in.PreviousID, in.PreviousVersion = store.String(current["id"]), store.Int(current["version"])
	}
	return in
}

func reservationFixture(t *testing.T) (*integrationFixture, string, string, store.Record, store.Record) {
	t.Helper()
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["mealplan"]
	if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	var day string
	if err := f.pool.QueryRow(f.ctx, "SELECT ((clock_timestamp() AT TIME ZONE timezone)::date+1)::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Reserved household meal", "starts_on": day, "ends_on": day})
	meal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": plan["id"], "planned_on": day, "meal_slot": "dinner", "title": "Private meal title", "kind": "free", "status": "planned", "servings": "1"})
	product, lot := pantryFixture(t, f, "reservation")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lot["id"], "quantity_delta": "1", "reason": "receive"})
	return f, store.String(plan["id"]), store.String(meal["id"]), product, lot
}

const reservationRootInsert = `INSERT INTO core.meal_reservations(id,household_id,mealplan_generation,plan_id,meal_id,pantry_generation,
 source_basis,created_by,request_key,request_hash,allocation_count,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,mealplan.reservation_basis($2,$5),$7,$8,$9,$10,clock_timestamp()+interval '1 hour')`

func reserveFixtureLot(t *testing.T, f *integrationFixture, pid, mid string, product, lot store.Record, amount string) (string, error) {
	t.Helper()
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "mealplan", f.app.Store.States()["mealplan"].Generation)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	rid, key := value.UUID(), value.UUID()
	hash := sha256.Sum256([]byte(key))
	if _, err = tx.Exec(f.ctx, reservationRootInsert, rid, f.identity.HouseholdID, f.app.Store.States()["mealplan"].Generation, pid, mid,
		f.app.Store.States()["pantry"].Generation, f.identity.UserID, key, hash[:], 1); err != nil {
		return "", err
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 VALUES($1,$2,$3,$4,$5,$6::numeric)`, f.identity.HouseholdID, rid, product["id"], lot["id"], product["unit_code"], amount); err != nil {
		return "", err
	}
	return rid, tx.Commit(f.ctx)
}

func TestReservationSchemaCompletenessAndPrivileges(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"NaN", "0", "-1", "1.000001"} {
		if _, err := reserveFixtureLot(t, f, pid, mid, product, lot, amount); err == nil {
			t.Fatal("invalid or over-reserved allocation accepted", amount)
		}
	}
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "mealplan", f.app.Store.States()["mealplan"].Generation)
	if err != nil {
		t.Fatal(err)
	}
	key := value.UUID()
	hash := sha256.Sum256([]byte(key))
	if _, err = tx.Exec(f.ctx, reservationRootInsert, value.UUID(), f.identity.HouseholdID, f.app.Store.States()["mealplan"].Generation, pid, mid,
		f.app.Store.States()["pantry"].Generation, f.identity.UserID, key, hash[:], 1); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("proof-only reservation committed")
	}
	rid, err := reserveFixtureLot(t, f, pid, mid, product, lot, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE core.meal_reservations SET expires_at=expires_at+interval '1 hour' WHERE id=$1",
		"DELETE FROM core.meal_reservations WHERE id=$1",
		"UPDATE core.meal_reservation_allocations SET quantity=0.1 WHERE reservation_id=$1",
		"DELETE FROM core.meal_reservation_allocations WHERE reservation_id=$1",
	} {
		if _, err = f.pool.Exec(f.ctx, query, rid); err == nil {
			t.Fatal("runtime changed reservation proof or quantities", query)
		}
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE core.meal_reservations SET state='released',release_reason='manual' WHERE id=$1", rid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE core.meal_reservations SET state='active',release_reason='',closed_at=NULL WHERE id=$1", rid); err == nil {
		t.Fatal("terminal reservation reopened")
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.meals DISABLE TRIGGER planning_reservation_meal"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("missing source release guard accepted")
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE mealplan.meals ENABLE TRIGGER planning_reservation_meal"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER INDEX core.meal_reservation_active RENAME TO reservation_damaged_index"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err == nil {
		t.Fatal("missing active-lease uniqueness index accepted")
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER INDEX core.reservation_damaged_index RENAME TO meal_reservation_active"); err != nil {
		t.Fatal(err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestReservationSourceAndModuleRelease(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	rid, err := reserveFixtureLot(t, f, pid, mid, product, lot, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	checkState := func(want string) {
		t.Helper()
		var state string
		if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.meal_reservations WHERE id=$1", rid).Scan(&state); err != nil || state != want {
			t.Fatal("reservation lifecycle", state, want, err)
		}
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET title='Retitled',notes='New private note' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	checkState("active")
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET status='cancelled' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	checkState("released")
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.meals SET status='planned' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	checkState("released")
	rid, err = reserveFixtureLot(t, f, pid, mid, product, lot, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.plans SET is_template=true WHERE id=$1", pid); err != nil {
		t.Fatal(err)
	}
	checkState("released")
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.plans SET is_template=false WHERE id=$1", pid); err != nil {
		t.Fatal(err)
	}
	rid, err = reserveFixtureLot(t, f, pid, mid, product, lot, "0.5")
	if err != nil {
		t.Fatal(err)
	}
	state := f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	checkState("released")
	state = f.app.Store.States()["pantry"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "pantry", true, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	checkState("released")
	var quantity string
	if err = f.pool.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE lot_id=$1", lot["id"]).Scan(&quantity); err != nil || quantity != "1.000000" {
		t.Fatal("soft reservation changed physical journal", quantity, err)
	}
}

func TestReservationReviewedReplayReplaceRollbackAndHTTP(t *testing.T) {
	f, pid, mid, _, lot := reservationFixture(t)
	lid := store.String(lot["id"])
	in := reservationReview(t, f, pid, mid, lid, "0.5")
	path := "/api/v1/planning/" + pid + "/meals/" + mid + "/reservations"
	response, _, err := f.request(http.MethodGet, path, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("anonymous reservation preview", response, err)
	}
	reader := shoppingTestMember(t, f, "reservation-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, "reservation-reader", integrationPassword, "", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	response, page, err := f.request(http.MethodGet, "/m/mealplan/reservations/"+pid+"/"+mid, readerToken, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || bytes.Contains(page, []byte(`id="reservation-editor-fields"`)) {
		t.Fatal("reader reservation page exposes mutations", response, err)
	}
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, reader, pid, mid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader reservation accepted", err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PlanningReservation(f.ctx, foreign, pid, mid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign reservation source exposed", err)
	}
	payload, _ := json.Marshal(in)
	response, _, err = f.request(http.MethodPost, path, f.token, "invalid", "application/json", bytes.NewReader(payload), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("reservation bypassed CSRF", response, err)
	}
	key := value.UUID()
	response, raw, err := f.request(http.MethodPost, path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(payload), map[string]string{"Idempotency-Key": key})
	var first store.Record
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &first) != nil || first["state"] != "active" {
		t.Fatal("actual reviewed reservation HTTP", response, string(raw), err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in)
	if err != nil || replay["id"] != first["id"] {
		t.Fatal("durable same-key reservation replay", replay, err)
	}
	changed := in
	changed.Allocations = append([]store.ReservationAllocation(nil), in.Allocations...)
	changed.Allocations[0].Quantity = "0.6"
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same-key different request accepted", err)
	}
	// Model an earlier root created by a host whose wall clock was ahead.
	// Ordering must use its source-serialized revision, including terminal history.
	clockFixture, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clockFixture.Rollback(f.ctx) }()
	if _, err = clockFixture.Exec(f.ctx, "ALTER TABLE core.meal_reservations DISABLE TRIGGER meal_reservation_identity"); err != nil {
		t.Fatal(err)
	}
	if _, err = clockFixture.Exec(f.ctx, "UPDATE core.meal_reservations SET created_at=clock_timestamp()+interval '7 days' WHERE id=$1", first["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = clockFixture.Exec(f.ctx, "ALTER TABLE core.meal_reservations ENABLE TRIGGER meal_reservation_identity"); err != nil {
		t.Fatal(err)
	}
	if err = clockFixture.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	replace := reservationReview(t, f, pid, mid, lid, "0.75")
	second, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), replace)
	if err != nil || second["id"] == first["id"] {
		t.Fatal("own reservation was subtracted twice", second, err)
	}
	view, err := f.app.Store.PlanningReservation(f.ctx, f.identity, pid, mid)
	if err != nil || view["reservation"].(store.Record)["id"] != second["id"] || store.Int(second["revision"]) != 2 {
		t.Fatal("wall-clock order hid the newer active lease", view, err)
	}
	replay, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in)
	if err != nil || replay["state"] != "released" || replay["id"] != first["id"] {
		t.Fatal("old replay reactivated its replaced lease", replay, err)
	}
	stale := reservationReview(t, f, pid, mid, lid, "0.7")
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "quantity_delta": "0.1", "reason": "receive"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "quantity_delta": "-0.1", "reason": "consume"})
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), stale); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same-balance journal change accepted", err)
	}
	late := reservationReview(t, f, pid, mid, lid, "0.7")
	lateKey := value.UUID()
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.meal_reservation_allocations ADD CONSTRAINT reservation_test_late CHECK(quantity<>0.7) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, lateKey, value.UUID(), late); err == nil {
		t.Fatal("late allocation failure did not occur")
	}
	var state string
	if err = f.pool.QueryRow(f.ctx, "SELECT state FROM core.meal_reservations WHERE id=$1", second["id"]).Scan(&state); err != nil || state != "active" {
		t.Fatal("failed replacement released existing lease", state, err)
	}
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE core.meal_reservation_allocations DROP CONSTRAINT reservation_test_late"); err != nil {
		t.Fatal(err)
	}
	third, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, lateKey, value.UUID(), late)
	if err != nil {
		t.Fatal("clean retry after full rollback", err)
	}
	for range 2 {
		released, releaseErr := f.app.Store.ReleasePlanningStock(f.ctx, f.identity, pid, mid, store.String(third["id"]), value.UUID(), in.Generation, store.Int(third["version"]))
		if releaseErr != nil || released["state"] != "released" {
			t.Fatal("exact lease release was not repeatable", released, releaseErr)
		}
	}
	view, err = f.app.Store.PlanningReservation(f.ctx, f.identity, pid, mid)
	if err != nil || view["reservation"].(store.Record)["id"] != third["id"] || store.Int(third["revision"]) != 3 {
		t.Fatal("wall-clock order hid the newer terminal lease", view, err)
	}
}

func TestReservationConcurrentPlansAndRealConsumption(t *testing.T) {
	f, pid, mid, _, lot := reservationFixture(t)
	var day string
	if err := f.pool.QueryRow(f.ctx, "SELECT planned_on::text FROM mealplan.meals WHERE id=$1", mid).Scan(&day); err != nil {
		t.Fatal(err)
	}
	otherPlan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Second competing plan", "starts_on": day, "ends_on": day})
	otherMeal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": otherPlan["id"], "planned_on": day, "meal_slot": "lunch", "title": "Competing meal", "servings": "1"})
	lid := store.String(lot["id"])
	pids, mids := []string{pid, store.String(otherPlan["id"])}, []string{mid, store.String(otherMeal["id"])}
	inputs := []store.ReservationInput{reservationReview(t, f, pids[0], mids[0], lid, "0.6"), reservationReview(t, f, pids[1], mids[1], lid, "0.6")}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			_, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pids[i], mids[i], value.UUID(), value.UUID(), inputs[i])
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal("unexpected concurrent reservation failure", err)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent plans over-reserved or both failed", successes)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "quantity_delta": "-0.75", "reason": "consume"})
	var winner string
	if err := f.pool.QueryRow(f.ctx, "SELECT meal_id FROM core.meal_reservations WHERE state='active'").Scan(&winner); err != nil {
		t.Fatal(err)
	}
	index := 0
	if winner == mids[1] {
		index = 1
	}
	view, err := f.app.Store.PlanningReservationLot(f.ctx, f.identity, pids[index], winner, lid)
	if err != nil {
		t.Fatal(err)
	}
	coverage := view["coverage"].(store.ReservationCoverage)
	if coverage.Physical != "0.250000" || coverage.Reserved != "0.600000" || coverage.Free != "0.000000" || coverage.Undercovered != "0.350000" || coverage.OwnCovered != "0.250000" {
		t.Fatal("physical truth or soft-claim undercoverage lost", coverage)
	}
}

func TestReservationPantryProjectionKeepsLotScopeAndHistory(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	lid := store.String(lot["id"])
	in := reservationReview(t, f, pid, mid, lid, "0.6")
	if _, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "quantity_delta": "-0.75", "reason": "consume"})
	for _, setup := range []struct {
		name, quantity string
		archived       bool
	}{{"Active surplus", "2", false}, {"Archived surplus", "3", true}} {
		loc, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": setup.name})
		extra, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": loc["id"], "received_on": "2026-01-01", "notes": setup.name})
		auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": extra["id"], "quantity_delta": setup.quantity, "reason": "receive"})
		if setup.archived {
			if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.locations SET archived_at=clock_timestamp() WHERE id=$1", loc["id"]); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(suffix string) store.Record {
		t.Helper()
		response, raw, err := f.request(http.MethodGet, "/api/v1/pantry/stock?q="+url.QueryEscape(store.String(product["name"]))+suffix, f.token, "", "", nil, nil)
		var result struct {
			Products []store.Record `json:"products"`
		}
		if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || len(result.Products) != 1 {
			t.Fatal("pantry reservation projection", response, string(raw), err)
		}
		return result.Products[0]
	}
	current := read("")
	if current["stock"] != "5.250000" || current["reserved"] != "0.600000" || current["physical_free"] != "5.000000" || current["free"] != "2.000000" || current["undercovered"] != "0.350000" {
		t.Fatal("other lots or archived locations silently covered a concrete claim", current)
	}
	var today string
	if err := f.pool.QueryRow(f.ctx, "SELECT (clock_timestamp() AT TIME ZONE timezone)::date::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&today); err != nil {
		t.Fatal(err)
	}
	historical := read("&at=" + today)
	for _, field := range []string{"reserved", "free", "physical_free", "undercovered"} {
		if historical[field] != nil {
			t.Fatal("current claims were presented as historical stock", field, historical)
		}
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE pantry.products SET archived_at=clock_timestamp() WHERE id=$1", product["id"]); err != nil {
		t.Fatal(err)
	}
	if archived := read(""); store.String(archived["free"]) != "0" && store.String(archived["free"]) != "0.000000" {
		t.Fatal("archived product remained eligible for new claims", archived)
	}
}
