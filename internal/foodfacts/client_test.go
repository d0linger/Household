package foodfacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (run roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func fixtureClient(t *testing.T, run roundTripFunc) *Client {
	t.Helper()
	client, err := New(Config{Enabled: true, UserAgent: "Household-test/1.0 (local-fixture)"})
	if err != nil {
		t.Fatal(err)
	}
	// Test-only replacement is private to this package. Production constructors
	// expose neither a custom URL nor a transport/DNS override.
	client.client.Transport = run
	return client
}

func fixtureResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestConfigurationIsOptionalAndStagingCannotEnterProduction(t *testing.T) {
	for _, test := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"disabled", Config{}, "disabled"},
		{"missing user agent", Config{Enabled: true}, "not_configured"},
		{"blank user agent", Config{Enabled: true, UserAgent: "  "}, "not_configured"},
		{"configured", Config{Enabled: true, UserAgent: "Household/1.0 (operator)"}, "ready"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(test.cfg)
			if err != nil || client.Configuration().State != test.want {
				t.Fatal(client, err)
			}
		})
	}
	for _, cfg := range []Config{{Staging: true}, {UserAgent: "bad\r\nHeader: value"}, {UserAgent: strings.Repeat("x", 257)}, {UserAgent: "nicht-ASCII-ä"}} {
		if _, err := New(cfg); err == nil {
			t.Fatal("unsafe client configuration accepted", cfg)
		}
	}
	client, err := New(Config{Staging: true, Development: true})
	if err != nil || client.Configuration().Provider != "off-staging" {
		t.Fatal(client, err)
	}
}

func TestGTINPreservesEveryOriginalLengthAndLeadingZero(t *testing.T) {
	for _, code := range []string{"96385074", "012345678905", "4006381333931", "00012345678905"} {
		if got, err := ValidateGTIN(code); err != nil || got != code {
			t.Fatal(code, got, err)
		}
	}
	for _, code := range []string{"4006381333932", " 4006381333931", "4006381333931\n", "household-A", "1234567", "１２３４５６７８", "1e1234567890"} {
		if _, err := ValidateGTIN(code); err == nil {
			t.Fatal("invalid or coerced GTIN accepted", code)
		}
	}
}

func TestProductionTransportDisablesProxyRedirectAndPrivateDNS(t *testing.T) {
	client, err := New(Config{Enabled: true, UserAgent: "Household/1.0 (operator)"})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || !transport.DisableCompression || transport.MaxConnsPerHost != 2 || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.ServerName != "world.openfoodfacts.org" {
		t.Fatal("production transport boundary changed")
	}
	if !errors.Is(client.client.CheckRedirect(nil, nil), http.ErrUseLastResponse) || client.client.Timeout != 4*time.Second {
		t.Fatal("redirect or total timeout changed")
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "240.1.2.3", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("non-public address accepted", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Fatal("public address refused", raw)
		}
	}
	for _, target := range []string{"localhost:443", "127.0.0.1:443", "world.openfoodfacts.org:80", "world.openfoodfacts.net:443"} {
		if connection, err := transport.DialContext(t.Context(), "tcp", target); err == nil || connection != nil {
			t.Fatal("unexpected dial target was not refused before DNS", target)
		}
	}
}

func TestLookupRequestsOnlyFixedTextFieldsAndHashesExactBytes(t *testing.T) {
	body := `{"status":"success","product":{"code":"012345678905","product_name":"Fallback","product_name_de":"  Lokaler\nName ","brands":"Brand","quantity":"3 x 150 g","image_url":"https://foreign.invalid/private","nutriments":{"sugars":999999999999999999999999999}}}`
	var calls atomic.Int32
	client := fixtureClient(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != "GET" || request.URL.Scheme != "https" || request.URL.Host != "world.openfoodfacts.org" || request.URL.Path != "/api/v3/product/012345678905" ||
			request.URL.Query().Get("fields") != "code,product_name,product_name_de,brands,quantity" || request.URL.Query().Get("product_type") != "food" ||
			request.Header.Get("User-Agent") == "" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("outbound request disclosed additional context or changed its fixed scope", request.URL)
		}
		return fixtureResponse(200, body), nil
	})
	result, err := client.Lookup(t.Context(), "012345678905")
	digest := sha256.Sum256([]byte(body))
	if err != nil || result.State != "found" || result.Product == nil || result.Product.Code != "012345678905" || result.Product.Name != "Lokaler Name" ||
		result.Product.PackDescription != "3 x 150 g" || result.PayloadSHA256 != hex.EncodeToString(digest[:]) || calls.Load() != 1 {
		t.Fatal(result, err)
	}
}

func TestDecodeRejectsDuplicateTrailingUnsuccessfulOrDimensionCoercion(t *testing.T) {
	for _, raw := range []string{
		`{"status":"success","status":"success","product":{"code":"4006381333931","product_name":"x"}}`,
		`{"status":"success","product":{"code":"4006381333931","code":"4006381333931","product_name":"x"}}`,
		`{"status":"success","product":{"code":"4006381333931","product_name":"x"}} {}`,
		`{"status":1,"product":{"code":"4006381333931","product_name":"x"}}`,
		`{"status":"failure","product":{"code":"4006381333931","product_name":"x"}}`,
		`{"status":"success","product":{"code":4006381333931,"product_name":"x"}}`,
		`{"status":"success","product":{"code":"04006381333931","product_name":"x"}}`,
		`{"status":"success","product":{"code":"4006381333931","product_name":123}}`,
		`{"status":"success","product":{"code":"4006381333931","product_name":"\u0000"}}`,
		`{"status":"success","product":{"code":"4006381333931","product_name":""}}`,
		`{"status":"success","product":{"code":"4006381333931","product_name":"` + strings.Repeat("n", 201) + `"}}`,
	} {
		if _, err := decodeProduct([]byte(raw), "4006381333931"); err == nil {
			t.Fatal("unsupported upstream input accepted", raw)
		}
	}
	if _, err := decodeProduct([]byte{0xff}, "4006381333931"); err == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestHTTPStatesStayExplicitAnd429DoesNotDependOnItsBody(t *testing.T) {
	for _, test := range []struct {
		name, body, media, retry, want string
		status                         int
	}{
		{"not found", `{}`, "application/json", "", "not_found", 404},
		{"redirect", `{}`, "application/json", "", "error", 302},
		{"error", `{}`, "application/json", "", "error", 503},
		{"wrong media", `{}`, "text/html", "", "error", 200},
		{"oversized", strings.Repeat("x", maxBody+1), "application/json", "", "error", 200},
		{"limited oversized", strings.Repeat("x", maxBody+1), "text/html", "86401", "limited", 429},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient(t, func(*http.Request) (*http.Response, error) {
				response := fixtureResponse(test.status, test.body)
				response.Header.Set("Content-Type", test.media)
				response.Header.Set("Retry-After", test.retry)
				return response, nil
			})
			result, err := client.Lookup(t.Context(), "4006381333931")
			if err != nil || result.State != test.want || result.Product != nil {
				t.Fatal(result, err)
			}
			if test.status == 429 && result.RetryAfter != 24*time.Hour {
				t.Fatal("Retry-After upper bound was not retained", result)
			}
		})
	}
}

func TestStagingBasicIdentityAndRedirectRefusalRemainHostBound(t *testing.T) {
	client, err := New(Config{Enabled: true, UserAgent: "Household-test/1.0 (local-fixture)", Development: true, Staging: true})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		user, password, ok := request.BasicAuth()
		if request.URL.Host != "world.openfoodfacts.net" || !ok || user != "off" || password != "off" {
			t.Fatal("staging identity escaped its fixed development endpoint")
		}
		response := fixtureResponse(302, `{}`)
		response.Header.Set("Location", "https://127.0.0.1/private")
		return response, nil
	})
	result, err := client.Lookup(t.Context(), "4006381333931")
	if err != nil || result.State != "error" || result.ErrorCode != "redirect" || calls.Load() != 1 {
		t.Fatal("upstream redirect followed or staging identity disclosed", result, err, calls.Load())
	}
}

func TestRetryAfterDateAndSecondsAreBoundedWithoutOverflow(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		raw  string
		want time.Duration
	}{
		{"0", time.Second}, {"12", 12 * time.Second}, {"999999999999", 24 * time.Hour},
		{"not-a-date", time.Minute}, {now.Add(2 * time.Minute).Format(http.TimeFormat), 2 * time.Minute},
		{now.Add(-time.Hour).Format(http.TimeFormat), time.Second},
	} {
		if got := retryAfter(test.raw, now); got != test.want {
			t.Fatal(test.raw, got, test.want)
		}
	}
}

func TestCancelledRequestsReleaseBothLeasesAndQueuedCancellationNeverCallsProvider(t *testing.T) {
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	client := fixtureClient(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	var writers sync.WaitGroup
	for range 2 {
		writers.Go(func() { _, _ = client.Lookup(ctx, "4006381333931") })
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("request did not acquire its lease")
		}
	}
	queued, stopQueued := context.WithCancel(t.Context())
	stopQueued()
	if _, err := client.Lookup(queued, "4006381333931"); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		cancel()
		t.Fatal("cancelled queue entered provider", err, calls.Load())
	}
	cancel()
	finished := make(chan struct{})
	go func() { writers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled provider goroutines retained a lease")
	}
	if len(client.slots) != 0 {
		t.Fatal("request lease leaked", len(client.slots))
	}
	client.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return fixtureResponse(404, `{}`), nil })
	if result, err := client.Lookup(t.Context(), "4006381333931"); err != nil || result.State != "not_found" {
		t.Fatal("leases unusable after cancellation", result, err)
	}
}

func TestDisabledAndInvalidCodeNeverAcquireOutboundLease(t *testing.T) {
	var calls atomic.Int32
	client := fixtureClient(t, func(*http.Request) (*http.Response, error) { calls.Add(1); return fixtureResponse(404, `{}`), nil })
	if result, err := client.Lookup(t.Context(), "internal-123"); err != nil || result.ErrorCode != "invalid_code" || calls.Load() != 0 || len(client.slots) != 0 {
		t.Fatal(result, err)
	}
	client.configuration.State = "disabled"
	if result, err := client.Lookup(t.Context(), "4006381333931"); err != nil || result.ErrorCode != "not_configured" || calls.Load() != 0 {
		t.Fatal(result, err)
	}
}
