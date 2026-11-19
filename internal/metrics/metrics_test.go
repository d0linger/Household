package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRegistryWritesBoundedPrometheusText(t *testing.T) {
	r := &Registry{started: time.Now().Add(-time.Minute)}
	r.ObserveRequest("GET", "GET /settings", 200, 30*time.Millisecond)
	r.ObserveRequest("GET", "GET /settings", 204, 2*time.Second)
	r.ObserveRequest("BREW", "", 418, time.Millisecond)
	r.Inc("household_login_failures_total")
	r.Add("household_login_failures_total", 2)
	var b strings.Builder
	if err := r.Write(&b, []Gauge{{Name: "household_backup_last_success_timestamp_seconds", Help: "Last success.", Labels: map[string]string{"target": `lo"cal`}, Value: 1700000000}}); err != nil {
		t.Fatal(err)
	}
	text := b.String()
	for _, want := range []string{
		`household_http_requests_total{method="GET",route="GET /settings",status="2xx"} 2`,
		`household_http_requests_total{method="other",route="unmatched",status="4xx"} 1`,
		`household_http_request_duration_seconds_bucket{le="0.05",method="GET"} 1`,
		`household_http_request_duration_seconds_bucket{le="+Inf",method="GET"} 2`,
		`household_http_request_duration_seconds_count{method="GET"} 2`,
		"household_login_failures_total 3",
		"household_http_panics_total 0",
		"# TYPE household_uptime_seconds gauge",
		`household_backup_last_success_timestamp_seconds{target="lo\"cal"} 1.7e+09`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}

func TestRegistryFoldsExcessRoutes(t *testing.T) {
	r := &Registry{}
	for index := range maxRoutes + 50 {
		r.ObserveRequest("GET", fmt.Sprintf("GET /r%d", index), 200, time.Millisecond)
	}
	var b strings.Builder
	if err := r.Write(&b, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `route="other",status="2xx"} 50`) {
		t.Fatal("excess routes were not folded into other")
	}
}
