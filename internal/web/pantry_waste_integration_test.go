//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"household/internal/auth"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"
)

func wasteInput(t *testing.T, f *integrationFixture, lot string) store.PantryWasteInput {
	t.Helper()
	v, err := f.app.Store.PantryLotState(f.ctx, f.identity, lot)
	if err != nil {
		t.Fatal(err)
	}
	return store.PantryWasteInput{Generation: f.app.Store.States()["pantry"].Generation, Action: "waste", LotVersion: store.Int(v["version"]), ProductVersion: store.Int(v["product_version"]), LocationVersion: store.Int(v["location_version"]), MovementCount: store.Int(v["movement_count"]), Balance: store.String(v["stock"]), Quantity: "0.250000", Reason: "spoiled"}
}

func wasteSeed(t *testing.T, f *integrationFixture, suffix string) (string, store.Record) {
	t.Helper()
	p, l := pantryFixture(t, f, suffix)
	lid := store.String(l["id"])
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lid, "reason": "receive", "quantity_delta": "3"})
	return lid, p
}

func wasteEffects(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var result string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_array(
 (SELECT count(*) FROM pantry.stock_movements WHERE household_id=$1),
 (SELECT coalesce(sum(quantity),0)::text FROM pantry.lot_balances WHERE household_id=$1),
 (SELECT count(*) FROM core.audit_log WHERE household_id=$1),
 (SELECT count(*) FROM core.outbox_events WHERE household_id=$1),
 (SELECT count(*) FROM core.search_documents WHERE household_id=$1),
 (SELECT count(*) FROM core.idempotency_records WHERE household_id=$1))::text`, f.identity.HouseholdID).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func wasteBook(t *testing.T, f *integrationFixture, lot, key string, in store.PantryWasteInput) store.Record {
	t.Helper()
	out, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lot, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPantryWasteWorkflowReplayUndoAndPrivateProjection(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "waste workflow")
	in, key := wasteInput(t, f, lid), value.UUID()
	out := wasteBook(t, f, lid, key, in)
	if out["stock_after"] != "2.750000" || out["reason"] != "spoiled" || out["quantity"] != "0.250000" {
		t.Fatal(out)
	}
	before := wasteEffects(t, f)
	again, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in)
	a, _ := json.Marshal(out)
	b, _ := json.Marshal(again)
	if err != nil || !bytes.Equal(a, b) || wasteEffects(t, f) != before {
		t.Fatal("genuine JSONB replay changed effects/response", again, err)
	}
	var effects bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE household_id=$1 AND resource_id=$2 AND resource_type='stock_movements')
 AND EXISTS(SELECT 1 FROM core.search_documents WHERE household_id=$1 AND resource_id=$2 AND resource_type='stock_movements')
 AND (SELECT count(*) FROM core.outbox_events WHERE household_id=$1 AND module_id='pantry' AND event_type='record_changed' AND payload->>'entity'='stock_movements' AND payload->>'id'=$2::text)=1`, f.identity.HouseholdID, out["movement_id"]).Scan(&effects); err != nil || !effects {
		t.Fatal("missing audit/search/outbox", effects, err)
	}
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("stock_movements")
	record, err := f.app.Store.Get(f.ctx, f.identity, m, e, store.String(out["movement_id"]))
	if err != nil || record["waste_reason"] != "spoiled" {
		t.Fatal(record, err)
	}
	for _, column := range []string{"waste_operation_key", "waste_generation", "waste_request_hash", "waste_result_hash"} {
		if _, exists := record[column]; exists {
			t.Fatal("proof exposed by generic Get", column)
		}
	}
	listing, err := f.app.Store.List(f.ctx, f.identity, m, e, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(listing)
	if strings.Contains(string(encoded), "waste_request_hash") {
		t.Fatal("proof exposed by list")
	}
	undo := wasteInput(t, f, lid)
	undo.Action = "undo"
	undo.Quantity = ""
	undo.Reason = ""
	undo.MovementID = store.String(out["movement_id"])
	reversal := wasteBook(t, f, lid, value.UUID(), undo)
	if reversal["stock_after"] != "3.000000" || reversal["quantity"] != "0.250000" || reversal["reason"] != "spoiled" {
		t.Fatal(reversal)
	}
	before = wasteEffects(t, f)
	undo = wasteInput(t, f, lid)
	undo.Action = "undo"
	undo.Quantity = ""
	undo.Reason = ""
	undo.MovementID = store.String(out["movement_id"])
	if _, err = f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, value.UUID(), value.UUID(), undo); !errors.Is(err, store.ErrConflict) || wasteEffects(t, f) != before {
		t.Fatal("double reversal", err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}

func TestPantryWasteDurableProofSurvivesPrunedExpiredAndCorruptCaches(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "durable proof")
	for _, tc := range []struct{ name, sql string }{
		{"pruned", "DELETE FROM core.idempotency_records WHERE key=$1"},
		{"expired", "UPDATE core.idempotency_records SET expires_at=now()-interval '1 second' WHERE key=$1"},
		{"wrong status", "UPDATE core.idempotency_records SET response_status=201 WHERE key=$1"},
		{"changed payload", "UPDATE core.idempotency_records SET response_payload=jsonb_set(response_payload,'{quantity}','\"0.500000\"') WHERE key=$1"},
		{"oversized", "UPDATE core.idempotency_records SET response_payload=jsonb_build_object('padding',repeat('x',70000)) WHERE key=$1"},
		{"wrong cache hash", "UPDATE core.idempotency_records SET request_hash=decode(repeat('ab',32),'hex') WHERE key=$1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, key := wasteInput(t, f, lid), value.UUID()
			wasteBook(t, f, lid, key, in)
			if _, err := f.pool.Exec(f.ctx, tc.sql, key); err != nil {
				t.Fatal(err)
			}
			before := wasteEffects(t, f)
			if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("untrusted/missing cache accepted", err)
			}
			if wasteEffects(t, f) != before {
				t.Fatal("duplicate physical effect after missing cache")
			}
			in.Reason = "damaged"
			if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) {
				t.Fatal("changed durable intent", err)
			}
		})
	}
	t.Run("locked cache", func(t *testing.T) {
		in, key := wasteInput(t, f, lid), value.UUID()
		wasteBook(t, f, lid, key, in)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(f.ctx) }()
		if _, err = tx.Exec(f.ctx, "SELECT 1 FROM core.idempotency_records WHERE key=$1 FOR UPDATE", key); err != nil {
			t.Fatal(err)
		}
		before := wasteEffects(t, f)
		if _, err = f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in); !errors.Is(err, store.ErrConflict) || wasteEffects(t, f) != before {
			t.Fatal("cache size/payload fence not bounded", err)
		}
	})
}

func TestPantryWasteLegacyCorrelationAndCrossIntentCannotProveSuccess(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "old correlation")
	key := value.UUID()
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,correlation_id,created_by) VALUES($1,$2,-0.25,'waste',$3,$4)", f.identity.HouseholdID, lid, key, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	in := wasteInput(t, f, lid)
	result := wasteBook(t, f, lid, key, in)
	if result["stock_after"] != "2.500000" {
		t.Fatal("bare correlation adopted as proof", result)
	}
	before := wasteEffects(t, f)
	changed := in
	changed.Action = "undo"
	changed.Quantity = ""
	changed.Reason = ""
	changed.MovementID = store.String(result["movement_id"])
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same key cross-intent accepted", err)
	}
	if wasteEffects(t, f) != before {
		t.Fatal("cross-intent conflict changed physical effects")
	}
	other, _ := wasteSeed(t, f, "other lot")
	before = wasteEffects(t, f)
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, other, key, value.UUID(), wasteInput(t, f, other)); !errors.Is(err, store.ErrConflict) || wasteEffects(t, f) != before {
		t.Fatal("same key cross-lot accepted", err)
	}
}

func TestPantryWasteCurrentAuthorityPrecedesReplayAndConcurrentWrites(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "authority")
	in, key := wasteInput(t, f, lid), value.UUID()
	wasteBook(t, f, lid, key, in)
	reader := shoppingTestMember(t, f, "waste-reader", "reader")
	before := wasteEffects(t, f)
	wrongGeneration := in
	wrongGeneration.Generation++
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), wrongGeneration); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale generation replayed", err)
	}
	for _, id := range []auth.Identity{reader, func() auth.Identity { x := f.identity; x.AuthVersion++; return x }(), func() auth.Identity { x := f.identity; x.HouseholdID = value.UUID(); return x }()} {
		if _, err := f.app.Store.MutatePantryWaste(f.ctx, id, lid, key, value.UUID(), in); err == nil {
			t.Fatal("stale/foreign/reader authority replayed")
		}
	}
	if wasteEffects(t, f) != before {
		t.Fatal("authority failure wrote")
	}
	in = wasteInput(t, f, lid)
	errs := make(chan error, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			_, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("stale concurrent writes", success)
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, key, value.UUID(), in); err == nil {
		t.Fatal("disabled module replayed")
	}
}

func TestPantryWasteHTTPStrictInputsCSRFAndViews(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "HTTP")
	url := "/api/v1/pantry/lots/" + lid + "/waste"
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, tc := range []struct {
		method, path, token, csrf, body string
		status                          int
	}{
		{"GET", url, "", "", "", 401}, {"POST", url, f.token, "", "{}", 403},
		{"POST", url, f.token, csrf, `{"administrator":true}`, 422}, {"POST", url, f.token, csrf, `{"quantity":0.25}`, 422},
		{"GET", url, f.token, "", "", 200}, {"GET", "/m/pantry/waste", f.token, "", "", 200}, {"GET", "/m/pantry/lots/" + lid + "/waste", f.token, "", "", 200},
		{"GET", "/api/v1/pantry/waste?from=2024-01-01&to=2025-01-01", f.token, "", "", 422},
		{"GET", "/api/v1/pantry/waste?cursor=forged", f.token, "", "", 422},
	} {
		r, raw, err := f.request(tc.method, tc.path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
		if err != nil || r.StatusCode != tc.status {
			t.Fatal(tc.path, r, string(raw), err)
		}
	}
	in := wasteInput(t, f, lid)
	body, _ := json.Marshal(in)
	key := value.UUID()
	r, raw, err := f.request(http.MethodPost, url, f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": key})
	if err != nil || r.StatusCode != 200 {
		t.Fatal(r, string(raw), err)
	}
	first := string(raw)
	r, raw, err = f.request(http.MethodPost, url, f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": key})
	if err != nil || r.StatusCode != 200 || string(raw) != first {
		t.Fatal("HTTP replay", r, string(raw), err)
	}
	r, raw, err = f.request(http.MethodGet, "/m/pantry/waste", f.token, "", "", nil, nil)
	if err != nil || r.StatusCode != 200 || !strings.Contains(string(raw), "pantry-waste.js") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("PWA/security view", r, string(raw), err)
	}
}

func TestPantryWasteForcedInsertedFieldDriftRollsBack(t *testing.T) {
	f := newIntegrationFixture(t)
	lid, _ := wasteSeed(t, f, "forced drift")
	in := wasteInput(t, f, lid)
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pantry.test_waste_drift() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.waste_operation_key IS NOT NULL THEN NEW.waste_reason='damaged';END IF;RETURN NEW;END$$;
 CREATE TRIGGER aaa_test_waste_drift BEFORE INSERT ON pantry.stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.test_waste_drift()`); err != nil {
		t.Fatal(err)
	}
	before := wasteEffects(t, f)
	if _, err := f.app.Store.MutatePantryWaste(f.ctx, f.identity, lid, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrConflict) || wasteEffects(t, f) != before {
		t.Fatal("stored intent drift escaped rollback", err)
	}
}
