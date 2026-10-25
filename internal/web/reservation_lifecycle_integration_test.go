//go:build integration

package web

import (
	"crypto/sha256"
	"errors"
	"testing"

	"household/internal/audit"
	"household/internal/maintenance"
	"household/internal/store"
	"household/internal/value"
	"household/internal/worker"
)

func reservationOtherMeal(t *testing.T, f *integrationFixture, mid string) (string, string) {
	t.Helper()
	var day string
	if err := f.pool.QueryRow(f.ctx, "SELECT planned_on::text FROM mealplan.meals WHERE id=$1", mid).Scan(&day); err != nil {
		t.Fatal(err)
	}
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Independent reservation source", "starts_on": day, "ends_on": day})
	meal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": plan["id"], "planned_on": day, "meal_slot": "lunch", "title": "Independent meal", "servings": "1"})
	return store.String(plan["id"]), store.String(meal["id"])
}

func TestReservationMultirowCapacityAndCompleteRootExtension(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	otherPlan, otherMeal := reservationOtherMeal(t, f, mid)
	pids, mids := []string{pid, otherPlan}, []string{mid, otherMeal}
	post := func(amount string) ([]string, error) {
		tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "mealplan", f.app.Store.States()["mealplan"].Generation)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback(f.ctx) }()
		rids := []string{value.UUID(), value.UUID()}
		for i := range 2 {
			key := value.UUID()
			hash := sha256.Sum256([]byte(key))
			if _, err = tx.Exec(f.ctx, reservationRootInsert, rids[i], f.identity.HouseholdID, f.app.Store.States()["mealplan"].Generation, pids[i], mids[i],
				f.app.Store.States()["pantry"].Generation, f.identity.UserID, key, hash[:], 1); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(f.ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 VALUES($1,$2,$4,$5,'kg',$6::numeric),($1,$3,$4,$5,'kg',$6::numeric)`, f.identity.HouseholdID, rids[0], rids[1], product["id"], lot["id"], amount); err != nil {
			return nil, err
		}
		return rids, tx.Commit(f.ctx)
	}
	if _, err := post("0.6"); err == nil {
		t.Fatal("one INSERT over-reserved across two source roots")
	}
	rids, err := post("0.5")
	if err != nil {
		t.Fatal("exact multirow capacity failed", err)
	}
	extra, _ := auditCreate(t, f, "pantry", "lots", store.Record{"product_id": product["id"], "location_id": lot["location_id"], "received_on": "2026-01-01", "notes": "Root extension must fail"})
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": extra["id"], "quantity_delta": "1", "reason": "receive"})
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 VALUES($1,$2,$3,$4,'kg',0.1)`, f.identity.HouseholdID, rids[0], product["id"], extra["id"]); err == nil {
		t.Fatal("complete root extended after its original commit")
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.meal_reservation_allocations").Scan(&count); err != nil || count != 2 {
		t.Fatal("late completeness failure changed committed allocations", count, err)
	}
}

func TestReservationJournalAndSourceLocks(t *testing.T) {
	f, pid, mid, _, lot := reservationFixture(t)
	lid := store.String(lot["id"])
	in := reservationReview(t, f, pid, mid, lid, "0.5")
	journal, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = journal.Rollback(f.ctx) }()
	if _, err = journal.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES($1,$2,-0.25,'consume',$3)", f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("reservation ignored an actual uncommitted stock writer", err)
	}
	if err = journal.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("post-lock stock read accepted a stale journal basis", err)
	}
	in = reservationReview(t, f, pid, mid, lid, "0.75")
	key := value.UUID()
	lease, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	changed := reservationReview(t, f, pid, mid, lid, "0.5")
	source, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Rollback(f.ctx) }()
	if _, err = source.Exec(f.ctx, "UPDATE mealplan.meals SET status='cancelled' WHERE id=$1", mid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("reservation ignored source mutation lock", err)
	}
	if err = source.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in)
	if err != nil || replay["id"] != lease["id"] || replay["state"] != "released" {
		t.Fatal("source change left or revived an active claim", replay, err)
	}
}

func TestReservationBoundedExpiryAndRealPurges(t *testing.T) {
	f, pid, mid, product, lot := reservationFixture(t)
	in := reservationReview(t, f, pid, mid, store.String(lot["id"]), "0.5")
	key := value.UUID()
	protected, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "mealplan", in.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var plans, roots []string
	if err = tx.QueryRow(f.ctx, `WITH p AS(INSERT INTO mealplan.plans(household_id,name,starts_on,ends_on,created_by)
 SELECT $1,'Expiry fixture',planned_on,planned_on,$3 FROM mealplan.meals CROSS JOIN generate_series(1,101) WHERE id=$2 RETURNING id)
 SELECT array_agg(id::text) FROM p`, f.identity.HouseholdID, mid, f.identity.UserID).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO mealplan.meals(household_id,plan_id,planned_on,title,created_by)
 SELECT household_id,id,starts_on,'Expiry fixture',$3 FROM mealplan.plans WHERE household_id=$1 AND id=ANY($2::uuid[])`, f.identity.HouseholdID, plans, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(f.ctx, `WITH r AS(INSERT INTO core.meal_reservations(household_id,mealplan_generation,plan_id,meal_id,pantry_generation,
 source_basis,created_by,request_key,request_hash,allocation_count,expires_at)
 SELECT $1,$3,plan_id,id,$4,mealplan.reservation_basis($1,id),$5,gen_random_uuid(),sha256(convert_to(id::text,'UTF8')),1,clock_timestamp()+interval '1 hour'
 FROM mealplan.meals WHERE household_id=$1 AND plan_id=ANY($2::uuid[]) RETURNING id) SELECT array_agg(id::text) FROM r`, f.identity.HouseholdID, plans, in.Generation, in.PantryGeneration, f.identity.UserID).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity)
 SELECT $1,id,$3,$4,'kg',0.000001 FROM core.meal_reservations WHERE household_id=$1 AND id=ANY($2::uuid[])`, f.identity.HouseholdID, roots, product["id"], lot["id"]); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := audit.Begin(f.ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Rollback(f.ctx) }()
	if _, err = owner.Exec(f.ctx, "ALTER TABLE core.meal_reservations DISABLE TRIGGER meal_reservation_identity"); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(f.ctx, "UPDATE core.meal_reservations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=ANY($1::uuid[])", roots); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(f.ctx, "ALTER TABLE core.meal_reservations ENABLE TRIGGER meal_reservation_identity"); err != nil {
		t.Fatal(err)
	}
	if err = owner.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.PlanningReservationLot(f.ctx, f.identity, pid, mid, store.String(lot["id"]))
	if err != nil || view["coverage"].(store.ReservationCoverage).Other != "0.000000" {
		t.Fatal("effective expiry depends on a later worker run", view, err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: f.app.Cfg, Log: f.app.Log}
	t.Cleanup(func() { _ = w.Close() })
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var expired, remaining int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FILTER(WHERE state='expired'),count(*) FILTER(WHERE state='active' AND expires_at<=clock_timestamp()) FROM core.meal_reservations WHERE id=ANY($1::uuid[])", roots).Scan(&expired, &remaining); err != nil || expired != 100 || remaining != 1 {
		t.Fatal("expiry processing was not bounded to100", expired, remaining, err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.meal_reservations WHERE id=$1 AND state='active'", protected["id"]).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("worker expired a future lease", remaining, err)
	}

	reservationPurgeModule(t, f, "mealplan")
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old source-generation replay survived real purge", err)
	}
	var proofs, allocations int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.meal_reservations),(SELECT count(*) FROM core.meal_reservation_allocations)").Scan(&proofs, &allocations); err != nil || proofs != 102 || allocations != 0 {
		t.Fatal("source purge lost minimal proofs or retained allocation payload", proofs, allocations, err)
	}
	var day string
	if err = f.pool.QueryRow(f.ctx, "SELECT ((clock_timestamp() AT TIME ZONE timezone)::date+1)::text FROM core.households WHERE id=$1", f.identity.HouseholdID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "Fresh source", "starts_on": day, "ends_on": day})
	meal, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": plan["id"], "planned_on": day, "title": "Fresh meal", "servings": "1"})
	pid, mid = store.String(plan["id"]), store.String(meal["id"])
	in = reservationReview(t, f, pid, mid, store.String(lot["id"]), "0.5")
	key = value.UUID()
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	reservationPurgeModule(t, f, "pantry")
	if _, err = f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old target-generation replay survived real purge", err)
	}
	view, err = f.app.Store.PlanningReservation(f.ctx, f.identity, pid, mid)
	if err != nil || view["reservation"].(store.Record)["details_available"] != false {
		t.Fatal("target-purged payload was still exposed", view, err)
	}
}

func reservationPurgeModule(t *testing.T, f *integrationFixture, module string) {
	t.Helper()
	state := f.app.Store.States()[module]
	if err := f.app.Store.SetModule(f.ctx, f.identity, module, false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}

	// Synthetic owner-only metadata authorizes a real disposable purge;
	// encrypted backup/restore is a separate operational acceptance.
	backup, request := value.UUID(), value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.backup_runs(id,state,started_at,verified_at,application_version,repository_reference,snapshot_reference) VALUES($1,'complete',now(),now(),'fixture','isolated-test',$2)", backup, "snapshot-"+backup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.module_purge_requests(id,household_id,module_id,expected_generation,requested_by,backup_reference) VALUES($1,$2,$3,$4,$5,$6)", request, f.identity.HouseholdID, module, state.Generation, f.identity.UserID, backup); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Purge(f.ctx, f.owner, request); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.LoadStates(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, module, true, f.app.Store.States()[module].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
}

func TestReservationComponentMetadataWorkspaceAndRecipeIndependence(t *testing.T) {
	f, pid, mid, _, lot := reservationFixture(t)
	rid := recipeSave(t, f, "", recipeInput(t, f))
	source, err := f.app.Store.PlanningSource(f.ctx, f.identity, rid)
	if err != nil {
		t.Fatal(err)
	}
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	var planVersion int64
	if err = f.pool.QueryRow(f.ctx, "SELECT version FROM mealplan.plans WHERE id=$1", pid).Scan(&planVersion); err != nil {
		t.Fatal(err)
	}
	input := store.PlanningInput{Generation: f.app.Store.States()["mealplan"].Generation, PlanVersion: planVersion, Action: "save", MealID: mid, Version: meal.Version,
		Meal: store.PlanningMealInput{Title: meal.Title, PlannedOn: meal.PlannedOn, Slot: meal.Slot, Kind: "recipe", Status: "planned", Servings: "1"}}
	for range 2 {
		input.Components = append(input.Components, store.PlanningComponentInput{SourceVersionID: store.String(source["source_version_id"]), SourceGeneration: store.Int(source["source_generation"]), Servings: "1"})
	}
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), input); err != nil {
		t.Fatal(err)
	}
	reserve := func(amount string) store.Record {
		t.Helper()
		in := reservationReview(t, f, pid, mid, store.String(lot["id"]), amount)
		result, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, mid, value.UUID(), value.UUID(), in)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	check := func(root store.Record, expected string) {
		t.Helper()
		var state string
		if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.meal_reservations WHERE id=$1", root["id"]).Scan(&state); err != nil || state != expected {
			t.Fatal("source release boundary", state, expected, err)
		}
	}
	lease := reserve("0.5")
	var auditSafe bool
	if err = f.pool.QueryRow(f.ctx, `SELECT actor_user_id=$2::uuid AND changes->'source_basis'->>'after'='***REDACTED***'
 AND changes->'request_hash'->>'after'='***REDACTED***' AND NOT(changes ? 'quantity')
 FROM core.audit_log WHERE resource_id=$1 AND action='CREATE'`, lease["id"], f.identity.UserID).Scan(&auditSafe); err != nil || !auditSafe {
		t.Fatal("reservation audit actor or privacy policy", auditSafe, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*)=1 AND bool_and(a.actor_user_id=$2::uuid AND a.changes->'quantity'->>'after'='***REDACTED***')
 FROM core.audit_log a JOIN core.meal_reservation_allocations l ON l.id=a.resource_id WHERE l.reservation_id=$1 AND a.action='CREATE'`, lease["id"], f.identity.UserID).Scan(&auditSafe); err != nil || !auditSafe {
		t.Fatal("allocation amount was not redacted under the actual actor", auditSafe, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.components SET prep_minutes=0,cook_minutes=0,position=1-position WHERE meal_id=$1", mid); err != nil {
		t.Fatal(err)
	}
	check(lease, "active")
	var basisMatches bool
	if err = f.pool.QueryRow(f.ctx, "SELECT source_basis=mealplan.reservation_basis(household_id,meal_id) FROM core.meal_reservations WHERE id=$1", lease["id"]).Scan(&basisMatches); err != nil || !basisMatches {
		t.Fatal("effort or ordering changed the semantic source basis", basisMatches, err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE mealplan.components SET servings=2 WHERE meal_id=$1 AND position=0", mid); err != nil {
		t.Fatal(err)
	}
	check(lease, "released")
	lease = reserve("0.5")
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT version FROM mealplan.plans WHERE id=$1", pid).Scan(&planVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), store.PlanningInput{Generation: input.Generation, PlanVersion: planVersion,
		Action: "move", MealID: mid, Version: meal.Version, TargetDate: meal.PlannedOn, TargetSlot: "lunch"}); err != nil {
		t.Fatal(err)
	}
	check(lease, "released")
	lease = reserve("0.5")
	other, _ := auditCreate(t, f, "mealplan", "meals", store.Record{"plan_id": pid, "planned_on": meal.PlannedOn, "meal_slot": "dinner", "title": "Swap target", "servings": "1"})
	otherInput := reservationReview(t, f, pid, store.String(other["id"]), store.String(lot["id"]), "0.25")
	otherLease, err := f.app.Store.ReservePlanningStock(f.ctx, f.identity, pid, store.String(other["id"]), value.UUID(), value.UUID(), otherInput)
	if err != nil {
		t.Fatal(err)
	}
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT version FROM mealplan.plans WHERE id=$1", pid).Scan(&planVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), store.PlanningInput{Generation: input.Generation, PlanVersion: planVersion,
		Action: "swap", MealID: mid, Version: meal.Version, OtherID: store.String(other["id"]), OtherVersion: store.Int(other["version"])}); err != nil {
		t.Fatal(err)
	}
	check(lease, "released")
	check(otherLease, "released")
	lease = reserve("0.5")
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM mealplan.components WHERE meal_id=$1 AND position=0", mid); err != nil {
		t.Fatal(err)
	}
	check(lease, "released")
	lease = reserve("0.5")
	reservationPurgeModule(t, f, "recipes")
	check(lease, "active")
	lease = reserve("0.4")
	meal, err = f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.app.Store.Module("mealplan")
	e, _ := m.Entity("meals")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, mid, meal.Version, input.Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	check(lease, "released")
}
