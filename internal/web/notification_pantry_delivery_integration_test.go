//go:build integration && testtools

package web

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
)

func notificationPantryWebScan(t *testing.T, f *integrationFixture) {
	t.Helper()
	for range 8 {
		if _, err := f.app.Store.StagePantryNotifications(f.ctx, 50); err != nil {
			t.Fatal("scan actual pantry pages", err)
		}
	}
}

func TestNotificationPantryControlledDeliveryEnrollmentAndWithdrawal(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		wire, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
		if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || len(wire) < 100 || len(wire) > 8192 ||
			req.Header.Get("Urgency") != "low" || bytes.Contains(wire, []byte("PRIVATE-PANTRY")) || bytes.Contains(wire, []byte("audience_scope")) {
			return nil, errors.New("pantry transport lost encryption or bounded generic payload")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"accepted", "late-device", "late-registration", "restocked", "account-withdrawn", "device-withdrawn", "logged-out"} {
		t.Run(action, func(t *testing.T) {
			actor := shoppingTestMember(t, f, "pantry-"+action, "reader")
			pref, err := f.app.Store.SaveNotificationPreference(f.ctx, actor, store.NotificationPreference{Category: "pantry", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}})
			if err != nil {
				t.Fatal(err)
			}
			connection := ""
			var device webpush.DevicePreference
			register := func(t *testing.T) {
				t.Helper()
				registered, e := service.Register(f.ctx, actor, shoppingPushInput(t, service))
				if e != nil {
					t.Fatal(e)
				}
				connection = registered.ID
			}
			enable := func(t *testing.T) {
				t.Helper()
				var e error
				device, e = service.SaveDevicePreference(f.ctx, actor, connection, webpush.DevicePreference{Category: "pantry", Enabled: true})
				if e != nil {
					t.Fatal(e)
				}
			}
			if action != "late-registration" {
				register(t)
				if action != "late-device" {
					enable(t)
				}
			}
			product, lot := pantryFixture(t, f, "PRIVATE-PANTRY "+action)
			m, _ := f.app.Store.Module("pantry")
			e, _ := m.Entity("products")
			if _, err := f.app.Store.Write(f.ctx, f.identity, m, e, store.String(product["id"]), value.UUID(), value.UUID(),
				auditMutation(t, f, "pantry", store.Record{"low_stock_threshold": "2"}, store.Int(product["version"]))); err != nil {
				t.Fatal(err)
			}
			lotID := store.String(lot["id"])
			auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "reason": "receive", "quantity_delta": "3"})
			notificationPantryWebScan(t, f)
			switch action {
			case "late-registration":
				register(t)
				enable(t)
			case "late-device":
				enable(t)
			}
			consume := pantryStockInput(t, f, lotID)
			consume.Quantity = "2"
			if _, err := f.app.Store.MutatePantryStock(f.ctx, f.identity, lotID, value.UUID(), value.UUID(), consume); err != nil {
				t.Fatal(err)
			}
			notificationPantryWebScan(t, f)
			var digest string
			lease := value.UUID()
			if err := f.owner.QueryRow(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$3,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 WHERE kind=$1 AND payload->>'user_id'=$2 AND state='pending' AND available_at<=clock_timestamp() RETURNING id::text`,
				store.PantryNotificationDigestJobKind, actor.UserID, lease).Scan(&digest); err != nil {
				t.Fatal("claim real daily pantry digest", err)
			}
			if err := f.app.Store.CompletePantryNotificationDigest(f.ctx, digest, lease); err != nil {
				t.Fatal(err)
			}
			var intent string
			if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_intents WHERE category='pantry' AND resource_id=$1 AND user_id=$2`, digest, actor.UserID).Scan(&intent); err != nil {
				t.Fatal("daily pantry intent missing", err)
			}
			if err := service.ScheduleNotifications(f.ctx); err != nil {
				t.Fatal(err)
			}
			var deliveries []string
			if err := f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_deliveries WHERE intent_id=$1 AND subscription_id=$2)`, intent, connection).Scan(&deliveries); err != nil {
				t.Fatal(err)
			}
			if action == "late-device" || action == "late-registration" {
				if len(deliveries) != 0 {
					t.Fatal("late device enrollment received old stock crossing", len(deliveries))
				}
				return
			}
			if len(deliveries) != 1 {
				t.Fatal("one subscribed device did not receive one staged delivery", len(deliveries))
			}
			switch action {
			case "restocked":
				auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": lotID, "reason": "receive", "quantity_delta": "3"})
			case "account-withdrawn":
				pref.Enabled = false
				if _, err := f.app.Store.SaveNotificationPreference(f.ctx, actor, pref); err != nil {
					t.Fatal(err)
				}
			case "device-withdrawn":
				device.Enabled = false
				if _, err := service.SaveDevicePreference(f.ctx, actor, connection, device); err != nil {
					t.Fatal(err)
				}
			case "logged-out":
				if err := f.app.Auth.Logout(f.ctx, actor); err != nil {
					t.Fatal(err)
				}
			}
			before := calls.Load()
			job, deliveryLease := notificationClaimFixture(t, f, deliveries[0])
			if err := service.SendNotificationJob(f.ctx, job, deliveryLease); err != nil {
				t.Fatal("send actual pantry delivery", err)
			}
			want := int32(0)
			if action == "accepted" {
				want = 1
			}
			if got := calls.Load() - before; got != want {
				t.Fatal("provider calls after final pantry authority check", got, want)
			}
		})
	}
}
