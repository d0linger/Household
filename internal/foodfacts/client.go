// Package foodfacts retrieves bounded, unverified product text from a fixed
// Open Food Facts endpoint. It never writes upstream or accepts a custom URL.
package foodfacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxBody = 1 << 20

type Config struct {
	Enabled     bool
	UserAgent   string
	Staging     bool
	Development bool
}

type Configuration struct {
	State    string `json:"state"`
	Provider string `json:"provider"`
}

type Product struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Brand           string `json:"brand"`
	PackDescription string `json:"pack_description"`
}

type Result struct {
	State         string
	Product       *Product
	PayloadSHA256 string
	ErrorCode     string
	RetryAfter    time.Duration
}

type Provider interface {
	Configuration() Configuration
	Lookup(context.Context, string) (Result, error)
}

// Client owns a fixed-host transport and a bounded per-instance request lease.
// The Store independently enforces the persistent installation-wide budget.
type Client struct {
	configuration Configuration
	host          string
	userAgent     string
	client        *http.Client
	slots         chan struct{}
	now           func() time.Time
}

func New(cfg Config) (*Client, error) {
	if cfg.Staging && !cfg.Development {
		return nil, errors.New("open food facts staging requires development mode")
	}
	if len(cfg.UserAgent) > 256 {
		return nil, errors.New("open food facts user agent exceeds 256 bytes")
	}
	for _, char := range cfg.UserAgent {
		if char < 32 || char > 126 {
			return nil, errors.New("open food facts user agent must contain printable ASCII")
		}
	}
	state, host, provider := "disabled", "world.openfoodfacts.org", "off-production"
	if cfg.Staging {
		host, provider = "world.openfoodfacts.net", "off-staging"
	}
	if cfg.Enabled {
		state = "ready"
		if strings.TrimSpace(cfg.UserAgent) == "" {
			state = "not_configured"
		}
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: publicDialer(host),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host},
		TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second,
		DisableCompression: true, MaxConnsPerHost: 2, MaxIdleConns: 2,
		MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
	}
	return &Client{
		configuration: Configuration{State: state, Provider: provider}, host: host,
		userAgent: cfg.UserAgent, slots: make(chan struct{}, 2), now: time.Now,
		client: &http.Client{Transport: transport, Timeout: 4 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *Client) Configuration() Configuration {
	if c == nil {
		return Configuration{State: "disabled", Provider: "off-production"}
	}
	return c.configuration
}

// ValidateGTIN checks the original decimal identity without collapsing lengths
// or leading zeros. Household ASCII aliases remain a separate local concept.
func ValidateGTIN(raw string) (string, error) {
	if len(raw) != 8 && len(raw) != 12 && len(raw) != 13 && len(raw) != 14 {
		return "", errors.New("GTIN requires 8, 12, 13 or 14 digits")
	}
	sum := 0
	for index := len(raw) - 1; index >= 0; index-- {
		if raw[index] < '0' || raw[index] > '9' {
			return "", errors.New("GTIN requires decimal digits")
		}
		weight := 1
		if (len(raw)-1-index)%2 == 1 {
			weight = 3
		}
		sum += int(raw[index]-'0') * weight
	}
	if sum%10 != 0 {
		return "", errors.New("GTIN check digit differs")
	}
	return raw, nil
}

func publicDialer(host string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		actualHost, port, err := net.SplitHostPort(address)
		if err != nil || actualHost != host || port != "443" || network != "tcp" {
			return nil, errors.New("open food facts dial target is not allowed")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 || len(addresses) > 16 {
			return nil, errors.New("open food facts public DNS resolution failed")
		}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				return nil, errors.New("open food facts DNS returned a non-public address")
			}
		}
		dialer := net.Dialer{Timeout: 2 * time.Second, KeepAlive: 15 * time.Second}
		for _, ip := range addresses {
			// Dial the checked literal; no second hostname lookup/rebinding window.
			connection, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
			if dialErr == nil {
				return connection, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, errors.New("open food facts connection unavailable")
	}
}

var excludedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.Zone() != "" {
		return false
	}
	for _, prefix := range excludedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (c *Client) Lookup(ctx context.Context, code string) (Result, error) {
	if c == nil || c.configuration.State != "ready" {
		return Result{State: "error", ErrorCode: "not_configured"}, nil
	}
	if _, err := ValidateGTIN(code); err != nil {
		return Result{State: "error", ErrorCode: "invalid_code"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return Result{State: "error", ErrorCode: "timeout"}, ctx.Err()
	}
	target := url.URL{Scheme: "https", Host: c.host, Path: "/api/v3/product/" + code}
	query := url.Values{"fields": {"code,product_name,product_name_de,brands,quantity"}, "product_type": {"food"}}
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Result{State: "error", ErrorCode: "invalid_request"}, nil
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	if c.configuration.Provider == "off-staging" {
		request.SetBasicAuth("off", "off")
	}
	response, err := c.client.Do(request)
	if err != nil {
		code := "network"
		if ctx.Err() != nil {
			code = "timeout"
		}
		// Transport errors are deliberately not returned as public provider text.
		return Result{State: "error", ErrorCode: code}, nil
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if response.StatusCode == http.StatusTooManyRequests {
		result := Result{State: "limited", ErrorCode: "upstream_limited", RetryAfter: retryAfter(response.Header.Get("Retry-After"), c.now())}
		if err == nil && len(body) <= maxBody {
			digest := sha256.Sum256(body)
			result.PayloadSHA256 = hex.EncodeToString(digest[:])
		}
		return result, nil
	}
	if err != nil || len(body) > maxBody {
		return Result{State: "error", ErrorCode: "response_size"}, nil
	}
	digest := sha256.Sum256(body)
	result := Result{PayloadSHA256: hex.EncodeToString(digest[:])}
	switch {
	case response.StatusCode == http.StatusNotFound:
		result.State = "not_found"
		return result, nil
	case response.StatusCode >= 300 && response.StatusCode < 400:
		result.State, result.ErrorCode = "error", "redirect"
		return result, nil
	case response.StatusCode != http.StatusOK:
		result.State, result.ErrorCode = "error", "upstream_status"
		return result, nil
	}
	if media := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0])); media != "application/json" {
		result.State, result.ErrorCode = "error", "invalid_response"
		return result, nil
	}
	product, err := decodeProduct(body, code)
	if err != nil {
		result.State, result.ErrorCode = "error", "invalid_response"
		return result, nil
	}
	result.State, result.Product = "found", product
	return result, nil
}

func retryAfter(raw string, now time.Time) time.Duration {
	duration := time.Minute
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		if seconds > 86400 {
			duration = 24 * time.Hour
		} else {
			duration = time.Duration(seconds) * time.Second
		}
	} else if until, err := http.ParseTime(raw); err == nil {
		duration = until.Sub(now)
	}
	if duration < time.Second {
		return time.Second
	}
	if duration > 24*time.Hour {
		return 24 * time.Hour
	}
	return duration
}

func plainText(value any, limit int) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit {
		return "", errors.New("invalid product text")
	}
	for _, char := range text {
		if unicode.IsControl(char) && char != '\t' && char != '\n' && char != '\r' {
			return "", errors.New("control character in product text")
		}
	}
	return strings.Join(strings.Fields(text), " "), nil
}

func decodeProduct(raw []byte, code string) (*Product, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("product response is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := uniqueJSON(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing product response")
	}
	envelope, ok := value.(map[string]any)
	if !ok || envelope["status"] != "success" {
		return nil, errors.New("product response was not successful")
	}
	row, ok := envelope["product"].(map[string]any)
	if !ok || row["code"] != code {
		return nil, errors.New("product code differs from requested identity")
	}
	name, err := plainText(row["product_name_de"], 200)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name, err = plainText(row["product_name"], 200)
		if err != nil {
			return nil, err
		}
	}
	brand, err := plainText(row["brands"], 300)
	if err != nil {
		return nil, err
	}
	pack, err := plainText(row["quantity"], 500)
	if err != nil {
		return nil, err
	}
	if name == "" && brand == "" && pack == "" {
		return nil, errors.New("product response contains no supported text")
	}
	return &Product{Code: code, Name: name, Brand: brand, PackDescription: pack}, nil
}

// uniqueJSON rejects duplicate keys, excessive nesting and unbounded token
// collections before selecting fields. Numbers retain their original text.
func uniqueJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("product JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || len(object) >= 10000 {
				return nil, errors.New("invalid product JSON object")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errors.New("duplicate product JSON key")
			}
			value, err := uniqueJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return nil, errors.New("invalid product JSON closing object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			if len(array) >= 10000 {
				return nil, errors.New("product JSON collection exceeds limit")
			}
			value, err := uniqueJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return nil, errors.New("invalid product JSON closing array")
		}
		return array, nil
	default:
		return nil, errors.New("invalid product JSON delimiter")
	}
}
