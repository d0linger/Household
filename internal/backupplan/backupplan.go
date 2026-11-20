// Package backupplan holds the pure scheduling, health and confirmation rules
// shared by the web process (which displays and requests backup work) and the
// owner backup service (which performs it). It has no database or I/O access.
package backupplan

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Settings mirrors the schedule columns of core.backup_settings.
type Settings struct {
	LocalTime            string `json:"local_time"` // HH:MM in the household time zone
	LocalIntervalHours   int    `json:"local_interval_hours"`
	KeepDaily            int    `json:"keep_daily"`
	KeepWeekly           int    `json:"keep_weekly"`
	KeepMonthly          int    `json:"keep_monthly"`
	OffsiteEnabled       bool   `json:"offsite_enabled"`
	OffsiteIntervalHours int    `json:"offsite_interval_hours"`
	OffsiteKeepDaily     int    `json:"offsite_keep_daily"`
	OffsiteKeepWeekly    int    `json:"offsite_keep_weekly"`
	OffsiteKeepMonthly   int    `json:"offsite_keep_monthly"`
	ProbeIntervalDays    int    `json:"probe_interval_days"`
	ProbeTarget          string `json:"probe_target"`
}

// Defaults are the Core41 behavior: daily at 03:00, 7/4/12 retention, a
// weekly restore probe of the local repository and no offsite copy.
func Defaults() Settings {
	return Settings{LocalTime: "03:00", LocalIntervalHours: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12,
		OffsiteIntervalHours: 24, OffsiteKeepDaily: 7, OffsiteKeepWeekly: 4, OffsiteKeepMonthly: 12,
		ProbeIntervalDays: 7, ProbeTarget: "local"}
}

var clock = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

func allowed(value int, choices ...int) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

// Validate applies the same bounds as the database CHECK constraints, so the
// web layer can return a precise field error before writing.
func (s Settings) Validate() error {
	switch {
	case !clock.MatchString(s.LocalTime):
		return errors.New("local_time")
	case !allowed(s.LocalIntervalHours, 1, 2, 3, 4, 6, 8, 12, 24):
		return errors.New("local_interval_hours")
	case s.KeepDaily < 1 || s.KeepDaily > 90:
		return errors.New("keep_daily")
	case s.KeepWeekly < 0 || s.KeepWeekly > 104:
		return errors.New("keep_weekly")
	case s.KeepMonthly < 0 || s.KeepMonthly > 120:
		return errors.New("keep_monthly")
	case !allowed(s.OffsiteIntervalHours, 1, 2, 3, 4, 6, 8, 12, 24, 48, 168):
		return errors.New("offsite_interval_hours")
	case s.OffsiteKeepDaily < 1 || s.OffsiteKeepDaily > 90:
		return errors.New("offsite_keep_daily")
	case s.OffsiteKeepWeekly < 0 || s.OffsiteKeepWeekly > 104:
		return errors.New("offsite_keep_weekly")
	case s.OffsiteKeepMonthly < 0 || s.OffsiteKeepMonthly > 120:
		return errors.New("offsite_keep_monthly")
	case !allowed(s.ProbeIntervalDays, 0, 1, 7, 14, 30):
		return errors.New("probe_interval_days")
	case s.ProbeTarget != "local" && s.ProbeTarget != "offsite":
		return errors.New("probe_target")
	}
	return nil
}

// LastSlot returns the most recent scheduled time at or before now. Slots
// start at the configured local time and repeat every intervalHours; longer
// intervals than a day (48, 168 hours) count whole days from the Unix epoch
// in the household zone, so they do not drift.
func LastSlot(now time.Time, zone *time.Location, localTime string, intervalHours int) (time.Time, error) {
	match := clock.FindStringSubmatch(localTime)
	if match == nil || intervalHours < 1 {
		return time.Time{}, errors.New("invalid schedule")
	}
	if zone == nil {
		zone = time.UTC
	}
	var hour, minute int
	_, _ = fmt.Sscanf(match[1]+" "+match[2], "%d %d", &hour, &minute)
	local := now.In(zone)
	anchor := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, zone)
	if intervalHours > 24 {
		days := intervalHours / 24
		dayNumber := int(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400)
		anchor = anchor.AddDate(0, 0, -(dayNumber % days))
		if anchor.After(local) {
			anchor = anchor.AddDate(0, 0, -days)
		}
		return anchor, nil
	}
	if anchor.After(local) {
		anchor = anchor.AddDate(0, 0, -1)
	}
	// Walk forward in whole intervals of wall-clock time from the day's
	// anchor (time.Date normalizes the hour), so the slots stay on the
	// configured clock times across daylight-saving changes.
	slot := anchor
	for k := 1; ; k++ {
		next := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), hour+k*intervalHours, minute, 0, 0, zone)
		if next.After(local) || !next.Before(anchor.AddDate(0, 0, 1)) {
			return slot, nil
		}
		slot = next
	}
}

// NextSlot returns the first scheduled time after the given last slot.
func NextSlot(last time.Time, zone *time.Location, localTime string, intervalHours int) (time.Time, error) {
	for step := time.Hour; step <= 8*24*time.Hour; step += time.Hour {
		slot, err := LastSlot(last.Add(step), zone, localTime, intervalHours)
		if err != nil {
			return time.Time{}, err
		}
		if slot.After(last) {
			return slot, nil
		}
	}
	return time.Time{}, errors.New("no next slot")
}

// Due reports whether a run is due: nothing was attempted since the most
// recent slot. A failed attempt waits for the next slot (and raises a warning)
// instead of retrying in a tight loop.
func Due(now time.Time, zone *time.Location, localTime string, intervalHours int, lastAttempt *time.Time) (bool, error) {
	slot, err := LastSlot(now, zone, localTime, intervalHours)
	if err != nil {
		return false, err
	}
	return lastAttempt == nil || lastAttempt.Before(slot), nil
}

// State is the health verdict of one backup target.
type State string

const (
	StateOK       State = "ok"
	StateNever    State = "never"
	StateFailed   State = "failed"
	StateOverdue  State = "overdue"
	StateDisabled State = "disabled"
)

// Health is what administrators, metrics and the healthcheck read.
type Health struct {
	State       State      `json:"state"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	NextDue     *time.Time `json:"next_due,omitempty"`
	AgeSeconds  *int64     `json:"age_seconds,omitempty"`
}

// grace is added to the expected next run before a target counts as overdue:
// one tenth of the interval, bounded to 15 minutes … 2 hours, so a running
// backup or a late scheduler tick does not turn the status red.
func grace(intervalHours int) time.Duration {
	g := time.Duration(intervalHours) * time.Hour / 10
	return max(15*time.Minute, min(g, 2*time.Hour))
}

// Evaluate derives the health of one target from its own schedule. lastFailed
// reports whether the most recent attempt failed.
// since is when the target became active (the household's creation, or the
// offsite target's enabling); a target that never succeeded is StateNever
// until the first slot after since plus grace passed, then StateOverdue.
func Evaluate(now time.Time, zone *time.Location, localTime string, intervalHours int, enabled bool, since, lastSuccess *time.Time, lastFailed bool) Health {
	if !enabled {
		return Health{State: StateDisabled, LastSuccess: lastSuccess}
	}
	h := Health{LastSuccess: lastSuccess}
	reference := lastSuccess
	if reference == nil {
		reference = since
	} else {
		age := int64(now.Sub(*lastSuccess) / time.Second)
		h.AgeSeconds = &age
	}
	if reference != nil {
		if next, err := NextSlot(*reference, zone, localTime, intervalHours); err == nil {
			h.NextDue = &next
		}
	}
	late := h.NextDue != nil && now.After(h.NextDue.Add(grace(intervalHours)))
	switch {
	case lastFailed:
		h.State = StateFailed
	case lastSuccess == nil && !late:
		h.State = StateNever
	case late:
		h.State = StateOverdue
	default:
		h.State = StateOK
	}
	return h
}

// HealthQuery reads one target's health inputs. $1 is the repository kind
// ('local' or 'offsite'). It returns the activation time, the newest
// successful run, whether the newest finished attempt failed and when that
// attempt ended (an incident key for notices). Attempts
// are runs and backup jobs together, ordered by when they ended, so a job
// that failed before it created a run, or after its run (retention), counts.
// Offsite failures carry the offsite_ prefix and do not fail the local target.
//
// Only history of the configured repository counts (the label the service
// reports in core.backup_settings): a replaced repository starts from its
// activation time. Before the service reported a label, all history counts.
const HealthQuery = `WITH configured AS (SELECT (SELECT CASE WHEN $1='local' THEN local_label ELSE offsite_label END
  FROM core.backup_settings LIMIT 1) AS label)
SELECT CASE WHEN $1='local' THEN (SELECT created_at FROM core.households WHERE singleton=1)
  ELSE (SELECT updated_at FROM core.backup_settings LIMIT 1) END,
 (SELECT max(r.started_at) FROM core.backup_runs r,configured c WHERE r.repository_kind=$1 AND r.state IN('complete','retention_pending')
   AND (c.label IS NULL OR r.repository_reference=c.label)),
 coalesce(latest.failed,false),latest.ended
FROM configured c LEFT JOIN LATERAL (SELECT ended,failed FROM (
   SELECT coalesce(r.finished_at,r.started_at) AS ended,r.state='failed' AS failed FROM core.backup_runs r
    WHERE r.repository_kind=$1 AND r.state IN('complete','retention_pending','failed','expired')
     AND (c.label IS NULL OR r.repository_reference=c.label)
   UNION ALL
   SELECT j.finished_at,j.state='failed' FROM core.backup_jobs j
    WHERE j.kind='backup' AND j.state IN('complete','failed') AND j.finished_at IS NOT NULL
     AND CASE WHEN $1='local' THEN coalesce(j.result_code,'') NOT LIKE 'offsite\_%' ELSE j.target='both' END
     AND (c.label IS NULL OR c.label=CASE WHEN $1='local' THEN j.local_label ELSE j.offsite_label END)
  ) attempts ORDER BY ended DESC LIMIT 1) latest ON true`

// ProbeStale reports whether the restore probe is older than its interval
// plus one day. A disabled probe still counts as stale after 30 days: module
// purge requires a backup verified within seven days.
func ProbeStale(now time.Time, intervalDays int, lastVerified *time.Time) bool {
	limit := 30
	if intervalDays > 0 {
		limit = intervalDays + 1
	}
	return lastVerified == nil || now.Sub(*lastVerified) > time.Duration(limit)*24*time.Hour
}

var snapshotID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSnapshot reports whether value is a full restic snapshot identifier.
func ValidSnapshot(value string) bool { return snapshotID.MatchString(value) }

// RestoreConfirmation is the phrase an administrator types to restore one
// snapshot. It names the snapshot, so a confirmation cannot be replayed for
// another restore point.
func RestoreConfirmation(snapshot string) string {
	if len(snapshot) < 8 {
		return ""
	}
	return "WIEDERHERSTELLEN " + strings.ToUpper(snapshot[:8])
}

// ConfirmationMatches compares a typed phrase with the expected one,
// ignoring surrounding whitespace and letter case.
func ConfirmationMatches(snapshot, typed string) bool {
	want := RestoreConfirmation(snapshot)
	return want != "" && strings.EqualFold(strings.Join(strings.Fields(typed), " "), want)
}

// SchemaCompatible reports whether a snapshot's schema manifest equals the
// running binary's schema versions exactly. Only such snapshots can be
// verified and restored by this release; older ones need their own release.
func SchemaCompatible(manifest, current map[string]int) bool {
	if len(manifest) == 0 || len(manifest) != len(current) {
		return false
	}
	for schema, version := range current {
		if manifest[schema] != version {
			return false
		}
	}
	return true
}
