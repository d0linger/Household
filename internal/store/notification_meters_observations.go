package store

import (
	"context"
	"strconv"
	"time"

	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

type meterObservation struct {
	state                                string
	episode                              int64
	healthy, detected, cutoff, lastAlert *time.Time
}

func meterObservationOccurrence(kind, state string, episode int64) string {
	return kind + "/episode-" + strconv.FormatInt(episode, 10) + "/" + state
}

// Unknown observations preserve the episode and do not heal or rearm. A new
// source/configuration is a silent baseline, even if already abnormal.
func advanceMeterObservation(old meterObservation, signal meterSignal, reset, accepted bool, at time.Time) (meterObservation, error) {
	next := old
	if reset {
		next.state = "unknown"
		next.healthy = nil
		next.detected = nil
		next.cutoff = nil
		if signal.known {
			next.state = "baseline"
			if !signal.alert {
				next.state = "healthy"
				next.healthy = &at
			}
		}
		return next, nil
	}
	if !signal.known {
		return next, nil
	}
	if !signal.alert {
		next.healthy = &at
		if old.state == "alert" && accepted {
			next.state = "recovered"
			next.detected = &at
			return next, nil
		}
		if old.state == "recovered" {
			return next, nil
		}
		next.state = "healthy"
		next.detected = nil
		next.cutoff = nil
		return next, nil
	}
	if old.state == "alert" || old.state == "baseline" {
		return next, nil
	}
	if old.state == "unknown" || old.healthy == nil || old.lastAlert != nil && at.Before(old.lastAlert.Add(24*time.Hour)) {
		next.state = "baseline"
		next.detected = nil
		next.cutoff = nil
		return next, nil
	}
	if old.episode >= notification.MaxRevision {
		return next, ErrConflict
	}
	next.state = "alert"
	next.episode++
	next.cutoff = old.healthy
	next.detected = &at
	next.lastAlert = &at
	return next, nil
}

func observeMeterNotification(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, kind string, at time.Time) (*notification.Intent, error) {
	signal, err := meterNotificationSignal(ctx, tx, household, s, kind, at)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.notification_observations(household_id,register_id,kind,module_generation,module_activated_at,
 config_version,register_version,source_stamp,observed_at,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'unknown')
 ON CONFLICT(household_id,register_id,kind) DO NOTHING`, household, s.id, kind, s.generation, s.activated, s.version, s.registerVersion, signal.stamp, at)
	if err != nil {
		return nil, err
	}
	var old meterObservation
	var generation, config, register int64
	var activated, observed time.Time
	var stamp string
	err = tx.QueryRow(ctx, `SELECT module_generation,module_activated_at,config_version,register_version,source_stamp,observed_at,
 state,episode,last_healthy_at,detected_at,consent_cutoff,last_alert_at FROM meters.notification_observations
 WHERE household_id=$1 AND register_id=$2 AND kind=$3 FOR UPDATE`, household, s.id, kind).
		Scan(&generation, &activated, &config, &register, &stamp, &observed, &old.state, &old.episode, &old.healthy, &old.detected, &old.cutoff, &old.lastAlert)
	if err != nil {
		return nil, err
	}
	if at.Before(observed) {
		return nil, nil
	}
	reset := generation != s.generation || !activated.Equal(s.activated) || config != s.version || register != s.registerVersion || stamp != signal.stamp
	// The inserted unknown state gets a silent initial baseline. Existing unknown
	// states also require an actual healthy sample before their first episode.
	if old.state == "unknown" && old.healthy == nil {
		reset = true
	}
	var accepted bool
	if old.state == "alert" && signal.known && !signal.alert && !reset {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_intents i WHERE i.household_id=$1 AND i.user_id=$2
 AND i.module_id='meters' AND i.module_generation=$3 AND i.resource_type='meter_observation' AND i.resource_id=$4
 AND i.source_revision=$5 AND i.occurrence=$6 AND(i.state='accepted' OR EXISTS(
 SELECT 1 FROM core.notification_deliveries d WHERE d.intent_id=i.id AND d.state='accepted')))`, household, s.user, s.generation, s.id, s.version,
			meterObservationOccurrence(kind, "alert", old.episode)).Scan(&accepted)
		if err != nil {
			return nil, err
		}
	}
	next, err := advanceMeterObservation(old, signal, reset, accepted, at)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE meters.notification_observations SET module_generation=$4,module_activated_at=$5,
 config_version=$6,register_version=$7,source_stamp=$8,observed_at=$9,state=$10,episode=$11,last_healthy_at=$12,
 detected_at=$13,consent_cutoff=$14,last_alert_at=$15 WHERE household_id=$1 AND register_id=$2 AND kind=$3`, household, s.id, kind, s.generation,
		s.activated, s.version, s.registerVersion, signal.stamp, at, next.state, next.episode, next.healthy, next.detected, next.cutoff, next.lastAlert)
	if err != nil {
		return nil, err
	}
	if !signal.known || !s.active || s.since == nil || next.detected == nil || next.cutoff == nil || s.since.After(*next.cutoff) ||
		(next.state != "alert" && next.state != "recovered") || !next.detected.Add(24*time.Hour).After(at) {
		return nil, nil
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: s.user, Module: "meters", Generation: s.generation,
		ResourceType: "meter_observation", ResourceID: s.id, Revision: s.version, Category: "meters", Occurrence: meterObservationOccurrence(kind, next.state, next.episode),
		EventAt: *next.detected, NotBefore: *next.detected, ExpiresAt: next.detected.Add(24 * time.Hour)}, nil
}
