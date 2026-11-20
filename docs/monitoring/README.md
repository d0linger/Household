# Monitoring Household

`/metrics` is off until `HOUSEHOLD_METRICS_TOKEN` is set. Prometheus then scrapes it with the token in the `X-Metrics-Token` header (never in the URL) and the configured origin as `Host`, because the app answers only its own origin:

```yaml
scrape_configs:
  - job_name: household
    scheme: https
    metrics_path: /metrics
    http_headers:
      X-Metrics-Token: {files: [/etc/prometheus/household_metrics_token]}
    static_configs:
      - targets: ['household.example.org']
```

Prometheus versions without `http_headers` can scrape through a reverse proxy that adds the header for the Prometheus host only.

## Series

| Metric | Meaning |
| --- | --- |
| `household_http_requests_total{method,route,status}` | Requests by fixed route pattern and status class (`2xx` … `5xx`). Never contains paths, IDs or query values. |
| `household_http_request_duration_seconds` | Duration histogram per method. |
| `household_http_panics_total` | Recovered request panics. |
| `household_login_failures_total`, `household_login_limited_total` | Rejected and rate-limited sign-ins. |
| `household_csrf_rejections_total` | Unsafe requests refused for CSRF. |
| `household_worker_ticks_total`, `household_worker_step_failures_total` | Background work. |
| `household_maintenance`, `household_maintenance_rejections_total` | Live-restore pause. |
| `household_backup_healthy{target}` | 1 when the local or offsite target is current by its own schedule, 0 when it failed, is overdue or never succeeded. A switched-off offsite target exports no series. |
| `household_backup_last_success_timestamp_seconds{target}` | Start of the last successful backup per target. |
| `household_backup_last_verified_timestamp_seconds` | Last successful restore test. |
| `household_backup_service_seen_timestamp_seconds` | Last heartbeat of the backup service. |
| `household_db_acquired_connections`, `household_db_total_connections` | Database pool use. |
| `household_uptime_seconds` | Process uptime. |

[`prometheus-alerts.yml`](prometheus-alerts.yml) contains example alert rules for these series. A server outage cannot report itself through the app's own notifications; monitor availability and backups from outside as well.
