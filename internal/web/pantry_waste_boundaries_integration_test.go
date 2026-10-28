//go:build integration

package web

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func wasteReport(t *testing.T, f *integrationFixture, filter store.PantryWasteFilter, point store.PantryWastePoint) store.PantryWasteReport {
	t.Helper()
	scope, err := f.app.Store.PantryWasteContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.app.Store.PantryWasteReport(f.ctx, f.identity, scope, filter, point)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPantryWasteCivilTimezoneReversalBookingAndHistoricalAsOf(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "civil days")
	if _, err := f.pool.Exec(f.ctx, "UPDATE core.households SET timezone='Europe/Vienna' WHERE id=$1", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	original := value.UUID()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,created_by,created_at)
 VALUES($1,$2,$3,-1,'waste',$4,'2026-03-28T23:30:00Z')`, original, f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,reverses_movement_id,created_by,created_at)
 VALUES($1,$2,1,'reversal',$3,$4,'2026-03-29T22:30:00Z')`, f.identity.HouseholdID, lid, original, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	first := wasteReport(t, f, store.PantryWasteFilter{From: "2026-03-29", To: "2026-03-29"}, store.PantryWastePoint{})
	if len(first.Records) != 1 || first.Records[0].Day != "2026-03-29" || first.Records[0].ReversedBy != "" || first.UnknownReasons != 1 || first.Totals[0].Net != "1.000000" || first.EstimatedCostMinor != nil {
		t.Fatal("historical day leaked future reversal/reason/cost", first)
	}
	second := wasteReport(t, f, store.PantryWasteFilter{From: "2026-03-30", To: "2026-03-30"}, store.PantryWastePoint{})
	if len(second.Records) != 1 || second.Records[0].Kind != "reversal" || second.Records[0].Day != "2026-03-30" || second.Totals[0].Waste != "0.000000" || second.Totals[0].Reversals != "1.000000" || second.Totals[0].Net != "-1.000000" {
		t.Fatal("DST booking/negative period balance", second)
	}
	all := wasteReport(t, f, store.PantryWasteFilter{From: "2026-03-29", To: "2026-03-30"}, store.PantryWastePoint{})
	if all.Movements != 2 || all.Totals[0].Net != "0.000000" || all.Totals[0].Waste != "1.000000" || all.Totals[0].Reversals != "1.000000" {
		t.Fatal(all)
	}
}

func TestPantryWasteSignedPagesDetectWholeScopeAndIdentityChanges(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, p := wasteSeed(t, f, "signed pages")
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,waste_reason,created_by,created_at)
 SELECT $1,$2,-0.000001,'waste','spoiled',$3,now()-interval '1 hour'+g*interval '1 microsecond' FROM generate_series(1,51) g`, f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	type page struct {
		store.PantryWasteReport
		Next string `json:"next_cursor"`
	}
	load := func(path string, want int) page {
		t.Helper()
		r, raw, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || r.StatusCode != want {
			t.Fatal(r, string(raw), err)
		}
		var out page
		if want == 200 {
			if err = json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	base := "/api/v1/pantry/waste?product=" + store.String(p["id"]) + "&reason=spoiled"
	first := load(base, 200)
	if len(first.Records) != 50 || first.Next == "" || first.Totals[0].Waste != "0.000051" {
		t.Fatal(first)
	}
	second := load(base+"&cursor="+url.QueryEscape(first.Next), 200)
	if len(second.Records) != 1 || second.Next != "" || second.Fingerprint != first.Fingerprint {
		t.Fatal(second)
	}
	load(base+"&location="+value.UUID()+"&cursor="+url.QueryEscape(first.Next), 422)
	reader := shoppingTestMember(t, f, "waste-page-reader", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "waste reader")
	if err != nil {
		t.Fatal(err)
	}
	r, raw, err := f.request(http.MethodGet, base+"&cursor="+url.QueryEscape(first.Next), token, "", "", nil, nil)
	if err != nil || r.StatusCode != 422 {
		t.Fatal("cursor transferred identity", r, string(raw), err)
	}
	// A backdated non-selected reason changes the complete immutable scope.
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,waste_reason,created_by,created_at)
 VALUES($1,$2,-0.000001,'waste','damaged',$3,now()-interval '2 hours')`, f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	load(base+"&cursor="+url.QueryEscape(first.Next), 409)
	fresh := load(base, 200)
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET name=name||' changed' WHERE id=$1", p["id"]); err != nil {
		t.Fatal(err)
	}
	load(base+"&cursor="+url.QueryEscape(fresh.Next), 409)
}

func TestPantryWastePotentialRowAndByteBudgetsFailClosed(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, p := wasteSeed(t, f, "report limits")
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by)
 SELECT $1,$2,-0.000001,'waste',$3 FROM generate_series(1,10001) g`, f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	scope, err := f.app.Store.PantryWasteContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.app.Store.PantryWasteReport(f.ctx, f.identity, scope, store.PantryWasteFilter{ProductID: store.String(p["id"])}, store.PantryWastePoint{})
	var invalid store.Invalid
	if !errors.As(err, &invalid) || len(out.Totals) != 0 || len(out.Records) != 0 {
		t.Fatal("partial report beyond 10000", out, err)
	}
	// More than 16 MiB of conservative projected labels is refused before labels
	// are materialized. Product names remain within their actual 200-rune limit.
	_, second := pantryFixture(t, f, "byte budget")
	secondID := store.String(second["id"])
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.products SET name=repeat('界',200) WHERE id=$1", second["product_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE pantry.locations SET name=repeat('界',100) WHERE id=$1", second["location_id"]); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": secondID, "quantity_delta": "1", "reason": "receive"})
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by)
 SELECT $1,$2,-0.000001,'waste',$3 FROM generate_series(1,3000) g`, f.identity.HouseholdID, secondID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	scope, err = f.app.Store.PantryWasteContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	out, err = f.app.Store.PantryWasteReport(f.ctx, f.identity, scope, store.PantryWasteFilter{ProductID: store.String(second["product_id"])}, store.PantryWastePoint{})
	if !errors.As(err, &invalid) || len(out.Totals) != 0 {
		t.Fatal("label budget produced partial totals", out, err)
	}
}

func TestPantryWasteReadinessPinsConstraintsIndexesTriggersAndACL(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, tc := range []struct{ name, breakSQL, repairSQL string }{
		{"proof guard", "ALTER TABLE pantry.stock_movements DISABLE TRIGGER pantry_waste_proof", "ALTER TABLE pantry.stock_movements ENABLE TRIGGER pantry_waste_proof"},
		{"immutability", "ALTER TABLE pantry.stock_movements DISABLE TRIGGER movements_immutable", "ALTER TABLE pantry.stock_movements ENABLE TRIGGER movements_immutable"},
		{"App column update", "GRANT UPDATE(waste_reason) ON pantry.stock_movements TO household_app", "REVOKE UPDATE(waste_reason) ON pantry.stock_movements FROM household_app"},
		{"private proof function", "GRANT EXECUTE ON FUNCTION pantry.guard_waste_proof() TO PUBLIC", "REVOKE EXECUTE ON FUNCTION pantry.guard_waste_proof() FROM PUBLIC"},
		{"index", "ALTER INDEX pantry.movements_waste_date RENAME TO test_missing_waste_date", "ALTER INDEX pantry.test_missing_waste_date RENAME TO movements_waste_date"},
		{"column default", "ALTER TABLE pantry.stock_movements ALTER COLUMN waste_reason SET DEFAULT 'other'", "ALTER TABLE pantry.stock_movements ALTER COLUMN waste_reason DROP DEFAULT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.breakSQL); err != nil {
				t.Fatal(err)
			}
			var ready bool
			err := f.pool.QueryRow(f.ctx, "SELECT coalesce(pantry.waste_ready(),false)").Scan(&ready)
			if err != nil || ready {
				t.Fatal("Ready accepted drift", ready, err)
			}
			if _, err = f.owner.Exec(f.ctx, tc.repairSQL); err != nil {
				t.Fatal(err)
			}
			if err = database.Ready(f.ctx, f.pool); err != nil {
				t.Fatal("Ready repair", err)
			}
		})
	}
	var definition string
	if err := f.owner.QueryRow(f.ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='pantry.stock_movements'::regclass AND conname='stock_waste_proof'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "ALTER TABLE pantry.stock_movements DROP CONSTRAINT stock_waste_proof"); err != nil {
		t.Fatal(err)
	}
	var ready bool
	if err := f.pool.QueryRow(f.ctx, "SELECT coalesce(pantry.waste_ready(),false)").Scan(&ready); err != nil || ready {
		t.Fatal("missing proof constraint accepted", ready, err)
	}
	if _, err := f.owner.Exec(f.ctx, "ALTER TABLE pantry.stock_movements ADD CONSTRAINT stock_waste_proof "+definition); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("constraint repair", err)
	}
}

func TestPantryWasteDirectAppProofShapeAndImmutability(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "direct proof")
	for _, extra := range []string{
		"waste_operation_key", "waste_generation", "waste_request_hash", "waste_result_hash",
	} {
		columns := map[string]string{"waste_operation_key": "gen_random_uuid()", "waste_generation": "1", "waste_request_hash": "decode(repeat('ab',32),'hex')", "waste_result_hash": "decode(repeat('cd',32),'hex')"}
		_, err := f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by,"+extra+") VALUES($1,$2,-0.1,'waste',$3,"+columns[extra]+")", f.identity.HouseholdID, lid, f.identity.UserID)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" {
			t.Fatal("partial proof", extra, err)
		}
	}
	result := wasteBook(t, f, lid, value.UUID(), wasteInput(t, f, lid))
	before := wasteEffects(t, f)
	for _, sql := range []string{"UPDATE pantry.stock_movements SET waste_reason='damaged' WHERE id=$1", "DELETE FROM pantry.stock_movements WHERE id=$1"} {
		if _, err := f.pool.Exec(f.ctx, sql, result["movement_id"]); err == nil {
			t.Fatal("App mutated immutable journal")
		}
	}
	if wasteEffects(t, f) != before {
		t.Fatal("immutable journal changed")
	}
}

func TestPantryWastePopulatedPantry10UpgradePreservesUnknownJournal(t *testing.T) {
	f := newIntegrationFixture(t)
	// Rebuild only this disposable independent module using the actual prior
	// runner. No synthetic version stamp or reconstructed dropped-column catalog.
	if _, err := f.owner.Exec(f.ctx, "DROP SCHEMA pantry CASCADE"); err != nil {
		t.Fatal(err)
	}
	migratePriorAuditSchema(t, f, "pantry", 10)
	if _, err := f.owner.Exec(f.ctx, "SELECT core.install_audit_triggers()"); err != nil {
		t.Fatal(err)
	}
	lid, _ := wasteSeed(t, f, "legacy upgrade")
	legacy := value.UUID()
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,note,created_by) VALUES($1,$2,$3,-0.1,'waste','Original historical note',$4)", legacy, f.identity.HouseholdID, lid, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.pool.QueryRow(f.ctx, "SELECT to_jsonb(t)::text FROM pantry.stock_movements t WHERE id=$1", legacy).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// The prior response cache is an explicit predecessor fixture with the exact
	// pre-existing generic Mutation digest and actual predecessor journal image.
	m, _ := f.app.Store.Module("pantry")
	entity, _ := m.Entity("stock_movements")
	legacyInput := auditMutation(t, f, "pantry", store.Record{"lot_id": lid, "reason": "waste", "quantity_delta": "-0.1"}, 0)
	request, _ := json.Marshal(legacyInput)
	digest := sha256.Sum256(request)
	legacyKey := value.UUID()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,'write/pantry/stock_movements/',$3,$4,'pantry',$5,200,$6::jsonb)`, f.identity.HouseholdID, f.identity.UserID, legacyKey, digest[:], legacyInput.Generation, before); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := f.pool.QueryRow(f.ctx, "SELECT (to_jsonb(t)-ARRAY['waste_reason','waste_operation_key','waste_generation','waste_request_hash','waste_result_hash'])::text FROM pantry.stock_movements t WHERE id=$1", legacy).Scan(&after); err != nil || before != after {
		t.Fatal("historical data rewritten", before, after, err)
	}
	if err := database.Migrate(f.ctx, os.Getenv("HOUSEHOLD_TEST_OWNER_URL")); err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	stable := wasteEffects(t, f)
	replay, err := f.app.Store.Write(f.ctx, f.identity, m, entity, "", legacyKey, value.UUID(), legacyInput)
	if err != nil || store.String(replay["id"]) != legacy || wasteEffects(t, f) != stable {
		t.Fatal("accepted predecessor replay changed", replay, err)
	}
	changed := auditMutation(t, f, "pantry", store.Record{"lot_id": lid, "reason": "waste", "quantity_delta": "-0.2"}, 0)
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, entity, "", legacyKey, value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("legacy hash changed", err)
	}
	if _, err = f.pool.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE key=$1", legacyKey); err != nil {
		t.Fatal(err)
	}
	stable = wasteEffects(t, f)
	var invalid store.Invalid
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, entity, "", legacyKey, value.UUID(), legacyInput); !errors.As(err, &invalid) || wasteEffects(t, f) != stable {
		t.Fatal("pruned generic waste duplicated physical effect", err)
	}
	report := wasteReport(t, f, store.PantryWasteFilter{}, store.PantryWastePoint{})
	if report.UnknownReasons != 1 || report.EstimatedCostMinor != nil || report.Records[0].Reason != "unknown" {
		t.Fatal(report)
	}
}

func TestPantryWasteGenericCacheMissRefusesOnlyFreshWasteAndOffersRecovery(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "generic closure")
	m, _ := f.app.Store.Module("pantry")
	entity, _ := m.Entity("stock_movements")
	in := auditMutation(t, f, "pantry", store.Record{"lot_id": lid, "reason": "waste", "quantity_delta": "-0.1"}, 0)
	before := wasteEffects(t, f)
	var invalid store.Invalid
	if _, err := f.app.Store.Write(f.ctx, f.identity, m, entity, "", value.UUID(), value.UUID(), in); !errors.As(err, &invalid) || !strings.Contains(invalid.Error(), "Vorrat → Abfall buchen") || wasteEffects(t, f) != before {
		t.Fatal("fresh generic waste accepted", err)
	}
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "consume", "quantity_delta": "-0.1"})
	r, raw, err := f.request(http.MethodGet, "/m/pantry/stock_movements", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), "disabled data-waste-legacy-disabled") || !strings.Contains(string(raw), "Vorrat → Abfall buchen") {
		t.Fatal("generic form offers misleading waste path", r, string(raw), err)
	}
}

func TestPantryWasteNoProofCacheDoesNotAuthorizeNewPhysicalEffect(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "cache without proof")
	key := value.UUID()
	in := wasteInput(t, f, lid)
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,decode(repeat('ab',32),'hex'),'pantry',$5,200,'{}')`, f.identity.HouseholdID, f.identity.UserID, "pantry-waste-v1/lots/"+lid, key, in.Generation); err != nil {
		t.Fatal(err)
	}
	before := wasteEffects(t, f)
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in); !errors.Is(err, store.ErrNotFound) || wasteEffects(t, f) != before {
		t.Fatal("cache alone became physical proof", err)
	}
	ctx := f.identity
	ctx.SessionID = value.UUID()
	if _, err := f.app.Store.PantryWasteContext(f.ctx, ctx); err == nil {
		t.Fatal("stale read authority")
	}
	if _, err := f.app.Store.PantryWasteLot(f.ctx, f.identity, lid, value.UUID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("selected foreign movement", err)
	}
	scope, err := f.app.Store.PantryWasteContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	scope.Timezone = "UTC"
	if _, err = f.app.Store.PantryWasteReport(f.ctx, f.identity, scope, store.PantryWasteFilter{}, store.PantryWastePoint{}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale timezone scope", err)
	}
}

func TestPantryWasteReportAsOfExcludesPostSnapshotReversal(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "live asof")
	out := wasteBook(t, f, lid, value.UUID(), wasteInput(t, f, lid))
	scope, err := f.app.Store.PantryWasteContext(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	undo := wasteInput(t, f, lid)
	undo.Action = "undo"
	undo.Quantity = ""
	undo.Reason = ""
	undo.MovementID = store.String(out["movement_id"])
	wasteBook(t, f, lid, value.UUID(), undo)
	old, err := f.app.Store.PantryWasteReport(f.ctx, f.identity, scope, store.PantryWasteFilter{}, store.PantryWastePoint{AsOf: scope.Now})
	if err != nil || old.Movements != 1 || old.Records[0].ReversedBy != "" || old.Totals[0].Reversals != "0.000000" {
		t.Fatal("later reversal leaked", old, err)
	}
	fresh := wasteReport(t, f, store.PantryWasteFilter{}, store.PantryWastePoint{})
	if fresh.Movements != 2 || fresh.Totals[0].Net != "0.000000" {
		t.Fatal(fresh)
	}
	if old.AsOf.After(time.Now()) || strings.Contains(store.String(out), "waste_request_hash") {
		t.Fatal("invalid bounded public snapshot")
	}
}
