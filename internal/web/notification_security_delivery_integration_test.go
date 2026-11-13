//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/notification"
	"household/internal/secretbox"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
)

type securityDeliveryFixture struct {
	actor                              auth.Identity
	token, event, connection, delivery string
	input                              webpush.RegisterInput
	key                                *ecdh.PrivateKey
	account                            store.NotificationPreference
	device                             webpush.DevicePreference
}

func securityDeliveryEnroll(t *testing.T, f *integrationFixture, service *webpush.Service, name string) securityDeliveryFixture {
	t.Helper()
	actor := shoppingTestMember(t, f, name, "reader")
	q := securityDeliveryFixture{}
	var err error
	q.token, q.actor, err = f.app.Auth.Login(f.ctx, actor.Username, integrationPassword, "", "127.0.0.1", "PRIVATE security receiver")
	if err != nil {
		t.Fatal(err)
	}
	q.key, err = ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	q.input = webpush.RegisterInput{DeviceID: value.UUID(), VAPIDKeyID: *service.Configuration().VAPIDKeyID, Subscription: webpush.Receiver{Endpoint: "https://fcm.googleapis.com/controlled-security/" + value.UUID(), Keys: webpush.ReceiverKeys{P256DH: base64.RawURLEncoding.EncodeToString(q.key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(secret)}}}
	q.account, err = f.app.Store.SaveNotificationPreference(f.ctx, q.actor, store.NotificationPreference{Category: "security", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Register(f.ctx, q.actor, q.input)
	if err != nil {
		t.Fatal(err)
	}
	q.connection = connection.ID
	q.device, err = service.SaveDevicePreference(f.ctx, q.actor, q.connection, webpush.DevicePreference{Category: "security", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func securityDeliveryEvent(t *testing.T, f *integrationFixture, q *securityDeliveryFixture) {
	t.Helper()
	_, other, err := f.app.Auth.Login(f.ctx, q.actor.Username, integrationPassword, "", "127.0.0.2", "PRIVATE revoked device")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.app.Auth.RevokeSession(f.ctx, q.actor, other.SessionID, "session_revoked"); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT id::text FROM core.notifications WHERE user_id=$1 AND kind='security_sessions_revoked' ORDER BY created_at DESC LIMIT 1", q.actor.UserID).Scan(&q.event); err != nil {
		t.Fatal(err)
	}
}

func securityDeliveryStage(t *testing.T, f *integrationFixture, service *webpush.Service) {
	t.Helper()
	for range 20 {
		n, err := f.app.Store.StageSecurityNotifications(f.ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	for range 8 {
		if err := service.ScheduleNotifications(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func securityDeliveryPostSlot(t *testing.T, f *integrationFixture, service *webpush.Service, q securityDeliveryFixture, action string) {
	t.Helper()
	type lease struct{ job, token string }
	var occupied []lease
	for _, suffix := range []string{"a", "b"} {
		actor := shoppingTestMember(t, f, "security-slot-"+strings.ReplaceAll(value.UUID(), "-", "")[:12]+suffix, "reader")
		connection, err := service.Register(f.ctx, actor, shoppingPushInput(t, service))
		if err != nil {
			t.Fatal(err)
		}
		delivery, err := service.QueueTest(f.ctx, actor, connection.ID)
		if err != nil {
			t.Fatal(err)
		}
		job, token := notificationClaimFixture(t, f, delivery.ID)
		occupied = append(occupied, lease{job, token})
	}
	entered, release := make(chan struct{}, 3), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	var wg sync.WaitGroup
	results := make(chan error, 3)
	defer func() { unblock(); cancel(); wg.Wait() }()
	for _, item := range occupied {
		wg.Go(func() { results <- service.SendNotificationJob(ctx, item.job, item.token) })
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("security transport slot not occupied")
		}
	}
	job, token := notificationClaimFixture(t, f, q.delivery)
	wg.Go(func() { results <- service.SendNotificationJob(ctx, job, token) })
	until := time.Now().Add(time.Second)
	for {
		var reserved bool
		if err := f.pool.QueryRow(f.ctx, "SELECT last_attempt_at IS NOT NULL FROM core.notification_deliveries WHERE id=$1", q.delivery).Scan(&reserved); err != nil {
			t.Fatal(err)
		}
		if reserved {
			break
		}
		if time.Now().After(until) {
			t.Fatal("security preflight did not reach slotwait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	switch action {
	case "account consent":
		pref := q.account
		pref.Enabled = false
		saved, err := f.app.Store.SaveNotificationPreference(f.ctx, q.actor, pref)
		if err != nil || saved.Enabled || saved.EnabledSince != nil || saved.Version != pref.Version+1 {
			t.Fatal("actual account withdrawal", err)
		}
	case "device consent":
		pref := q.device
		pref.Enabled = false
		saved, err := service.SaveDevicePreference(f.ctx, q.actor, q.connection, pref)
		if err != nil || saved.Enabled || saved.EnabledSince != nil || saved.Version != pref.Version+1 {
			t.Fatal("actual device withdrawal", err)
		}
	case "session revoked":
		if err := f.app.Auth.RevokeSession(f.ctx, q.actor, q.actor.SessionID, "session_revoked"); err != nil {
			t.Fatal(err)
		}
	case "source dismissed":
		r, _, err := f.request(http.MethodPost, "/notifications/"+q.event+"/dismiss", q.token, f.app.Auth.CSRF(q.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(""), nil)
		if err != nil || r.StatusCode != 303 {
			t.Fatal("actual source dismissal", err)
		}
	default:
		t.Fatal("unknown security withdrawal")
	}
	unblock()
	for range 3 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("security controlled sender timed out")
		}
	}
	var state string
	if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.delivery).Scan(&state); err != nil || state != "cancelled" || calls.Load() != 2 {
		t.Fatal("ended security delivery crossed finalguard", state, calls.Load(), err)
	}
}

func TestNotificationSecurityControlledDeliveryAndFinalGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	scenarios := []string{"accepted", "account consent", "device consent", "session revoked", "source dismissed"}
	cases := make([]securityDeliveryFixture, len(scenarios))
	for i := range cases {
		cases[i] = securityDeliveryEnroll(t, f, service, "security-delivery-"+strconv.Itoa(i))
		securityDeliveryEvent(t, f, &cases[i])
	}
	securityDeliveryStage(t, f, service)
	for i := range cases {
		q := &cases[i]
		var recipients int
		if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_type='security_event' AND i.resource_id=$1 AND i.user_id=$2 AND d.subscription_id=$3),
 (SELECT count(DISTINCT user_id) FROM core.notification_intents WHERE category='security' AND resource_id=$1)`, q.event, q.actor.UserID, q.connection).Scan(&q.delivery, &recipients); err != nil || recipients != 1 {
			t.Fatal("personal security delivery missing or broadcast", err)
		}
	}
	for i, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			q := cases[i]
			if scenario != "accepted" {
				securityDeliveryPostSlot(t, f, service, q, scenario)
				return
			}
			var calls atomic.Int32
			if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				raw, err := io.ReadAll(io.LimitReader(req.Body, 8193))
				if err != nil {
					return nil, err
				}
				if req.Header.Get("Content-Encoding") != "aes128gcm" || req.Header.Get("Authorization") == "" || req.URL.String() != q.input.Subscription.Endpoint {
					return nil, errors.New("security push protocol mismatch")
				}
				clear, err := notificationDocumentClear(raw, q.key, q.input.Subscription.Keys.Auth)
				if err != nil {
					return nil, err
				}
				var message struct {
					Schema  int    `json:"schema"`
					Kind    string `json:"kind"`
					ID      string `json:"id"`
					Expires int64  `json:"expires_at"`
					Scope   string `json:"audience_scope"`
				}
				decoder := json.NewDecoder(bytes.NewReader(clear))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&message) != nil || decoder.Decode(new(any)) != io.EOF || message.Schema != 2 || message.Kind != "security" || message.ID != q.delivery || !notification.ValidMessage(message.Schema, message.Kind, message.ID, message.Scope, message.Expires, time.Now().Unix()) || bytes.Contains(clear, []byte("PRIVATE")) || bytes.Contains(clear, []byte(q.actor.UserID)) || bytes.Contains(clear, []byte(q.event)) {
					return nil, errors.New("security payload violates reference-only privacy contract")
				}
				return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
			})); err != nil {
				t.Fatal(err)
			}
			job, token := notificationClaimFixture(t, f, q.delivery)
			if err := service.SendNotificationJob(f.ctx, job, token); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.delivery).Scan(&state); err != nil || state != "accepted" || calls.Load() != 1 {
				t.Fatal("real encrypted controlled acceptance", state, calls.Load(), err)
			}
		})
	}
}

func TestNotificationSecurityDeviceHooksAndNoHistoricalReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	q := securityDeliveryEnroll(t, f, service, "security-device-history")
	personalSecurityExpect(t, f, q.actor.UserID, "device_authorized", 1)
	if _, err := service.Register(f.ctx, q.actor, q.input); err != nil {
		t.Fatal("ordinary registration refresh", err)
	}
	personalSecurityExpect(t, f, q.actor.UserID, "device_authorized", 1)
	securityDeliveryEvent(t, f, &q)
	late, err := service.Register(f.ctx, q.actor, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveDevicePreference(f.ctx, q.actor, late.ID, webpush.DevicePreference{Category: "security", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	personalSecurityExpect(t, f, q.actor.UserID, "device_authorized", 2)
	securityDeliveryStage(t, f, service)
	var lateCount, originalCount int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER(WHERE d.subscription_id=$2),count(*) FILTER(WHERE d.subscription_id=$3) FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_id=$1 AND i.category='security'`, q.event, late.ID, q.connection).Scan(&lateCount, &originalCount); err != nil || lateCount != 0 || originalCount != 1 {
		t.Fatal("new device replayed historical event", lateCount, originalCount, err)
	}
	// Reauthorization after an actual session transition is a new event, unlike
	// refreshing the same still-authorized device envelope.
	if err = f.app.Auth.RevokeSession(f.ctx, q.actor, q.actor.SessionID, "logout"); err != nil {
		t.Fatal(err)
	}
	_, fresh, err := f.app.Auth.Login(f.ctx, q.actor.Username, integrationPassword, "", "127.0.0.1", "fresh device session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Register(f.ctx, fresh, q.input); err != nil {
		t.Fatal("reauthorize device", err)
	}
	personalSecurityExpect(t, f, q.actor.UserID, "device_authorized", 3)
	// A personal inbox event that predates account opt-in is never rediscovered.
	actor := shoppingTestMember(t, f, "security-late-account", "reader")
	historical := securityDeliveryFixture{actor: actor}
	securityDeliveryEvent(t, f, &historical)
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, actor, store.NotificationPreference{Category: "security", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	securityDeliveryStage(t, f, service)
	var intents int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM core.notification_intents WHERE category='security' AND resource_id=$1", historical.event).Scan(&intents); err != nil || intents != 0 {
		t.Fatal("account opt-in replayed old personal inbox", intents, err)
	}
}

func TestNotificationSecurityMissingRetainedKeyPreservesDeviceAndDelivery(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	q := securityDeliveryEnroll(t, f, service, "security-missing-key")
	securityDeliveryEvent(t, f, &q)
	securityDeliveryStage(t, f, service)
	if err := f.pool.QueryRow(f.ctx, `SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id WHERE i.resource_id=$1 AND i.category='security' AND d.subscription_id=$2`, q.event, q.connection).Scan(&q.delivery); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.pool.QueryRow(f.ctx, "SELECT to_jsonb(s)::text FROM core.push_subscriptions s WHERE id=$1", q.connection).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, secret, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg.EncryptionKeys, err = secretbox.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := webpush.New(f.pool, f.app.Auth, f.app.Store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(missing.Close)
	if _, err = missing.Register(f.ctx, q.actor, q.input); !errors.Is(err, secretbox.ErrKeyUnavailable) {
		t.Fatal("missing reader silently rewrote registration", err)
	}
	if err = missing.Housekeeping(f.ctx, 100); !errors.Is(err, secretbox.ErrKeyUnavailable) {
		t.Fatal("missing reader silently retired device", err)
	}
	job, lease := notificationClaimFixture(t, f, q.delivery)
	var calls atomic.Int32
	client := shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})
	if err = missing.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	if err = missing.SendNotificationJob(f.ctx, job, lease); !errors.Is(err, secretbox.ErrKeyUnavailable) {
		t.Fatal("missing reader cancelled or transmitted pending delivery", err)
	}
	var after, state, jobState string
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT to_jsonb(s)::text FROM core.push_subscriptions s WHERE id=$1),(SELECT state FROM core.notification_deliveries WHERE id=$2),(SELECT state FROM core.jobs WHERE id=$3)`, q.connection, q.delivery, job).Scan(&after, &state, &jobState); err != nil || before != after || state != "pending" || jobState != "running" || calls.Load() != 0 {
		t.Fatal("unavailable key changed live device or terminal delivery state", state, jobState, calls.Load(), err)
	}
	if err = service.SetHTTPClientForTests(client); err != nil {
		t.Fatal(err)
	}
	if err = service.SendNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal("retained reader restored delivery", err)
	}
	if err = f.pool.QueryRow(f.ctx, "SELECT state FROM core.notification_deliveries WHERE id=$1", q.delivery).Scan(&state); err != nil || state != "accepted" || calls.Load() != 1 {
		t.Fatal("restored reader did not recover same owned job", state, calls.Load(), err)
	}
}
