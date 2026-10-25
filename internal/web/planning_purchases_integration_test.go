//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPlanningPurchasesHTTPAuthorizationCSRFHistoryAndExactReplay(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "HTTP purchase destination"})
	url := "/api/v1/planning-purchases/preview?source=" + pid + "/" + mid
	response, _, err := f.request(http.MethodGet, url, "", "", "", nil, nil)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatal("preview allowed anonymous access", response, err)
	}
	response, raw, err := f.request(http.MethodGet, url, f.token, "", "", nil, nil)
	var view store.PlanningPurchasesPreview
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &view) != nil || !view.Ready {
		t.Fatal("authenticated complete preview", string(raw), err)
	}
	in, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	body, _ := json.Marshal(in)
	key := value.UUID()
	headers := map[string]string{"Idempotency-Key": key}
	response, _, err = f.request(http.MethodPost, "/api/v1/planning-purchases/sync", f.token, "invalid", "application/json", bytes.NewReader(body), headers)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("sync bypassed CSRF", response, err)
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	response, raw, err = f.request(http.MethodPost, "/api/v1/planning-purchases/sync", f.token, csrf, "application/json", bytes.NewReader(body), headers)
	var result store.Record
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &result) != nil {
		t.Fatal("actual HTTP sync", response, string(raw), err)
	}
	response, replay, err := f.request(http.MethodPost, "/api/v1/planning-purchases/sync", f.token, csrf, "application/json", bytes.NewReader(body), headers)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(raw, replay) {
		t.Fatal("same HTTP key did not return exact immutable response", string(replay), err)
	}
	for _, identifier := range []string{value.UUID(), "invalid"} {
		response, _, err = f.request(http.MethodGet, "/api/v1/planning-purchases/operations/"+identifier, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != http.StatusNotFound {
			t.Fatal("unknown history must be404", response, err)
		}
	}
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"malformed selection", http.MethodGet, "/api/v1/planning-purchases/preview?source=invalid", "", http.StatusUnprocessableEntity},
		{"too many whole sources", http.MethodGet, "/api/v1/planning-purchases/preview?" + strings.Repeat("source="+pid+"/"+mid+"&", 43), "", http.StatusUnprocessableEntity},
		{"arbitrary quantity forbidden", http.MethodPost, "/api/v1/planning-purchases/sync", `{"quantity":"100"}`, http.StatusUnprocessableEntity},
		{"unbounded body", http.MethodPost, "/api/v1/planning-purchases/sync", `{"fingerprint":"` + strings.Repeat("a", (1<<20)+1) + `"}`, http.StatusUnprocessableEntity},
		{"unknown target", http.MethodGet, "/api/v1/planning-purchases/destinations/" + value.UUID(), "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, raw, err := f.request(tc.method, tc.path, f.token, csrf, "application/json", strings.NewReader(tc.body), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || response.StatusCode != tc.status {
				t.Fatal("invalid purchase request boundary", response, string(raw), err)
			}
		})
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE household_id=$1 AND user_id=$2", f.identity.HouseholdID, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{url, "/api/v1/planning-purchases/scope", "/api/v1/planning-purchases/operations/" + store.String(result["id"])} {
		response, raw, err = f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatal("reader could not inspect household evidence", path, response, string(raw), err)
		}
	}
	response, _, err = f.request(http.MethodPost, "/api/v1/planning-purchases/sync", f.token, csrf, "application/json", bytes.NewReader(body), headers)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("reader bypassed authorization via durable replay", response, err)
	}
	control := reviewedPurchaseControl(t, f, "end", []string{mid}, nil)
	body, _ = json.Marshal(control)
	response, _, err = f.request(http.MethodPost, "/api/v1/planning-purchases/control", f.token, csrf, "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("reader ended active demand", response, err)
	}
}

func TestPlanningPurchasesActualPurgeRejectsOldMutationButRetainsMinimalHistory(t *testing.T) {
	f, pid, mid, cid, _, product := planningBindingFixture(t)
	bindPlanningIngredients(t, f, pid, mid, cid, product)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Generation-bound API destination"})
	in, _ := reviewedPurchaseInput(t, f, []store.PlanningPurchaseSelection{{PlanID: pid, MealIDs: []string{mid}}}, store.String(list["id"]))
	key := value.UUID()
	result, err := f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	reservationPurgeModule(t, f, "shopping")
	body, _ := json.Marshal(in)
	response, raw, err := f.request(http.MethodPost, "/api/v1/planning-purchases/sync", f.token, f.app.Auth.CSRF(f.token, "session"), "application/json", bytes.NewReader(body), map[string]string{"Idempotency-Key": key})
	if err != nil || response.StatusCode != http.StatusConflict {
		t.Fatal("durable old request bypassed actual target generation change", response, string(raw), err)
	}
	response, raw, err = f.request(http.MethodGet, "/api/v1/planning-purchases/operations/"+store.String(result["id"]), f.token, "", "", nil, nil)
	var history store.Record
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(raw, &history) != nil || history["id"] != result["id"] || history["details_available"] != false || history["snapshot"] != nil {
		t.Fatal("generation purge lost minimal history or exposed removed detail", response, string(raw), err)
	}
	var items, active int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM shopping.items),(SELECT count(*) FROM core.planning_purchase_requirements WHERE active)").Scan(&items, &active); err != nil || items != 0 || active != 2 {
		t.Fatal("old mutation recreated a purchase or erased source demand", items, active, err)
	}
	newList, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "New current target generation"})
	_, blocked := reviewedPurchaseInput(t, f, nil, store.String(newList["id"]))
	choices := purchaseDecisions(t, blocked)
	if blocked.Ready || len(choices) != 1 || choices[0].Action != "renew" {
		t.Fatal("purged target silently authorized new procurement", blocked)
	}
	current, view := reviewedPurchaseDecisionInput(t, f, nil, choices, store.String(newList["id"]))
	if !view.Ready {
		t.Fatal(view)
	}
	if _, err = f.app.Store.SyncPlanningPurchases(f.ctx, f.identity, value.UUID(), value.UUID(), current); err != nil {
		t.Fatal("reviewed new-generation destination was not recoverable", err)
	}
}
