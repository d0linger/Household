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

func notificationPlanningEnable(t *testing.T, f *integrationFixture) {
	t.Helper()
	state := f.app.Store.States()["mealplan"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "mealplan", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
}

func notificationPlanningSource(t *testing.T, f *integrationFixture, at time.Time, name string) (string, string) {
	t.Helper()
	var zone string
	if err := f.pool.QueryRow(f.ctx, `SELECT timezone FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	date := at.In(loc).Format(time.DateOnly)
	plan, _ := auditCreate(t, f, "mealplan", "plans", store.Record{"name": "PRIVATE-PLANNING " + name, "starts_on": date, "ends_on": date})
	pid := store.String(plan["id"])
	out := planningMutation(t, f, pid, store.PlanningInput{
		Generation: f.app.Store.States()["mealplan"].Generation, PlanVersion: store.Int(plan["version"]), Action: "save",
		Meal: store.PlanningMealInput{Title: "PRIVATE-PLANNING meal " + name, PlannedOn: date, Slot: "dinner", Kind: "free", Status: "planned", Servings: "1", Notes: "PRIVATE-PLANNING notes",
			Participation: &store.PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{f.identity.MembershipID}}},
	})
	return pid, store.String(out["meal_id"])
}

func notificationPlanningInput(t *testing.T, f *integrationFixture, pid, mid, member string, at time.Time) store.PlanningNotificationInput {
	t.Helper()
	view, err := f.app.Store.PlanningNotificationSettings(f.ctx, f.identity, pid, mid)
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := view["settings"].(store.Record)
	if !ok {
		t.Fatal("planning notification settings missing")
	}
	zone := store.String(view["timezone"])
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	local := at.In(loc)
	return store.PlanningNotificationInput{
		Generation: store.Int(view["generation"]), PlanVersion: store.Int(view["plan_version"]), MealVersion: store.Int(view["meal_version"]),
		SettingsVersion: store.Int(settings["version"]), Timezone: zone, Responsible: member, Enabled: true, Confirmed: true,
		Steps: []store.PlanningNotificationStepInput{{ID: value.UUID(), Kind: "start", Label: "PRIVATE-PLANNING preparation", LocalDate: local.Format(time.DateOnly), LocalTime: local.Format("15:04")}},
	}
}

func notificationPlanningPost(t *testing.T, f *integrationFixture, pid, mid, token, csrf, key string, body any, want int) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, out, err := f.request(http.MethodPost, "/api/v1/planning/"+pid+"/meals/"+mid+"/notifications", token, csrf,
		"application/json", bytes.NewReader(raw), map[string]string{"Idempotency-Key": key})
	if err != nil || response.StatusCode != want {
		t.Fatalf("planning notification HTTP status: want=%d response=%v body=%s error=%v", want, response, out, err)
	}
	return out
}

func TestNotificationMealplanHTTPAuthorityValidationAndReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	notificationPlanningEnable(t, f)
	at := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Minute)
	pid, mid := notificationPlanningSource(t, f, at, "HTTP contract")
	path := "/api/v1/planning/" + pid + "/meals/" + mid + "/notifications"
	csrf := f.app.Auth.CSRF(f.token, "session")
	member := shoppingTestMember(t, f, "planning-notify-reader", "reader")
	readerToken, _, err := f.app.Auth.Login(f.ctx, member.Username, integrationPassword, "", "127.0.0.1", "notification reader")
	if err != nil {
		t.Fatal(err)
	}
	in := notificationPlanningInput(t, f, pid, mid, member.MembershipID, at)
	for _, token := range []string{f.token, readerToken} {
		response, raw, err := f.request(http.MethodGet, path, token, "", "", nil, nil)
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("authorized private settings read", response, err)
		}
		var view struct {
			Eligible          bool `json:"eligible"`
			NeedsConfirmation bool `json:"needs_confirmation"`
			Settings          struct {
				ID          string            `json:"id"`
				Version     int64             `json:"version"`
				Enabled     bool              `json:"enabled"`
				Responsible *string           `json:"responsible_member_id"`
				Steps       []json.RawMessage `json:"steps"`
			} `json:"settings"`
		}
		if err = json.Unmarshal(raw, &view); err != nil || !view.Eligible || view.NeedsConfirmation || view.Settings.ID != mid || view.Settings.Version != 0 || view.Settings.Enabled || view.Settings.Responsible != nil || len(view.Settings.Steps) != 0 {
			t.Fatal("default settings fabricated consent or responsibility", string(raw), err)
		}
	}
	var rows int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM mealplan.notification_settings`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("GET persisted notification defaults", rows, err)
	}
	t.Run("reader cannot mutate", func(t *testing.T) {
		notificationPlanningPost(t, f, pid, mid, readerToken, f.app.Auth.CSRF(readerToken, "session"), value.UUID(), in, 403)
	})
	t.Run("CSRF required", func(t *testing.T) { notificationPlanningPost(t, f, pid, mid, f.token, "", value.UUID(), in, 403) })
	t.Run("idempotency required", func(t *testing.T) { notificationPlanningPost(t, f, pid, mid, f.token, csrf, "", in, 422) })
	for _, tc := range []struct {
		name   string
		mutate func(*store.PlanningNotificationInput)
		status int
	}{
		{"explicit confirmation", func(v *store.PlanningNotificationInput) { v.Confirmed = false }, 422},
		{"explicit responsibility", func(v *store.PlanningNotificationInput) { v.Responsible = "" }, 422},
		{"zero recipient", func(v *store.PlanningNotificationInput) { v.Responsible = "00000000-0000-0000-0000-000000000000" }, 422},
		{"zero step", func(v *store.PlanningNotificationInput) { v.Steps[0].ID = "00000000-0000-0000-0000-000000000000" }, 422},
		{"future only", func(v *store.PlanningNotificationInput) { v.Steps[0].LocalDate = "2000-01-01" }, 422},
		{"unknown step kind", func(v *store.PlanningNotificationInput) { v.Steps[0].Kind = "broadcast" }, 422},
		{"bounded label", func(v *store.PlanningNotificationInput) { v.Steps[0].Label = strings.Repeat("x", 121) }, 422},
		{"duplicate step identity", func(v *store.PlanningNotificationInput) { v.Steps = append(v.Steps, v.Steps[0]) }, 422},
		{"stale plan", func(v *store.PlanningNotificationInput) { v.PlanVersion++ }, 409},
		{"stale meal", func(v *store.PlanningNotificationInput) { v.MealVersion++ }, 409},
		{"stale settings", func(v *store.PlanningNotificationInput) { v.SettingsVersion = 9 }, 409},
		{"changed timezone", func(v *store.PlanningNotificationInput) { v.Timezone = "Pacific/Auckland" }, 409},
		{"missing member", func(v *store.PlanningNotificationInput) { v.Responsible = value.UUID() }, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := in
			copy.Steps = append([]store.PlanningNotificationStepInput(nil), in.Steps...)
			tc.mutate(&copy)
			notificationPlanningPost(t, f, pid, mid, f.token, csrf, value.UUID(), copy, tc.status)
		})
	}
	t.Run("closed body", func(t *testing.T) {
		raw, _ := json.Marshal(in)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		body["scheduled_at"] = at.Format(time.RFC3339)
		notificationPlanningPost(t, f, pid, mid, f.token, csrf, value.UUID(), body, 422)
	})
	validRaw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"duplicate property", `{"enabled":false,` + string(validRaw[1:])},
		{"multiple documents", string(validRaw) + `{}`},
		{"null steps", string(validRaw[:bytes.Index(validRaw, []byte(`"steps":`))]) + `"steps":null}`},
		{"server timestamp injection", strings.Replace(string(validRaw), `"local_time":`, `"scheduled_at":"2027-01-01T00:00:00Z","local_time":`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, out, err := f.request(http.MethodPost, path, f.token, csrf, "application/json", strings.NewReader(tc.raw), map[string]string{"Idempotency-Key": value.UUID()})
			if err != nil || response.StatusCode != 422 {
				t.Fatal("ambiguous or forged JSON was accepted", response, string(out), err)
			}
		})
	}
	t.Run("plan meal binding", func(t *testing.T) {
		other, _ := notificationPlanningSource(t, f, at, "other plan")
		notificationPlanningPost(t, f, other, mid, f.token, csrf, value.UUID(), in, 404)
	})
	key := value.UUID()
	first := notificationPlanningPost(t, f, pid, mid, f.token, csrf, key, in, 200)
	second := notificationPlanningPost(t, f, pid, mid, f.token, csrf, key, in, 200)
	if !bytes.Equal(first, second) {
		t.Fatal("exact retry changed the saved confirmation response")
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE module_id='mealplan' AND resource_id=$1 AND action='notification_settings_insert'`, mid).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("confirmation replay duplicated the audit transition", rows, err)
	}
	var settingsVersion, planVersion, mealVersion int64
	if err := f.pool.QueryRow(f.ctx, `SELECT n.version,p.version,m.version FROM mealplan.notification_settings n JOIN mealplan.meals m ON m.id=n.id JOIN mealplan.plans p ON p.id=m.plan_id WHERE n.id=$1`, mid).Scan(&settingsVersion, &planVersion, &mealVersion); err != nil || settingsVersion != 1 || planVersion != in.PlanVersion || mealVersion != in.MealVersion {
		t.Fatal("settings save or replay altered source revisions", settingsVersion, planVersion, mealVersion, err)
	}
	changed := in
	changed.Enabled = false
	notificationPlanningPost(t, f, pid, mid, f.token, csrf, key, changed, 409)
	notificationPlanningPost(t, f, pid, mid, f.token, csrf, value.UUID(), in, 409)
	// A session that was valid for GET must lose mutation authority immediately.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.memberships SET role='reader' WHERE id=$1`, f.identity.MembershipID); err != nil {
		t.Fatal(err)
	}
	notificationPlanningPost(t, f, pid, mid, f.token, csrf, key, in, 403)
}
