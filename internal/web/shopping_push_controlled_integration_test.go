//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
	"household/internal/worker"
)

type shoppingControlledPushClient func(*http.Request) (*http.Response, error)

func (f shoppingControlledPushClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

func shoppingControlledPushFixture(t *testing.T) (*integrationFixture, *webpush.Service, auth.Identity, webpush.RegisterInput, webpush.SubscriptionView, *worker.Worker) {
	t.Helper()
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	reader := shoppingTestMember(t, f, "push-control-"+strings.ReplaceAll(value.UUID(), "-", "")[:10], "reader")
	input := shoppingPushInput(t, service)
	connection, err := service.Register(f.ctx, reader, input)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := auditCreate(t, f, "shopping", "lists", store.Record{"name": "Controlled push dispatch"})
	listID := store.String(list["id"])
	shoppingPushWatch(t, f, reader, listID)
	shoppingPushChange(t, f, listID)
	if _, err = f.app.Store.StageShoppingNotifications(f.ctx, 20); err != nil {
		t.Fatal(err)
	}
	// Only fixture scheduling is accelerated; actual consumer/reference/lease
	// and current-authority checks remain in the normal Worker and Service.
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.jobs SET available_at=clock_timestamp()-interval '1 second' WHERE kind=$1", store.ShoppingDigestJobKind); err != nil {
		t.Fatal(err)
	}
	w := &worker.Worker{Pool: f.pool, Cfg: cfg, Push: service, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { _ = w.Close() })
	return f, service, reader, input, connection, w
}

func TestShoppingPushControlledEncryptedJobAndDatabaseBoundary(t *testing.T) {
	f, service, reader, _, _, w := shoppingControlledPushFixture(t)
	calls := 0
	boundary := false
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(request *http.Request) (*http.Response, error) {
		calls++
		wire, err := io.ReadAll(io.LimitReader(request.Body, 8193))
		if err != nil || len(wire) > 8192 || len(wire) < 100 || bytes.Contains(wire, []byte("shopping_notice")) {
			return nil, errors.New("controlled encrypted request is invalid")
		}
		if request.Header.Get("Content-Encoding") != "aes128gcm" || request.Header.Get("Authorization") == "" {
			return nil, errors.New("controlled protocol headers absent")
		}
		probeCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
		defer cancel()
		probe, err := f.owner.Begin(probeCtx)
		if err != nil {
			return nil, errors.New("controlled boundary probe unavailable")
		}
		defer func() { _ = probe.Rollback(probeCtx) }()
		for _, query := range []string{"SELECT id FROM core.households WHERE id=$1 FOR UPDATE NOWAIT", "SELECT module_id FROM core.module_states WHERE household_id=$1 AND module_id='shopping' FOR UPDATE NOWAIT"} {
			if _, err = probe.Exec(probeCtx, query, reader.HouseholdID); err != nil {
				return nil, errors.New("network boundary retained household or module locks")
			}
		}
		if _, err = probe.Exec(probeCtx, "SELECT id FROM core.sessions WHERE id=$1 FOR UPDATE NOWAIT", reader.SessionID); err != nil {
			return nil, errors.New("network boundary retained authentication locks")
		}
		boundary = true
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := w.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	var accepted, notices int
	err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM core.jobs WHERE kind=$1 AND state='done' AND last_error='gateway_accepted'),
 (SELECT count(*) FROM core.notifications WHERE user_id=$2 AND kind='shopping_list_changed_v1')`, store.ShoppingPushJobKind, reader.UserID).Scan(&accepted, &notices)
	if err != nil || calls != 1 || !boundary || accepted != 1 || notices != 1 {
		t.Fatal("actual controlled Worker push did not complete with its database boundary")
	}
}

func TestShoppingPushControlled429AndOldEndpointReplacement(t *testing.T) {
	t.Run("persistent429", func(t *testing.T) {
		f, service, _, _, _, w := shoppingControlledPushFixture(t)
		calls := 0
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 17<<10)))}, nil
		})); err != nil {
			t.Fatal(err)
		}
		if err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		var protected bool
		if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.rate_limit_buckets WHERE scope='webpush-cooldown-v1' AND window_end>clock_timestamp()+interval '59 minutes')
 AND EXISTS(SELECT 1 FROM core.jobs WHERE kind=$1 AND state='pending' AND last_error='gateway_limited' AND available_at>clock_timestamp()+interval '59 minutes')`, store.ShoppingPushJobKind).Scan(&protected); err != nil || !protected {
			t.Fatal("actual429 backoff was not durable")
		}
		if _, err := f.owner.Exec(f.ctx, "UPDATE core.jobs SET available_at=clock_timestamp()-interval '1 second' WHERE kind=$1", store.ShoppingPushJobKind); err != nil {
			t.Fatal(err)
		}
		if err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatal("persisted gateway cooldown allowed another outbound attempt")
		}
	})
	t.Run("old404doesnotdisablefreshconnection", func(t *testing.T) {
		f, service, reader, input, connection, w := shoppingControlledPushFixture(t)
		calls := 0
		replacement := input
		replacement.Subscription.Endpoint = "https://fcm.googleapis.com/controlled-replacement/" + value.UUID()
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls++
			view, err := service.Register(f.ctx, reader, replacement)
			if err != nil || view.ID != connection.ID {
				return nil, errors.New("controlled connection replacement failed")
			}
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("gone"))}, nil
		})); err != nil {
			t.Fatal(err)
		}
		if err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		var disabled bool
		var hash []byte
		if err := f.owner.QueryRow(f.ctx, "SELECT disabled_at IS NOT NULL,endpoint_hash FROM core.push_subscriptions WHERE id=$1", connection.ID).Scan(&disabled, &hash); err != nil {
			t.Fatal(err)
		}
		expected := sha256.Sum256([]byte(replacement.Subscription.Endpoint))
		if calls != 1 || disabled || !bytes.Equal(hash, expected[:]) {
			t.Fatal("old HTTP response retired a newly registered connection")
		}
	})
}

func TestShoppingPushControlledSessionWithdrawalAndLostLease(t *testing.T) {
	t.Run("sessionrevokedduringHTTP", func(t *testing.T) {
		f, service, reader, _, _, w := shoppingControlledPushFixture(t)
		calls := 0
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls++
			if err := f.app.Auth.Logout(f.ctx, reader); err != nil {
				return nil, errors.New("controlled session withdrawal failed")
			}
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		})); err != nil {
			t.Fatal(err)
		}
		if err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		var superseded bool
		if err := f.owner.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.jobs WHERE kind=$1 AND state='done' AND last_error='superseded_after_attempt')", store.ShoppingPushJobKind).Scan(&superseded); err != nil || calls != 1 || !superseded {
			t.Fatal("withdrawn session was presented as current gateway success")
		}
	})
	t.Run("oldHTTPcannotfinishnewlease", func(t *testing.T) {
		f, service, _, _, _, w := shoppingControlledPushFixture(t)
		newLease := value.UUID()
		calls := 0
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
			calls++
			tag, err := f.owner.Exec(f.ctx, "UPDATE core.jobs SET lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds' WHERE kind=$1 AND state='running'", store.ShoppingPushJobKind, newLease)
			if err != nil || tag.RowsAffected() != 1 {
				return nil, errors.New("controlled lease replacement failed")
			}
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
		})); err != nil {
			t.Fatal(err)
		}
		if err := w.Tick(f.ctx); err != nil {
			t.Fatal(err)
		}
		var retained bool
		if err := f.owner.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM core.jobs WHERE kind=$1 AND state='running' AND lease_token=$2 AND last_error IS NULL)", store.ShoppingPushJobKind, newLease).Scan(&retained); err != nil || calls != 1 || !retained {
			t.Fatal("old HTTP completion overwrote the current lease")
		}
	})
}
