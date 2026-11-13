//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
)

func TestNotificationChoresControlledDeliveryQuietAndSourceFences(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["chores"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "chores", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
		state = f.app.Store.States()["chores"]
	}
	service, _ := shoppingPushService(t, f, true)
	connection, err := service.Register(f.ctx, f.identity, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveDevicePreference(f.ctx, f.identity, connection.ID, webpush.DevicePreference{Category: "chores_due", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	corePreference := store.NotificationPreference{Category: "chores_due", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}
	corePreference, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, corePreference)
	if err != nil {
		t.Fatal(err)
	}
	other := shoppingTestMember(t, f, "chore-delivery-new-assignee", "editor")
	calls := 0
	if err = service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls++
		wire, e := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, parseErr := strconv.Atoi(req.Header.Get("TTL"))
		if e != nil || parseErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" ||
			len(wire) < 100 || bytes.Contains(wire, []byte("audience_scope")) || bytes.Contains(wire, []byte("PRIVATE-CHORE")) {
			return nil, errors.New("chore notification did not preserve bounded encrypted transport")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	modulePrefs := store.ChorePreferencesInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation}, DailyMinutes: 30,
		NotificationMode: "legacy", DigestMinute: 540, Timezone: "UTC", EscalationAfterDays: 3}
	saveModuleQuiet := func(t *testing.T, start, end *int) {
		t.Helper()
		modulePrefs.QuietStart, modulePrefs.QuietEnd = start, end
		if _, e := f.app.Store.SaveChorePreferences(f.ctx, f.identity, value.UUID(), value.UUID(), modulePrefs); e != nil {
			t.Fatal(e)
		}
		if e := f.pool.QueryRow(f.ctx, `SELECT version FROM chores.member_preferences WHERE household_id=$1 AND user_id=$2`, f.identity.HouseholdID, f.identity.UserID).Scan(&modulePrefs.Version); e != nil {
			t.Fatal(e)
		}
	}
	quietWindow := func(t *testing.T) (int, int) {
		t.Helper()
		var now time.Time
		if e := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			t.Fatal(e)
		}
		minute := now.UTC().Hour()*60 + now.UTC().Minute()
		return (minute + 1439) % 1440, (minute + 120) % 1440
	}
	type queuedChore struct{ occurrence, delivery string }
	queue := func(t *testing.T) queuedChore {
		t.Helper()
		// The real legacy write path captures the occurrence and publishes its
		// domain records after all account/device consents and registration.
		template, _ := auditCreate(t, f, "chores", "task_templates", store.Record{"title": "PRIVATE-CHORE fixture", "active": true, "assignee_id": f.identity.MembershipID})
		var due time.Time
		if e := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&due); e != nil {
			t.Fatal(e)
		}
		// The generic legacy input contract stores whole seconds. Use the next
		// whole deadline so normalization cannot move it before fresh consent.
		due = due.UTC().Truncate(time.Second).Add(time.Second)
		occurrence, _ := auditCreate(t, f, "chores", "task_occurrences", store.Record{"template_id": template["id"], "occurrence_key": value.UUID(),
			"due_at": due.Format(time.RFC3339), "assignee_id": f.identity.MembershipID})
		waitCtx, stopWaiting := context.WithTimeout(f.ctx, 3*time.Second)
		defer stopWaiting()
		poll := time.NewTicker(20 * time.Millisecond)
		defer poll.Stop()
		for {
			var persisted time.Time
			var elapsed bool
			if e := f.pool.QueryRow(waitCtx, `SELECT due_at,clock_timestamp()>=due_at FROM chores.task_occurrences WHERE id=$1`, occurrence["id"]).Scan(&persisted, &elapsed); e != nil {
				t.Fatal("wait for actual persisted chore deadline", e)
			}
			if !persisted.Equal(due) {
				t.Fatal("legacy write changed the whole-second deadline")
			}
			if elapsed {
				break
			}
			select {
			case <-waitCtx.Done():
				t.Fatal("persisted chore deadline was not reached", waitCtx.Err())
			case <-poll.C:
			}
		}
		job, lease := value.UUID(), value.UUID()
		if _, e := f.owner.Exec(f.ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,dedupe_key,state,lease_token,lease_until,attempts)
 VALUES($1::uuid,$2,'chores',$3,$4,$1::uuid::text,'running',$5,clock_timestamp()+interval '60 seconds',1)`, job, f.identity.HouseholdID, state.Generation, store.ChoreWorkspaceJobKind, lease); e != nil {
			t.Fatal(e)
		}
		if e := f.app.Store.CompleteChoreWorkspaceJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		if e := service.ScheduleNotifications(f.ctx); e != nil {
			t.Fatal(e)
		}
		queued := queuedChore{occurrence: store.String(occurrence["id"])}
		if e := f.pool.QueryRow(f.ctx, `SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE i.resource_id=$1 AND i.category='chores_due' AND i.user_id=$2 AND d.subscription_id=$3 AND d.state='pending'`, queued.occurrence, f.identity.UserID, connection.ID).Scan(&queued.delivery); e != nil {
			t.Fatal("real chore intent was not staged to its opted-in device", e)
		}
		return queued
	}
	send := func(t *testing.T, q queuedChore) webpush.DeliveryView {
		t.Helper()
		job, lease := notificationClaimFixture(t, f, q.delivery)
		if e := service.SendNotificationJob(f.ctx, job, lease); e != nil {
			t.Fatal(e)
		}
		view, e := service.Delivery(f.ctx, f.identity, q.delivery)
		if e != nil {
			t.Fatal(e)
		}
		return view
	}
	t.Run("task-owned quiet settings override core quiet settings", func(t *testing.T) {
		saveModuleQuiet(t, nil, nil)
		start, end := quietWindow(t)
		corePreference.Start, corePreference.End = &start, &end
		corePreference, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, corePreference)
		if err != nil {
			t.Fatal(err)
		}
		q := queue(t)
		var due bool
		if err = f.pool.QueryRow(f.ctx, `SELECT j.available_at<=clock_timestamp() FROM core.jobs j JOIN core.notification_deliveries d ON d.job_id=j.id WHERE d.id=$1`, q.delivery).Scan(&due); err != nil || !due {
			t.Fatal("core quiet hours overrode authoritative task settings", due, err)
		}
		before := calls
		view := send(t, q)
		if view.State != "accepted" || view.SafeCode != "gateway_accepted" || calls != before+1 {
			t.Fatal("chore intent did not traverse encrypted controlled delivery", view, calls)
		}
	})
	t.Run("task quiet period cannot extend source freshness", func(t *testing.T) {
		corePreference.Start, corePreference.End = nil, nil
		corePreference, err = f.app.Store.SaveNotificationPreference(f.ctx, f.identity, corePreference)
		if err != nil {
			t.Fatal(err)
		}
		start, end := quietWindow(t)
		saveModuleQuiet(t, &start, &end)
		q := queue(t)
		var capped, unchanged bool
		if err = f.pool.QueryRow(f.ctx, `SELECT j.available_at=i.expires_at,i.expires_at=i.event_at+interval '1 hour'
 FROM core.jobs j JOIN core.notification_deliveries d ON d.job_id=j.id JOIN core.notification_intents i ON i.id=d.intent_id WHERE d.id=$1`, q.delivery).Scan(&capped, &unchanged); err != nil || !capped || !unchanged {
			t.Fatal("quiet scheduling changed source deadline or exceeded it", capped, unchanged, err)
		}
		// Claim deliberately early in the controlled fixture: the send preflight
		// must independently refuse a quiet deferral beyond the freshness window.
		before := calls
		view := send(t, q)
		if view.State != "expired" || view.SafeCode != "retry_window_exhausted" || calls != before {
			t.Fatal("quiet-expired chore reached provider", view, calls)
		}
	})
	for _, action := range []string{"complete", "reassign"} {
		t.Run(action+" before transport refuses old recipient", func(t *testing.T) {
			saveModuleQuiet(t, nil, nil)
			q := queue(t)
			var version int64
			if err = f.pool.QueryRow(f.ctx, `SELECT version FROM chores.task_occurrences WHERE id=$1`, q.occurrence).Scan(&version); err != nil {
				t.Fatal(err)
			}
			input := store.ChoreActionInput{ChoreCommand: store.ChoreCommand{Generation: state.Generation, Version: version}, Action: "complete"}
			if action == "reassign" {
				input.Action, input.AssigneeID = "edit_once", &other.MembershipID
			}
			if _, err = f.app.Store.MutateChoreOccurrence(f.ctx, f.identity, q.occurrence, value.UUID(), value.UUID(), input); err != nil {
				t.Fatal(err)
			}
			before := calls
			view := send(t, q)
			if view.State != "cancelled" && view.State != "expired" || calls != before {
				t.Fatal("superseded source reached provider", action, view, calls)
			}
		})
	}
}
