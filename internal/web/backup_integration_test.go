//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"household/internal/backupplan"
	"household/internal/value"
	"household/internal/worker"
)

var backupFixtureSnapshot = "0123abcd" + strings.Repeat("e", 56)

func TestArchiveUploadQueuesOnlyAProbeAndExceedsOrdinaryFormLimit(t *testing.T) {
	f := newIntegrationFixture(t)
	directory := t.TempDir()
	t.Setenv("HOUSEHOLD_BACKUP_EXCHANGE", directory)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_settings(household_id,service_seen_at)
 VALUES($1,now()) ON CONFLICT(household_id) DO UPDATE SET service_seen_at=now()`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	const route = "/api/v1/core/backup/upload"
	response, _, err := f.request("POST", route, f.token, "", "application/octet-stream", strings.NewReader("no csrf"), nil)
	if err != nil || response.StatusCode != 403 {
		t.Fatalf("missing CSRF: %v, %v", response, err)
	}
	// Deliberately above the ordinary 12 MiB limit. The worker, rather than the
	// admission route, authenticates the archive before any restore is possible.
	data := make([]byte, 13<<20)
	copy(data, "age-encryption.org/v1\n")
	response, body, err := f.request("POST", route, f.token, f.app.Auth.CSRF(f.token, "session"), "application/octet-stream", bytes.NewReader(data), nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("archive stage: %v, %s, %v", response, body, err)
	}
	var reply map[string]string
	if err = json.Unmarshal(body, &reply); err != nil || !value.ValidUUID(reply["id"]) {
		t.Fatalf("archive stage receipt: %v, %v", reply, err)
	}
	var snapshot, kind, state string
	if err = f.owner.QueryRow(f.ctx, "SELECT snapshot_reference,kind,state FROM core.backup_jobs WHERE id=$1", reply["id"]).Scan(&snapshot, &kind, &state); err != nil {
		t.Fatal(err)
	}
	if kind != "verify" || state != "requested" {
		t.Fatalf("upload queued %s/%s", kind, state)
	}
	stat, err := os.Stat(filepath.Join(directory, backupplan.ExchangeName("import", snapshot)))
	if err != nil || stat.Size() != int64(len(data)) {
		t.Fatalf("archive staging: %v, %v", stat, err)
	}
	if status, _ := backupForm(f, "/settings/backup/jobs", url.Values{
		"kind": {"restore"}, "target": {"local"}, "snapshot": {snapshot}, "confirmation": {backupplan.RestoreConfirmation(snapshot)},
	}); status != 422 {
		t.Fatalf("unverified upload became eligible for live restore: %d", status)
	}
}

// testLogWriter shows server-side failures of a request in the test output.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func backupForm(f *integrationFixture, path string, values url.Values) (int, map[string]string) {
	response, body, err := f.request("POST", path, f.token, f.app.Auth.CSRF(f.token, "session"), "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil)
	if err != nil {
		return 0, nil
	}
	result := map[string]string{}
	_ = json.Unmarshal(body, &result)
	return response.StatusCode, result
}

// seedBackupRun records a completed local snapshot with the current schema.
func seedBackupRun(t *testing.T, f *integrationFixture, snapshot string, compatible bool) {
	t.Helper()
	schemas := map[string]int{}
	var raw []byte
	rows, err := f.owner.Query(f.ctx, `SELECT 'core',version FROM core.schema_migrations UNION ALL SELECT 'shopping',version FROM shopping.schema_migrations
 UNION ALL SELECT 'fuel',version FROM fuel.schema_migrations UNION ALL SELECT 'recipes',version FROM recipes.schema_migrations
 UNION ALL SELECT 'pantry',version FROM pantry.schema_migrations UNION ALL SELECT 'expenses',version FROM expenses.schema_migrations
 UNION ALL SELECT 'chores',version FROM chores.schema_migrations UNION ALL SELECT 'meters',version FROM meters.schema_migrations
 UNION ALL SELECT 'documents',version FROM documents.schema_migrations UNION ALL SELECT 'mealplan',version FROM mealplan.schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var schema string
		var version int
		if err = rows.Scan(&schema, &version); err != nil {
			t.Fatal(err)
		}
		schemas[schema] = version
	}
	rows.Close()
	if !compatible {
		schemas["core"]--
	}
	raw, _ = json.Marshal(schemas)
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,repository_kind,snapshot_reference,schema_manifest,finished_at,total_bytes)
 VALUES('complete','0.1.0','/data/backups','local',$1,$2,now(),12345)`, snapshot, raw); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRequestsAreValidatedAndOnlyCancellable(t *testing.T) {
	f := newIntegrationFixture(t)
	f.app.Log = slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelError}))
	seedBackupRun(t, f, backupFixtureSnapshot, true)
	old := "ffffffff" + backupFixtureSnapshot[8:]
	seedBackupRun(t, f, old, false)

	status, body := backupForm(f, "/settings/backup/jobs", url.Values{"kind": {"backup"}, "target": {"local"}})
	if status != 200 || !value.ValidUUID(body["id"]) {
		t.Fatalf("backup request: %d %v", status, body)
	}
	backupJob := body["id"]
	if status, _ = backupForm(f, "/settings/backup/jobs", url.Values{"kind": {"backup"}, "target": {"local"}}); status != 422 {
		t.Fatalf("duplicate pending backup request: %d", status)
	}
	for name, values := range map[string]url.Values{
		"unknown kind":        {"kind": {"wipe"}, "target": {"local"}},
		"offsite unavailable": {"kind": {"backup"}, "target": {"both"}},
		"verify no snapshot":  {"kind": {"verify"}, "target": {"local"}},
		"verify unknown":      {"kind": {"verify"}, "target": {"local"}, "snapshot": {"aa" + backupFixtureSnapshot[2:]}},
		"restore wrong text":  {"kind": {"restore"}, "target": {"local"}, "snapshot": {backupFixtureSnapshot}, "confirmation": {"WIEDERHERSTELLEN FFFFFFFF"}},
		"restore old schema":  {"kind": {"restore"}, "target": {"local"}, "snapshot": {old}, "confirmation": {backupplan.RestoreConfirmation(old)}},
		"discard nothing":     {"kind": {"discard_previous"}, "target": {"local"}},
	} {
		if status, _ = backupForm(f, "/settings/backup/jobs", values); status != 422 {
			t.Fatalf("%s: %d", name, status)
		}
	}
	// A restore needs a sign-in confirmed within ten minutes.
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET reauthenticated_at=now()-interval '1 hour' WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	restore := url.Values{"kind": {"restore"}, "target": {"local"}, "snapshot": {backupFixtureSnapshot}, "confirmation": {strings.ToLower(backupplan.RestoreConfirmation(backupFixtureSnapshot))}}
	if status, body = backupForm(f, "/settings/backup/jobs", restore); status != 422 || body["code"] != "reauth_required" {
		t.Fatalf("stale restore request: %d %v", status, body)
	}
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.sessions SET reauthenticated_at=now() WHERE id=$1", f.identity.SessionID); err != nil {
		t.Fatal(err)
	}
	if status, body = backupForm(f, "/settings/backup/jobs", restore); status != 200 {
		t.Fatalf("fresh restore request: %d %v", status, body)
	}
	restoreJob := body["id"]
	var session, confirmation string
	if err := f.owner.QueryRow(f.ctx, "SELECT requested_session::text,confirmation FROM core.backup_jobs WHERE id=$1 AND state='requested'", restoreJob).Scan(&session, &confirmation); err != nil || session != f.identity.SessionID {
		t.Fatalf("restore request row: %q %v", session, err)
	}
	if status, _ = backupForm(f, "/settings/backup/jobs", restore); status != 422 {
		t.Fatalf("second active restore: %d", status)
	}

	// The web role can create requests and cancel unclaimed ones, nothing else.
	for name, statement := range map[string]string{
		"claim":        "UPDATE core.backup_jobs SET state='running' WHERE id=$1",
		"result":       "UPDATE core.backup_jobs SET state='complete',result_code='ok',finished_at=now() WHERE id=$1",
		"drain column": "UPDATE core.backup_jobs SET app_drained_at=now() WHERE id=$1",
		"delete":       "DELETE FROM core.backup_jobs WHERE id=$1",
	} {
		if _, err := f.pool.Exec(f.ctx, statement, backupJob); err == nil {
			t.Fatalf("web role could %s backup work", name)
		}
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO core.backup_jobs(household_id,kind,target,requested_by,state) VALUES($1,'backup','local',$2,'running')`, f.identity.HouseholdID, f.identity.UserID); err == nil {
		t.Fatal("web role inserted claimed work")
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO core.backup_jobs(household_id,kind,target) VALUES($1,'offsite_test','offsite')`, f.identity.HouseholdID); err == nil {
		t.Fatal("web role inserted work without a requester")
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE core.backup_settings SET service_seen_at=now()`); err == nil {
		t.Fatal("web role wrote a service column")
	}
	var drained bool
	if err := f.pool.QueryRow(f.ctx, "SELECT core.acknowledge_backup_drain($1)", restoreJob).Scan(&drained); err != nil || drained {
		t.Fatalf("drain acknowledged for unclaimed restore: %v %v", drained, err)
	}
	if status, _ = backupForm(f, "/settings/backup/jobs/"+backupJob+"/cancel", url.Values{}); status != 200 {
		t.Fatalf("cancel: %d", status)
	}
	if status, _ = backupForm(f, "/settings/backup/jobs/"+backupJob+"/cancel", url.Values{}); status != 409 {
		t.Fatalf("cancel twice: %d", status)
	}
	var actions int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM core.audit_log WHERE action IN('backup_requested','backup_cancelled')").Scan(&actions); err != nil || actions != 3 {
		t.Fatalf("semantic audit events = %d %v", actions, err)
	}

	// Other roles see neither the pages nor the API.
	if _, err := f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='editor' WHERE user_id=$1", f.identity.UserID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/settings/backup", "/settings/system", "/api/v1/core/backup"} {
		response, _, err := f.request("GET", path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 403 {
			t.Fatalf("editor at %s: %v %v", path, response, err)
		}
	}
	if status, _ = backupForm(f, "/settings/backup/jobs", url.Values{"kind": {"backup"}, "target": {"local"}}); status != 403 {
		t.Fatalf("editor request: %d", status)
	}
}

func TestBackupPagesAndSettings(t *testing.T) {
	f := newIntegrationFixture(t)
	seedBackupRun(t, f, backupFixtureSnapshot, true)
	for _, path := range []string{"/settings/backup", "/settings/system"} {
		response, body, err := f.request("GET", path, f.token, "", "", nil, map[string]string{"Accept": "text/html"})
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, response, err)
		}
		if path == "/settings/backup" && (!strings.Contains(string(body), backupFixtureSnapshot) || !strings.Contains(string(body), "WIEDERHERSTELLEN 0123ABCD")) {
			t.Fatal("backup page lacks the restore point and its confirmation")
		}
	}
	response, body, err := f.request("GET", "/api/v1/core/backup", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("overview: %v %v", response, err)
	}
	var overview struct {
		SettingsVersion int64            `json:"settings_version"`
		LocalSnapshots  []backupSnapshot `json:"local_snapshots"`
		ServiceOnline   bool             `json:"service_online"`
	}
	if err = json.Unmarshal(body, &overview); err != nil || len(overview.LocalSnapshots) != 1 || !overview.LocalSnapshots[0].Compatible || overview.ServiceOnline {
		t.Fatalf("overview = %+v %v", overview, err)
	}
	settings := url.Values{"version": {"0"}, "local_time": {"02:30"}, "local_interval_hours": {"12"}, "keep_daily": {"10"}, "keep_weekly": {"5"},
		"keep_monthly": {"6"}, "offsite_enabled": {"on"}, "offsite_interval_hours": {"168"}, "offsite_keep_daily": {"3"}, "offsite_keep_weekly": {"2"},
		"offsite_keep_monthly": {"1"}, "probe_interval_days": {"14"}, "probe_target": {"offsite"}}
	if status, result := backupForm(f, "/settings/backup/settings", settings); status != 200 {
		t.Fatalf("save settings: %d %v", status, result)
	}
	if status, _ := backupForm(f, "/settings/backup/settings", settings); status != 409 {
		t.Fatalf("stale settings version: %d", status)
	}
	settings.Set("version", "1")
	settings.Set("local_interval_hours", "5")
	if status, _ := backupForm(f, "/settings/backup/settings", settings); status != 422 {
		t.Fatalf("invalid interval: %d", status)
	}
	var localTime time.Time
	var interval, probe int
	var target string
	if err = f.owner.QueryRow(f.ctx, "SELECT local_time,local_interval_hours,probe_interval_days,probe_target FROM core.backup_settings").Scan(&localTime, &interval, &probe, &target); err != nil ||
		localTime.Format("15:04") != "02:30" || interval != 12 || probe != 14 || target != "offsite" {
		t.Fatalf("stored settings: %s %d %d %s %v", localTime.Format("15:04"), interval, probe, target, err)
	}
}

func TestLiveRestorePausesAndResumesTheApp(t *testing.T) {
	f := newIntegrationFixture(t)
	seedBackupRun(t, f, backupFixtureSnapshot, true)
	job := value.UUID()
	confirmation := backupplan.RestoreConfirmation(backupFixtureSnapshot)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_jobs(id,household_id,kind,target,snapshot_reference,confirmation,state,phase,started_at,heartbeat_at)
 VALUES($1,$2,'restore','local',$3,$4,'awaiting_drain','pausing_app',now(),now())`, job, f.identity.HouseholdID, backupFixtureSnapshot, confirmation); err != nil {
		t.Fatal(err)
	}
	f.app.checkRestore(f.ctx)
	if !f.app.Paused() {
		t.Fatal("app did not pause for the restore")
	}
	var drained bool
	if err := f.owner.QueryRow(f.ctx, "SELECT app_drained_at IS NOT NULL FROM core.backup_jobs WHERE id=$1", job).Scan(&drained); err != nil || !drained {
		t.Fatalf("pause not confirmed: %v %v", drained, err)
	}
	response, body, err := f.request("GET", "/api/v1/core/today", f.token, "", "", nil, nil)
	if err != nil || response.StatusCode != 503 || !strings.Contains(string(body), "maintenance") || response.Header.Get("Retry-After") == "" {
		t.Fatalf("paused API: %v %s %v", response, body, err)
	}
	response, body, err = f.request("GET", "/settings", f.token, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 503 || !strings.Contains(string(body), `http-equiv="refresh"`) {
		t.Fatalf("paused page: %v %v", response, err)
	}
	for _, path := range []string{"/health/live", "/maintenance/status"} {
		if response, _, err = f.request("GET", path, "", "", "", nil, nil); err != nil || response.StatusCode != 200 {
			t.Fatalf("%s while paused: %v %v", path, response, err)
		}
	}
	if response, _, err = f.request("GET", "/health/ready", "", "", "", nil, nil); err != nil || response.StatusCode != 503 {
		t.Fatalf("readiness while paused: %v %v", response, err)
	}
	f.app.checkRestore(f.ctx) // still running: stays paused
	if !f.app.Paused() {
		t.Fatal("app resumed during the switch")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.backup_jobs SET state='complete',result_code='ok',finished_at=now() WHERE id=$1", job); err != nil {
		t.Fatal(err)
	}
	f.app.checkRestore(f.ctx)
	if f.app.Paused() {
		t.Fatal("app did not resume after the restore")
	}
	if response, _, err = f.request("GET", "/api/v1/core/today", f.token, "", "", nil, nil); err != nil || response.StatusCode != 200 {
		t.Fatalf("resumed API: %v %v", response, err)
	}
}

func TestBackupNoticesReachAdministratorsOnce(t *testing.T) {
	f := newIntegrationFixture(t)
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,repository_kind,started_at,finished_at,failure_reason)
 VALUES('failed','0.1.0','/data/backups','local',now()-interval '1 hour',now(),'operator backup failed')`); err != nil {
		t.Fatal(err)
	}
	job := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO core.backup_jobs(id,household_id,kind,target,snapshot_reference,confirmation,state,result_code,started_at,finished_at)
 VALUES($1,$2,'restore','local',$3,'x','failed','drain_timeout',now(),now())`, job, f.identity.HouseholdID, backupFixtureSnapshot); err != nil {
		t.Fatal(err)
	}
	background := &worker.Worker{Pool: f.pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Cfg: f.app.Cfg}
	for range 2 {
		if err := background.BackupNotices(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.owner.Query(f.ctx, "SELECT kind FROM core.notifications WHERE user_id=$1 AND kind LIKE 'backup_%' ORDER BY kind", f.identity.UserID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for rows.Next() {
		var kind string
		_ = rows.Scan(&kind)
		kinds = append(kinds, kind)
	}
	rows.Close()
	if strings.Join(kinds, ",") != "backup_failed,backup_restore_failed" {
		t.Fatalf("notices = %v", kinds)
	}
	response, body, err := f.request("GET", "/notifications", f.token, "", "", nil, map[string]string{"Accept": "text/html"})
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Die letzte Sicherung ist fehlgeschlagen.") || !strings.Contains(string(body), "/settings/backup") {
		t.Fatalf("notices page: %v %v", response, err)
	}
}

// An offsite file of another version can still be checked and staged for
// download; only its live restore needs this version.
func TestOffsiteArchiveOfAnotherVersionCanBeStagedForDownload(t *testing.T) {
	f := newIntegrationFixture(t)
	f.app.Log = slog.New(slog.NewTextHandler(testLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelError}))
	tag, err := f.owner.Exec(f.ctx, "UPDATE core.backup_settings SET offsite_label='/mnt/offsite'")
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_settings(household_id,offsite_label) VALUES($1,'/mnt/offsite')", f.identity.HouseholdID); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,repository_kind,snapshot_reference,schema_manifest,finished_at,total_bytes)
 VALUES('complete','0.0.1','/mnt/offsite','offsite',$1,'{"core":1}',now(),12345)`, snapshot); err != nil {
		t.Fatal(err)
	}
	if status, body := backupForm(f, "/settings/backup/jobs", url.Values{"kind": {"verify"}, "target": {"offsite"}, "snapshot": {snapshot}}); status != 200 {
		t.Fatalf("staging an offsite file of another version: %d %v", status, body)
	}
	restore := url.Values{"kind": {"restore"}, "target": {"offsite"}, "snapshot": {snapshot}, "confirmation": {backupplan.RestoreConfirmation(snapshot)}}
	if status, _ := backupForm(f, "/settings/backup/jobs", restore); status != 422 {
		t.Fatalf("live restore of another version: %d", status)
	}
}

// An upload records its validation as the verification of its new run. That
// is no check of an existing restore point, so the scheduled probe stays
// due; a later trial restore of the imported point counts.
func TestArchiveUploadDoesNotRefreshTheProbe(t *testing.T) {
	f := newIntegrationFixture(t)
	tag, err := f.owner.Exec(f.ctx, "UPDATE core.backup_settings SET probe_target='local',probe_interval_days=7")
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err = f.owner.Exec(f.ctx, "INSERT INTO core.backup_settings(household_id,probe_target,probe_interval_days) VALUES($1,'local',7)", f.identity.HouseholdID); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := backupplan.ArchiveID(time.Now().Add(-48 * time.Hour))
	var job string
	if err = f.owner.QueryRow(f.ctx, `INSERT INTO core.backup_jobs(household_id,kind,target,snapshot_reference,requested_by,state,started_at,finished_at,result_code)
 VALUES($1,'verify','local',$3,$2,'complete',now(),now(),'ok') RETURNING id`, f.identity.HouseholdID, f.identity.UserID, snapshot).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO core.backup_runs(state,application_version,repository_reference,repository_kind,job_id,snapshot_reference,schema_manifest,started_at,finished_at,verified_at,total_bytes)
 VALUES('complete','0.1.0','/data/backups','local',$1,$2,'{}',now()-interval '2 days',now(),now(),12345)`, job, snapshot); err != nil {
		t.Fatal(err)
	}
	stale := func() bool {
		t.Helper()
		response, body, err := f.request("GET", "/api/v1/core/backup", f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("overview: %v %v", response, err)
		}
		var overview struct {
			ProbeStale bool `json:"probe_stale"`
		}
		if err = json.Unmarshal(body, &overview); err != nil {
			t.Fatal(err)
		}
		return overview.ProbeStale
	}
	if !stale() {
		t.Fatal("an upload's validation refreshed the scheduled probe")
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.backup_runs SET verified_at=now()+interval '1 minute' WHERE snapshot_reference=$1", snapshot); err != nil {
		t.Fatal(err)
	}
	if stale() {
		t.Fatal("a trial restore of the imported point did not refresh the probe")
	}
}
