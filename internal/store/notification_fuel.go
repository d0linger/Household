package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/civiltime"
	"household/internal/notification"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const FuelNotificationScanJobKind = "fuel_notification_scan_v1"

type fuelNotificationSource struct {
	id, vehicle, user, zone                                string
	version, vehicleVersion, cycle                         int64
	since                                                  *time.Time
	updated, activated                                     time.Time
	active                                                 bool
	service, inspection, insurance, due, series, threshold *string
}

// Source selection never infers a recipient from a creator or a personal vehicle
// default. Maintenance assignment remains independent of linked chore ownership.
func readFuelNotificationSource(ctx context.Context, tx pgx.Tx, household, entity, id string) (fuelNotificationSource, error) {
	var s fuelNotificationSource
	var err error
	switch entity {
	case "vehicles":
		err = tx.QueryRow(ctx, `SELECT v.id::text,v.id::text,coalesce(m.user_id::text,''),h.timezone,
 v.version,v.version,0::bigint,v.responsible_since,v.updated_at,ms.changed_at,
 v.archived_at IS NULL AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT coalesce(u.must_change_password,true),
 v.next_service_on::text,v.next_inspection_on::text,v.insurance_renewal_on::text
 FROM fuel.vehicles v JOIN core.households h ON h.id=v.household_id
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='fuel'
 LEFT JOIN core.memberships m ON m.household_id=v.household_id AND m.id=v.responsible_member_id
 LEFT JOIN core.users u ON u.id=m.user_id WHERE v.household_id=$1 AND v.id=$2`, household, id).
			Scan(&s.id, &s.vehicle, &s.user, &s.zone, &s.version, &s.vehicleVersion, &s.cycle, &s.since, &s.updated, &s.activated, &s.active, &s.service, &s.inspection, &s.insurance)
	case "maintenance_plans":
		err = tx.QueryRow(ctx, `SELECT p.id::text,v.id::text,coalesce(m.user_id::text,''),h.timezone,
 p.version,v.version,p.cycle,p.assignee_since,p.updated_at,ms.changed_at,
 p.active AND v.archived_at IS NULL AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT coalesce(u.must_change_password,true),
 p.due_on::text,p.odometer_series_id::text,p.due_odometer::text
 FROM fuel.maintenance_plans p JOIN fuel.vehicles v ON v.household_id=p.household_id AND v.id=p.vehicle_id
 JOIN core.households h ON h.id=p.household_id
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='fuel'
 LEFT JOIN core.memberships m ON m.household_id=p.household_id AND m.id=p.assignee_id
 LEFT JOIN core.users u ON u.id=m.user_id WHERE p.household_id=$1 AND p.id=$2`, household, id).
			Scan(&s.id, &s.vehicle, &s.user, &s.zone, &s.version, &s.vehicleVersion, &s.cycle, &s.since, &s.updated, &s.activated, &s.active, &s.due, &s.series, &s.threshold)
	default:
		return s, ErrNotFound
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func fuelDateOccurrence(field, day string, stage int, cycle int64) string {
	return "date/" + field + "/" + day + "/stage-" + strconv.Itoa(stage) + "/cycle-" + strconv.FormatInt(cycle, 10)
}

func fuelDistanceOccurrence(cycle, episode int64) string {
	return "distance/cycle-" + strconv.FormatInt(cycle, 10) + "/episode-" + strconv.FormatInt(episode, 10)
}

func fuelDateEvent(day, zone string, stage int) (time.Time, error) {
	if stage != 0 && stage != 7 {
		return time.Time{}, ErrConflict
	}
	date, err := time.Parse(time.DateOnly, day)
	if err != nil || date.Format(time.DateOnly) != day {
		return time.Time{}, ErrConflict
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	event, _, err := civiltime.Resolve(date.AddDate(0, 0, -stage), "09:00", location)
	return event, err
}

func fuelDateIntents(s fuelNotificationSource, household, entity string, generation int64, now time.Time) ([]notification.Intent, error) {
	if !s.active || s.user == "" || s.since == nil {
		return nil, nil
	}
	fields := []struct {
		name string
		day  *string
	}{{"next_service_on", s.service}, {"next_inspection_on", s.inspection}, {"insurance_renewal_on", s.insurance}, {"due_on", s.due}}
	var intents []notification.Intent
	for _, field := range fields {
		if field.day == nil {
			continue
		}
		for _, stage := range []int{7, 0} {
			event, err := fuelDateEvent(*field.day, s.zone, stage)
			if err != nil {
				return nil, err
			}
			if event.After(now) || !event.Add(24*time.Hour).After(now) || s.since.After(event) || s.updated.After(event) || s.activated.After(event) {
				continue
			}
			intents = append(intents, notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: s.user, Module: "fuel", Generation: generation,
				ResourceType: entity, ResourceID: s.id, Revision: s.version, Category: "fuel", Occurrence: fuelDateOccurrence(field.name, *field.day, stage, s.cycle),
				EventAt: event, NotBefore: event, ExpiresAt: event.Add(24 * time.Hour)})
		}
	}
	return intents, nil
}

// Mileage uses the existing four-stream authority and exact decimal comparison.
// Thirty civil days is an explicit freshness limit, not a mileage forecast.
func fuelNotificationMileage(ctx context.Context, tx pgx.Tx, household string, s fuelNotificationSource, now time.Time) (eligible, due bool, err error) {
	if !s.active || s.series == nil || s.threshold == nil || s.since == nil || s.user == "" {
		return false, false, nil
	}
	location, err := time.LoadLocation(s.zone)
	if err != nil {
		return false, false, err
	}
	today := now.In(location).Format(time.DateOnly)
	reading, err := fuelCurrentReading(ctx, tx, auth.Identity{HouseholdID: household}, s.vehicle, today)
	if err != nil {
		return false, false, err
	}
	if String(reading["reason"]) != "known" || String(reading["series_id"]) != *s.series {
		return false, false, nil
	}
	day, err := time.Parse(time.DateOnly, String(reading["day"]))
	if err != nil {
		return false, false, nil
	}
	currentDay, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return false, false, err
	}
	if day.Before(currentDay.AddDate(0, 0, -30)) || day.After(currentDay) {
		return false, false, nil
	}
	actual, err := value.Decimal(String(reading["reading"]))
	if err != nil {
		return false, false, nil
	}
	threshold, err := value.Decimal(*s.threshold)
	if err != nil {
		return false, false, nil
	}
	return true, actual.Cmp(threshold) >= 0, nil
}

func (s *Store) fuelNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Module != "fuel" || i.Category != "fuel" || i.Validate() != nil {
		return time.Time{}, nil
	}
	source, err := readFuelNotificationSource(ctx, tx, i.HouseholdID, i.ResourceType, i.ResourceID)
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if !source.active || source.user != i.UserID || source.version != i.Revision || source.since == nil || source.since.After(i.EventAt) ||
		!i.NotBefore.Equal(i.EventAt) || !i.ExpiresAt.Equal(i.EventAt.Add(24*time.Hour)) {
		return time.Time{}, nil
	}
	if strings.HasPrefix(i.Occurrence, "date/") {
		intents, err := fuelDateIntents(source, i.HouseholdID, i.ResourceType, i.Generation, i.EventAt)
		if err != nil {
			return time.Time{}, err
		}
		for _, candidate := range intents {
			if candidate.Occurrence == i.Occurrence && candidate.EventAt.Equal(i.EventAt) {
				return i.EventAt, nil
			}
		}
		return time.Time{}, nil
	}
	if i.ResourceType != "maintenance_plans" {
		return time.Time{}, nil
	}
	var cutoff, detected time.Time
	var episode int64
	err = tx.QueryRow(ctx, `SELECT consent_cutoff,detected_at,episode FROM fuel.notification_maintenance_observations
 WHERE household_id=$1 AND plan_id=$2 AND module_generation=$3 AND module_activated_at=$4
 AND plan_version=$5 AND vehicle_version=$6 AND state='due'`, i.HouseholdID, i.ResourceID, i.Generation, source.activated, source.version, source.vehicleVersion).Scan(&cutoff, &detected, &episode)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if i.Occurrence != fuelDistanceOccurrence(source.cycle, episode) || !detected.Equal(i.EventAt) || source.since.After(cutoff) {
		return time.Time{}, nil
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, err
	}
	eligible, due, err := fuelNotificationMileage(ctx, tx, i.HouseholdID, source, now)
	if !eligible || !due || err != nil {
		return time.Time{}, err
	}
	return cutoff, nil
}
