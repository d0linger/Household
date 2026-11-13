package webpush

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"household/internal/notification"

	webpushlib "github.com/SherClockHolmes/webpush-go"
)

func allowedHost(host string) bool {
	if host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" {
		return true
	}
	for _, suffix := range []string{".push.apple.com", ".notify.windows.com"} {
		if !strings.HasSuffix(host, suffix) {
			continue
		}
		label := strings.TrimSuffix(host, suffix)
		if len(label) < 1 || len(label) > 63 || strings.Contains(label, ".") || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
		return true
	}
	return false
}

func validateEndpoint(raw string) (*url.URL, error) {
	if len(raw) < 10 || len(raw) > 2048 || strings.Contains(raw, "#") {
		return nil, errors.New("push endpoint is not allowed")
	}
	for _, r := range raw {
		if r < 33 || r > 126 {
			return nil, errors.New("push endpoint is not allowed")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("push endpoint is not allowed")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || u.Port() != "" && u.Port() != "443" || !allowedHost(host) {
		return nil, errors.New("push endpoint is not allowed")
	}
	if _, err = netip.ParseAddr(host); err == nil {
		return nil, errors.New("push IP endpoint is not allowed")
	}
	return u, nil
}

// Canonicalize only the authority. Never parse, decode, or rebuild path/query.
func canonicalEndpoint(raw string) (string, error) {
	u, err := validateEndpoint(raw)
	if err != nil {
		return "", err
	}
	return "https://" + strings.ToLower(u.Hostname()) + raw[len("https://")+len(u.Host):], nil
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("::/96"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"), netip.MustParsePrefix("fec0::/10"),
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.Zone() != "" {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || network != "tcp" || port != "443" || !allowedHost(strings.ToLower(host)) {
		return nil, errors.New("push dial target is not allowed")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 || len(addresses) > 16 {
		return nil, errors.New("push public DNS unavailable")
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return nil, errors.New("push DNS returned a non-public address")
		}
	}
	dialer := net.Dialer{Timeout: 2 * time.Second, KeepAlive: 15 * time.Second}
	for _, ip := range addresses {
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
		if err == nil {
			return connection, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("push connection unavailable")
}

type checkedClient struct{ client *http.Client }

func (c *checkedClient) Do(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.ContentLength < 0 || request.ContentLength > 8192 {
		return nil, errors.New("push request is not allowed")
	}
	if _, err := validateEndpoint(request.URL.String()); err != nil {
		return nil, err
	}
	return c.client.Do(request) // #nosec G704 -- HTTPS443 vendor allowlist; public-IP-pinned dialer with TLS hostname verification; proxy and redirects disabled.
}

type sender struct {
	client                   webpushlib.HTTPClient
	httpTransport            *http.Transport
	slots                    chan struct{}
	public, private, contact string
}

func newSender(public, private, contact string) *sender {
	transport := &http.Transport{Proxy: nil, DialContext: publicDial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 16 << 10, MaxConnsPerHost: 2, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second, DisableCompression: true, ForceAttemptHTTP2: false}
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &sender{client: &checkedClient{client: client}, httpTransport: transport, slots: make(chan struct{}, 2), public: public, private: private, contact: contact}
}

func (s *sender) close() { s.httpTransport.CloseIdleConnections() }

type outcome struct {
	status  int
	code    string
	retryAt time.Time
}

func (s *sender) send(ctx context.Context, receiver Receiver, payload []byte, topic string) outcome {
	return s.sendAuthorized(ctx, receiver, payload, topic, nil)
}

// sendAuthorized revalidates mutable application authority after waiting for a
// transport slot. The guard must finish its database transaction before return.
func (s *sender) sendAuthorized(ctx context.Context, receiver Receiver, payload []byte, topic string, guard func(context.Context) error) outcome {
	if len(payload) > 1024 || len(topic) != 32 {
		return outcome{code: "invalid_payload"}
	}
	var notice pushPayload
	if strictJSON(payload, &notice) != nil || !notification.ValidMessage(notice.Schema, notice.Kind, notice.ID, notice.AudienceScope, notice.ExpiresAt, time.Now().Unix()) {
		return outcome{code: "invalid_payload"}
	}
	if validateReceiver(receiver) != nil {
		return outcome{code: "invalid_connection"}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return outcome{code: "network_timeout"}
	}
	if ctx.Err() != nil {
		return outcome{code: "network_timeout"}
	}
	if guard != nil && guard(ctx) != nil {
		return outcome{code: "authority_superseded"}
	}
	// A short-lived timer may expire while waiting for one of the two slots.
	// Recompute TTL after the wait; never send an already stale wire reference.
	if !notification.ValidMessage(notice.Schema, notice.Kind, notice.ID, notice.AudienceScope, notice.ExpiresAt, time.Now().Unix()) {
		return outcome{code: "invalid_payload"}
	}
	remaining := notice.ExpiresAt - time.Now().Unix()
	if remaining < 1 {
		return outcome{code: "invalid_payload"}
	}
	ttl := min(300, int(remaining))
	urgency := webpushlib.UrgencyLow
	if notice.Schema == 2 && notice.Kind == "cooking_timer" {
		urgency = webpushlib.UrgencyHigh
	}
	// The pinned library adds mailto: itself for a non-HTTPS subscriber.
	subscriber := strings.TrimPrefix(s.contact, "mailto:")
	response, err := webpushlib.SendNotificationWithContext(ctx, payload, &webpushlib.Subscription{Endpoint: receiver.Endpoint, Keys: webpushlib.Keys{P256dh: receiver.Keys.P256DH, Auth: receiver.Keys.Auth}}, &webpushlib.Options{HTTPClient: s.client, Subscriber: subscriber, VAPIDPublicKey: s.public, VAPIDPrivateKey: s.private, TTL: ttl, Topic: topic, Urgency: urgency, RecordSize: 2048})
	if err != nil {
		return outcome{code: "network_uncertain"}
	}
	if response == nil || response.Body == nil {
		return outcome{code: "network_uncertain"}
	}
	defer func() { _ = response.Body.Close() }()
	// Response content is not part of the protocol. Bound draining; a 2xx
	// status still proves only gateway acceptance, never actual display.
	_, _ = io.CopyN(io.Discard, response.Body, 16<<10)
	result := outcome{status: response.StatusCode, code: "gateway_rejected"}
	if response.StatusCode == 429 {
		result.code = "gateway_limited"
		result.retryAt = retryAfter(response.Header.Get("Retry-After"), time.Now())
		return result
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.code = "gateway_accepted"
	case response.StatusCode == 404 || response.StatusCode == 410:
		result.code = "connection_expired"
	case response.StatusCode >= 500:
		result.code = "gateway_temporary"
	case response.StatusCode == 401 || response.StatusCode == 403:
		result.code = "vapid_rejected"
	}
	return result
}

func retryAfter(raw string, now time.Time) time.Time {
	delay := time.Minute
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		if seconds > 86400 {
			seconds = 86400
		}
		delay = time.Duration(seconds) * time.Second
	} else if parsed, err := http.ParseTime(raw); err == nil {
		delay = parsed.Sub(now)
	}
	if delay < time.Second {
		delay = time.Second
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	return now.Add(delay)
}
