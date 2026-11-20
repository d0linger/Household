// Package updatecheck optionally asks GitHub which commit the published
// production image is built from (the head of main) and compares it with the
// running build. It is off by default, sends nothing but the request itself,
// and keeps its result in memory only.
package updatecheck

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Endpoint returns the head commit of main; tests replace it.
var Endpoint = "https://api.github.com/repos/d0linger/Household/commits/main"

// Interval spaces the lookups.
const Interval = 24 * time.Hour

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// revisionPattern accepts the build revisions buildinfo reports, full or short.
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Result is the last lookup.
type Result struct {
	Checked   time.Time
	Latest    string // full commit of main
	Available bool   // the running revision is known and differs from main
	Failed    bool
}

// Checker performs at most one lookup per interval.
type Checker struct {
	Client  *http.Client
	Current func() string // running revision ("unknown" for local builds)

	mu     sync.Mutex
	result Result
}

// New returns a checker for the running revision.
func New(current func() string) *Checker {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxIdleConns: 1, IdleConnTimeout: 30 * time.Second}
	return &Checker{Current: current, Client: &http.Client{Timeout: 15 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Result returns the last lookup.
func (c *Checker) Result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

// Check looks up main when the interval passed since the last lookup.
func (c *Checker) Check(ctx context.Context, now time.Time) Result {
	c.mu.Lock()
	if !c.result.Checked.IsZero() && now.Sub(c.result.Checked) < Interval {
		result := c.result
		c.mu.Unlock()
		return result
	}
	c.mu.Unlock()
	latest, err := c.lookup(ctx)
	result := Result{Checked: now, Latest: latest, Failed: err != nil}
	current := c.Current()
	// A short build revision is compared with the same prefix of the latest commit.
	result.Available = err == nil && revisionPattern.MatchString(current) && !strings.HasPrefix(latest, current)
	c.mu.Lock()
	c.result = result
	c.mu.Unlock()
	return result
}

func (c *Checker) lookup(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Household-update-check")
	response, err := c.Client.Do(request) // #nosec G107 G704 -- fixed public endpoint
	if err != nil {
		return "", errors.New("update source unreachable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("update source refused the lookup")
	}
	var body struct {
		SHA string `json:"sha"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil || !commitPattern.MatchString(body.SHA) {
		return "", errors.New("unexpected update response")
	}
	return body.SHA, nil
}
