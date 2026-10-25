//go:build integration

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPantryBatchDraftSavePrivacyAndReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	draft, key := value.UUID(), value.UUID()
	in := store.PantryBatchDraftInput{Generation: f.app.Store.States()["pantry"].Generation, Kind: "intake", Title: "Private incomplete intake"}
	save := func(key string) (store.Record, error) {
		return f.app.Store.SavePantryBatchDraft(f.ctx, f.identity, draft, key, value.UUID(), in)
	}
	first, err := save(key)
	if err != nil || store.Int(first["version"]) != 1 || len(first["lines"].([]any)) != 0 {
		t.Fatal("empty draft did not save", first, err)
	}
	prior, err := save(key)
	if err != nil || prior["fingerprint"] != first["fingerprint"] || store.Int(prior["version"]) != 1 {
		t.Fatal("save replay", prior, err)
	}
	in.Title = "Changed same key"
	if _, err = save(key); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same key changed payload accepted", err)
	}
	in.Title, in.Version = "Private incomplete intake", 1
	in.Lines = []store.PantryBatchLine{{ID: value.UUID(), Quantity: "1,", BestBefore: "2026-", Reason: "Private count reason"}}
	second, err := save(value.UUID())
	if err != nil || store.Int(second["version"]) != 2 {
		t.Fatal("incomplete draft was rejected", second, err)
	}
	loaded, err := f.app.Store.PantryBatchDraft(f.ctx, f.identity, draft)
	if err != nil || loaded["fingerprint"] != second["fingerprint"] || loaded["lines"].([]any)[0].(map[string]any)["quantity"] != "1," {
		t.Fatal("saved incomplete input was lost", loaded, err)
	}
	if _, err = save(value.UUID()); !errors.Is(err, store.ErrConflict) {
		t.Fatal("two-tab stale save accepted", err)
	}
	other := shoppingTestMember(t, f, "batch-other-editor", "editor")
	if _, err = f.app.Store.PantryBatchDraft(f.ctx, other, draft); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another editor read private draft", err)
	}
	if list, listErr := f.app.Store.PantryBatchDrafts(f.ctx, other); listErr != nil || len(list) != 0 {
		t.Fatal("private draft listed for another editor", list, listErr)
	}
	reader := shoppingTestMember(t, f, "batch-reader", "reader")
	if _, err = f.app.Store.PantryBatchDrafts(f.ctx, reader); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader entered private draft workspace", err)
	}
	if _, err = f.app.Store.SavePantryBatchDraft(f.ctx, other, draft, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another editor overwrote draft", err)
	}
	discardKey := value.UUID()
	discard := store.PantryBatchDiscardInput{Generation: in.Generation, Version: 2}
	for range 2 {
		result, discardErr := f.app.Store.DiscardPantryBatchDraft(f.ctx, f.identity, draft, discardKey, value.UUID(), discard)
		if discardErr != nil || result["state"] != "discarded" || store.Int(result["version"]) != 3 {
			t.Fatal("discard/replay", result, discardErr)
		}
	}
	if list, listErr := f.app.Store.PantryBatchDrafts(f.ctx, f.identity); listErr != nil || len(list) != 0 {
		t.Fatal("discarded draft remained active", list, listErr)
	}
	var effects int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM pantry.lots)+(SELECT count(*) FROM pantry.stock_movements)+(SELECT count(*) FROM pantry.stock_batch_receipts)").Scan(&effects); err != nil || effects != 0 {
		t.Fatal("draft operations created stock", effects, err)
	}
	var leaked bool
	if err = f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.audit_log WHERE changes::text LIKE '%Private%')").Scan(&leaked); err != nil || leaked {
		t.Fatal("private draft content leaked to safe audit", leaked, err)
	}
}

func TestPantryBatchDraftHTTPBoundaries(t *testing.T) {
	f := newIntegrationFixture(t)
	path := "/api/v1/pantry/batches/drafts/" + value.UUID()
	in := store.PantryBatchDraftInput{Generation: f.app.Store.States()["pantry"].Generation, Kind: "inventory"}
	raw, _ := json.Marshal(in)
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, tc := range []struct {
		name, method, token, csrf, body string
		want                            int
	}{
		{"anonymous", http.MethodGet, "", "", "", 401},
		{"csrf", http.MethodPut, f.token, "", string(raw), 403},
		{"unknown field", http.MethodPut, f.token, csrf, `{"owner_id":"forged"}`, 422},
		{"null scalar", http.MethodPut, f.token, csrf, `{"generation":1,"kind":null}`, 422},
		{"body limit", http.MethodPut, f.token, csrf, `{"title":"` + strings.Repeat("a", 1<<20) + `"}`, 422},
		{"saved empty", http.MethodPut, f.token, csrf, string(raw), 200},
		{"detail", http.MethodGet, f.token, "", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, body, err := f.request(tc.method, path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || response.StatusCode != tc.want {
				t.Fatal(response, string(body), err)
			}
		})
	}
	if err := f.app.Store.SetModule(f.ctx, f.identity, "pantry", false, f.app.Store.States()["pantry"].Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	response, body, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("disabled module disclosed draft", response, string(body), err)
	}
}

func TestPantryBatchPostHTTPAuthorizationAndSharedReceipt(t *testing.T) {
	f := newIntegrationFixture(t)
	_, lot := pantryFixture(t, f, "HTTP completion")
	line := pantryCountLine(t, f, store.String(lot["id"]), "0", "")
	draft, in := savePantryBatch(t, f, store.PantryBatchDraftInput{Kind: "inventory", LocationID: line.LocationID, Lines: []store.PantryBatchLine{line}})
	path := "/api/v1/pantry/batches/drafts/" + draft + "/post"
	raw, _ := json.Marshal(in)
	for _, role := range []string{"reader", "editor"} {
		member := shoppingTestMember(t, f, "batch-final-"+role, role)
		token, _, err := f.app.Auth.Login(f.ctx, member.Username, integrationPassword, "", "127.0.0.1", "batch-final")
		if err != nil {
			t.Fatal(err)
		}
		want := 403
		if role == "editor" {
			want = 404
		}
		response, body, err := f.request(http.MethodPost, path, token, f.app.Auth.CSRF(token, "session"), "application/json", strings.NewReader(string(raw)), map[string]string{"Idempotency-Key": value.UUID()})
		if err != nil || response.StatusCode != want {
			t.Fatal("another member posted private draft", role, response, string(body), err)
		}
	}
	response, body, err := f.request(http.MethodPost, path, f.token, "", "application/json", strings.NewReader(string(raw)), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("completion skipped CSRF", response, string(body), err)
	}
	posted, err := f.app.Store.PostPantryBatch(f.ctx, f.identity, draft, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := f.app.Auth.Login(f.ctx, "batch-final-reader", integrationPassword, "", "127.0.0.1", "batch-final-read")
	if err != nil {
		t.Fatal(err)
	}
	response, body, err = f.request(http.MethodGet, "/api/v1/pantry/batches/receipts/"+store.String(posted["id"]), token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), store.String(posted["id"])) {
		t.Fatal("household reader cannot inspect shared completed receipt", response, string(body), err)
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PantryBatchReceipt(f.ctx, foreign, store.String(posted["id"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign household read receipt", err)
	}
	if _, err = f.app.Store.PantryBatchDraft(f.ctx, foreign, draft); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign household read draft", err)
	}
}
