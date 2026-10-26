//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func participantSaveInput(t *testing.T, f *integrationFixture, pid, mid string) store.PlanningInput {
	t.Helper()
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	in := planningInput(t, f, pid)
	in.Action, in.MealID, in.Version = "save", mid, meal.Version
	in.Meal = store.PlanningMealInput{Title: meal.Title, PlannedOn: meal.PlannedOn, Slot: meal.Slot, Kind: meal.Kind, Status: meal.Status, Servings: meal.Servings, Notes: meal.Notes}
	for _, c := range meal.Components {
		in.Components = append(in.Components, store.PlanningComponentInput{ID: c.ID, Version: c.Version, Servings: c.Servings, PrepMinutes: c.PrepMinutes, CookMinutes: c.CookMinutes})
	}
	return in
}

func participantSelf(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var member string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM core.memberships WHERE household_id=$1 AND user_id=$2", f.identity.HouseholdID, f.identity.UserID).Scan(&member); err != nil {
		t.Fatal(err)
	}
	return member
}

func participantCurrent(t *testing.T, f *integrationFixture, pid, mid string) store.PlanningMeal {
	t.Helper()
	meal, err := f.app.Store.PlanningMeal(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	return meal
}

func participantDisable(t *testing.T, f *integrationFixture, id auth.Identity) {
	t.Helper()
	csrf := f.app.Auth.CSRF(f.token, "session")
	body := url.Values{"role": {id.Role}, "disabled": {"true"}, "csrf": {csrf}}
	response, raw, err := f.request(http.MethodPost, "/settings/users/"+id.UserID, f.token, csrf, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()), nil)
	if err != nil || response.StatusCode != http.StatusSeeOther {
		t.Fatal("actual administrative member deactivation", string(raw), err)
	}
}

func TestPlanningParticipantsDerivedDemandReservationAndProcurementReview(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Participant source review destination"})
	purchase, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	if _, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), purchase); err != nil {
		t.Fatal(err)
	}
	var lotID string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM pantry.lots WHERE product_id=$1", product["id"]).Scan(&lotID); err != nil {
		t.Fatal(err)
	}
	reservation, err := reserveFixtureLot(t, f, pid, mid, product, store.Record{"id": lotID}, "0.2")
	if err != nil {
		t.Fatal(err)
	}
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: []string{strings.ToUpper(participantSelf(t, f))}, GuestCount: 1}
	in.Meal.Servings = "2"
	in.Components[0].ServingsMode = "meal"
	key := value.UUID()
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in); err != nil {
		t.Fatal("derived save", err)
	}
	meal := participantCurrent(t, f, pid, mid)
	if meal.Servings != "2.000000" || meal.DerivedServings != "2" || meal.ServingsMode != "participants" || meal.GuestCount != 1 || len(meal.Participants) != 1 || meal.Components[0].ID != cid || meal.Components[0].Servings != "2.000000" || meal.Components[0].ServingsMode != "meal" {
		t.Fatal("derived fields or frozen source identity", meal)
	}
	var released bool
	var bindings int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT state='released' FROM core.meal_reservations WHERE id=$1),(SELECT count(*) FROM core.planning_ingredient_bindings WHERE component_id=$2)", reservation, cid).Scan(&released, &bindings); err != nil || !released || bindings != 2 {
		t.Fatal("effective portion change failed to release real lease or preserve mappings", released, bindings, err)
	}
	view, err := f.app.Store.PlanningPurchases(f.ctx, f.identity, nil)
	if err != nil || view.Ready || purchaseDesiredTotal(t, view) != "7/5" || len(purchaseDecisions(t, view)) != 2 {
		t.Fatal("linked portions did not reach real demand/explicit procurement review", view, err)
	}
	legacy := participantSaveInput(t, f, pid, mid)
	legacy.Meal.Notes = "Legacy metadata edit retains derived roster and links"
	planningMutation(t, f, pid, legacy)
	retained := participantCurrent(t, f, pid, mid)
	if retained.ServingsMode != "participants" || retained.GuestCount != 1 || len(retained.Participants) != 1 || retained.Components[0].ServingsMode != "meal" {
		t.Fatal("omitted legacy fields cleared participant data", retained)
	}
	module, _ := f.app.Store.Module("mealplan")
	entity, _ := module.Entity("meals")
	_, err = f.app.Store.Write(f.ctx, f.identity, module, entity, mid, value.UUID(), value.UUID(), auditMutation(t, f, "mealplan", store.Record{"servings": "9"}, retained.Version))
	var invalid store.Invalid
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "Wochenplanung") {
		t.Fatal("generic amount edit lacked explicit participant/link recovery", err)
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, module, entity, mid, value.UUID(), value.UUID(), auditMutation(t, f, "mealplan", store.Record{"notes": "Generic metadata remains compatible"}, retained.Version)); err != nil {
		t.Fatal("generic metadata edit rejected valid participant state", err)
	}
	legacy = participantSaveInput(t, f, pid, mid)
	legacy.Meal.Servings = "3"
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), legacy); err == nil {
		t.Fatal("legacy client silently overrode derived portions")
	}
	manual := participantSaveInput(t, f, pid, mid)
	manual.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{participantSelf(t, f)}, GuestCount: 1}
	manual.Meal.Servings = "3.500001"
	manual.Components[0].ServingsMode, manual.Components[0].Servings = "independent", "0.250001"
	planningMutation(t, f, pid, manual)
	meal = participantCurrent(t, f, pid, mid)
	if meal.Servings != "3.500001" || meal.DerivedServings != "2" || len(meal.Participants) != 1 || meal.Components[0].Servings != "0.250001" {
		t.Fatal("explicit manual override lost roster or component independence", meal)
	}
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in); err != nil {
		t.Fatal("original request did not replay after successor", err)
	}
	if current := participantCurrent(t, f, pid, mid); current.Servings != "3.500001" {
		t.Fatal("old replay reapplied quantities", current)
	}
}

func TestPlanningParticipantsInactiveCopyAndRealPurge(t *testing.T) {
	f, pid, mid, _, _, _ := planningBindingFixture(t)
	member := shoppingTestMember(t, f, "participant-copy-member", "reader")
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: []string{member.MembershipID}, GuestCount: 2}
	in.Meal.Servings = "3"
	in.Components[0].ServingsMode = "meal"
	planningMutation(t, f, pid, in)
	participantDisable(t, f, member)
	meal := participantCurrent(t, f, pid, mid)
	if meal.Servings != "3.000000" || len(meal.Participants) != 1 || meal.Participants[0].Active {
		t.Fatal("deactivation changed actual quantity or hid historical roster", meal)
	}
	legacy := participantSaveInput(t, f, pid, mid)
	legacy.Meal.Notes = "Preserved inactive participant"
	planningMutation(t, f, pid, legacy)
	copyInput := planningInput(t, f, pid)
	copyInput.Action, copyInput.Name, copyInput.Week = "copy", "Copied historical participants", meal.PlannedOn
	day, _ := time.Parse(time.DateOnly, meal.PlannedOn)
	copyInput.TargetDate = day.AddDate(0, 0, 7).Format(time.DateOnly)
	key := value.UUID()
	result, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), copyInput)
	if err != nil {
		t.Fatal("copy retained inactive participant", err)
	}
	newPlan := store.String(result["plan_id"])
	week, err := f.app.Store.PlanningWeek(f.ctx, f.identity, newPlan, copyInput.TargetDate)
	if err != nil || len(week.Meals) != 1 {
		t.Fatal(week, err)
	}
	copied := week.Meals[0]
	if copied.ID == mid || copied.Status != "open" || copied.Servings != "3.000000" || copied.GuestCount != 2 || len(copied.Participants) != 1 || copied.Participants[0].ID == meal.Participants[0].ID || copied.Participants[0].Active || copied.Components[0].ServingsMode != "meal" {
		t.Fatal("copy lost intended roster/portions or reused identity", copied)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE key=$1", key); err != nil {
		t.Fatal(err)
	}
	if replay, replayErr := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), copyInput); replayErr != nil || replay["plan_id"] != result["plan_id"] {
		t.Fatal("durable copy replay", replay, replayErr)
	}
	reservationPurgeModule(t, f, "mealplan")
	var participants, members int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM mealplan.participants),(SELECT count(*) FROM core.memberships WHERE household_id=$1)", f.identity.HouseholdID).Scan(&participants, &members); err != nil || participants != 0 || members != 2 {
		t.Fatal("actual Mealplan purge retained roster or removed Core members", participants, members, err)
	}
}

func TestPlanningParticipantsFinalStateGuardsAndReadiness(t *testing.T) {
	f, _, mid, cid, _, _ := planningBindingFixture(t)
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"empty derived meal", "UPDATE mealplan.meals SET servings_mode='participants' WHERE id=$1", []any{mid}},
		{"linked mismatch", "UPDATE mealplan.components SET servings_mode='meal',servings=2 WHERE id=$1", []any{cid}},
		{"negative guest count", "UPDATE mealplan.meals SET guest_count=-1 WHERE id=$1", []any{mid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := audit.Exec(ctx, f.pool, tc.sql, tc.args...); err == nil {
				t.Fatal("invalid final portion state committed")
			}
		})
	}
	for _, tc := range []struct{ name, breakSQL, restoreSQL string }{
		{"ineffective meal mode check", "ALTER TABLE mealplan.meals DROP CONSTRAINT meal_servings_mode;ALTER TABLE mealplan.meals ADD CONSTRAINT meal_servings_mode CHECK(true)", "ALTER TABLE mealplan.meals DROP CONSTRAINT meal_servings_mode;ALTER TABLE mealplan.meals ADD CONSTRAINT meal_servings_mode CHECK(servings_mode IN('manual','participants'))"},
		{"ineffective guest bound check", "ALTER TABLE mealplan.meals DROP CONSTRAINT meal_guest_count;ALTER TABLE mealplan.meals ADD CONSTRAINT meal_guest_count CHECK(true)", "ALTER TABLE mealplan.meals DROP CONSTRAINT meal_guest_count;ALTER TABLE mealplan.meals ADD CONSTRAINT meal_guest_count CHECK(guest_count BETWEEN 0 AND 100)"},
		{"ineffective component mode check", "ALTER TABLE mealplan.components DROP CONSTRAINT component_servings_mode;ALTER TABLE mealplan.components ADD CONSTRAINT component_servings_mode CHECK(true)", "ALTER TABLE mealplan.components DROP CONSTRAINT component_servings_mode;ALTER TABLE mealplan.components ADD CONSTRAINT component_servings_mode CHECK(servings_mode IN('independent','meal'))"},
		{"wrong member FK", "ALTER TABLE mealplan.participants DROP CONSTRAINT participant_member_household;ALTER TABLE mealplan.participants ADD CONSTRAINT participant_member_household FOREIGN KEY(membership_id) REFERENCES core.memberships(id)", "ALTER TABLE mealplan.participants DROP CONSTRAINT participant_member_household;ALTER TABLE mealplan.participants ADD CONSTRAINT participant_member_household FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id)"},
		{"wrong unique identity", "ALTER TABLE mealplan.participants DROP CONSTRAINT participant_meal_member;ALTER TABLE mealplan.participants ADD CONSTRAINT participant_meal_member UNIQUE(household_id,id)", "ALTER TABLE mealplan.participants DROP CONSTRAINT participant_meal_member;ALTER TABLE mealplan.participants ADD CONSTRAINT participant_meal_member UNIQUE(household_id,meal_id,membership_id)"},
		{"missing final trigger", "ALTER TABLE mealplan.meals DISABLE TRIGGER planning_meal_portions", "ALTER TABLE mealplan.meals ENABLE TRIGGER planning_meal_portions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.breakSQL); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := f.owner.Exec(f.ctx, tc.restoreSQL); err != nil {
					t.Error(err)
				}
			}()
			if err := database.Ready(f.ctx, f.pool); err == nil {
				t.Fatal("readiness accepted damaged participant contract")
			}
		})
	}
	for _, privilege := range []string{"SELECT", "INSERT", "DELETE"} {
		t.Run("missing "+privilege, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, "REVOKE "+privilege+" ON mealplan.participants FROM household_app"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := f.owner.Exec(f.ctx, "GRANT "+privilege+" ON mealplan.participants TO household_app"); err != nil {
					t.Error(err)
				}
			}()
			if err := database.Ready(f.ctx, f.pool); err == nil {
				t.Fatal("readiness accepted missing individual grant")
			}
		})
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("restored participant readiness", err)
	}
}

func TestPlanningParticipantsRejectForeignInactiveAndConcurrentChanges(t *testing.T) {
	f, pid, mid, _, _, _ := planningBindingFixture(t)
	member := shoppingTestMember(t, f, "participant-inactive-selection", "reader")
	participantDisable(t, f, member)
	for _, tc := range []struct {
		name string
		p    store.PlanningParticipationInput
	}{
		{"inactive new member", store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{member.MembershipID}}},
		{"unknown household member", store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{value.UUID()}}},
		{"duplicate identity", store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{participantSelf(t, f), strings.ToUpper(participantSelf(t, f))}}},
		{"guest overflow", store.PlanningParticipationInput{Mode: "manual", GuestCount: 101}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := participantSaveInput(t, f, pid, mid)
			in.Meal.Participation = &tc.p
			if _, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); err == nil {
				t.Fatal("invalid participant selection committed")
			}
		})
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	foreignInput := participantSaveInput(t, f, pid, mid)
	foreignInput.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{participantSelf(t, f)}}
	if _, err := f.app.Store.MutatePlanning(f.ctx, foreign, pid, value.UUID(), value.UUID(), foreignInput); !errors.Is(err, store.ErrForbidden) && !errors.Is(err, store.ErrNotFound) {
		t.Fatal("forged household identity reached participant records", err)
	}
	inputs := []store.PlanningInput{participantSaveInput(t, f, pid, mid), participantSaveInput(t, f, pid, mid)}
	for i := range inputs {
		inputs[i].Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: []string{participantSelf(t, f)}, GuestCount: i}
		inputs[i].Meal.Servings = fmt.Sprint(i + 1)
		inputs[i].Components[0].ServingsMode = "meal"
	}
	start, done := make(chan struct{}), make(chan error, 2)
	for _, in := range inputs {
		go func() {
			<-start
			_, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in)
			done <- err
		}()
	}
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			winners++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal("unexpected concurrent participant outcome", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal("concurrent roster updates silently overwrite", winners, conflicts)
	}
	if meal := participantCurrent(t, f, pid, mid); len(meal.Participants) != 1 || meal.Components[0].Servings != meal.Servings {
		t.Fatal("partial concurrent roster/component state", meal)
	}
}

func TestPlanningParticipantsNewSelectionLocksActivity(t *testing.T) {
	f, pid, mid, _, _, _ := planningBindingFixture(t)
	member := shoppingTestMember(t, f, "participant-activity-lock", "reader")
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: []string{member.MembershipID}, GuestCount: 1}
	in.Meal.Servings = "2"
	in.Components[0].ServingsMode = "meal"
	before := participantCurrent(t, f, pid, mid)
	for _, tc := range []struct{ name, query, id string }{
		{"membership", "SELECT id FROM core.memberships WHERE id=$1 FOR UPDATE", member.MembershipID},
		{"user", "SELECT id FROM core.users WHERE id=$1 FOR UPDATE", member.UserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := tx.Rollback(ctx); err != nil {
					t.Error("release participant activity fixture lock", err)
				}
			}()
			if _, err = tx.Exec(f.ctx, tc.query, tc.id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
			defer cancel()
			if _, err = f.app.Store.MutatePlanning(ctx, f.identity, pid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) {
				t.Fatal("new selection ignored contested activity or waited instead of reporting conflict", err)
			}
			current := participantCurrent(t, f, pid, mid)
			if current.Version != before.Version || current.Servings != before.Servings || len(current.Participants) != 0 || current.Components[0].Version != before.Components[0].Version {
				t.Fatal("contested activity left a partial roster or quantity change", current)
			}
		})
	}
	participantDisable(t, f, member)
	var invalid store.Invalid
	if _, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), in); !errors.As(err, &invalid) {
		t.Fatal("actual deactivation did not reject a new participant selection", err)
	}
}

func TestPlanningParticipantsActualBoundaryReplacement(t *testing.T) {
	f, pid, mid, _, _ := reservationFixture(t)
	rows, err := f.owner.Query(f.ctx, `WITH members AS(
 INSERT INTO core.users(username,display_name,password_hash,must_change_password)
 SELECT 'bounded-participant-'||n,'Bounded participant '||n,'fixture-no-login',false FROM generate_series(1,101) n RETURNING id)
 INSERT INTO core.memberships(household_id,user_id,role) SELECT $1,id,'reader' FROM members RETURNING id::text`, f.identity.HouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 101)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) != 101 {
		t.Fatal("actual member boundary fixture", len(ids), err)
	}
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: ids[:100], GuestCount: 100}
	in.Meal.Servings = "200"
	planningMutation(t, f, pid, in)
	current := participantCurrent(t, f, pid, mid)
	if len(current.Participants) != 100 || current.Servings != "200.000000" {
		t.Fatal("legitimate full roster was truncated", current)
	}
	replace := participantSaveInput(t, f, pid, mid)
	replace.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: ids[1:], GuestCount: 100}
	planningMutation(t, f, pid, replace)
	var count int
	var oldRemoved, newPresent bool
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*),NOT bool_or(membership_id=$2),bool_or(membership_id=$3)
 FROM mealplan.participants WHERE meal_id=$1`, mid, ids[0], ids[100]).Scan(&count, &oldRemoved, &newPresent); err != nil || count != 100 || !oldRemoved || !newPresent {
		t.Fatal("replace-at-boundary failed or silently kept deselection", count, oldRemoved, newPresent, err)
	}
	overflow := participantSaveInput(t, f, pid, mid)
	overflow.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: ids, GuestCount: 100}
	if _, err = f.app.Store.MutatePlanning(f.ctx, f.identity, pid, value.UUID(), value.UUID(), overflow); err == nil {
		t.Fatal("101-member roster accepted")
	}
}

func TestPlanningParticipantsLateFailureRollsBackRosterVersionsAndEvidence(t *testing.T) {
	f, pid, mid, _, _, _ := planningBindingFixture(t)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION mealplan.reject_participant_fixture() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
 BEGIN IF NEW.servings_mode='participants' THEN RAISE EXCEPTION 'forced participant late failure' USING ERRCODE='23514';END IF;RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER zz_reject_participant_fixture AFTER UPDATE ON mealplan.meals DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION mealplan.reject_participant_fixture()`); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.owner.Exec(ctx, "DROP TRIGGER IF EXISTS zz_reject_participant_fixture ON mealplan.meals;DROP FUNCTION IF EXISTS mealplan.reject_participant_fixture()"); err != nil {
			t.Error(err)
		}
	}
	defer drop()
	key, requestID := value.UUID(), value.UUID()
	snapshot := func() string {
		var raw string
		err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'plan',(SELECT to_jsonb(p) FROM mealplan.plans p WHERE p.id=$1),
 'meal',(SELECT to_jsonb(m) FROM mealplan.meals m WHERE m.id=$2),
 'components',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM mealplan.components c WHERE c.meal_id=$2),
 'participants',(SELECT jsonb_agg(to_jsonb(part) ORDER BY part.id) FROM mealplan.participants part WHERE part.meal_id=$2),
 'request_proof',(SELECT count(*) FROM core.idempotency_records WHERE key=$3),
 'request_audit',(SELECT count(*) FROM core.audit_log WHERE request_id=$4),
 'outbox',(SELECT count(*) FROM core.outbox_events))::text`, pid, mid, key, requestID).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "participants", MembershipIDs: []string{participantSelf(t, f)}, GuestCount: 1}
	in.Meal.Servings = "2"
	in.Components[0].ServingsMode = "meal"
	if _, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, requestID, in); err == nil || !strings.Contains(err.Error(), "forced participant late failure") {
		t.Fatal("actual deferred failure was not exercised", err)
	}
	if after := snapshot(); after != before {
		t.Fatal("late failure left partial roster/versions/components/audit/evidence", before, after)
	}
	drop()
	if _, err := f.app.Store.MutatePlanning(f.ctx, f.identity, pid, key, value.UUID(), in); err != nil {
		t.Fatal("rollback left key or expected versions unusable", err)
	}
}

func TestPlanningParticipantsHTTPAuthorizationAndDisabledPrivacy(t *testing.T) {
	f, pid, mid, _, _, _ := planningBindingFixture(t)
	in := participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{participantSelf(t, f)}, GuestCount: 1}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/planning/" + pid
	csrf := f.app.Auth.CSRF(f.token, "session")
	response, _, err := f.request(http.MethodPost, path, f.token, "invalid", "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("participant write without CSRF accepted", response, err)
	}
	response, raw, err := f.request(http.MethodPost, path, f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("authenticated participant save", string(raw), err)
	}
	reader := shoppingTestMember(t, f, "participant-readonly-member", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "participant-reader")
	if err != nil {
		t.Fatal(err)
	}
	get := path + "/meals/" + mid
	response, raw, err = f.request(http.MethodGet, get, token, "", "", nil, nil)
	var meal store.PlanningMeal
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &meal) != nil || len(meal.Participants) != 1 || meal.Participants[0].MembershipID != participantSelf(t, f) || meal.Participants[0].Name == "" {
		t.Fatal("reader cannot inspect permitted saved roster", string(raw), err)
	}
	in = participantSaveInput(t, f, pid, mid)
	in.Meal.Participation = &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{participantSelf(t, f)}, GuestCount: 2}
	body, _ = json.Marshal(in)
	response, _, err = f.request(http.MethodPost, path, token, f.app.Auth.CSRF(token, "session"), "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("reader changed participant roster", response, err)
	}
	response, _, err = f.request(http.MethodGet, get, "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous roster read accepted", response, err)
	}
	malformed := strings.Replace(string(body), `"guest_count":2`, `"guest_count":2.5`, 1)
	response, _, err = f.request(http.MethodPost, path, f.token, csrf, "application/json", strings.NewReader(malformed), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatal("fractional guest count accepted", response, err)
	}
	state := f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	response, raw, err = f.request(http.MethodGet, get, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusNotFound || strings.Contains(string(raw), participantSelf(t, f)) {
		t.Fatal("disabled Mealplan exposed private roster", string(raw), err)
	}
}
