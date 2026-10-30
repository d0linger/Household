package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"go/build"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/value"
)

func TestPushConfigurationAndGeneratedKeyConsistency(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal("key generation failed")
	}
	decoded, err := decodeKey(private, 32)
	if err != nil {
		t.Fatal("generated private key is not canonical")
	}
	key, err := ecdh.P256().NewPrivateKey(decoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) != public {
		t.Fatal("generated key pair differs")
	}
	cfg := config.Config{WebPushEnabled: true, WebPushPrivateKey: private, WebPushContact: "mailto:operator@example.org"}
	s, err := New(nil, nil, nil, cfg, nil)
	if err != nil {
		t.Fatal("valid setup refused")
	}
	defer s.Close()
	view := s.Configuration()
	if view.State != "ready" || view.ApplicationServerKey == nil || *view.ApplicationServerKey != public || view.VAPIDKeyID == nil || !hexDigest(*view.VAPIDKeyID) {
		t.Fatal("public setup projection is inconsistent")
	}
	for _, tc := range []struct {
		name    string
		cfg     config.Config
		state   string
		invalid bool
	}{
		{"off", config.Config{}, "disabled", false},
		{"missing", config.Config{WebPushEnabled: true}, "not_configured", false},
		{"invalid even when off", config.Config{WebPushPrivateKey: "invalid"}, "", true},
		{"zero scalar", config.Config{WebPushPrivateKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}, "", true},
		{"contact control", config.Config{WebPushContact: "mailto:operator@example.org\n"}, "", true},
		{"wrong port", config.Config{WebPushContact: "https://example.org:8443/contact"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := New(nil, nil, nil, tc.cfg, nil)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid configured value accepted")
				}
				return
			}
			if err != nil {
				t.Fatal("optional setup refused")
			}
			defer candidate.Close()
			if candidate.Configuration().State != tc.state {
				t.Fatal("incorrect setup state")
			}
		})
	}
}

func TestPushScopesAreNonBearerAndSessionAuthorityBound(t *testing.T) {
	id := auth.Identity{UserID: value.UUID(), HouseholdID: value.UUID(), SessionID: value.UUID(), AuthVersion: 1, Role: "reader"}
	first := personal(id)
	if !hexDigest(first.AuthScope) || !hexDigest(first.SessionScope) || first.AuthScope == id.SessionID || first.SessionScope == id.SessionID {
		t.Fatal("scope is not a non-bearer digest")
	}
	changed := id
	changed.AuthVersion++
	second := personal(changed)
	if first.AuthScope == second.AuthScope || first.SessionScope != second.SessionScope {
		t.Fatal("auth revision boundary lost")
	}
	changed = id
	changed.SessionID = value.UUID()
	third := personal(changed)
	if first.AuthScope == third.AuthScope || first.SessionScope == third.SessionScope {
		t.Fatal("session boundary lost")
	}
	changed = id
	changed.HouseholdID = value.UUID()
	if first.AuthScope == personal(changed).AuthScope {
		t.Fatal("household boundary lost")
	}
}

func TestPushVendorAuthorityAndOpaqueEndpointIdentity(t *testing.T) {
	for _, endpoint := range []string{"https://fcm.googleapis.com/a%2Fb?token=A%2B%2F&x=1", "https://updates.push.services.mozilla.com/wpush/v2/token", "https://web.push.apple.com/token", "https://wns2-sg2p.notify.windows.com/w/?token=1"} {
		if _, err := validateEndpoint(endpoint); err != nil {
			t.Fatal("reviewed vendor refused")
		}
	}
	for _, endpoint := range []string{"http://fcm.googleapis.com/token", "https://fcm.googleapis.com:8443/token", "https://127.0.0.1/token", "https://169.254.169.254/token", "https://fcm.googleapis.com.evil.example/token", "https://notify.windows.com/token", "https://wns.notify.windows.com.evil.example/token", "https://a.b.push.apple.com/token", "https://user@fcm.googleapis.com/token", "https://fcm.googleapis.com/token#", "https://fcm.googleapis.com/token\n", "https://fcm.googleapis.com:/token"} {
		if _, err := validateEndpoint(endpoint); err == nil {
			t.Fatal("unapproved endpoint accepted")
		}
	}
	canonical, err := canonicalEndpoint("https://FCM.GOOGLEAPIS.COM:443/a%2fb?token=%2f%2F+A&x=")
	if err != nil || canonical != "https://fcm.googleapis.com/a%2fb?token=%2f%2F+A&x=" {
		t.Fatal("authority canonicalization rewrote opaque receiver data")
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "::ffff:127.0.0.1", "2001:db8::1", "2002::1", "fec0::1", "3fff::1"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("non-public address accepted")
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("public address refused")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = publicDial(ctx, "tcp", "fcm.googleapis.com:443"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled dial did not stop before DNS")
	}
}

func TestPushProductionTransportHasNoProxyRedirectOrUnboundedBody(t *testing.T) {
	s := newSender("", "", "")
	defer s.close()
	client := s.client.(*checkedClient).client
	if s.httpTransport.Proxy != nil || s.httpTransport.DialContext == nil || client.Timeout != 4*time.Second || s.httpTransport.MaxResponseHeaderBytes != 16<<10 || cap(s.slots) != 2 {
		t.Fatal("transport resource boundary differs")
	}
	if !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("redirect enabled")
	}
	for _, raw := range []string{`{"schema":1,"schema":2}`, `{"schema":1,"unknown":true}`, `{"schema":1} {}`, `{"schema":1,"kind":"x","id":[]} `} {
		var payload pushPayload
		if strictJSON([]byte(raw), &payload) == nil {
			t.Fatal("malformed closed payload accepted")
		}
	}
}

func TestPushReceiverKeysAndRetryDecisionsRemainBounded(t *testing.T) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiver := Receiver{Endpoint: "https://fcm.googleapis.com/controlled-token", Keys: ReceiverKeys{P256DH: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
	if err = validateReceiver(receiver); err != nil {
		t.Fatal("valid receiver refused")
	}
	bad := receiver
	bad.Keys.P256DH = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	if validateReceiver(bad) == nil {
		t.Fatal("invalid P-256 point accepted")
	}
	bad = receiver
	bad.Keys.Auth += "="
	if validateReceiver(bad) == nil {
		t.Fatal("noncanonical receiver secret accepted")
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for _, raw := range []string{"9999999999999999999999999999999", "999999999", "-1", "0", "Thu, 29 Oct 2026 13:00:00 GMT", "malformed"} {
		until := retryAfter(raw, now)
		if !until.After(now) || until.After(now.Add(24*time.Hour)) {
			t.Fatal("Retry-After exceeds time bounds")
		}
	}
	if retryAfter("3600", now) != now.Add(time.Hour) {
		t.Fatal("Retry-After seconds changed")
	}
	for attempt := 0; attempt < 10; attempt++ {
		delay := retryDelay(attempt, "fixed-job")
		if delay < time.Minute || delay > 7*time.Hour {
			t.Fatal("retry delay is not bounded")
		}
	}
	encoded, _ := json.Marshal(pushPayload{Schema: 1, Kind: "shopping_notice", ID: value.UUID(), ExpiresAt: now.Unix(), AudienceScope: strings.Repeat("a", 64)})
	if strings.Contains(string(encoded), "endpoint") || strings.Contains(string(encoded), "url") || strings.Contains(string(encoded), "list") {
		t.Fatal("private push fields escaped closed payload")
	}
}

func TestPushTestSetterIsExcludedFromProductionFiles(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	directory := filepath.Dir(filename)
	production := build.Default
	production.BuildTags = nil
	match, err := production.MatchFile(directory, "testtools.go")
	if err != nil || match {
		t.Fatal("test setter enters default production file list")
	}
	controlled := production
	controlled.BuildTags = []string{"testtools"}
	match, err = controlled.MatchFile(directory, "testtools.go")
	if err != nil || !match {
		t.Fatal("explicit controlled test build omits its seam")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "testtools.go" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "SetHTTPClientForTests") {
			t.Fatal("another production setter was introduced")
		}
	}
}

type pushGateRoundTripper func(*http.Request) (*http.Response, error)

func (f pushGateRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPushCheckedClientRejectsBeforeTransportAndPreservesOpaqueTarget(t *testing.T) {
	const endpoint = "https://fcm.googleapis.com/opaque%2fsegment?token=A%2B%2F&empty="
	calls := 0
	client := &checkedClient{client: &http.Client{Transport: pushGateRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != endpoint || request.ContentLength != 3 {
			return nil, errors.New("allowed request changed before the controlled transport")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
	})}}
	request := func(method, target string, length int64) *http.Request {
		t.Helper()
		value, err := http.NewRequestWithContext(t.Context(), method, target, strings.NewReader("abc"))
		if err != nil {
			t.Fatal("invalid controlled request fixture")
		}
		value.ContentLength = length
		return value
	}
	for _, tc := range []struct {
		name    string
		request *http.Request
	}{
		{"nil_request", nil},
		{"nil_url", &http.Request{Method: http.MethodPost}},
		{"wrong_method", request(http.MethodGet, endpoint, 3)},
		{"unknown_length", request(http.MethodPost, endpoint, -1)},
		{"over_8kib", request(http.MethodPost, endpoint, 8193)},
		{"plain_http", request(http.MethodPost, "http://fcm.googleapis.com/token", 3)},
		{"foreign_host", request(http.MethodPost, "https://fcm.googleapis.com.evil.example/token", 3)},
		{"loopback", request(http.MethodPost, "https://127.0.0.1/token", 3)},
		{"userinfo", request(http.MethodPost, "https://user@fcm.googleapis.com/token", 3)},
		{"wrong_port", request(http.MethodPost, "https://fcm.googleapis.com:8443/token", 3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := client.Do(tc.request)
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil || response != nil || calls != 0 {
				t.Fatal("denied request reached the controlled transport")
			}
		})
	}
	response, err := client.Do(request(http.MethodPost, endpoint, 3))
	if err != nil || response == nil {
		t.Fatal("reviewed opaque target was refused")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent || calls != 1 {
		t.Fatal("reviewed request did not reach the controlled transport exactly once")
	}
}
