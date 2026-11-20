package web

import (
	"context"
	"net/http"
	"sort"
	"time"

	"household/internal/backupplan"
	"household/internal/buildinfo"
	"household/internal/database"
	"household/internal/metrics"
	"household/internal/store"

	"github.com/jackc/pgx/v5"
)

// metricGauges samples the database pool, the maintenance pause and the
// backup state for /metrics. A failed backup query omits only those gauges.
func (s *Server) metricGauges(ctx context.Context) []metrics.Gauge {
	stats := s.Pool.Stat()
	maintenance := 0.0
	if s.Paused() {
		maintenance = 1
	}
	gauges := []metrics.Gauge{
		{Name: "household_db_acquired_connections", Help: "Database connections in use.", Value: float64(stats.AcquiredConns())},
		{Name: "household_db_total_connections", Help: "Open database connections.", Value: float64(stats.TotalConns())},
		{Name: "household_maintenance", Help: "1 while a live restore pauses the app.", Value: maintenance},
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	overview, err := s.loadBackupOverview(queryCtx)
	if err != nil {
		return gauges
	}
	// Only a target that is current counts as healthy; one that never
	// succeeded is not. A switched-off target exports no health series.
	healthy := func(state backupplan.State) float64 {
		if state == backupplan.StateOK {
			return 1
		}
		return 0
	}
	for _, target := range []struct {
		name   string
		health backupplan.Health
	}{{"local", overview.Local}, {"offsite", overview.Offsite}} {
		labels := map[string]string{"target": target.name}
		if target.health.State != backupplan.StateDisabled {
			gauges = append(gauges, metrics.Gauge{Name: "household_backup_healthy", Help: "1 when the backup target is current by its own schedule.", Labels: labels, Value: healthy(target.health.State)})
		}
		if target.health.LastSuccess != nil {
			gauges = append(gauges, metrics.Gauge{Name: "household_backup_last_success_timestamp_seconds", Help: "Start of the last successful backup.", Labels: labels, Value: float64(target.health.LastSuccess.Unix())})
		}
	}
	if overview.LastVerified != nil {
		gauges = append(gauges, metrics.Gauge{Name: "household_backup_last_verified_timestamp_seconds", Help: "Last successful restore test.", Value: float64(overview.LastVerified.Unix())})
	}
	if overview.ServiceSeen != nil {
		gauges = append(gauges, metrics.Gauge{Name: "household_backup_service_seen_timestamp_seconds", Help: "Last heartbeat of the backup service.", Value: float64(overview.ServiceSeen.Unix())})
	}
	sort.SliceStable(gauges, func(i, j int) bool { return gauges[i].Name < gauges[j].Name })
	return gauges
}

// systemCheck is one line of the system page: a plain-language state with a
// tone and an optional link to the page where it can be fixed.
type systemCheck struct {
	Label  string
	Value  string
	Detail string
	Tone   string // ok, warn, bad
	Link   string
}

type systemPageData struct {
	Version  string
	Revision string
	Checks   []systemCheck
	Schemas  []schemaVersion
	Backup   backupPageData
}

type schemaVersion struct {
	Schema   string
	Version  int
	Expected int
}

// systemPage gathers the operational signals administrators need on one
// read-only page. It changes nothing and never shows a secret.
func (s *Server) systemPage(w http.ResponseWriter, r *http.Request) {
	if identity(r).Role != "admin" {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	ctx := r.Context()
	data := systemPageData{Version: buildinfo.Release(), Revision: buildinfo.Revision()}
	for _, schema := range database.Schemas {
		item := schemaVersion{Schema: schema, Expected: database.ExpectedVersion(schema)}
		_ = s.Pool.QueryRow(ctx, "SELECT version FROM "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()).Scan(&item.Version)
		data.Schemas = append(data.Schemas, item)
	}
	overview, err := s.loadBackupOverview(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Backup = backupPageData{Overview: overview, Locale: requestLocale(r)}
	ready := s.databaseReady(ctx)
	check := func(label, value, detail, tone, link string) {
		data.Checks = append(data.Checks, systemCheck{Label: label, Value: value, Detail: detail, Tone: tone, Link: link})
	}
	if ready {
		check("Datenbank", "Bereit", "Alle Verträge und Migrationen sind vollständig.", "ok", "")
	} else {
		check("Datenbank", "Nicht bereit", "Die Bereitschaftsprüfung schlägt fehl. Prüfe die Protokolle des App-Dienstes.", "bad", "")
	}
	switch {
	case !overview.ServiceOnline:
		check("Sicherungsdienst", "Nicht erreichbar", "Der Dienst backup hat sich seit mehr als drei Minuten nicht gemeldet.", "bad", "/settings/backup")
	case overview.Local.State == "failed" || overview.Local.State == "overdue":
		check("Lokale Sicherung", backupStateLabel(string(overview.Local.State)), "Die letzte Sicherung ist fehlgeschlagen oder überfällig.", "bad", "/settings/backup")
	case overview.Local.State == "never":
		check("Lokale Sicherung", backupStateLabel(string(overview.Local.State)), "Es gibt noch keine Sicherung. Starte eine Sicherung auf der Sicherungsseite.", "warn", "/settings/backup")
	default:
		check("Lokale Sicherung", backupStateLabel(string(overview.Local.State)), data.Backup.When(overview.Local.LastSuccess), "ok", "/settings/backup")
	}
	switch {
	case !overview.OffsiteConfigured:
		check("Externe Sicherung", "Nicht eingerichtet", "Ohne externes Ziel schützt die Sicherung nicht vor einem Ausfall dieses Rechners.", "warn", "/settings/backup")
	case !overview.Settings.OffsiteEnabled:
		check("Externe Sicherung", "Eingerichtet, Zeitplan aus", "Das externe Ziel ist eingerichtet, aber der Zeitplan ist ausgeschaltet.", "warn", "/settings/backup")
	case overview.Offsite.State == "failed" || overview.Offsite.State == "overdue":
		check("Externe Sicherung", backupStateLabel(string(overview.Offsite.State)), "Die letzte externe Kopie ist fehlgeschlagen oder überfällig.", "bad", "/settings/backup")
	case overview.Offsite.State == "never":
		check("Externe Sicherung", backupStateLabel(string(overview.Offsite.State)), "Es gibt noch keine externe Kopie. Starte eine externe Sicherung auf der Sicherungsseite.", "warn", "/settings/backup")
	default:
		check("Externe Sicherung", backupStateLabel(string(overview.Offsite.State)), data.Backup.When(overview.Offsite.LastSuccess), "ok", "/settings/backup")
	}
	if overview.ProbeStale {
		check("Wiederherstellung geprüft", "Veraltet", "Prüfe eine Sicherung durch eine Probewiederherstellung. Das Löschen von Moduldaten verlangt eine Prüfung der letzten sieben Tage.", "warn", "/settings/backup")
	} else {
		check("Wiederherstellung geprüft", data.Backup.When(overview.LastVerified), "", "ok", "/settings/backup")
	}
	if overview.Previous != "" {
		check("Früherer Stand", "Aufbewahrt", "Nach einer Wiederherstellung bleibt der vorherige Stand erhalten, bis du ihn verwirfst.", "warn", "/settings/backup")
	}
	switch s.Push.Configuration().State {
	case "ready":
		check("Gerätebenachrichtigungen", "Aktiv", "", "ok", "/settings/notifications")
	case "disabled":
		check("Gerätebenachrichtigungen", "Ausgeschaltet", "Web Push ist in der Konfiguration ausgeschaltet.", "warn", "")
	default:
		check("Gerätebenachrichtigungen", "Nicht bereit", "Prüfe die Web-Push-Einstellungen in der .env.", "warn", "")
	}
	if ring, err := s.Cfg.Keyring(); err == nil {
		active := ring.ActiveID()
		if len(active) > 8 {
			active = active[:8]
		}
		check("Verschlüsselung", "Aktiver Schlüssel "+active, "", "ok", "")
	} else {
		check("Verschlüsselung", "Fehler", "Der Schlüsselring konnte nicht gelesen werden.", "bad", "")
	}
	if s.checks.updates == nil {
		check("Updates", "Prüfung ausgeschaltet", "Mit HOUSEHOLD_UPDATE_CHECK=true fragt Household einmal täglich bei GitHub nach dem aktuellen Stand.", "ok", "")
	} else {
		switch result := s.checks.updates.Result(); {
		case result.Checked.IsZero():
			check("Updates", "Noch nicht geprüft", "", "warn", "")
		case result.Failed:
			check("Updates", "Prüfung fehlgeschlagen", "GitHub war nicht erreichbar; die Prüfung wird morgen wiederholt.", "warn", "")
		case result.Available:
			check("Updates", "Neuere Version verfügbar", "Lies die Versionshinweise und aktualisiere mit docker compose pull und docker compose up -d.", "warn", "")
		default:
			check("Updates", "Aktuell", "", "ok", "")
		}
	}
	if s.Cfg.MetricsToken != "" {
		check("Metriken", "Aktiv", "Abrufbar unter /metrics mit dem Kopfzeilen-Token.", "ok", "")
	} else {
		check("Metriken", "Ausgeschaltet", "", "ok", "")
	}
	p := s.page(r, "Systemstatus")
	p.View, p.Extra = "system", data
	s.render(w, r, p)
}

func backupStateLabel(state string) string {
	switch state {
	case "ok":
		return "Aktuell"
	case "never":
		return "Noch nie"
	case "failed":
		return "Fehlgeschlagen"
	case "overdue":
		return "Überfällig"
	case "disabled":
		return "Ausgeschaltet"
	}
	return state
}
