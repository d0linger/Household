//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"household/internal/chores"
	"household/internal/store"
	"household/internal/value"
)

func TestChoreHTTPStrictAuthorityCSRFAndWorkspaceAssets(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["chores"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "chores", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["chores"]
	}
	csrf := f.app.Auth.CSRF(f.token, "session")
	for _, pane := range []string{"today", "areas", "series", "plan", "history", "settings"} {
		response, raw, err := f.request(http.MethodGet, "/m/chores/workspace/"+pane, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 || !bytes.Contains(raw, []byte("chores-workspace.js")) || !bytes.Contains(raw, []byte("chores.css")) {
			t.Fatal("workspace route/assets", pane, err)
		}
	}
	command := store.ChoreAreaInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation}, Name: "HTTP native area"}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := f.request(http.MethodPost, "/api/v1/chores/areas", f.token, "", "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 403 {
		t.Fatal("missing CSRF accepted", err)
	}
	response, _, err = f.request(http.MethodPost, "/api/v1/chores/areas", f.token, csrf, "application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("typed area creation", err)
	}
	reader := shoppingTestMember(t, f, "chores-native-reader", "reader")
	if _, err = f.app.Store.SaveChoreArea(f.ctx, reader, "", value.UUID(), value.UUID(), command); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader wrote business data", err)
	}
	prefs := store.ChorePreferencesInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation}, DailyMinutes: 20, NotificationMode: "off", DigestMinute: 540, Timezone: "Europe/Vienna", EscalationAfterDays: 3}
	if _, err = f.app.Store.SaveChorePreferences(f.ctx, reader, value.UUID(), value.UUID(), prefs); err != nil {
		t.Fatal("reader denied own preferences", err)
	}
	response, _, err = f.request(http.MethodGet, "/api/v1/chores/tasks?scope=mine&scope=household", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 422 {
		t.Fatal("ambiguous query accepted", err)
	}
	response, _, err = f.request(http.MethodPost, "/api/v1/chores/areas", f.token, csrf, "application/json", strings.NewReader(`{"generation":1,"version":0,"name":"A","name":"B"}`), map[string]string{"Idempotency-Key": value.UUID()})
	if err != nil || response.StatusCode != 422 {
		t.Fatal("duplicate JSON accepted", err)
	}
}

func TestChoreHTTPFeedCapabilityHidesPrivateInstructionsAndInvalidatesCurrentIssuer(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["chores"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "chores", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["chores"]
	}
	var day string
	if err := f.owner.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Vienna')::date::text`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	in := store.ChoreSeriesInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation}, Title: "Public chore title", Description: "PRIVATE-CHORE-INSTRUCTION", Schedule: chores.Schedule{Kind: "once", StartsOn: day, Timezone: "Europe/Vienna"}, AssignmentKind: "fixed", AssigneeID: f.identity.MembershipID}
	preview, err := f.app.Store.PreviewChoreSeries(f.ctx, f.identity, in)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := json.Marshal(preview["normalized"])
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(normalized, &in); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.SaveChoreSeries(f.ctx, f.identity, value.UUID(), value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	result, err := f.app.Store.MutateChoreCalendarFeed(f.ctx, f.identity, "", value.UUID(), value.UUID(), store.ChoreFeedInput{ChoreCommand: in.ChoreCommand, Action: "create", Scope: "mine", DaysBack: 7, DaysAhead: 90})
	if err != nil {
		t.Fatal(err)
	}
	path := store.String(result["url"])
	response, raw, err := f.request(http.MethodGet, path, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 200 || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") || response.Header.Get("Referrer-Policy") != "no-referrer" || !bytes.Contains(raw, []byte("DTSTART:")) || bytes.Contains(raw, []byte("PRIVATE-CHORE")) {
		t.Fatal("actual calendar privacy", err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1`, f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	response, _, err = f.request(http.MethodGet, path, "", "", "", nil, nil)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("stale issuer feed authorized", err)
	}
}
