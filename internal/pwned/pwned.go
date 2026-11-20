// Package pwned checks passwords against the Have I Been Pwned range API with
// k-anonymity: only the first five hexadecimal characters of the password's
// SHA-1 hash leave the server, and responses are padded so their size reveals
// nothing. The check is opt-in and fails open on network errors.
package pwned

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- required by the HIBP range protocol; not used for password storage
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the range API; tests replace it.
var Endpoint = "https://api.pwnedpasswords.com/range/"

// Checker queries the range API with bounded time and response size.
type Checker struct {
	Client *http.Client
}

// New returns a checker with TLS 1.2+, short timeouts and no redirects.
func New() *Checker {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	return &Checker{Client: &http.Client{Timeout: 8 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// ErrUnavailable reports that the service could not answer; callers accept
// the password then, because the check is a hint, not a gate.
var ErrUnavailable = errors.New("password breach check unavailable")

// Count returns how often the password appears in known breaches.
func (c *Checker) Count(ctx context.Context, password string) (int, error) {
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- HIBP range protocol
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := digest[:5], digest[5:]
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint+prefix, nil)
	if err != nil {
		return 0, ErrUnavailable
	}
	request.Header.Set("Add-Padding", "true")
	request.Header.Set("User-Agent", "Household-password-check")
	response, err := c.Client.Do(request) // #nosec G107 G704 -- fixed endpoint plus a five-character hash prefix
	if err != nil {
		return 0, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, ErrUnavailable
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
	for scanner.Scan() {
		hash, count, found := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		if !found || !strings.EqualFold(hash, suffix) {
			continue
		}
		n, err := strconv.Atoi(count)
		if err != nil {
			return 0, ErrUnavailable
		}
		return n, nil // padding entries carry a count of 0
	}
	if scanner.Err() != nil {
		return 0, ErrUnavailable
	}
	return 0, nil
}
