package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/backupplan"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Server) registerBackupRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/backup", s.require(s.backupPage))
	mux.Handle("GET /settings/system", s.require(s.systemPage))
	mux.Handle("GET /api/v1/core/backup", s.require(s.backupAPI))
	mux.Handle("GET /api/v1/core/backup/jobs/{id}", s.require(s.backupJobAPI))
	mux.Handle("POST /settings/backup/jobs", s.require(s.requestBackupJob))
	mux.Handle("POST /settings/backup/jobs/{id}/cancel", s.require(s.cancelBackupJob))
	mux.Handle("POST /settings/backup/settings", s.require(s.saveBackupSettings))
	mux.Handle("GET /settings/backup/files/{target}/{snapshot}", s.require(s.downloadBackupArchive))
	upload := s.require(s.uploadBackupArchive)
	mux.HandleFunc("POST /api/v1/core/backup/upload", func(w http.ResponseWriter, r *http.Request) {
		// Require the CSRF header so session middleware never parses the entire
		// file as an ordinary multipart form before authorization.
		if r.Header.Get("X-CSRF-Token") == "" || r.Header.Get("Content-Type") != "application/octet-stream" {
			s.fail(w, r, store.ErrForbidden)
			return
		}
		upload.ServeHTTP(w, r)
	})
}

// backupSnapshot is one restore point shown to administrators.
type backupSnapshot struct {
	Snapshot      string     `json:"snapshot"`
	Short         string     `json:"short"`
	Kind          string     `json:"kind"`
	Started       time.Time  `json:"started"`
	Version       string     `json:"version"`
	Verified      *time.Time `json:"verified,omitempty"`
	TotalBytes    *int64     `json:"total_bytes,omitempty"`
	Compatible    bool       `json:"compatible"`
	Confirmation  string     `json:"confirmation"`
	Archive       string     `json:"archive,omitempty"`
	DownloadReady bool       `json:"download_ready,omitempty"`
}

// backupJob is one requested or scheduled unit of backup work.
type backupJob struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Target    string     `json:"target"`
	State     string     `json:"state"`
	Phase     string     `json:"phase,omitempty"`
	Result    string     `json:"result,omitempty"`
	Snapshot  string     `json:"snapshot,omitempty"`
	Requester string     `json:"requester,omitempty"`
	Created   time.Time  `json:"created"`
	Started   *time.Time `json:"started,omitempty"`
	Finished  *time.Time `json:"finished,omitempty"`
	Previous  string     `json:"previous,omitempty"`
}

// Active reports work that has not finished yet.
func (j backupJob) Active() bool {
	return j.State == "requested" || j.State == "running" || j.State == "preparing" || j.State == "awaiting_drain" || j.State == "swapping"
}

func (j backupJob) OffsiteFailure() bool { return strings.HasPrefix(j.Result, "offsite_") }
func (j backupJob) ResultReason() string { return strings.TrimPrefix(j.Result, "offsite_") }

// backupOverview is everything the backup page and its poller show.
type backupOverview struct {
	Settings          backupplan.Settings `json:"settings"`
	SettingsVersion   int64               `json:"settings_version"`
	ServiceSeen       *time.Time          `json:"service_seen,omitempty"`
	ServiceOnline     bool                `json:"service_online"`
	ServiceVersion    string              `json:"service_version,omitempty"`
	LocalLabel        string              `json:"local_label,omitempty"`
	OffsiteLabel      string              `json:"offsite_label,omitempty"`
	OffsiteConfigured bool                `json:"offsite_configured"`
	Local             backupplan.Health   `json:"local"`
	Offsite           backupplan.Health   `json:"offsite"`
	LastVerified      *time.Time          `json:"last_verified,omitempty"`
	ProbeStale        bool                `json:"probe_stale"`
	LocalSnapshots    []backupSnapshot    `json:"local_snapshots"`
	OffsiteSnapshots  []backupSnapshot    `json:"offsite_snapshots"`
	Jobs              []backupJob         `json:"jobs"`
	ActiveRestore     *backupJob          `json:"active_restore,omitempty"`
	Previous          string              `json:"previous,omitempty"`
	// Retained reports any database kept by a live restore or a rollback:
	// the rollback copy (Previous) or a restored state that was rolled back.
	Retained bool   `json:"retained"`
	Zone     string `json:"zone"`
	location *time.Location
}

func (s *Server) loadBackupOverview(ctx context.Context) (backupOverview, error) {
	o := backupOverview{Settings: backupplan.Defaults(), LocalSnapshots: []backupSnapshot{}, OffsiteSnapshots: []backupSnapshot{}, Jobs: []backupJob{}}
	var localTime time.Time
	var localLabel, offsiteLabel, serviceVersion *string
	err := s.Pool.QueryRow(ctx, `SELECT h.timezone,coalesce(b.local_time,'03:00'::time),coalesce(b.local_interval_hours,24),coalesce(b.keep_daily,7),coalesce(b.keep_weekly,4),
 coalesce(b.keep_monthly,12),coalesce(b.offsite_enabled,false),coalesce(b.offsite_interval_hours,24),coalesce(b.offsite_keep_daily,7),coalesce(b.offsite_keep_weekly,4),
 coalesce(b.offsite_keep_monthly,12),coalesce(b.probe_interval_days,7),coalesce(b.probe_target,'local'),coalesce(b.version,0),b.service_seen_at,b.service_version,b.local_label,b.offsite_label
 FROM core.households h LEFT JOIN core.backup_settings b ON b.household_id=h.id WHERE h.singleton=1`).Scan(&o.Zone, &localTime,
		&o.Settings.LocalIntervalHours, &o.Settings.KeepDaily, &o.Settings.KeepWeekly, &o.Settings.KeepMonthly, &o.Settings.OffsiteEnabled,
		&o.Settings.OffsiteIntervalHours, &o.Settings.OffsiteKeepDaily, &o.Settings.OffsiteKeepWeekly, &o.Settings.OffsiteKeepMonthly,
		&o.Settings.ProbeIntervalDays, &o.Settings.ProbeTarget, &o.SettingsVersion, &o.ServiceSeen, &serviceVersion, &localLabel, &offsiteLabel)
	if err != nil {
		return o, err
	}
	o.Settings.LocalTime = localTime.Format("15:04")
	if o.location, err = time.LoadLocation(o.Zone); err != nil {
		o.location = time.UTC
	}
	if serviceVersion != nil {
		o.ServiceVersion = *serviceVersion
	}
	if localLabel != nil {
		o.LocalLabel = *localLabel
	}
	if offsiteLabel != nil {
		o.OffsiteLabel, o.OffsiteConfigured = *offsiteLabel, true
	}
	now := time.Now()
	o.ServiceOnline = o.ServiceSeen != nil && now.Sub(*o.ServiceSeen) < 3*time.Minute
	current := make(map[string]int, len(database.Schemas))
	for _, schema := range database.Schemas {
		current[schema] = database.ExpectedVersion(schema)
	}
	rows, err := s.Pool.Query(ctx, `SELECT repository_kind,snapshot_reference,started_at,application_version,verified_at,total_bytes,schema_manifest FROM core.backup_runs
 WHERE state='complete' AND snapshot_reference IS NOT NULL ORDER BY started_at DESC LIMIT 120`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var item backupSnapshot
		var schemas map[string]int
		if err = rows.Scan(&item.Kind, &item.Snapshot, &item.Started, &item.Version, &item.Verified, &item.TotalBytes, &schemas); err != nil {
			rows.Close()
			return o, err
		}
		if len(item.Snapshot) >= 8 {
			item.Short = item.Snapshot[:8]
		}
		item.Compatible = backupplan.SchemaCompatible(schemas, current)
		item.Confirmation = backupplan.RestoreConfirmation(item.Snapshot)
		item.Archive = backupplan.ArchiveName(item.Snapshot)
		if item.Archive != "" {
			item.DownloadReady = archiveDownloadReady(item.Kind, item.Snapshot)
		}
		if item.Kind == "offsite" {
			o.OffsiteSnapshots = append(o.OffsiteSnapshots, item)
		} else {
			o.LocalSnapshots = append(o.LocalSnapshots, item)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return o, err
	}
	health := func(kind string, enabled bool, interval int) (backupplan.Health, error) {
		var since, success, ended *time.Time
		var failed bool
		if err := s.Pool.QueryRow(ctx, backupplan.HealthQuery, kind).Scan(&since, &success, &failed, &ended); err != nil {
			return backupplan.Health{}, err
		}
		return backupplan.Evaluate(now, o.location, o.Settings.LocalTime, interval, enabled, since, success, failed), nil
	}
	if o.Local, err = health("local", true, o.Settings.LocalIntervalHours); err != nil {
		return o, err
	}
	if o.Offsite, err = health("offsite", o.Settings.OffsiteEnabled, o.Settings.OffsiteIntervalHours); err != nil {
		return o, err
	}
	// Verification freshness is read independently of the listed snapshots,
	// and only a check of the configured probe target keeps the probe fresh.
	var probeVerified *time.Time
	if err = s.Pool.QueryRow(ctx, `SELECT max(verified_at),max(verified_at) FILTER (WHERE repository_kind=$1) FROM core.backup_runs WHERE verified_at IS NOT NULL`,
		o.Settings.ProbeTarget).Scan(&o.LastVerified, &probeVerified); err != nil {
		return o, err
	}
	o.ProbeStale = backupplan.ProbeStale(now, o.Settings.ProbeIntervalDays, probeVerified)
	rows, err = s.Pool.Query(ctx, `SELECT j.id::text,j.kind,j.target,j.state,coalesce(j.phase,''),coalesce(j.result_code,''),coalesce(j.snapshot_reference,coalesce(j.result_snapshot,'')),
 coalesce(u.username,''),j.created_at,j.started_at,j.finished_at,coalesce(j.previous_database,'')
 FROM core.backup_jobs j LEFT JOIN core.users u ON u.id=j.requested_by ORDER BY (j.state IN ('running','preparing','awaiting_drain','swapping')) DESC,(j.state='requested') DESC,j.created_at DESC,j.id DESC LIMIT 15`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var job backupJob
		if err = rows.Scan(&job.ID, &job.Kind, &job.Target, &job.State, &job.Phase, &job.Result, &job.Snapshot, &job.Requester, &job.Created, &job.Started, &job.Finished, &job.Previous); err != nil {
			rows.Close()
			return o, err
		}
		if job.Kind == "restore" && job.Active() && o.ActiveRestore == nil {
			active := job
			o.ActiveRestore = &active
		}
		o.Jobs = append(o.Jobs, job)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return o, err
	}
	// pg_database is readable by every role; no restore runs while the
	// overview offers discarding, because the service holds its lock then.
	err = s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT previous_database FROM core.backup_jobs WHERE previous_database IS NOT NULL ORDER BY finished_at DESC LIMIT 1),''),
 EXISTS(SELECT 1 FROM pg_database WHERE starts_with(datname,'household_pre_restore_') OR starts_with(datname,'household_restore_job_'))`).Scan(&o.Previous, &o.Retained)
	o.Retained = o.Retained && o.ActiveRestore == nil
	return o, err
}

func (s *Server) backupPage(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	overview, err := s.loadBackupOverview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Sicherung")
	p.View, p.Extra = "backup", backupPageData{Overview: overview, Locale: requestLocale(r)}
	s.render(w, r, p)
}

// backupPageData adds display helpers to the overview for the template.
type backupPageData struct {
	Overview backupOverview
	Locale   string
}

// backupPageView carries the page for nested templates that need its CSRF token.
type backupPageView struct {
	backupPageData
	Page Page
}

// WithPage pairs the display helpers with the page.
func (d backupPageData) WithPage(p Page) backupPageView {
	return backupPageView{backupPageData: d, Page: p}
}

// When formats an instant in the household zone.
func (d backupPageData) When(t *time.Time) string {
	if t == nil {
		return ""
	}
	layout := "02.01.2006 15:04"
	if d.Locale == "en" {
		layout = "2006-01-02 15:04"
	}
	return t.In(d.Overview.location).Format(layout)
}

// At formats a non-nil instant.
func (d backupPageData) At(t time.Time) string { return d.When(&t) }

// Size formats a byte count.
func (d backupPageData) Size(n *int64) string {
	if n == nil {
		return ""
	}
	value, units := float64(*n), []string{"B", "KB", "MB", "GB", "TB"}
	unit := 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	return strconv.FormatFloat(value, 'f', min(unit, 1), 64) + " " + units[unit]
}

func (s *Server) backupAPI(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	overview, err := s.loadBackupOverview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) backupJobAPI(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" || !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	var job backupJob
	err := s.Pool.QueryRow(r.Context(), `SELECT j.id::text,j.kind,j.target,j.state,coalesce(j.phase,''),coalesce(j.result_code,''),coalesce(j.snapshot_reference,coalesce(j.result_snapshot,'')),
 coalesce(u.username,''),j.created_at,j.started_at,j.finished_at,coalesce(j.previous_database,'')
 FROM core.backup_jobs j LEFT JOIN core.users u ON u.id=j.requested_by WHERE j.id=$1`, r.PathValue("id")).Scan(
		&job.ID, &job.Kind, &job.Target, &job.State, &job.Phase, &job.Result, &job.Snapshot, &job.Requester, &job.Created, &job.Started, &job.Finished, &job.Previous)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// requestBackupJob records backup work for the backup service. The service
// validates the request again with its own privileges before performing it.
func (s *Server) requestBackupJob(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	kind, target := r.FormValue("kind"), r.FormValue("target")
	snapshot, confirmation := r.FormValue("snapshot"), r.FormValue("confirmation")
	valid := map[string][]string{"backup": {"local", "both"}, "verify": {"local", "offsite"}, "restore": {"local", "offsite"},
		"offsite_test": {"offsite"}, "discard_previous": {"local"}}
	targets, ok := valid[kind]
	if !ok || !contains(targets, target) {
		s.fail(w, r, store.Invalid{Message: "Unbekannter Sicherungsauftrag"})
		return
	}
	needsSnapshot := kind == "verify" || kind == "restore"
	if needsSnapshot != (snapshot != "") || needsSnapshot && !backupplan.ValidSnapshot(snapshot) {
		s.fail(w, r, store.Invalid{Message: "Wiederherstellungspunkt auswählen"})
		return
	}
	if kind == "restore" && !backupplan.ConfirmationMatches(snapshot, confirmation) {
		s.fail(w, r, store.Invalid{Message: "Die Bestätigung stimmt nicht mit dem Wiederherstellungspunkt überein"})
		return
	}
	overview, err := s.loadBackupOverview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if (target == "offsite" || target == "both") && !overview.OffsiteConfigured {
		s.fail(w, r, store.Invalid{Message: "Kein externes Sicherungsziel eingerichtet"})
		return
	}
	if kind == "discard_previous" && r.FormValue("confirm") != "on" {
		s.fail(w, r, store.Invalid{Message: "Bestätige das endgültige Löschen des früheren Stands."})
		return
	}
	if kind == "discard_previous" && !overview.Retained {
		s.fail(w, r, store.Invalid{Message: "Es gibt keinen aufbewahrten früheren Stand"})
		return
	}
	if needsSnapshot {
		var found *backupSnapshot
		list := overview.LocalSnapshots
		if target == "offsite" {
			list = overview.OffsiteSnapshots
		}
		for index := range list {
			if list[index].Snapshot == snapshot {
				found = &list[index]
			}
		}
		if found == nil {
			s.fail(w, r, store.Invalid{Message: "Wiederherstellungspunkt nicht gefunden"})
			return
		}
		if !found.Compatible {
			s.fail(w, r, store.Invalid{Message: "Dieser Wiederherstellungspunkt stammt von einer anderen Version und kann nur mit dieser Version per Wartungsbefehl wiederhergestellt werden"})
			return
		}
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()
	if kind == "restore" || kind == "discard_previous" {
		err = auth.LockRecentSession(r.Context(), tx, id)
	} else {
		err = auth.LockSession(r.Context(), tx, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	job := value.UUID()
	// The service re-checks that this session is still recently re-authenticated.
	var session any
	if kind == "restore" || kind == "discard_previous" {
		session = id.SessionID
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO core.backup_jobs(id,household_id,kind,target,snapshot_reference,requested_by,requested_session,confirmation)
 VALUES($1,$2,$3,$4,nullif($5,''),$6,$7,nullif($8,''))`, job, id.HouseholdID, kind, target, snapshot, id.UserID, session, confirmation)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "backup_jobs_one_active_restore":
			s.fail(w, r, store.Invalid{Message: "Ein anderer Wiederherstellungsvorgang läuft gerade."})
			return
		case "backup_jobs_one_pending":
			s.fail(w, r, store.Invalid{Message: "Für dieses Ziel wartet bereits ein Auftrag desselben Typs."})
			return
		}
	}
	if err == nil {
		err = audit.Event(r.Context(), tx, "core", "backup_jobs", job, "backup_requested", nil)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.backupDone(w, r, map[string]string{"id": job}, "Auftrag gespeichert. Der Sicherungsdienst übernimmt ihn in Kürze.")
}

func contains(list []string, item string) bool {
	for _, value := range list {
		if value == item {
			return true
		}
	}
	return false
}

func (s *Server) backupDone(w http.ResponseWriter, r *http.Request, body map[string]string, message string) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, body)
		return
	}
	p := s.page(r, "Sicherung")
	p.View, p.Success = "message", message
	s.render(w, r, p)
}

func wantsJSON(r *http.Request) bool {
	return r.Header.Get("Accept") == "application/json" || len(r.Header.Values("X-CSRF-Token")) > 0
}

func (s *Server) cancelBackupJob(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" || !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()
	if err = auth.LockSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	tag, err := tx.Exec(r.Context(), "UPDATE core.backup_jobs SET state='cancelled',result_code='cancelled',finished_at=now() WHERE id=$1 AND state='requested'", r.PathValue("id"))
	if err == nil && tag.RowsAffected() == 0 {
		err = store.ErrConflict
	}
	if err == nil {
		err = audit.Event(r.Context(), tx, "core", "backup_jobs", r.PathValue("id"), "backup_cancelled", nil)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.backupDone(w, r, map[string]string{"id": r.PathValue("id")}, "Auftrag abgebrochen.")
}

func formInt(r *http.Request, name string) int {
	n, err := strconv.Atoi(r.FormValue(name))
	if err != nil {
		return -1
	}
	return n
}

func (s *Server) saveBackupSettings(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	settings := backupplan.Settings{
		LocalTime: r.FormValue("local_time"), LocalIntervalHours: formInt(r, "local_interval_hours"),
		KeepDaily: formInt(r, "keep_daily"), KeepWeekly: formInt(r, "keep_weekly"), KeepMonthly: formInt(r, "keep_monthly"),
		OffsiteEnabled: r.FormValue("offsite_enabled") == "on", OffsiteIntervalHours: formInt(r, "offsite_interval_hours"),
		OffsiteKeepDaily: formInt(r, "offsite_keep_daily"), OffsiteKeepWeekly: formInt(r, "offsite_keep_weekly"), OffsiteKeepMonthly: formInt(r, "offsite_keep_monthly"),
		ProbeIntervalDays: formInt(r, "probe_interval_days"), ProbeTarget: r.FormValue("probe_target"),
	}
	if err := settings.Validate(); err != nil {
		s.fail(w, r, store.Invalid{Message: "Bitte die Angaben zu Zeitplan und Aufbewahrung prüfen"})
		return
	}
	version, err := strconv.ParseInt(r.FormValue("version"), 10, 64)
	if err != nil || version < 0 {
		s.fail(w, r, store.Invalid{Message: "Bitte die Seite neu laden"})
		return
	}
	tx, err := audit.Begin(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()
	if err = auth.LockSession(r.Context(), tx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	tag, err := tx.Exec(r.Context(), `INSERT INTO core.backup_settings(household_id,local_time,local_interval_hours,keep_daily,keep_weekly,keep_monthly,offsite_enabled,
 offsite_interval_hours,offsite_keep_daily,offsite_keep_weekly,offsite_keep_monthly,probe_interval_days,probe_target,version,updated_by,updated_at)
 VALUES($1,$2::time,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,$14,now())
 ON CONFLICT(household_id) DO UPDATE SET local_time=excluded.local_time,local_interval_hours=excluded.local_interval_hours,keep_daily=excluded.keep_daily,
  keep_weekly=excluded.keep_weekly,keep_monthly=excluded.keep_monthly,offsite_enabled=excluded.offsite_enabled,offsite_interval_hours=excluded.offsite_interval_hours,
  offsite_keep_daily=excluded.offsite_keep_daily,offsite_keep_weekly=excluded.offsite_keep_weekly,offsite_keep_monthly=excluded.offsite_keep_monthly,
  probe_interval_days=excluded.probe_interval_days,probe_target=excluded.probe_target,version=core.backup_settings.version+1,updated_by=excluded.updated_by,updated_at=now()
 WHERE core.backup_settings.version=$15`, id.HouseholdID, settings.LocalTime, settings.LocalIntervalHours, settings.KeepDaily, settings.KeepWeekly, settings.KeepMonthly,
		settings.OffsiteEnabled, settings.OffsiteIntervalHours, settings.OffsiteKeepDaily, settings.OffsiteKeepWeekly, settings.OffsiteKeepMonthly,
		settings.ProbeIntervalDays, settings.ProbeTarget, id.UserID, version)
	if err == nil && tag.RowsAffected() == 0 {
		err = store.ErrConflict
	}
	if err == nil {
		err = audit.Event(r.Context(), tx, "core", "backup_settings", "", "backup_settings_changed", nil)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.backupDone(w, r, map[string]string{"status": "saved"}, "Zeitplan gespeichert.")
}
