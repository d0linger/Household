//go:build integration

package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

func shoppingExtensionWatch(t *testing.T, f *integrationFixture, id auth.Identity, list, revision string, enabled bool) store.ShoppingPreferenceView {
	t.Helper()
	out, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, id, list, value.UUID(), store.ShoppingPreferenceInput{Generation: f.app.Store.States()["shopping"].Generation, Revision: revision, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func shoppingExtensionStage(t *testing.T, f *integrationFixture) {
	t.Helper()
	for range 8 {
		n, err := f.app.Store.StageShoppingNotifications(f.ctx, 20)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("bounded staging did not finish")
}

func shoppingExtensionDigest(t *testing.T, f *integrationFixture) (string, string) {
	t.Helper()
	// Scheduler fault injection only: make the genuine staged minute bucket
	// due without sleeping. The actual App role then acquires the real lease.
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.jobs SET available_at=clock_timestamp()-interval '1 second' WHERE kind=$1 AND state='pending'", store.ShoppingDigestJobKind); err != nil {
		t.Fatal(err)
	}
	lease := value.UUID()
	var id string
	err := f.pool.QueryRow(f.ctx, `WITH candidate AS(SELECT id FROM core.jobs WHERE kind=$1 AND state='pending' AND available_at<=clock_timestamp()
 ORDER BY available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE core.jobs j SET state='running',lease_token=$2,
 lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id`, store.ShoppingDigestJobKind, lease).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id, lease
}

func shoppingExtensionCount(t *testing.T, f *integrationFixture, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.owner.QueryRow(f.ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestShoppingExtensionsReaderPreferencesRevisionAndMetadata(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Private watch"})
	reader := shoppingTestMember(t, f, "shopping-watch-reader", "reader")
	gen := f.app.Store.States()["shopping"].Generation
	var before string
	if err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_array(state_version,changed_at,changed_by)::text FROM core.module_states WHERE household_id=$1 AND module_id='shopping'`, reader.HouseholdID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	view, err := f.app.Store.ShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), gen)
	if err != nil || view.Revision != "0" || view.Preference != nil {
		t.Fatal("default read mutated or wrong", view, err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.module_states WHERE module_id='shopping' AND settings ? 'shopping_notify_v1'") != 0 {
		t.Fatal("GET created defaults")
	}
	first := shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	if first.Revision != "1" || first.Preference == nil || first.Preference.PushEnabled {
		t.Fatal("reader optin", first)
	}
	if _, err = f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "0", Enabled: false}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale unsubscribe", err)
	}
	last := shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "1", false)
	if last.Revision != "2" || last.Preference != nil {
		t.Fatal("unsubscribe fence missing", last)
	}
	if _, err = f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "0", Enabled: true}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("version0 resurrected removed watch", err)
	}
	var after string
	if err = f.owner.QueryRow(f.ctx, `SELECT jsonb_build_array(state_version,changed_at,changed_by)::text FROM core.module_states WHERE household_id=$1 AND module_id='shopping'`, reader.HouseholdID).Scan(&after); err != nil || after != before {
		t.Fatal("personal prefs changed module lifecycle metadata", before, after, err)
	}
	other, err := f.app.Store.ShoppingNotificationPreferences(f.ctx, f.identity, store.String(list["id"]), gen)
	if err != nil || other.Revision != "0" || other.Preference != nil {
		t.Fatal("foreign prefs disclosed", other, err)
	}
	m, _ := f.app.Store.Module("shopping")
	items, _ := m.Entity("items")
	if _, err = f.app.Store.Write(f.ctx, reader, m, items, "", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"list_id": list["id"], "name": "reader cannot write"}, 0)); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("personal write granted domain edit", err)
	}
}

func TestShoppingExtensionsProducerMoveDeleteReplayAndNoop(t *testing.T) {
	f := newIntegrationFixture(t)
	a, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "A"})
	b, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "B"})
	item, _ := auditCreate(t, f, "shopping", "items", store.Record{"list_id": a["id"], "name": "Original", "quantity": "1"})
	m, _ := f.app.Store.Module("shopping")
	e, _ := m.Entity("items")
	key := value.UUID()
	in := auditMutation(t, f, "shopping", store.Record{"list_id": b["id"]}, 1)
	moved, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(item["id"]), key, value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, store.String(item["id"]), key, value.UUID(), in); err != nil {
		t.Fatal(err)
	}
	var parents []string
	err = f.owner.QueryRow(f.ctx, `SELECT ARRAY(SELECT jsonb_array_elements_text(payload->'shopping'->'list_ids')) FROM core.outbox_events
 WHERE module_id='shopping' AND payload->>'id'=$1 AND event_version=2 ORDER BY created_at DESC,id DESC LIMIT 1`, item["id"]).Scan(&parents)
	if err != nil || len(parents) != 2 || !strings.Contains(strings.Join(parents, ","), store.String(a["id"])) || !strings.Contains(strings.Join(parents, ","), store.String(b["id"])) {
		t.Fatal("move parents", parents, err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events WHERE payload->>'id'=$1 AND event_version=2", item["id"]) != 2 {
		t.Fatal("move replay produced extra v2 event")
	}
	if _, err = f.app.Store.Write(f.ctx, f.identity, m, e, store.String(item["id"]), value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"name": "Original"}, store.Int(moved["version"]))); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.outbox_events WHERE payload->>'id'=$1 AND event_version=2", item["id"]) != 2 {
		t.Fatal("content noop emitted digest event")
	}
	if err = f.app.Store.Delete(f.ctx, f.identity, m, e, store.String(item["id"]), store.Int(moved["version"])+1, f.app.Store.States()["shopping"].Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, `SELECT count(*) FROM core.outbox_events WHERE payload->>'id'=$1 AND payload->'shopping'->>'kind'='item_deleted' AND payload->'shopping'->'list_ids'=jsonb_build_array($2::text)`, item["id"], b["id"]) != 1 {
		t.Fatal("delete lost original parent")
	}
	lists, _ := m.Entity("lists")
	if err = f.app.Store.Delete(f.ctx, f.identity, m, lists, store.String(a["id"]), 1, f.app.Store.States()["shopping"].Generation, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, `SELECT count(*) FROM core.outbox_events WHERE payload->>'id'=$1 AND payload->'shopping'->>'kind'='list_deleted'`, a["id"]) != 1 {
		t.Fatal("cascade signal missing")
	}
}

func TestShoppingExtensionsDigestCurrentAudienceAndLease(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Watch"})
	reader := shoppingTestMember(t, f, "digest-reader", "reader")
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	for _, name := range []string{"One", "Two"} {
		auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": name})
	}
	shoppingExtensionStage(t, f)
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind) != 1 {
		t.Fatal("changes did not coalesce")
	}
	job, lease := shoppingExtensionDigest(t, f)
	if err := f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, value.UUID()); !errors.Is(err, store.ErrConflict) {
		t.Fatal("foreign lease accepted", err)
	}
	if err := f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var nid string
	if err := f.pool.QueryRow(f.ctx, "SELECT id FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1'", reader.UserID).Scan(&nid); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.notifications WHERE kind='shopping_list_changed_v1'") != 1 {
		t.Fatal("self notification or duplicate")
	}
	if _, err := f.app.Store.ShoppingNotificationProjection(f.ctx, reader, nid); err != nil {
		t.Fatal("current notice unavailable", err)
	}
	if _, err := f.app.Store.ShoppingNotificationProjection(f.ctx, f.identity, nid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign notice projection", err)
	}
	if err := f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, lease); !errors.Is(err, store.ErrConflict) {
		t.Fatal("completed lease reused", err)
	}
	// A later profile revision invalidates pending work atomically.
	auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Muted backlog"})
	shoppingExtensionStage(t, f)
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "1", false)
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.notifications WHERE kind='shopping_list_changed_v1'") != 1 {
		t.Fatal("unsubscribe altered accepted receipt")
	}
	if _, err := f.app.Store.ShoppingNotificationProjection(f.ctx, reader, nid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unsubscribed inbox still exposes notice", err)
	}
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "2", true)
	if _, err := f.app.Store.ShoppingNotificationProjection(f.ctx, reader, nid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("re-enabled watch exposed pre-consent notice", err)
	}
}

func TestShoppingExtensionsPreferenceQuotaAndSettingsPrivacy(t *testing.T) {
	f := newIntegrationFixture(t)
	reader := shoppingTestMember(t, f, "quota-reader", "reader")
	revision := "0"
	for n := 0; n < 21; n++ {
		list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Quota list"})
		view, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: f.app.Store.States()["shopping"].Generation, Revision: revision, Enabled: true})
		if n < 20 {
			if err != nil {
				t.Fatal(n, err)
			}
			revision = view.Revision
		} else {
			var invalid store.Invalid
			if !errors.As(err, &invalid) {
				t.Fatal("21st watch accepted", err)
			}
		}
	}
	view, err := f.app.Store.ShoppingNotificationOverview(f.ctx, reader, f.app.Store.States()["shopping"].Generation)
	if err != nil || len(view.Lists) != 20 || view.Revision != "20" {
		t.Fatal("quota failure changed profile", view, err)
	}
	var unsafe int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM core.audit_log WHERE module_id='core' AND resource_type='module_states' AND action='UPDATE'
 AND changes ? 'settings' AND (changes->'settings'->>'redacted' IS DISTINCT FROM 'true' OR changes->'settings'->>'before' IS DISTINCT FROM '***REDACTED***' OR changes->'settings'->>'after' IS DISTINCT FROM '***REDACTED***')`).Scan(&unsafe); err != nil {
		t.Fatal(err)
	}
	if unsafe != 0 {
		t.Fatal("settings namespace disclosed to audit values")
	}
	// Preserve unrelated operator configuration through actual reader write.
	if _, err = audit.Exec(audit.WithSystem(context.Background(), "operator"), f.owner, "UPDATE core.module_states SET settings=settings||jsonb_build_object('unrelated',jsonb_build_object('keep',true)) WHERE household_id=$1 AND module_id='shopping'", reader.HouseholdID); err != nil {
		t.Fatal(err)
	}
	list := view.Lists[0].List.ID
	shoppingExtensionWatch(t, f, reader, list, "20", false)
	var kept bool
	if err = f.owner.QueryRow(f.ctx, "SELECT settings->'unrelated'=jsonb_build_object('keep',true) FROM core.module_states WHERE household_id=$1 AND module_id='shopping'", reader.HouseholdID).Scan(&kept); err != nil || !kept {
		t.Fatal("unrelated namespace lost", err)
	}
}

func TestShoppingExtensionsDigestPendingRevocationAndMalformedInbox(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Revocation"})
	reader := shoppingTestMember(t, f, "revoked-watch", "reader")
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Pending"})
	shoppingExtensionStage(t, f)
	job, lease := shoppingExtensionDigest(t, f)
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "1", false)
	if err := f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.notifications WHERE kind='shopping_list_changed_v1'") != 0 {
		t.Fatal("revoked audience notified")
	}
	nid := value.UUID()
	_, err := audit.Exec(audit.WithSystem(f.ctx, "worker"), f.pool, `INSERT INTO core.notifications(id,household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 VALUES($1,$2,$3,'shopping','shopping_list_changed_v1','MUST NOT SHOW','https://foreign.invalid',$4)`, nid, reader.HouseholdID, reader.UserID, "malformed/"+value.UUID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ShoppingNotificationProjection(f.ctx, reader, nid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("malformed key projected stored unsafe title/URL", err)
	}
	// The JSON namespace has no public route: context carries only declared scopes.
	scope, err := f.app.Store.ShoppingExtensionsContext(f.ctx, reader)
	raw, _ := json.Marshal(scope)
	if err != nil || strings.Contains(string(raw), reader.SessionID) || strings.Contains(string(raw), "watches") {
		t.Fatal("secret scope/settings exposure", err)
	}
}

func TestShoppingExtensionsPreferenceContentionAndMuteInbox(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Lock and mute"})
	reader := shoppingTestMember(t, f, "lock-reader", "reader")
	gen := f.app.Store.States()["shopping"].Generation
	tx, err := f.app.Store.BeginMutation(f.ctx, f.identity, "shopping", gen)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "0", Enabled: true})
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = tx.Rollback(cleanup)
	cancel()
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal("SHARE to personal UPDATE did not fail fast", err)
	}
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Visible then muted"})
	shoppingExtensionStage(t, f)
	job, lease := shoppingExtensionDigest(t, f)
	if err = f.app.Store.CompleteShoppingNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var notice string
	if err = f.pool.QueryRow(f.ctx, "SELECT id FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1'", reader.UserID).Scan(&notice); err != nil {
		t.Fatal(err)
	}
	muted, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, store.String(list["id"]), value.UUID(), store.ShoppingPreferenceInput{Generation: gen, Revision: "1", Enabled: true, MuteForSeconds: 3600})
	if err != nil || muted.Preference.MutedUntil == nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ShoppingNotificationProjection(f.ctx, reader, notice); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("muted inbox notice remained visible", err)
	}
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "2", true)
	if _, err = f.app.Store.ShoppingNotificationProjection(f.ctx, reader, notice); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("resume revived mute backlog", err)
	}
	m, _ := f.app.Store.Module("shopping")
	lists, _ := m.Entity("lists")
	if _, err = f.app.Store.Action(f.ctx, f.identity, m, lists, store.String(list["id"]), "archive", value.UUID(), value.UUID(), auditMutation(t, f, "shopping", store.Record{"archived": true}, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Store.ShoppingNotificationProjection(f.ctx, reader, notice); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("archived inbox still exposes list", err)
	}
}

func TestShoppingExtensionsFanoutCursorSyntheticAudienceAndRestart(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "51-recipient cursor"})
	shoppingExtensionStage(t, f)
	var now time.Time
	if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	users := map[string]any{}
	// Explicit synthetic audience/backfill fixture, not a claim that 51 browser
	// users performed opt-in. Real authority/FKs remain present for every user;
	// two-user actual preference consent is covered by the other main tests.
	for n := 0; n < 51; n++ {
		uid := value.UUID()
		if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) VALUES($1,$2,'Synthetic fanout member','no-login-fixture',false)", uid, "fanout-"+uid); err != nil {
			t.Fatal(err)
		}
		if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'reader')", f.identity.HouseholdID, uid); err != nil {
			t.Fatal(err)
		}
		users[uid] = store.Record{"revision": "1", "watches": store.Record{store.String(list["id"]): store.Record{"enabled": true, "push_enabled": false, "muted_until": nil, "enabled_since": now.UTC(), "push_enabled_since": nil}}}
	}
	namespace, _ := json.Marshal(store.Record{"schema": 1, "generation": f.app.Store.States()["shopping"].Generation, "users": users})
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.module_states SET settings=jsonb_set(settings,ARRAY['shopping_notify_v1'],$2::jsonb,true) WHERE household_id=$1 AND module_id='shopping'", f.identity.HouseholdID, namespace); err != nil {
		t.Fatal(err)
	}
	auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Bounded fanout event"})
	if n, err := f.app.Store.StageShoppingNotifications(f.ctx, 1); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind) != 50 {
		t.Fatal("first bounded fanout did not stage exactly50")
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.event_deliveries WHERE consumer_id='shopping-list-notifications-v1' AND state='pending' AND last_error LIKE 'cursor:%'") != 1 {
		t.Fatal("unseen recipient was acknowledged")
	}
	restarted := store.New(f.pool, f.app.Store.Modules)
	if n, err := restarted.StageShoppingNotifications(f.ctx, 1); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind) != 51 {
		t.Fatal("restart lost/duplicated recipient")
	}
	if _, err := restarted.StageShoppingNotifications(f.ctx, 1); err != nil {
		t.Fatal(err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind) != 51 {
		t.Fatal("done event fanout repeated")
	}
}

func TestShoppingExtensionsExpiredPausedWorkTerminatesWithoutDelivery(t *testing.T) {
	f := newIntegrationFixture(t)
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Expired transport work"})
	reader := shoppingTestMember(t, f, "expired-notice-reader", "reader")
	shoppingExtensionWatch(t, f, reader, store.String(list["id"]), "0", true)
	auditCreate(t, f, "shopping", "items", store.Record{"list_id": list["id"], "name": "Pending change"})
	shoppingExtensionStage(t, f)
	var job string
	var raw []byte
	var now time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT id,payload,clock_timestamp() FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind).Scan(&job, &raw, &now); err != nil {
		t.Fatal(err)
	}
	var ref store.ShoppingNotificationReference
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	// Clock/state fault injection into a genuinely staged job. This does not
	// claim an accepted vendor delivery or fabricate a new notification receipt.
	ref.ExpiresAt = now.Add(-time.Second)
	ref.Bucket = ref.ExpiresAt.Unix()/60 - 24*60
	ref.EventAt = ref.ExpiresAt.Add(-24*time.Hour - time.Second)
	raw, _ = json.Marshal(ref)
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.jobs SET state='paused',payload=$2::jsonb,lease_token=NULL,lease_until=NULL WHERE id=$1", job, raw); err != nil {
		t.Fatal(err)
	}
	unrelated := value.UUID()
	if _, err := f.owner.Exec(f.ctx, "INSERT INTO core.jobs(id,household_id,kind,dedupe_key,state) VALUES($1,$2,'housekeeping',$1::uuid::text,'paused')", unrelated, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	shoppingExtensionStage(t, f)
	var state, reason string
	if err := f.owner.QueryRow(f.ctx, "SELECT state,last_error FROM core.jobs WHERE id=$1", job).Scan(&state, &reason); err != nil || state != "done" || reason != "notification_expired" {
		t.Fatal("paused work retained expired authority", state, reason, err)
	}
	if shoppingExtensionCount(t, f, "SELECT count(*) FROM core.notifications WHERE kind='shopping_list_changed_v1'") != 0 || shoppingExtensionCount(t, f, "SELECT count(*) FROM core.jobs WHERE id=$1 AND state='paused'", unrelated) != 1 {
		t.Fatal("expiry delivered or changed another job namespace")
	}
}
