//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"household/internal/expenseedit"
	"household/internal/store"
	"household/internal/value"
)

func TestExpenseEditorHTTPAuthorizationStrictProtocolAndReceipt(t *testing.T) {
	f := newIntegrationFixture(t)
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "HTTP editor category"})
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	in.Header.Description = "Shared HTTP sentinel <script>alert(1)</script>"
	in.Header.PaidByMembershipID = f.identity.MembershipID
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "/api/v1/expenses/editor"
	csrf := f.app.Auth.CSRF(f.token, "session")
	before := expenseEditSnapshot(t, f)
	response, _, err := f.request(http.MethodPost, endpoint, f.token, "invalid", "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusForbidden || expenseEditSnapshot(t, f) != before {
		t.Fatal("write without CSRF changed expense", response, err)
	}
	for _, malformed := range []string{strings.Replace(string(body), `"amount_eur":"10.00"`, `"amount_eur":1000`, 1), string(body) + ` {}`, `{"unexpected":true}`} {
		response, _, err = f.request(http.MethodPost, endpoint, f.token, csrf, "application/json", strings.NewReader(malformed), map[string]string{"Idempotency-Key": value.UUID()})
		if err != nil || response.StatusCode != http.StatusUnprocessableEntity || expenseEditSnapshot(t, f) != before {
			t.Fatal("noncanonical input changed expense", response, err)
		}
	}
	invalid := in
	invalid.Header = expenseedit.HeaderInput{OccurredOn: "2026-10-26", Description: "", AmountEUR: "10.00", Kind: "expense"}
	badBody, _ := json.Marshal(invalid)
	response, raw, err := f.request(http.MethodPost, endpoint, f.token, csrf, "application/json", bytes.NewReader(badBody), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 422 || !bytes.Contains(raw, []byte(`"header.description"`)) || expenseEditSnapshot(t, f) != before {
		t.Fatal("validation did not retain named field problems without mutation", string(raw), err)
	}
	key := value.UUID()
	response, raw, err = f.request(http.MethodPost, endpoint, f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": key})
	var receipt store.ExpenseEditReceipt
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &receipt) != nil || receipt.EntryID == "" || receipt.CurrentState != "draft" || strings.Contains(string(raw), "sentinel") || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("private minimal acceptance receipt failed", string(raw), err)
	}
	response, raw, err = f.request(http.MethodGet, endpoint+"?entry="+receipt.EntryID, f.token, "", "", nil, nil)
	var bundle store.ExpenseBundle
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &bundle) != nil || len(bundle.Baseline) != 1 || bundle.RemainingMinor == nil || *bundle.RemainingMinor != "100" || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("complete private bundle failed", string(raw), err)
	}
	response, raw, err = f.request(http.MethodPost, endpoint, f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": key})
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &receipt) != nil || !receipt.Replayed {
		t.Fatal("HTTP same-key replay failed", string(raw), err)
	}
	response, raw, err = f.request(http.MethodGet, "/m/expenses/editor?entry="+receipt.EntryID, f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte(`id="expense-editor"`)) || !bytes.Contains(raw, []byte(`value="`+f.identity.MembershipID+`"`)) || bytes.Contains(raw, []byte("<script>alert(1)</script>")) {
		t.Fatal("editor template missing or unescaped input rendered", string(raw), err)
	}
}

func TestExpenseEditorHTTPReaderAndDisabledModulePrivacy(t *testing.T) {
	f, category, entry, _ := expenseIntegrityFixture(t)
	in := expenseEditNewInput(t, f, store.String(category["id"]))
	body, _ := json.Marshal(in)
	reader := shoppingTestMember(t, f, "shared-expense-http-reader", "reader")
	token, _, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "expense-editor-reader")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/expenses/editor?entry=" + store.String(entry["id"])
	response, raw, err := f.request(http.MethodGet, endpoint, token, "", "", nil, nil)
	var bundle store.ExpenseBundle
	if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &bundle) != nil || bundle.Editable || bundle.LockReason == "" {
		t.Fatal("reader did not receive shared read-only facts", string(raw), err)
	}
	response, _, err = f.request(http.MethodPost, "/api/v1/expenses/editor", token, f.app.Auth.CSRF(token, "session"), "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("reader changed shared expense", response, err)
	}
	response, _, err = f.request(http.MethodGet, endpoint, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 401 {
		t.Fatal("anonymous bundle read accepted", response, err)
	}
	state := f.app.Store.States()["expenses"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "expenses", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{endpoint, "/m/expenses/editor?entry=" + store.String(entry["id"])} {
		response, raw, err = f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 404 || strings.Contains(string(raw), "Complete initial line") || strings.Contains(string(raw), "Editor integrity draft") {
			t.Fatal("disabled Expenses exposed editor data", path, response, err)
		}
	}
}
