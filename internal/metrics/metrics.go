// Package metrics keeps the process's operational counters for the
// token-protected Prometheus endpoint. Label values come only from fixed
// route patterns and enumerated states, so cardinality stays bounded and no
// request data is ever exposed.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Buckets are the upper bounds of the request duration histogram in seconds.
var Buckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

type routeKey struct {
	method, route, class string
}

type histogram struct {
	counts []atomic.Uint64
	sum    atomic.Uint64 // microseconds
	total  atomic.Uint64
}

// Registry holds every counter; the zero value is ready to use.
type Registry struct {
	mu       sync.Mutex
	requests map[routeKey]*atomic.Uint64
	duration map[string]*histogram
	counters map[string]*atomic.Uint64
	started  time.Time
}

// Default is the process-wide registry.
var Default = &Registry{started: time.Now()}

// maxRoutes bounds distinct route series; further routes fold into "other".
const maxRoutes = 400

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "other"
	}
	return fmt.Sprintf("%dxx", status/100)
}

// ObserveRequest counts one finished HTTP request by method, route pattern and status class.
func (r *Registry) ObserveRequest(method, route string, status int, elapsed time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		method = "other"
	}
	r.mu.Lock()
	if r.requests == nil {
		r.requests, r.duration = map[routeKey]*atomic.Uint64{}, map[string]*histogram{}
	}
	key := routeKey{method, route, statusClass(status)}
	counter, ok := r.requests[key]
	if !ok {
		if len(r.requests) >= maxRoutes {
			key.route = "other"
			if counter, ok = r.requests[key]; !ok {
				counter = new(atomic.Uint64)
				r.requests[key] = counter
			}
		} else {
			counter = new(atomic.Uint64)
			r.requests[key] = counter
		}
	}
	h, ok := r.duration[method]
	if !ok {
		h = &histogram{counts: make([]atomic.Uint64, len(Buckets))}
		r.duration[method] = h
	}
	r.mu.Unlock()
	counter.Add(1)
	seconds := elapsed.Seconds()
	for index, bound := range Buckets {
		if seconds <= bound {
			h.counts[index].Add(1)
		}
	}
	h.sum.Add(uint64(max(elapsed.Microseconds(), 0))) // #nosec G115 -- non-negative
	h.total.Add(1)
}

// Inc increments a named counter; names must be fixed identifiers.
func (r *Registry) Inc(name string) { r.Add(name, 1) }

// Add increases a named counter.
func (r *Registry) Add(name string, n uint64) {
	r.mu.Lock()
	if r.counters == nil {
		r.counters = map[string]*atomic.Uint64{}
	}
	counter, ok := r.counters[name]
	if !ok {
		counter = new(atomic.Uint64)
		r.counters[name] = counter
	}
	r.mu.Unlock()
	counter.Add(n)
}

// Gauge is one value sampled at scrape time.
type Gauge struct {
	Name   string
	Help   string
	Labels map[string]string
	Value  float64
}

func escapeLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}

func labels(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+`="`+escapeLabel(values[key])+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// counterHelp documents the named counters the application increments.
var counterHelp = map[string]string{
	"household_http_panics_total":            "Recovered request panics.",
	"household_login_failures_total":         "Rejected sign-in attempts.",
	"household_login_limited_total":          "Sign-in attempts refused by rate limits.",
	"household_worker_step_failures_total":   "Failed background worker steps.",
	"household_worker_ticks_total":           "Completed background worker ticks.",
	"household_csrf_rejections_total":        "Unsafe requests refused for a missing or invalid CSRF token.",
	"household_maintenance_rejections_total": "Requests answered with 503 during a live restore.",
}

// Write renders the registry plus gauges in the Prometheus text format.
func (r *Registry) Write(w io.Writer, gauges []Gauge) error {
	var b strings.Builder
	b.WriteString("# HELP household_http_requests_total HTTP requests by method, route pattern and status class.\n# TYPE household_http_requests_total counter\n")
	r.mu.Lock()
	keys := make([]routeKey, 0, len(r.requests))
	for key := range r.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, c := keys[i], keys[j]
		return a.route+a.method+a.class < c.route+c.method+c.class
	})
	for _, key := range keys {
		fmt.Fprintf(&b, "household_http_requests_total%s %d\n", labels(map[string]string{"method": key.method, "route": key.route, "status": key.class}), r.requests[key].Load())
	}
	b.WriteString("# HELP household_http_request_duration_seconds HTTP request duration by method.\n# TYPE household_http_request_duration_seconds histogram\n")
	methods := make([]string, 0, len(r.duration))
	for method := range r.duration {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		h := r.duration[method]
		for index, bound := range Buckets {
			fmt.Fprintf(&b, "household_http_request_duration_seconds_bucket%s %d\n", labels(map[string]string{"method": method, "le": fmt.Sprint(bound)}), h.counts[index].Load())
		}
		fmt.Fprintf(&b, "household_http_request_duration_seconds_bucket%s %d\n", labels(map[string]string{"method": method, "le": "+Inf"}), h.total.Load())
		fmt.Fprintf(&b, "household_http_request_duration_seconds_sum%s %g\n", labels(map[string]string{"method": method}), float64(h.sum.Load())/1e6)
		fmt.Fprintf(&b, "household_http_request_duration_seconds_count%s %d\n", labels(map[string]string{"method": method}), h.total.Load())
	}
	names := make([]string, 0, len(counterHelp))
	for name := range counterHelp {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var value uint64
		if counter, ok := r.counters[name]; ok {
			value = counter.Load()
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, counterHelp[name], name, name, value)
	}
	started := r.started
	r.mu.Unlock()
	if !started.IsZero() {
		fmt.Fprintf(&b, "# HELP household_uptime_seconds Seconds since the process started.\n# TYPE household_uptime_seconds gauge\nhousehold_uptime_seconds %d\n", int64(time.Since(started).Seconds()))
	}
	written := map[string]bool{}
	for _, gauge := range gauges {
		if !written[gauge.Name] {
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", gauge.Name, gauge.Help, gauge.Name)
			written[gauge.Name] = true
		}
		fmt.Fprintf(&b, "%s%s %g\n", gauge.Name, labels(gauge.Labels), gauge.Value)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
