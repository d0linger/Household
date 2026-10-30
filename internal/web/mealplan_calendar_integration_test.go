//go:build integration

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"household/internal/ical"
	"household/internal/store"
	"household/internal/value"
)

func calendarTestCommand(t *testing.T, f *integrationFixture, pid string) store.PlanningCalendarInput {
	t.Helper()
	base := planningInput(t, f, pid)
	return store.PlanningCalendarInput{PlanningExtensionCommand: store.PlanningExtensionCommand{
		Generation: base.Generation, PlanVersion: base.PlanVersion, Week: "2026-10-22"}, Action: "create"}
}

func calendarTestSecret(t *testing.T, result store.Record) string {
	t.Helper()
	path := store.String(result["url"])
	if !strings.HasPrefix(path, "/calendar/mealplan/") || !strings.HasSuffix(path, ".ics") {
		t.Fatal("first response has no one-time capability")
	}
	secret := strings.TrimSuffix(strings.TrimPrefix(path, "/calendar/mealplan/"), ".ics")
	if len(secret) != 43 {
		t.Fatal("capability has wrong entropy profile")
	}
	return secret
}

func TestPlanningCalendarNativeCapabilityUpdatesCancellationAndNoStoredSecret(t *testing.T) {
	f, pid := planningFixture(t)
	in := planningInput(t, f, pid)
	in.Action = "save"
	in.Meal = store.PlanningMealInput{Title: "Public meal title", Notes: "PRIVATE-NOTE-MUST-NOT-EXPORT", PlannedOn: "2026-10-25", Slot: "dinner", Kind: "free", Status: "planned", Servings: "2"}
	meal := planningMutation(t, f, pid, in)
	mid := store.String(meal["meal_id"])
	command, key := calendarTestCommand(t, f, pid), value.UUID()
	created, err := f.app.Store.MutatePlanningCalendarFeed(f.ctx, f.identity, pid, "", key, value.UUID(), command)
	if err != nil {
		t.Fatal("create capability", err)
	}
	secret := calendarTestSecret(t, created)
	replay, err := f.app.Store.MutatePlanningCalendarFeed(f.ctx, f.identity, pid, "", key, value.UUID(), command)
	if err != nil || store.String(replay["url"]) != "" || replay["secret_unavailable"] != true {
		t.Fatal("receipt retained/re-exposed a secret", err)
	}
	digest := sha256.Sum256([]byte(secret))
	var storedHash string
	if err = f.pool.QueryRow(f.ctx, `SELECT encode(token_hash,'hex') FROM mealplan.calendar_feeds WHERE household_id=$1`, f.identity.HouseholdID).Scan(&storedHash); err != nil || storedHash != hex.EncodeToString(digest[:]) {
		t.Fatal("wrong capability hash", err)
	}
	var disclosed bool
	if err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.idempotency_records WHERE response_payload::text LIKE '%'||$1||'%')`, secret).Scan(&disclosed); err != nil || disclosed {
		t.Fatal("bearer secret stored in idempotency response", err)
	}
	feed, err := f.app.Store.PlanningCalendarFeed(f.ctx, secret)
	if err != nil || len(feed.Events) != 1 || feed.Events[0].UID != ical.MealUID(f.identity.HouseholdID, command.Generation, mid) {
		t.Fatal("bounded event identity", err)
	}
	uid, version := feed.Events[0].UID, feed.Events[0].Sequence
	response, raw, err := f.request(http.MethodGet, store.String(created["url"]), "", "", "", nil, map[string]string{"If-None-Match": `"untrusted"`})
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") ||
		response.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(string(raw), "BEGIN:VCALENDAR") ||
		strings.Contains(string(raw), "PRIVATE-NOTE") {
		t.Fatal("public feed projection/privacy failed", err)
	}
	edit := participantSaveInput(t, f, pid, mid)
	edit.Meal.Title = "Updated public title"
	planningMutation(t, f, pid, edit)
	feed, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret)
	if err != nil || len(feed.Events) != 1 || feed.Events[0].UID != uid || feed.Events[0].Sequence <= version {
		t.Fatal("event update changed UID or failed sequence", err)
	}
	current := participantCurrent(t, f, pid, mid)
	move := planningInput(t, f, pid)
	move.Action, move.MealID, move.Version, move.TargetDate, move.TargetSlot = "move", mid, current.Version, "2026-10-30", "dinner"
	planningMutation(t, f, pid, move)
	feed, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret)
	if err != nil || len(feed.Events) != 1 || feed.Events[0].UID != uid || !feed.Events[0].Cancelled || feed.Events[0].Summary != "" {
		t.Fatal("move out of scope lost minimal cancellation", err)
	}
	var fid string
	var feedVersion int64
	if err = f.pool.QueryRow(f.ctx, `SELECT id,version FROM mealplan.calendar_feeds WHERE household_id=$1`, f.identity.HouseholdID).Scan(&fid, &feedVersion); err != nil {
		t.Fatal(err)
	}
	revoke := calendarTestCommand(t, f, pid)
	revoke.Action, revoke.Version = "revoke", feedVersion
	if _, err = f.app.Store.MutatePlanningCalendarFeed(f.ctx, f.identity, pid, fid, value.UUID(), value.UUID(), revoke); err != nil {
		t.Fatal("revoke current capability", err)
	}
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("revoked capability retained authority", err)
	}
	response, _, err = f.request(http.MethodGet, store.String(created["url"]), "", "", "", nil, map[string]string{"If-None-Match": `"untrusted"`})
	if err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatal("conditional request bypassed revocation", err)
	}
}

func TestPlanningCalendarNativeReaderSharingAndCurrentIssuerAuthority(t *testing.T) {
	f, pid := planningFixture(t)
	reader := shoppingTestMember(t, f, "calendar-reader", "reader")
	command := calendarTestCommand(t, f, pid)
	created, err := f.app.Store.MutatePlanningCalendarFeed(f.ctx, reader, pid, "", value.UUID(), value.UUID(), command)
	if err != nil {
		t.Fatal("explicit Reader-owned sharing denied", err)
	}
	secret := calendarTestSecret(t, created)
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); err != nil {
		t.Fatal("Reader capability not usable", err)
	}
	listed, err := f.app.Store.PlanningCalendarFeeds(f.ctx, reader, pid, command.Week)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(listed)
	if err != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "token_hash") {
		t.Fatal("feed listing discloses secret/hash", err)
	}
	other := shoppingTestMember(t, f, "calendar-other-reader", "reader")
	var fid string
	if err = f.pool.QueryRow(f.ctx, `SELECT id FROM mealplan.calendar_feeds WHERE issuer_user_id=$1`, reader.UserID).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	revoke := command
	revoke.Action, revoke.Version = "revoke", 1
	if _, err = f.app.Store.MutatePlanningCalendarFeed(f.ctx, other, pid, fid, value.UUID(), value.UUID(), revoke); !errors.Is(err, store.ErrConflict) {
		t.Fatal("unrelated Reader revoked another person's sharing", err)
	}
	for _, tc := range []struct {
		name        string
		apply, undo string
		arg         any
	}{
		{"role", "UPDATE core.memberships SET role='editor' WHERE id=$1", "UPDATE core.memberships SET role='reader' WHERE id=$1", reader.MembershipID},
		{"membership", "UPDATE core.memberships SET inactive_at=now() WHERE id=$1", "UPDATE core.memberships SET inactive_at=NULL WHERE id=$1", reader.MembershipID},
		{"password", "UPDATE core.users SET must_change_password=true WHERE id=$1", "UPDATE core.users SET must_change_password=false WHERE id=$1", reader.UserID},
		{"disabled", "UPDATE core.users SET disabled_at=now() WHERE id=$1", "UPDATE core.users SET disabled_at=NULL WHERE id=$1", reader.UserID},
		{"auth version", "UPDATE core.users SET auth_version=auth_version+1 WHERE id=$1", "UPDATE core.users SET auth_version=auth_version-1 WHERE id=$1", reader.UserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.owner.Exec(f.ctx, tc.apply, tc.arg); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.Store.PlanningCalendarFeed(f.ctx, secret); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("stale issuer retained calendar authority", err)
			}
			if _, err := f.owner.Exec(f.ctx, tc.undo, tc.arg); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A browser logout revokes the session, not the separately shared feed.
	if err = f.app.Auth.Logout(f.ctx, reader); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); err != nil {
		t.Fatal("calendar incorrectly depended on the browser session", err)
	}
	state := f.app.Store.States()["mealplan"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "mealplan", false, state.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("disabled Mealplan capability remained usable", err)
	}
}

func TestPlanningCalendarNativeLiveFeedCannotDisappearBehindRetainedHistory(t *testing.T) {
	f, pid := planningFixture(t)
	command := calendarTestCommand(t, f, pid)
	created, err := f.app.Store.MutatePlanningCalendarFeed(f.ctx, f.identity, pid, "", value.UUID(), value.UUID(), command)
	if err != nil {
		t.Fatal(err)
	}
	secret := calendarTestSecret(t, created)
	var fid string
	if err = f.pool.QueryRow(f.ctx, `SELECT id FROM mealplan.calendar_feeds WHERE issuer_user_id=$1`, f.identity.UserID).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	// Model legitimate retained revoked history without falsely claiming these
	// one hundred fixture rows were created through a browser sharing workflow.
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, `ALTER TABLE mealplan.calendar_feeds DISABLE TRIGGER calendar_feed_change`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO mealplan.calendar_feeds
 (household_id,plan_id,week_start,issuer_user_id,issuer_membership_id,issuer_auth_version,issuer_role,
 module_generation,token_hash,expires_at,revoked_at,created_at)
 SELECT f.household_id,f.plan_id,f.week_start,f.issuer_user_id,f.issuer_membership_id,f.issuer_auth_version,
 f.issuer_role,f.module_generation,sha256(convert_to('revoked-fixture-'||g::text,'UTF8')),f.expires_at,
 clock_timestamp(),clock_timestamp()+g*interval '1 microsecond'
 FROM mealplan.calendar_feeds f CROSS JOIN generate_series(1,100) g WHERE f.id=$1`, fid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE mealplan.calendar_feeds ENABLE TRIGGER calendar_feed_change`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	listed, err := f.app.Store.PlanningCalendarFeeds(f.ctx, f.identity, pid, command.Week)
	if err != nil {
		t.Fatal(err)
	}
	feeds, ok := listed["feeds"].([]store.Record)
	if !ok || len(feeds) != 100 || listed["more"] != true || store.String(feeds[0]["id"]) != fid || feeds[0]["active"] != true {
		t.Fatal("live personal capability hidden behind bounded history")
	}
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); err != nil {
		t.Fatal("live capability incorrectly invalidated by history", err)
	}
	// Expiry is checked from current server state, independently of the session.
	tx, err = f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	for _, query := range []string{
		`ALTER TABLE mealplan.calendar_feeds DISABLE TRIGGER calendar_feed_change`,
		`UPDATE mealplan.calendar_feeds SET expires_at=clock_timestamp()-interval '1 second' WHERE revoked_at IS NULL`,
		`ALTER TABLE mealplan.calendar_feeds ENABLE TRIGGER calendar_feed_change`,
	} {
		if _, err = tx.Exec(f.ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.PlanningCalendarFeed(f.ctx, secret); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("expired capability retained authority", err)
	}
}
