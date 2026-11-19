//go:build integration

package web

import (
	"strings"
	"testing"
)

func TestMetricsExposeBoundedOperationalSeries(t *testing.T) {
	f := newIntegrationFixture(t)
	seedBackupRun(t, f, backupFixtureSnapshot, true)
	if response, _, err := f.request("GET", "/metrics", "", "", "", nil, nil); err != nil || response.StatusCode != 404 {
		t.Fatalf("metrics without configured token: %v %v", response, err)
	}
	f.app.Cfg.MetricsToken = "integration-metrics-token-0123456789"
	if response, _, err := f.request("GET", "/metrics", "", "", "", nil, map[string]string{"X-Metrics-Token": "wrong"}); err != nil || response.StatusCode != 404 {
		t.Fatalf("metrics with wrong token: %v %v", response, err)
	}
	if response, _, err := f.request("GET", "/api/v1/core/today", f.token, "", "", nil, nil); err != nil || response.StatusCode != 200 {
		t.Fatalf("warm-up request: %v %v", response, err)
	}
	response, body, err := f.request("GET", "/metrics", "", "", "", nil, map[string]string{"X-Metrics-Token": f.app.Cfg.MetricsToken})
	if err != nil || response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %v %v", response, err)
	}
	text := string(body)
	for _, want := range []string{
		`household_http_requests_total{method="GET",route="GET /api/v1/core/today",status="2xx"}`,
		"household_http_request_duration_seconds_bucket",
		"household_login_failures_total",
		"household_maintenance 0",
		`household_backup_healthy{target="local"} 1`,
		`household_backup_last_success_timestamp_seconds{target="local"}`,
		"household_db_total_connections",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics lack %q", want)
		}
	}
	for _, private := range []string{f.token, f.identity.UserID, backupFixtureSnapshot, "/data/backups"} {
		if strings.Contains(text, private) {
			t.Fatalf("metrics expose %q", private)
		}
	}
}
