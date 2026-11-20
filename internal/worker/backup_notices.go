package worker

import (
	"context"
	"fmt"
	"time"

	"household/internal/backupplan"
)

// backupNotice is one operational warning or outcome for administrators.
type backupNotice struct {
	kind, title, key string
}

// BackupNotices tells administrators in the app when a backup failed or is
// overdue, the offsite copy failed, the restore probe is stale, the backup
// service stopped reporting, or a live restore finished. Each incident has its
// own dedupe key, so dismissing an old warning never hides a new one.
func (w *Worker) BackupNotices(ctx context.Context) error {
	var zone string
	var localTime time.Time
	settings := backupplan.Defaults()
	var serviceSeen, created *time.Time
	err := w.Pool.QueryRow(ctx, `SELECT h.timezone,coalesce(b.local_time,'03:00'::time),coalesce(b.local_interval_hours,24),coalesce(b.offsite_enabled,false),
 coalesce(b.offsite_interval_hours,24),coalesce(b.probe_interval_days,7),b.service_seen_at,h.created_at
 FROM core.households h LEFT JOIN core.backup_settings b ON b.household_id=h.id WHERE h.singleton=1`).Scan(
		&zone, &localTime, &settings.LocalIntervalHours, &settings.OffsiteEnabled, &settings.OffsiteIntervalHours, &settings.ProbeIntervalDays, &serviceSeen, &created)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
	}
	now := time.Now()
	settings.LocalTime = localTime.Format("15:04")
	var notices []backupNotice
	type latest struct {
		since, success, ended *time.Time
		failed                bool
	}
	read := func(kind string) (latest, error) {
		var item latest
		err := w.Pool.QueryRow(ctx, backupplan.HealthQuery, kind).Scan(&item.since, &item.success, &item.failed, &item.ended)
		return item, err
	}
	// unix keys one incident by a time; a missing time keys the never-ran case.
	unix := func(at *time.Time) int64 {
		if at == nil {
			return 0
		}
		return at.Unix()
	}
	local, err := read("local")
	if err != nil {
		return err
	}
	switch health := backupplan.Evaluate(now, location, settings.LocalTime, settings.LocalIntervalHours, true, local.since, local.success, local.failed); health.State {
	case backupplan.StateFailed:
		notices = append(notices, backupNotice{"backup_failed", "Die letzte Sicherung ist fehlgeschlagen.", fmt.Sprintf("backup/failed/%d", unix(local.ended))})
	case backupplan.StateOverdue:
		notices = append(notices, backupNotice{"backup_overdue", "Die Sicherung ist überfällig.", fmt.Sprintf("backup/overdue/%d", unix(local.success))})
	}
	if settings.OffsiteEnabled {
		offsite, err := read("offsite")
		if err != nil {
			return err
		}
		if offsite.failed {
			notices = append(notices, backupNotice{"backup_offsite_failed", "Die externe Sicherungskopie ist fehlgeschlagen.", fmt.Sprintf("backup/offsite-failed/%d", unix(offsite.ended))})
		}
	}
	// A new installation gets a day before missing service or probe reports count.
	settled := created != nil && now.Sub(*created) > 26*time.Hour
	if settled && (serviceSeen == nil || now.Sub(*serviceSeen) > 15*time.Minute) {
		seen := int64(0)
		if serviceSeen != nil {
			seen = serviceSeen.Unix()
		}
		notices = append(notices, backupNotice{"backup_service_missing", "Der Sicherungsdienst meldet sich nicht.", fmt.Sprintf("backup/service/%d", seen)})
	}
	var verified *time.Time
	// Only a check of the configured probe target keeps the probe fresh.
	if err = w.Pool.QueryRow(ctx, "SELECT max(verified_at) FROM core.backup_runs WHERE repository_kind=coalesce((SELECT probe_target FROM core.backup_settings LIMIT 1),'local')").Scan(&verified); err != nil {
		return err
	}
	if settled && local.success != nil && backupplan.ProbeStale(now, settings.ProbeIntervalDays, verified) {
		since := int64(0)
		if verified != nil {
			since = verified.Unix()
		}
		notices = append(notices, backupNotice{"backup_probe_stale", "Keine Sicherung wurde kürzlich durch eine Probewiederherstellung geprüft.", fmt.Sprintf("backup/probe/%d", since)})
	}
	rows, err := w.Pool.Query(ctx, `SELECT id::text,state FROM core.backup_jobs WHERE kind='restore' AND state IN('complete','failed') AND finished_at>now()-interval '7 days'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return err
		}
		if state == "complete" {
			notices = append(notices, backupNotice{"backup_restore_complete", "Household wurde aus einer Sicherung wiederhergestellt.", "backup/restore/" + id})
		} else {
			notices = append(notices, backupNotice{"backup_restore_failed", "Eine Wiederherstellung ist fehlgeschlagen. Details stehen auf der Sicherungsseite.", "backup/restore/" + id})
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, notice := range notices {
		if _, err = w.Pool.Exec(ctx, `INSERT INTO core.notifications(household_id,user_id,module_id,kind,title,target_path,dedupe_key)
 SELECT m.household_id,m.user_id,NULL,$1,$2,'/settings/backup',$3||'/'||m.user_id FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 JOIN core.households h ON h.id=m.household_id AND h.singleton=1
 WHERE m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL ON CONFLICT(dedupe_key) DO NOTHING`, notice.kind, notice.title, notice.key); err != nil {
			return err
		}
	}
	return nil
}
