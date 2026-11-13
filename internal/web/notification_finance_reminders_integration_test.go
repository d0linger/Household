//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"household/internal/store"
	"household/internal/value"
)

func TestNotificationFinancePaymentReminderHTTPBoundaries(t *testing.T) {
	f, _, entry, _ := expenseIntegrityFixture(t)
	entryID := store.String(entry["id"])
	path := "/api/v1/expenses/finance/entries/" + entryID + "/payment-reminder"
	csrf := f.app.Auth.CSRF(f.token, "session")
	get := func(t *testing.T) store.Record {
		t.Helper()
		res, body, err := f.request(http.MethodGet, path, f.token, "", "", nil, nil)
		if err != nil || res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("authorized reminder GET", res, err)
		}
		var out store.Record
		if json.Unmarshal(body, &out) != nil {
			t.Fatal("invalid reminder projection")
		}
		return out
	}
	view := get(t)
	if view["reminder"] != nil || view["can_edit"] != true || view["verified_paid"] != false {
		t.Fatal("initial reminder projection fabricated reminder or payment")
	}
	in := store.FinancePaymentReminderInput{Action: "save", Version: 0, EntryVersion: store.Int(view["entry_version"]),
		Generation: store.Int(view["generation"]), AccessVersion: store.String(view["access_version"]),
		DueOn: time.Now().UTC().AddDate(0, 0, 10).Format(time.DateOnly), ResponsibleMemberID: f.identity.MembershipID, RequestID: value.UUID()}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, csrf, body string
		status                  int
	}{
		{"missing authentication", "", "", string(raw), http.StatusUnauthorized},
		{"missing CSRF", f.token, "", string(raw), http.StatusForbidden},
		{"injected paid", f.token, csrf, strings.TrimSuffix(string(raw), "}") + `,"paid":true}`, http.StatusUnprocessableEntity},
		{"duplicate action", f.token, csrf, strings.TrimSuffix(string(raw), "}") + `,"action":"confirm"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _, err := f.request(http.MethodPost, path, tc.token, tc.csrf, "application/json", strings.NewReader(tc.body), nil)
			if err != nil || res.StatusCode != tc.status {
				t.Fatal("closed reminder HTTP boundary", res, err)
			}
		})
	}
	var createdVersion int64
	for attempt := range 2 {
		res, body, err := f.request(http.MethodPost, path, f.token, csrf, "application/json", bytes.NewReader(raw), nil)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatal("actual reminder save/replay", attempt, res, err)
		}
		var out struct {
			VerifiedPaid bool `json:"verified_paid"`
			Reminder     struct {
				Version int64  `json:"version"`
				State   string `json:"state"`
			} `json:"reminder"`
		}
		if json.Unmarshal(body, &out) != nil || out.Reminder.State != "draft" || out.VerifiedPaid || out.Reminder.Version <= 0 {
			t.Fatal("save/replay response did not preserve draft semantics")
		}
		if attempt == 0 {
			createdVersion = out.Reminder.Version
		} else if out.Reminder.Version != createdVersion {
			t.Fatal("HTTP retry mutated version")
		}
	}
	in.RequestID, in.Action, in.Version = value.UUID(), "confirm", createdVersion
	raw, err = json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := f.request(http.MethodPost, path, f.token, csrf, "application/json", bytes.NewReader(raw), nil)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatal("explicit reminder confirmation", res, err)
	}
	in.RequestID = value.UUID()
	raw, err = json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err = f.request(http.MethodPost, path, f.token, csrf, "application/json", bytes.NewReader(raw), nil)
	if err != nil || res.StatusCode != http.StatusConflict {
		t.Fatal("stale reminder revision accepted", res, err)
	}
}
