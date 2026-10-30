//go:build integration

package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
	"household/internal/worker"
)

func shoppingPushService(t *testing.T, f *integrationFixture, enabled bool) (*webpush.Service, config.Config) {
	t.Helper()
	private, _, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal("generate controlled fixture key")
	}
	cfg := f.app.Cfg
	cfg.WebPushEnabled = enabled
	cfg.WebPushPrivateKey = private
	cfg.WebPushContact = "mailto:integration@example.org"
	service, err := webpush.New(f.pool, f.app.Auth, f.app.Store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service, cfg
}

func shoppingPushInput(t *testing.T, s *webpush.Service) webpush.RegisterInput {
	t.Helper()
	_, public, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal("generate controlled receiver")
	}
	return webpush.RegisterInput{DeviceID: value.UUID(), VAPIDKeyID: *s.Configuration().VAPIDKeyID, Subscription: webpush.Receiver{Endpoint: "https://fcm.googleapis.com/controlled-test/" + value.UUID() + "?token=A%2B%2F", Keys: webpush.ReceiverKeys{P256DH: public, Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}}
}

func shoppingPushWatch(t *testing.T, f *integrationFixture, reader auth.Identity, listID string) store.ShoppingPreferenceView {
	t.Helper()
	generation := f.app.Store.States()["shopping"].Generation
	view, err := f.app.Store.SetShoppingNotificationPreferences(f.ctx, reader, listID, value.UUID(), store.ShoppingPreferenceInput{Generation: generation, Revision: "0", Enabled: true, PushEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func shoppingPushChange(t *testing.T, f *integrationFixture, listID string) {
	t.Helper()
	_, _ = auditCreate(t, f, "shopping", "items", store.Record{"list_id": listID, "name": "Controlled notification fixture", "quantity": "2", "unit_code": "piece"})
}

func TestShoppingPushHTTPReaderRegistrationAndClosedBoundaries(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	f.app.Push = service
	reader := shoppingTestMember(t, f, "push-http-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	token, reader, err := f.app.Auth.Login(f.ctx, reader.Username, integrationPassword, "", "127.0.0.1", "push-http-fixture")
	if err != nil {
		t.Fatal("authenticate the real reader HTTP fixture")
	}
	csrf := f.app.Auth.CSRF(token, "session")
	input := shoppingPushInput(t, service)
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal("encode the controlled registration fixture")
	}
	call := func(t *testing.T, method, path, cookie, csrfToken, data string, want int) []byte {
		t.Helper()
		response, raw, requestErr := f.request(method, path, cookie, csrfToken, "application/json", strings.NewReader(data), nil)
		if requestErr != nil || response == nil {
			t.Fatal("actual push HTTP request failed")
		}
		if response.StatusCode != want {
			t.Fatalf("push HTTP status = %d, want %d", response.StatusCode, want)
		}
		return raw
	}
	const endpoint = "/api/v1/core/push/subscriptions"
	call(t, http.MethodGet, "/api/v1/core/push/context", "", "", "", http.StatusUnauthorized)
	call(t, http.MethodPost, endpoint, "", "", string(body), http.StatusUnauthorized)
	call(t, http.MethodPost, endpoint, token, "", string(body), http.StatusForbidden)
	call(t, http.MethodPost, endpoint, token, "wrong-csrf", string(body), http.StatusForbidden)

	for _, bad := range []struct{ name, body string }{
		{"unknown_outer", strings.TrimSuffix(string(body), "}") + `,"extra":true}`},
		{"unknown_receiver", strings.Replace(string(body), `"endpoint":`, `"extra":true,"endpoint":`, 1)},
		{"unknown_key", strings.Replace(string(body), `"p256dh":`, `"extra":true,"p256dh":`, 1)},
		{"duplicate_device", strings.TrimSuffix(string(body), "}") + `,"device_id":"` + input.DeviceID + `"}`},
		{"wrong_case", strings.Replace(string(body), `"device_id":`, `"Device_id":`, 1)},
		{"null_receiver", `{"device_id":"` + input.DeviceID + `","vapid_key_id":"` + input.VAPIDKeyID + `","subscription":null}`},
		{"trailing_document", string(body) + `{}`},
		{"over_8kib", strings.TrimSuffix(string(body), "}") + `,"padding":"` + strings.Repeat("x", 8192) + `"}`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			call(t, http.MethodPost, endpoint, token, csrf, bad.body, http.StatusUnprocessableEntity)
		})
	}
	var before int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.push_subscriptions WHERE household_id=$1", reader.HouseholdID).Scan(&before); err != nil || before != 0 {
		t.Fatal("rejected HTTP requests changed subscription storage")
	}
	var context webpush.Context
	if err = json.Unmarshal(call(t, http.MethodGet, "/api/v1/core/push/context", token, "", "", http.StatusOK), &context); err != nil || context.Context.UserID != reader.UserID || context.Context.Role != "reader" {
		t.Fatal("Core push HTTP context did not preserve reader authority")
	}
	var accepted webpush.SubscriptionView
	if err = json.Unmarshal(call(t, http.MethodPost, endpoint, token, csrf, string(body), http.StatusOK), &accepted); err != nil || accepted.State != "registered" || accepted.DeviceID != input.DeviceID {
		t.Fatal("actual reader registration did not return its committed connection")
	}
	var own, foreign webpush.Subscriptions
	if err = json.Unmarshal(call(t, http.MethodGet, endpoint, token, "", "", http.StatusOK), &own); err != nil || len(own.Subscriptions) != 1 || own.Subscriptions[0].ID != accepted.ID {
		t.Fatal("actual own-device HTTP list does not match registration")
	}
	if err = json.Unmarshal(call(t, http.MethodGet, endpoint, f.token, "", "", http.StatusOK), &foreign); err != nil || len(foreign.Subscriptions) != 0 {
		t.Fatal("another current household member can see the reader connection")
	}
	path := endpoint + "/" + accepted.ID
	call(t, http.MethodDelete, path, f.token, f.app.Auth.CSRF(f.token, "session"), "", http.StatusNotFound)
	call(t, http.MethodDelete, path, token, "", "", http.StatusForbidden)
	var disabled webpush.Disabled
	if err = json.Unmarshal(call(t, http.MethodDelete, path, token, csrf, "", http.StatusOK), &disabled); err != nil || disabled.State != "disabled" {
		t.Fatal("actual reader withdrawal was not committed")
	}
	var retired bool
	if err = f.owner.QueryRow(f.ctx, "SELECT disabled_at IS NOT NULL FROM core.push_subscriptions WHERE id=$1 AND household_id=$2 AND user_id=$3", accepted.ID, reader.HouseholdID, reader.UserID).Scan(&retired); err != nil || !retired {
		t.Fatal("HTTP withdrawal did not persist the owned disabled state")
	}
}

func TestShoppingPushOwnReaderDeviceEncryptionAndPrivacy(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	reader := shoppingTestMember(t, f, "push-reader-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	in := shoppingPushInput(t, service)
	view, err := service.Register(f.ctx, reader, in)
	if err != nil {
		t.Fatal(err)
	}
	if view.DeviceID != in.DeviceID || view.State != "registered" {
		t.Fatal("own reader registration did not commit")
	}
	var ciphertext, hash []byte
	if err = f.owner.QueryRow(f.ctx, "SELECT encrypted_subscription,endpoint_hash FROM core.push_subscriptions WHERE id=$1", view.ID).Scan(&ciphertext, &hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || len(ciphertext) < 32 || bytes.Contains(ciphertext, []byte(in.Subscription.Endpoint)) || bytes.Contains(ciphertext, []byte(in.Subscription.Keys.Auth)) {
		t.Fatal("push receiver was not privately stored")
	}
	context, err := service.Context(f.ctx, reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(context)
	if bytes.Contains(raw, []byte(reader.SessionID)) || bytes.Contains(raw, []byte(in.Subscription.Endpoint)) || context.Context.AuthScope == reader.SessionID {
		t.Fatal("public context leaked a server-only identity or receiver")
	}
	list, err := service.Subscriptions(f.ctx, reader)
	if err != nil || len(list.Subscriptions) != 1 {
		t.Fatal("own connection not available")
	}
	st := f.app.Store.States()["shopping"]
	if err = f.app.Store.SetModule(f.ctx, f.identity, "shopping", false, st.Version, value.UUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Context(f.ctx, reader); err != nil {
		t.Fatal("Core personal push context incorrectly depends on Shopping availability")
	}
	if _, err = service.Disable(f.ctx, reader, view.ID); err != nil {
		t.Fatal("own withdrawal incorrectly depends on Shopping availability")
	}
	list, err = service.Subscriptions(f.ctx, reader)
	if err != nil || len(list.Subscriptions) != 1 || list.Subscriptions[0].State != "disabled" {
		t.Fatal("withdrawal not visible")
	}
}

func TestShoppingPushReaderQuotaEndpointOwnershipAndSessionRevocation(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	reader := shoppingTestMember(t, f, "push-owner-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	foreign := shoppingTestMember(t, f, "push-other-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	inputs := []webpush.RegisterInput{}
	views := []webpush.SubscriptionView{}
	for range 5 {
		input := shoppingPushInput(t, service)
		view, err := service.Register(f.ctx, reader, input)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
		views = append(views, view)
	}
	if _, err := service.Register(f.ctx, reader, shoppingPushInput(t, service)); err == nil {
		t.Fatal("sixth active device accepted")
	}
	stolen := shoppingPushInput(t, service)
	stolen.Subscription = inputs[0].Subscription
	stolen.Subscription.Endpoint = strings.Replace(stolen.Subscription.Endpoint, "https://fcm.googleapis.com/", "https://FCM.GOOGLEAPIS.COM:443/", 1)
	if _, err := service.Register(f.ctx, foreign, stolen); !errors.Is(err, store.ErrConflict) {
		t.Fatal("authority alias stole a foreign endpoint")
	}
	if _, err := service.Disable(f.ctx, foreign, views[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign connection withdrawal accepted")
	}
	var first, second []byte
	if err := f.owner.QueryRow(f.ctx, "SELECT encrypted_subscription FROM core.push_subscriptions WHERE id=$1", views[0].ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, "SELECT encrypted_subscription FROM core.push_subscriptions WHERE id=$1", views[1].ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.push_subscriptions SET encrypted_subscription=$2 WHERE id=$1", views[1].ID, first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Subscriptions(f.ctx, reader); !errors.Is(err, store.ErrConflict) {
		t.Fatal("encrypted row transplantation was trusted")
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.push_subscriptions SET encrypted_subscription=$2 WHERE id=$1", views[1].ID, second); err != nil {
		t.Fatal(err)
	}
	if err := f.app.Auth.Logout(f.ctx, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Context(f.ctx, reader); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked registering session remained authorized")
	}
	if _, err := service.Context(f.ctx, foreign); err != nil {
		t.Fatal("unrelated current reader was disabled")
	}
	if err := service.Housekeeping(f.ctx, 100); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.push_subscriptions WHERE user_id=$1 AND disabled_at IS NULL", reader.UserID).Scan(&active); err != nil || active != 0 {
		t.Fatal("revoked device sessions remain active")
	}
}

func TestShoppingPushWorkerTickContinuesWhenDeliveryNotConfigured(t *testing.T) {
	f := newIntegrationFixture(t)
	ready, cfg := shoppingPushService(t, f, true)
	reader := shoppingTestMember(t, f, "push-tick-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	if _, err := ready.Register(f.ctx, reader, shoppingPushInput(t, ready)); err != nil {
		t.Fatal(err)
	}
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Actual tick list"})
	listID := store.String(list["id"])
	shoppingPushWatch(t, f, reader, listID)
	shoppingPushChange(t, f, listID)
	cfg.WebPushEnabled = false
	disabled, err := webpush.New(f.pool, f.app.Auth, f.app.Store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer disabled.Close()
	w := worker.Worker{Pool: f.pool, Cfg: cfg, Push: disabled, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer func() { _ = w.Close() }()
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal("actual Tick did not progress through notification staging")
	}
	var staged int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.jobs WHERE kind=$1", store.ShoppingDigestJobKind).Scan(&staged); err != nil || staged != 1 {
		t.Fatal("actual v2 content event was not staged once")
	}
	// Fixture-only due time exposes dispatch without waiting for a civil minute.
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.jobs SET available_at=clock_timestamp()-interval '1 second' WHERE kind=$1", store.ShoppingDigestJobKind); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(f.ctx); err != nil {
		t.Fatal("actual Tick failed before digest/push/housekeeping completion")
	}
	var notices, unavailable, housekeeping, sent int
	err = f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM core.notifications WHERE user_id=$1 AND kind='shopping_list_changed_v1'),
 (SELECT count(*) FROM core.jobs WHERE kind=$2 AND state='done' AND last_error='push_not_configured'),
 (SELECT count(*) FROM core.jobs WHERE kind='housekeeping' AND state='done'),
 (SELECT count(*) FROM core.rate_limit_buckets WHERE scope='webpush-send-installation-v1')`, reader.UserID, store.ShoppingPushJobKind).Scan(&notices, &unavailable, &housekeeping, &sent)
	if err != nil || notices != 1 || unavailable != 1 || housekeeping < 1 || sent != 0 {
		t.Fatal("disabled optional push blocked in-app delivery or entered the send budget")
	}
}
