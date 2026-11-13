package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

const MeterNotificationScanJobKind = "meter_notification_scan_v1"

type meterNotificationSource struct {
	id, user, zone, input, measurement, unit           string
	version, registerVersion, meterVersion, generation int64
	activated, updated                                 time.Time
	since                                              *time.Time
	active, reading, anomaly, stale, dirty             bool
	staleSeconds, gapSeconds, minimum                  int
	daily, base                                        *string
}

func readMeterNotificationSource(ctx context.Context, tx pgx.Tx, household, id string) (meterNotificationSource, error) {
	var s meterNotificationSource
	err := tx.QueryRow(ctx, `SELECT n.id::text,coalesce(member.user_id::text,''),r.timezone,r.input_mode,r.measurement_kind,r.unit_code,
 n.version,r.version,meter.version,ms.data_generation,ms.changed_at,greatest(n.updated_at,r.updated_at,meter.updated_at),n.responsible_since,
 ms.state='enabled' AND r.archived_at IS NULL AND meter.archived_at IS NULL AND member.inactive_at IS NULL
 AND u.disabled_at IS NULL AND NOT coalesce(u.must_change_password,true),
 n.reading_enabled,n.anomaly_enabled,n.stale_enabled,EXISTS(SELECT 1 FROM meters.dirty_ranges d WHERE d.register_id=r.id),
 n.stale_after_seconds,r.max_gap_seconds,r.min_history,r.daily_warning::text,r.base_load_warning::text
 FROM meters.notification_settings n JOIN meters.registers r ON r.household_id=n.household_id AND r.id=n.id
 JOIN meters.meters meter ON meter.household_id=r.household_id AND meter.id=r.meter_id
 JOIN core.module_states ms ON ms.household_id=n.household_id AND ms.module_id='meters'
 LEFT JOIN core.memberships member ON member.household_id=n.household_id AND member.id=n.responsible_member_id
 LEFT JOIN core.users u ON u.id=member.user_id WHERE n.household_id=$1 AND n.id=$2`, household, id).
		Scan(&s.id, &s.user, &s.zone, &s.input, &s.measurement, &s.unit, &s.version, &s.registerVersion, &s.meterVersion, &s.generation,
			&s.activated, &s.updated, &s.since, &s.active, &s.reading, &s.anomaly, &s.stale, &s.dirty,
			&s.staleSeconds, &s.gapSeconds, &s.minimum, &s.daily, &s.base)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// Only today's or yesterday's civil due date can have a fresh 09:00 stage.
// Arithmetic occurrence selection cannot be blocked by an ancient missed plan.
func meterDueStages(anchor, zone string, days int, now time.Time) ([]time.Time, error) {
	start, err := time.Parse(time.DateOnly, anchor)
	if err != nil || days < 1 || days > 366 {
		return nil, ErrConflict
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	today, err := time.Parse(time.DateOnly, now.In(location).Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	var stages []time.Time
	for _, date := range []time.Time{today.AddDate(0, 0, -1), today} {
		delta := int((date.Unix() - start.Unix()) / 86400)
		if date.Before(start) || delta%days != 0 {
			continue
		}
		at, e := notification.CalendarStage(date.Format(time.DateOnly), zone, "09:00", 0)
		if e != nil {
			return nil, e
		}
		if !at.After(now) && at.Add(24*time.Hour).After(now) {
			stages = append(stages, at)
		}
	}
	return stages, nil
}

func meterReadingIntents(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, now time.Time) ([]notification.Intent, error) {
	if !s.active || !s.reading || s.since == nil || s.user == "" {
		return nil, nil
	}
	var plan, anchor string
	var version int64
	var updated time.Time
	var days int
	err := tx.QueryRow(ctx, `SELECT id::text,anchor_on::text,version,updated_at,interval_days FROM meters.reading_plans
 WHERE household_id=$1 AND register_id=$2 AND active`, household, s.id).Scan(&plan, &anchor, &version, &updated, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stages, err := meterDueStages(anchor, s.zone, days, now)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(s.zone)
	if err != nil {
		return nil, err
	}
	var out []notification.Intent
	for _, at := range stages {
		if s.since.After(at) || s.updated.After(at) || s.activated.After(at) || updated.After(at) {
			continue
		}
		day := at.In(location).Format(time.DateOnly)
		var completed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters.plan_completions c
 JOIN meters.reading_plans p ON p.household_id=c.household_id AND p.id=c.plan_id AND p.version=c.plan_version
 JOIN meters.readings r ON r.household_id=c.household_id AND r.id=c.reading_id AND r.version=c.reading_revision
 WHERE c.household_id=$1 AND c.plan_id=$2 AND c.plan_version=$3 AND c.due_on=$4::date
 AND r.register_id=p.register_id AND (NOT p.actual_only OR r.kind='actual'))`, household, plan, version, day).Scan(&completed)
		if err != nil {
			return nil, err
		}
		if completed {
			continue
		}
		out = append(out, notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: s.user, Module: "meters", Generation: s.generation,
			ResourceType: "meter_reading", ResourceID: s.id, Revision: s.version, Category: "meters",
			Occurrence: "reading/" + plan + "/" + strconv.FormatInt(version, 10) + "/" + day, EventAt: at, NotBefore: at, ExpiresAt: at.Add(24 * time.Hour)})
	}
	return out, nil
}

type meterSignal struct {
	known, alert bool
	stamp        string
}

func meterStamp(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (s *Store) meterNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Validate() != nil || i.Category != "meters" || i.Module != "meters" {
		return time.Time{}, nil
	}
	source, err := readMeterNotificationSource(ctx, tx, i.HouseholdID, i.ResourceID)
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if !source.active || source.user != i.UserID || source.version != i.Revision || source.generation != i.Generation || source.since == nil ||
		!i.NotBefore.Equal(i.EventAt) || !i.ExpiresAt.Equal(i.EventAt.Add(24*time.Hour)) {
		return time.Time{}, nil
	}
	if i.ResourceType == "meter_reading" {
		candidates, err := meterReadingIntents(ctx, tx, i.HouseholdID, source, i.EventAt)
		if err != nil {
			return time.Time{}, err
		}
		for _, candidate := range candidates {
			if candidate.Occurrence == i.Occurrence && candidate.EventAt.Equal(i.EventAt) {
				return i.EventAt, nil
			}
		}
		return time.Time{}, nil
	}
	if i.ResourceType != "meter_observation" {
		return time.Time{}, nil
	}
	kind, _, ok := strings.Cut(i.Occurrence, "/")
	if !ok {
		return time.Time{}, nil
	}
	if kind != "consumption" && kind != "base_load" && kind != "adapter" {
		return time.Time{}, nil
	}
	var state, stamp string
	var episode int64
	var cutoff, detected time.Time
	err = tx.QueryRow(ctx, `SELECT state,source_stamp,episode,consent_cutoff,detected_at FROM meters.notification_observations
 WHERE household_id=$1 AND register_id=$2 AND kind=$3 AND module_generation=$4 AND module_activated_at=$5
 AND config_version=$6 AND register_version=$7 AND state IN('alert','recovered')`, i.HouseholdID, i.ResourceID, kind, i.Generation,
		source.activated, source.version, source.registerVersion).Scan(&state, &stamp, &episode, &cutoff, &detected)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if i.Occurrence != meterObservationOccurrence(kind, state, episode) || !detected.Equal(i.EventAt) || source.since.After(cutoff) {
		return time.Time{}, nil
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, err
	}
	signal, err := meterNotificationSignal(ctx, tx, i.HouseholdID, source, kind, now)
	if err != nil || !signal.known || signal.stamp != stamp || signal.alert != (state == "alert") {
		return time.Time{}, err
	}
	return cutoff, nil
}
