package webpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"household/internal/value"
)

func TestDeviceWirePreservesEncryptionAndCategoryFreshness(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, receiverPrivate := receiverFixture(t)
	for _, tc := range []struct {
		kind, urgency string
		seconds       int64
	}{
		{"cooking_timer", "high", 60}, {"test", "low", 60}, {"documents", "low", 86400},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			s := newSender(public, private, "mailto:operator@example.org")
			defer s.close()
			payload, err := json.Marshal(pushPayload{Schema: 2, Kind: tc.kind, ID: value.UUID(), AudienceScope: strings.Repeat("a", 64), ExpiresAt: time.Now().Unix() + tc.seconds})
			if err != nil {
				t.Fatal(err)
			}
			calls, guards := 0, 0
			s.client = clientFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if guards != 1 || request.Header.Get("Urgency") != tc.urgency {
					t.Fatal("send skipped authority guard or changed category urgency")
				}
				ttl, err := strconv.ParseInt(request.Header.Get("TTL"), 10, 64)
				if err != nil || ttl < 1 || ttl > min(300, tc.seconds) {
					t.Fatal("provider lifetime exceeds message freshness", ttl, err)
				}
				wire, err := io.ReadAll(io.LimitReader(request.Body, 8193))
				if err != nil || len(wire) > 8192 {
					t.Fatal("unbounded wire", err)
				}
				if clear := decryptFixture(t, wire, receiverPrivate, receiver.Keys.Auth); !bytes.Equal(clear, payload) {
					t.Fatal("new wire payload was changed or not receiver-encrypted")
				}
				return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			result := s.sendAuthorized(t.Context(), receiver, payload, strings.Repeat("a", 32), func(context.Context) error { guards++; return nil })
			if result.code != "gateway_accepted" || calls != 1 || guards != 1 {
				t.Fatal("device payload not accepted as expected", result, calls, guards)
			}
		})
	}
}

func TestDeviceWireFailsClosedBeforeNetwork(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, _ := receiverFixture(t)
	s := newSender(public, private, "mailto:operator@example.org")
	defer s.close()
	notice := pushPayload{Schema: 2, Kind: "test", ID: value.UUID(), AudienceScope: strings.Repeat("a", 64), ExpiresAt: time.Now().Unix() + 60}
	valid, err := json.Marshal(notice)
	if err != nil {
		t.Fatal(err)
	}
	calls, guards := 0, 0
	s.client = clientFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected HTTP") })
	for _, raw := range []string{
		strings.Replace(string(valid), `"schema":2`, `"schema":2,"schema":2`, 1),
		strings.TrimSuffix(string(valid), "}") + `,"url":"https://outside.invalid"}`,
		strings.Replace(string(valid), `"kind":"test"`, `"kind":"arbitrary"`, 1),
	} {
		result := s.sendAuthorized(t.Context(), receiver, []byte(raw), strings.Repeat("b", 32), func(context.Context) error { guards++; return nil })
		if result.code != "invalid_payload" || guards != 0 || calls != 0 {
			t.Fatal("malformed payload crossed the guard/network boundary", result)
		}
	}
	result := s.sendAuthorized(t.Context(), receiver, valid, strings.Repeat("b", 32), func(context.Context) error { guards++; return errors.New("revoked identity") })
	if result.code != "authority_superseded" || guards != 1 || calls != 0 {
		t.Fatal("rejected authority reached provider", result)
	}
}

func TestDeviceWireRechecksExpiryAfterAuthorityGuard(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, _ := receiverFixture(t)
	s := newSender(public, private, "mailto:operator@example.org")
	defer s.close()
	expires := time.Now().Unix() + 2
	payload, err := json.Marshal(pushPayload{Schema: 2, Kind: "test", ID: value.UUID(), AudienceScope: strings.Repeat("a", 64), ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.client = clientFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected expired HTTP")
	})
	result := s.sendAuthorized(t.Context(), receiver, payload, strings.Repeat("b", 32), func(ctx context.Context) error {
		return waitForNotificationExpiry(ctx, expires)
	})
	if result.code != "invalid_payload" || calls != 0 {
		t.Fatal("message expired during guard reached provider", result)
	}
}

func TestDeviceWireRechecksExpiryAfterWaitingForSlot(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, _ := receiverFixture(t)
	s := newSender(public, private, "mailto:operator@example.org")
	defer s.close()
	expires := time.Now().Unix() + 2
	payload, err := json.Marshal(pushPayload{Schema: 2, Kind: "test", ID: value.UUID(), AudienceScope: strings.Repeat("a", 64), ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.client = clientFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected queued HTTP") })
	s.slots <- struct{}{}
	s.slots <- struct{}{}
	done := make(chan outcome, 1)
	go func() { done <- s.send(t.Context(), receiver, payload, strings.Repeat("b", 32)) }()
	if err := waitForNotificationExpiry(t.Context(), expires); err != nil {
		t.Fatal(err)
	}
	<-s.slots
	result := <-done
	<-s.slots
	if result.code != "invalid_payload" || calls != 0 {
		t.Fatal("queued expired notification reached provider", result)
	}
}

// A timer wakeup uses a monotonic deadline, while the wire contract uses Unix
// wall time. Verify the actual expiry condition after waking, including when
// the host adjusts its wall clock during a container test.
func waitForNotificationExpiry(ctx context.Context, expires int64) error {
	for time.Now().Unix() < expires {
		timer := time.NewTimer(max(time.Millisecond, time.Until(time.Unix(expires, 0))))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return nil
}
