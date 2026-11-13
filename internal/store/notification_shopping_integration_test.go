//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/value"
)

func notificationShoppingWrite(t *testing.T, f *recipeExpansionFixture, actor auth.Identity, entity, rid string, version int64, values Record) Record {
	t.Helper()
	m, _ := f.store.Module("shopping")
	e, _ := m.Entity(entity)
	data := make(map[string]json.RawMessage, len(values))
	for key, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		data[key] = raw
	}
	r, err := f.store.Write(f.ctx, actor, m, e, rid, value.UUID(), value.UUID(), Mutation{Generation: f.store.States()["shopping"].Generation, Version: version, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func notificationShoppingWatch(t *testing.T, f *recipeExpansionFixture, actor auth.Identity, list string, enabled, push bool, mute int64) ShoppingPreferenceView {
	t.Helper()
	gen := f.store.States()["shopping"].Generation
	current, err := f.store.ShoppingNotificationPreferences(f.ctx, actor, list, gen)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.store.SetShoppingNotificationPreferences(f.ctx, actor, list, value.UUID(), ShoppingPreferenceInput{Generation: gen, Revision: current.Revision, Enabled: enabled, PushEnabled: push, MuteForSeconds: mute})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func notificationShoppingStage(t *testing.T, f *recipeExpansionFixture, recipient, list string) (string, string) {
	t.Helper()
	for range 8 {
		n, err := f.store.StageShoppingNotifications(f.ctx, 20)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	lease := value.UUID()
	var job string
	// Advance only the actual staged digest job, preserving its closed source
	// timestamps. A core intent must retain its real minute deadline.
	err := f.owner.QueryRow(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$4,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 WHERE kind=$1 AND state='pending' AND payload->>'recipient_id'=$2 AND payload->>'list_id'=$3 RETURNING id::text`, ShoppingDigestJobKind, recipient, list, lease).Scan(&job)
	if err != nil {
		t.Fatal("claim real shopping digest", err)
	}
	return job, lease
}

func TestNotificationShoppingCoreOwnershipAndCurrentAudience(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	reader := financeAuthorityMember(t, f, "reader")
	newList := func(t *testing.T) string {
		t.Helper()
		list := notificationShoppingWrite(t, f, f.identity, "lists", "", 0, Record{"name": "Private notification list"})
		lid := String(list["id"])
		notificationShoppingWatch(t, f, reader, lid, true, true, 0)
		return lid
	}
	change := func(t *testing.T, list string) {
		t.Helper()
		notificationShoppingWrite(t, f, f.identity, "items", "", 0, Record{"list_id": list, "name": "Private notification item"})
	}
	finish := func(t *testing.T, list string) (string, string) {
		t.Helper()
		job, lease := notificationShoppingStage(t, f, reader.UserID, list)
		if err := f.store.CompleteShoppingNotificationJob(f.ctx, job, lease); err != nil {
			t.Fatal(err)
		}
		if err := f.store.CompleteShoppingNotificationJob(f.ctx, job, lease); !errors.Is(err, ErrConflict) {
			t.Fatal("completed digest lease reused", err)
		}
		var notice string
		if err := f.app.QueryRow(f.ctx, `SELECT id::text FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1' AND dedupe_key LIKE $2`, reader.UserID, "%/"+list+"/%").Scan(&notice); err != nil {
			t.Fatal(err)
		}
		return job, notice
	}
	intent := func(t *testing.T, job string, want int) string {
		t.Helper()
		var ids []string
		if err := f.app.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_intents WHERE resource_id=$1 AND user_id=$2 AND category='shopping')`, job, reader.UserID).Scan(&ids); err != nil || len(ids) != want {
			t.Fatal("core shopping intent count", len(ids), want, err)
		}
		if len(ids) == 0 {
			return ""
		}
		return ids[0]
	}
	var pref NotificationPreference
	t.Run("first core choice owns channel including disabled and old events", func(t *testing.T) {
		list := newList(t)
		change(t, list)
		var err error
		pref, err = f.store.SaveNotificationPreference(f.ctx, reader, NotificationPreference{Category: "shopping", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}})
		if err != nil {
			t.Fatal(err)
		}
		job, _ := finish(t, list)
		intent(t, job, 0)
		pref.Enabled = false
		pref, err = f.store.SaveNotificationPreference(f.ctx, reader, pref)
		if err != nil {
			t.Fatal(err)
		}
		list = newList(t)
		change(t, list)
		job, _ = finish(t, list)
		intent(t, job, 0)
		var legacy int
		if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.jobs WHERE kind=$1 AND payload->>'recipient_id'=$2`, ShoppingPushJobKind, reader.UserID).Scan(&legacy); err != nil || legacy != 0 {
			t.Fatal("explicit central opt-out fell back", legacy, err)
		}
		pref.Enabled = true
		pref, err = f.store.SaveNotificationPreference(f.ctx, reader, pref)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("digest coalesces and reuses finalized source without replay", func(t *testing.T) {
		list := newList(t)
		notificationShoppingWatch(t, f, f.identity, list, true, true, 0)
		change(t, list)
		change(t, list)
		job, notice := finish(t, list)
		iid := intent(t, job, 1)
		i := notificationChoreAudience(t, f, iid, true)
		if i.ResourceType != "shopping_digest" || i.Revision != 1 || i.Occurrence != "notice/"+notice || i.Module != "shopping" {
			t.Fatal("closed shopping reference changed", i)
		}
		var raw []byte
		if err := f.app.QueryRow(f.ctx, `SELECT payload FROM core.jobs WHERE id=$1`, job).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var ref ShoppingNotificationReference
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatal(err)
		}
		if !i.EventAt.Equal(ref.EventAt) || !i.NotBefore.Equal(time.Unix((ref.Bucket+1)*60, 0)) || !i.ExpiresAt.Equal(ref.ExpiresAt) {
			t.Fatal("digest timing changed")
		}
		if _, err := f.store.StageShoppingNotifications(f.ctx, 20); err != nil {
			t.Fatal(err)
		}
		if intent(t, job, 1) != iid {
			t.Fatal("replay replaced intent")
		}
		var self int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1' AND dedupe_key LIKE $2`, f.identity.UserID, "%/"+list+"/%").Scan(&self); err != nil || self != 0 {
			t.Fatal("actor received own digest", self, err)
		}
		notificationShoppingWatch(t, f, reader, list, true, true, 3600)
		notificationChoreAudience(t, f, iid, false)
		notificationShoppingWatch(t, f, reader, list, true, true, 0)
		notificationChoreAudience(t, f, iid, false)
	})
	t.Run("concurrent digest completion commits one atomic episode", func(t *testing.T) {
		list := newList(t)
		change(t, list)
		job, lease := notificationShoppingStage(t, f, reader.UserID, list)
		if err := f.store.CompleteShoppingNotificationJob(f.ctx, job, value.UUID()); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign digest lease accepted", err)
		}
		intent(t, job, 0)
		results := make(chan error, 2)
		for range 2 {
			go func() { results <- f.store.CompleteShoppingNotificationJob(f.ctx, job, lease) }()
		}
		successes, conflicts := 0, 0
		for range 2 {
			err := <-results
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				t.Fatal("concurrent digest completion", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatal("digest lease completed more than once", successes, conflicts)
		}
		intent(t, job, 1)
		var notices int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1' AND dedupe_key LIKE $2`, reader.UserID, "%/"+list+"/%").Scan(&notices); err != nil || notices != 1 {
			t.Fatal("atomic digest receipt duplicated", notices, err)
		}
	})
	for _, action := range []string{"dismiss", "archive", "template", "unsubscribe", "push-off", "generation", "foreign-user", "revision", "payload"} {
		t.Run(action+" invalidates current source authority", func(t *testing.T) {
			list := newList(t)
			change(t, list)
			job, notice := finish(t, list)
			iid := intent(t, job, 1)
			i := notificationChoreAudience(t, f, iid, true)
			switch action {
			case "dismiss":
				if _, err := f.app.Exec(f.ctx, `UPDATE core.notifications SET dismissed_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, notice, reader.UserID); err != nil {
					t.Fatal(err)
				}
			case "archive":
				// Archive is an authoritative checkout effect. Exercise its read
				// fence without constructing unrelated inventory checkout facts.
				if _, err := f.owner.Exec(f.ctx, `UPDATE shopping.lists SET archived_at=clock_timestamp() WHERE id=$1`, list); err != nil {
					t.Fatal(err)
				}
			case "template":
				notificationShoppingWrite(t, f, f.identity, "lists", list, 1, Record{"is_template": true})
			case "unsubscribe":
				notificationShoppingWatch(t, f, reader, list, false, false, 0)
			case "push-off":
				notificationShoppingWatch(t, f, reader, list, true, false, 0)
			case "generation":
				i.Generation++
			case "foreign-user":
				i.UserID = f.identity.UserID
			case "revision":
				i.Revision++
			case "payload":
				if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET payload=jsonb_set(payload,'{watch_revision}','"999999"'::jsonb) WHERE id=$1`, job); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(tx)
			allowed, err := f.store.NotificationAudience(f.ctx, tx, i)
			if err != nil || allowed {
				t.Fatal("obsolete shopping authority accepted", action, err)
			}
		})
	}
	t.Run("deleted watched list keeps its final safe notice", func(t *testing.T) {
		list := newList(t)
		m, _ := f.store.Module("shopping")
		entity, _ := m.Entity("lists")
		if err := f.store.Delete(f.ctx, f.identity, m, entity, list, 1, f.store.States()["shopping"].Generation, value.UUID()); err != nil {
			t.Fatal(err)
		}
		job, notice := finish(t, list)
		iid := intent(t, job, 1)
		notificationChoreAudience(t, f, iid, true)
		view, err := f.store.ShoppingNotificationProjection(f.ctx, reader, notice)
		if err != nil || view.TargetPath != "/notifications" {
			t.Fatal("deleted-list safe projection", view, err)
		}
	})
	t.Run("first save fences absent-row management decision before account locks", func(t *testing.T) {
		fresh := financeAuthorityMember(t, f, "reader")
		blocker, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(blocker)
		if _, err = blocker.Exec(f.ctx, `SELECT id FROM core.users WHERE id=$1 FOR UPDATE`, fresh.UserID); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
		defer cancel()
		saved := make(chan error, 1)
		go func() {
			_, e := f.store.SaveNotificationPreference(ctx, fresh, NotificationPreference{Category: "shopping", Enabled: false, QuietHours: notification.QuietHours{Timezone: "UTC"}})
			saved <- e
		}()
		// The first writer owns the advisory key while waiting for the account
		// row. A missing preference row must not let another channel slip past.
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
		for {
			var waiting bool
			if err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE NOT granted AND locktype='transactionid')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-poll.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		type management struct {
			managed bool
			err     error
		}
		checked := make(chan management, 1)
		go func() {
			tx, e := f.app.Begin(ctx)
			if e != nil {
				checked <- management{err: e}
				return
			}
			defer rollback(tx)
			owned, e := LockShoppingNotificationManagement(ctx, tx, fresh.HouseholdID, fresh.UserID)
			checked <- management{owned, e}
		}()
		select {
		case v := <-checked:
			t.Fatal("missing-row decision bypassed in-progress first save", v)
		case <-time.After(40 * time.Millisecond):
		}
		if err = blocker.Rollback(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-saved; err != nil {
			t.Fatal(err)
		}
		v := <-checked
		if v.err != nil || !v.managed {
			t.Fatal("committed disabled row did not own channel", v)
		}
	})
}
