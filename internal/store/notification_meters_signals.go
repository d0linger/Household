package store

import (
	"context"
	"math/big"
	"slices"
	"time"

	"household/internal/civiltime"
	"household/internal/energy"

	"github.com/jackc/pgx/v5"
)

func meterNotificationSignal(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, kind string, now time.Time) (meterSignal, error) {
	stamp := meterStamp([]any{s.version, s.registerVersion, s.meterVersion, s.generation, s.activated})
	unknown := meterSignal{stamp: stamp}
	if !s.active || s.since == nil || s.user == "" {
		return unknown, nil
	}
	if kind == "adapter" {
		return meterAdapterSignal(ctx, tx, household, s, now)
	}
	if !s.anomaly {
		return unknown, nil
	}
	var since time.Time
	if s.input == "telemetry" {
		var current bool
		var err error
		stamp, since, current, err = meterTelemetryAuthority(ctx, tx, household, s, now, stamp)
		if err != nil || !current {
			return meterSignal{stamp: stamp}, err
		}
	}
	if s.dirty {
		return meterSignal{stamp: stamp}, nil
	}
	switch kind {
	case "consumption":
		return meterConsumptionSignal(ctx, tx, household, s, now, stamp, since)
	case "base_load":
		return meterBaseLoadSignal(ctx, tx, household, s, now, stamp, since)
	default:
		return unknown, ErrConflict
	}
}

func meterConsumptionSignal(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, now time.Time, stamp string, since time.Time) (meterSignal, error) {
	result := meterSignal{stamp: stamp}
	if s.daily == nil {
		return result, nil
	}
	rows, err := tx.Query(ctx, `SELECT from_at,through_at,quantity::text,quality FROM meters.intervals
 WHERE household_id=$1 AND register_id=$2 AND through_at<=$3 AND from_at>=$4 ORDER BY through_at DESC,from_at DESC LIMIT 100`, household, s.id, now, since)
	if err != nil {
		return result, err
	}
	var intervals []energy.Interval
	for rows.Next() {
		var interval energy.Interval
		var raw *string
		if err = rows.Scan(&interval.From, &interval.Through, &raw, &interval.Quality); err != nil {
			break
		}
		if raw != nil {
			var ok bool
			interval.Quantity, ok = new(big.Rat).SetString(*raw)
			if !ok || len(*raw) > 40 || interval.Quantity.Sign() < 0 {
				err = ErrConflict
				break
			}
		}
		intervals = append(intervals, interval)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	slices.Reverse(intervals)
	if len(intervals) < s.minimum {
		return result, nil
	}
	latest := intervals[len(intervals)-1]
	if now.Sub(latest.Through) > time.Duration(s.gapSeconds)*time.Second {
		return result, nil
	}
	// Retain only a contiguous actual tail. A missing/estimated interval must
	// never become either a spike or a healthy rearm observation.
	start := len(intervals) - 1
	for start >= 0 {
		item := intervals[start]
		if item.Quality != "actual" || item.Quantity == nil || !item.Through.After(item.From) {
			break
		}
		if start+1 < len(intervals) && !item.Through.Equal(intervals[start+1].From) {
			break
		}
		start--
	}
	intervals = intervals[start+1:]
	if len(intervals) < s.minimum {
		return result, nil
	}
	if latest.Through.Sub(intervals[0].From) < 6*24*time.Hour {
		covered, err := meterHourlyActualHistory(ctx, tx, household, s.id, latest.Through, since)
		if err != nil || !covered {
			return result, err
		}
	}
	limit, err := energy.Decimal(*s.daily)
	if err != nil || limit.Sign() <= 0 {
		return result, energy.ErrInvalid
	}
	// The coverage proof changes no threshold semantics: compare the newest
	// actual interval normalized to24h, with exact rational arithmetic.
	perDay := new(big.Rat).Mul(latest.Quantity, big.NewRat(int64(24*time.Hour), latest.Through.Sub(latest.From).Nanoseconds()))
	result.known = true
	result.alert = perDay.Cmp(limit) > 0
	return result, nil
}

func meterBaseLoadSignal(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, now time.Time, stamp string, since time.Time) (meterSignal, error) {
	result := meterSignal{stamp: stamp}
	if s.base == nil || s.measurement != "power" {
		return result, nil
	}
	// Query newest nights, never the oldest 100. Estimated seconds are excluded
	// from both energy and coverage; incomplete latest nights remain unknown.
	rows, err := tx.Query(ctx, `SELECT (from_at AT TIME ZONE $3)::date::text,
 coalesce(sum(quantity) FILTER(WHERE estimated_seconds=0),0)::text,
 coalesce(sum(covered_seconds) FILTER(WHERE estimated_seconds=0),0)::bigint
 FROM meters.aggregates WHERE household_id=$1 AND register_id=$2 AND bucket_kind='hour'
 AND from_at>=greatest($4::timestamptz-interval '101 days',$5::timestamptz) AND through_at<=$4
 AND extract(hour FROM from_at AT TIME ZONE $3)<6
 GROUP BY (from_at AT TIME ZONE $3)::date ORDER BY (from_at AT TIME ZONE $3)::date DESC LIMIT 100`, household, s.id, s.zone, now, since)
	if err != nil {
		return result, err
	}
	var nights []energy.Night
	for rows.Next() {
		var night energy.Night
		if err = rows.Scan(&night.Day, &night.QuantityKWh, &night.CoveredSeconds); err != nil {
			break
		}
		nights = append(nights, night)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	loc, err := time.LoadLocation(s.zone)
	if err != nil {
		return result, err
	}
	var complete []energy.Night
	var latestEnd time.Time
	for _, night := range nights {
		date, e := time.Parse(time.DateOnly, night.Day)
		if e != nil {
			return result, e
		}
		from, _, e := civiltime.Resolve(date, "00:00", loc)
		if e != nil {
			return result, e
		}
		through, _, e := civiltime.Resolve(date, "06:00", loc)
		if e != nil {
			return result, e
		}
		if through.After(now) {
			continue
		}
		night.ExpectedSeconds = int64(through.Sub(from) / time.Second)
		if latestEnd.IsZero() {
			latestEnd = through
			if now.Sub(through) > 36*time.Hour || night.CoveredSeconds*5 < night.ExpectedSeconds*4 {
				return result, nil
			}
		}
		complete = append(complete, night)
	}
	if len(complete) < s.minimum {
		return result, nil
	}
	anomalies, err := energy.BaseLoad(complete, s.unit, *s.base, s.minimum)
	if err != nil {
		return result, err
	}
	result.known = true
	for _, a := range anomalies {
		if a.Code == "base_history_insufficient" {
			result.known = false
		}
		if a.Code == "base_load" {
			result.alert = true
		}
	}
	return result, nil
}

func meterAdapterSignal(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, now time.Time) (meterSignal, error) {
	result := meterSignal{stamp: meterStamp([]any{s.version, s.registerVersion, s.meterVersion, s.generation, s.activated})}
	if !s.stale || s.input != "telemetry" {
		return result, nil
	}
	rows, err := tx.Query(ctx, `SELECT src.id::text,src.version,src.updated_at,
 (SELECT max(t.measured_at) FROM meters.telemetry t WHERE t.household_id=src.household_id AND t.source_id=src.id
 AND t.register_id=src.register_id AND t.device_id=src.device_id AND t.source_version=src.version
 AND t.kind='actual' AND t.measured_at<=$3),
 EXISTS(SELECT 1 FROM meters.device_registers d WHERE d.household_id=src.household_id AND d.register_id=src.register_id
 AND d.device_id=src.device_id AND d.started_at<=$3 AND(d.ended_at IS NULL OR d.ended_at>$3))
 FROM meters.integration_sources src WHERE src.household_id=$1 AND src.register_id=$2 AND src.enabled
 AND src.module_generation=$4 ORDER BY src.id LIMIT 17`, household, s.id, now, s.generation)
	if err != nil {
		return result, err
	}
	type source struct {
		id      string
		version int64
		updated time.Time
		latest  *time.Time
		mapped  bool
	}
	var sources []source
	for rows.Next() {
		var source source
		if err = rows.Scan(&source.id, &source.version, &source.updated, &source.latest, &source.mapped); err != nil {
			break
		}
		sources = append(sources, source)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	var stamps []any
	stamps = append(stamps, s.version, s.registerVersion, s.meterVersion, s.generation, s.activated)
	for _, source := range sources {
		stamps = append(stamps, source.id, source.version, source.mapped)
	}
	result.stamp = meterStamp(stamps)
	if len(sources) == 0 || len(sources) > 16 {
		return result, nil
	}
	result.known = true
	for _, source := range sources {
		if !source.mapped {
			result.known = false
			continue
		}
		// A successful connection or invalid packet is not a measurement. Before
		// the first accepted sample, allow the configured startup grace period.
		latest := source.updated
		if source.latest != nil {
			latest = *source.latest
		}
		if now.Sub(latest) > time.Duration(s.staleSeconds)*time.Second {
			result.alert = true
		}
	}
	return result, nil
}

// Latest source-version samples and mapping identity are required for consumed
// telemetry. Retained readings from a disabled/reconfigured feed are history,
// never current evidence for a new warning or an all-clear.
func meterTelemetryAuthority(ctx context.Context, tx pgx.Tx, household string, s meterNotificationSource, now time.Time, baseStamp string) (string, time.Time, bool, error) {
	rows, err := tx.Query(ctx, `SELECT src.id::text,src.version,src.updated_at,
 coalesce(mapping.id::text,''),coalesce(mapping.version,0),mapping.started_at,
 (SELECT max(t.measured_at) FROM meters.telemetry t WHERE t.household_id=src.household_id AND t.source_id=src.id
  AND t.register_id=src.register_id AND t.device_id=src.device_id AND t.source_version=src.version
  AND t.kind='actual' AND t.measured_at<=$3 AND t.measured_at>=greatest(src.updated_at,mapping.started_at))
 FROM meters.integration_sources src LEFT JOIN LATERAL(
 SELECT d.id,d.version,d.started_at FROM meters.device_registers d
 WHERE d.household_id=src.household_id AND d.register_id=src.register_id AND d.device_id=src.device_id
 AND d.started_at<=$3 AND(d.ended_at IS NULL OR d.ended_at>$3) ORDER BY d.started_at DESC,d.id LIMIT 1) mapping ON true
 WHERE src.household_id=$1 AND src.register_id=$2 AND src.enabled AND src.module_generation=$4
 ORDER BY src.id LIMIT 17`, household, s.id, now, s.generation)
	if err != nil {
		return baseStamp, time.Time{}, false, err
	}
	type source struct {
		id, mapping             string
		version, mappingVersion int64
		updated                 time.Time
		started, latest         *time.Time
	}
	var sources []source
	for rows.Next() {
		var item source
		if err = rows.Scan(&item.id, &item.version, &item.updated, &item.mapping, &item.mappingVersion, &item.started, &item.latest); err != nil {
			break
		}
		sources = append(sources, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return baseStamp, time.Time{}, false, err
	}
	stamps := []any{baseStamp}
	var since time.Time
	current := len(sources) > 0 && len(sources) <= 16
	for _, item := range sources {
		stamps = append(stamps, item.id, item.version, item.mapping, item.mappingVersion, item.started)
		if item.updated.After(since) {
			since = item.updated
		}
		if item.started != nil && item.started.After(since) {
			since = *item.started
		}
		if item.mapping == "" || item.latest == nil || now.Sub(*item.latest) > time.Duration(s.gapSeconds)*time.Second {
			current = false
		}
	}
	return meterStamp(stamps), since, current, nil
}

type meterHistoryHour struct {
	from, through      time.Time
	covered, estimated int64
	quantity           bool
}

func meterHasActualHours(hours []meterHistoryHour, latest time.Time) bool {
	if len(hours) == 0 || len(hours) > 169 {
		return false
	}
	end := hours[0].through
	if end.After(latest) || latest.Sub(end) >= time.Hour {
		return false
	}
	cursor := end
	for _, hour := range hours {
		if !hour.through.Equal(cursor) || !hour.through.After(hour.from) || !hour.quantity || hour.estimated != 0 ||
			hour.covered != int64(hour.through.Sub(hour.from)/time.Second) {
			return false
		}
		cursor = hour.from
		if end.Sub(cursor) >= 6*24*time.Hour {
			return true
		}
	}
	return false
}

func meterHourlyActualHistory(ctx context.Context, tx pgx.Tx, household, register string, latest, since time.Time) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT from_at,through_at,covered_seconds,estimated_seconds,quantity IS NOT NULL
 FROM meters.aggregates WHERE household_id=$1 AND register_id=$2 AND bucket_kind='hour' AND through_at<=$3
 AND from_at>=greatest($4::timestamptz,$3::timestamptz-interval '169 hours')
 ORDER BY from_at DESC LIMIT 169`, household, register, latest, since)
	if err != nil {
		return false, err
	}
	var hours []meterHistoryHour
	for rows.Next() {
		var hour meterHistoryHour
		if err = rows.Scan(&hour.from, &hour.through, &hour.covered, &hour.estimated, &hour.quantity); err != nil {
			break
		}
		hours = append(hours, hour)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	return meterHasActualHours(hours, latest), nil
}
