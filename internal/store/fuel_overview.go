package store

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// Reviewed Core 26 and recipe-only Core 27 support the manual Fuel domain.
// A newer Core must integrate inherited per-record visibility before this read
// surface can be admitted; omitting monetary columns is not authorization.
const fuelOverviewScopeSQL = `SELECT ms.data_generation,h.timezone,(transaction_timestamp() AT TIME ZONE h.timezone)::date::text,transaction_timestamp(),ms.state='enabled'
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.id=$2 AND m.user_id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.auth_version=$5 AND NOT u.must_change_password AND u.disabled_at IS NULL
 JOIN core.sessions s ON s.id=$6 AND s.user_id=u.id AND s.auth_version=u.auth_version AND s.revoked_at IS NULL
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='fuel'
 WHERE h.id=$1 AND (SELECT count(*)=1 AND bool_and(version IN(26,27,28) AND NOT dirty) FROM core.schema_migrations)`

func fuelOverviewScope(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id auth.Identity) (FuelOverviewScope, error) {
	var scope FuelOverviewScope
	var enabled bool
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return scope, ErrForbidden
	}
	err := q.QueryRow(ctx, fuelOverviewScopeSQL, id.HouseholdID, id.MembershipID, id.UserID, id.Role, id.AuthVersion, id.SessionID).Scan(&scope.Generation, &scope.Timezone, &scope.Today, &scope.Now, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return scope, ErrForbidden
	}
	if err == nil && (!enabled || scope.Generation <= 0) {
		return scope, ErrNotFound
	}
	return scope, err
}

func (s *Store) FuelOverviewContext(ctx context.Context, id auth.Identity) (FuelOverviewScope, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return fuelOverviewScope(ctx, s.Pool, id)
}

func (s *Store) FuelOverview(ctx context.Context, id auth.Identity, expected FuelOverviewScope, q FuelOverviewQuery) (FuelOverviewPage, error) {
	out := FuelOverviewPage{Vehicles: []FuelOverviewVehicle{}}
	q, err := normalizeFuelOverviewQuery(q)
	if err != nil || !utf8.ValidString(q.Search) {
		if err == nil {
			err = Invalid{"Suchtext erneut prüfen"}
		}
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	scope, err := fuelOverviewScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if scope.Generation != expected.Generation || scope.Today != expected.Today || scope.Timezone != expected.Timezone {
		return out, ErrConflict
	}
	out, err = s.fuelOverviewRead(ctx, tx, id, scope, q)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Store) fuelOverviewRead(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelOverviewScope, q FuelOverviewQuery) (FuelOverviewPage, error) {
	out := FuelOverviewPage{Vehicles: []FuelOverviewVehicle{}}
	var after, afterID any
	var vehicleID any
	if q.VehicleID != "" {
		vehicleID = q.VehicleID
	}
	if q.AfterID != "" {
		after, afterID = q.After, q.AfterID
	}
	registration := "left(v.registration,80),length(v.registration)>80"
	if fuelModuleFullExport(ctx) {
		registration = "CASE WHEN octet_length(v.registration)<=1048576 THEN v.registration ELSE '' END,octet_length(v.registration)>1048576"
	}
	rows, err := tx.Query(ctx, `SELECT v.id,v.name,`+registration+`,v.archived_at IS NOT NULL,v.version,v.created_at,
 v.next_service_on::text,v.next_inspection_on::text,v.insurance_renewal_on::text,v.current_series_id::text,
 EXISTS(SELECT 1 FROM fuel.fillups f WHERE f.household_id=v.household_id AND f.vehicle_id=v.id AND f.occurred_on>$7::date)
 FROM fuel.vehicles v WHERE v.household_id=$1 AND ($2 OR v.archived_at IS NULL) AND($8::uuid IS NULL OR v.id=$8)
 AND ($3='' OR strpos(lower(v.name),lower($3))>0 OR strpos(lower(left(v.registration,300)),lower($3))>0)
 AND ($4::timestamptz IS NULL OR (v.created_at,v.id)<($4,$5::uuid)) AND v.created_at<=$6
 ORDER BY v.created_at DESC,v.id DESC LIMIT 21`, id.HouseholdID, q.Archived, q.Search, after, afterID, scope.Now, scope.Today, vehicleID)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var vehicle FuelOverviewVehicle
		if err = rows.Scan(&vehicle.ID, &vehicle.Name, &vehicle.Registration, &vehicle.RegistrationTruncated, &vehicle.Archived, &vehicle.Version, &vehicle.Created, &vehicle.NextService, &vehicle.NextInspection, &vehicle.InsuranceRenewal, &vehicle.CurrentSeriesID, &vehicle.FutureFills); err != nil {
			rows.Close()
			return out, err
		}
		if fuelModuleFullExport(ctx) && vehicle.RegistrationTruncated {
			rows.Close()
			return out, Invalid{"Kennzeichen ist für den vollständigen Export zu groß"}
		}
		if len(out.Vehicles) == FuelOverviewPageSize {
			out.More = true
			break
		}
		if err = fuelOverviewDates(&vehicle, scope.Today); err != nil {
			rows.Close()
			return out, err
		}
		out.Vehicles = append(out.Vehicles, vehicle)
		ids = append(ids, vehicle.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Context, out.Query = scope, q
	if len(ids) == 0 {
		return out, nil
	}
	series, limited, err := fuelOverviewSeriesRows(ctx, tx, id, ids, scope.Today)
	if err != nil {
		return out, err
	}
	out.SeriesLimited = limited
	latest, err := fuelOverviewLatestRows(ctx, tx, id, ids, nil, scope.Today)
	if err != nil {
		return out, err
	}
	activeIDs := []string{}
	for i := range out.Vehicles {
		v := &out.Vehicles[i]
		v.Last, v.LastState, v.LastDay, err = fuelLatestFill(latest[v.ID])
		if err != nil {
			return out, err
		}
		if limited {
			v.SeriesState = "series_limit"
			v.ConsumptionState = "series_limit"
			continue
		}
		v.SeriesSelection = "legacy_unambiguous"
		if v.CurrentSeriesID != nil {
			v.SeriesSelection = "explicit"
			v.SeriesState = "selected_series_unavailable"
			for _, candidate := range series[v.ID] {
				if candidate.ID == *v.CurrentSeriesID {
					v.Series, v.SeriesState, err = fuelCurrentSeries([]FuelOverviewSeries{candidate}, scope.Today)
					break
				}
			}
		} else {
			v.Series, v.SeriesState, err = fuelCurrentSeries(series[v.ID], scope.Today)
		}
		if err != nil {
			return out, err
		}
		if v.Series != nil {
			activeIDs = append(activeIDs, v.Series.ID)
		} else {
			v.ConsumptionState = v.SeriesState
		}
	}
	current, err := fuelOverviewLatestRows(ctx, tx, id, nil, activeIDs, scope.Today)
	if err != nil {
		return out, err
	}
	history, historyLimited, err := fuelOverviewHistoryRows(ctx, tx, id, activeIDs, scope.Today)
	if err != nil {
		return out, err
	}
	out.HistoryLimited = historyLimited
	for i := range out.Vehicles {
		v := &out.Vehicles[i]
		if v.Series == nil {
			continue
		}
		reading, readErr := fuelCurrentReading(ctx, tx, id, v.ID, scope.Today)
		if readErr != nil {
			return out, readErr
		}
		v.OdometerReason = String(reading["reason"])
		if reading["reading"] != nil {
			copy := String(reading["reading"])
			v.Odometer = &copy
			v.OdometerSource, v.OdometerDate = String(reading["source"]), String(reading["day"])
			if v.OdometerSource == "fillup" {
				v.OdometerSource = "fill_reading"
			}
		}
		if historyLimited {
			v.Consumption, v.ConsumptionState, err = fuelOverviewCachedInterval(ctx, tx, id, v.ID, v.Series.ID, scope.Today, current[v.ID])
			if err != nil {
				return out, err
			}
			continue
		}
		v.Consumption, v.ConsumptionState, err = fuelExplainedConsumption(history[v.Series.ID], *v.Series)
		if err != nil {
			return out, err
		}
	}
	if err = ctx.Err(); err != nil {
		return FuelOverviewPage{}, err
	}
	return out, nil
}

func fuelOverviewSeriesRows(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicles []string, today string) (map[string][]FuelOverviewSeries, bool, error) {
	result := map[string][]FuelOverviewSeries{}
	rows, err := tx.Query(ctx, `SELECT id,vehicle_id,started_on::text,ended_on::text,initial_reading::text,left(reason,200)
 FROM fuel.odometer_series WHERE household_id=$1 AND vehicle_id=ANY($2::uuid[])
 AND started_on<=$3::date AND(ended_on IS NULL OR ended_on>=$3::date)
 ORDER BY vehicle_id,started_on,id LIMIT 1001`, id.HouseholdID, vehicles, today)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var row FuelOverviewSeries
		if err = rows.Scan(&row.ID, &row.VehicleID, &row.Started, &row.Ended, &row.Initial, &row.Label); err != nil {
			return nil, false, err
		}
		count++
		if count > fuelOverviewSeriesLimit {
			return map[string][]FuelOverviewSeries{}, true, nil
		}
		result[row.VehicleID] = append(result[row.VehicleID], row)
	}
	return result, false, rows.Err()
}

func fuelOverviewLatestRows(ctx context.Context, tx pgx.Tx, id auth.Identity, vehicles, series []string, today string) (map[string][]FuelOverviewFill, error) {
	result := map[string][]FuelOverviewFill{}
	if len(vehicles) == 0 && len(series) == 0 {
		return result, nil
	}
	// Both branches use the existing household/vehicle/date index. Each selected
	// vehicle or current series contributes at most two bounded physical rows.
	from := `fuel.vehicles p`
	predicate := `p.id=ANY($2::uuid[])`
	match := `x.vehicle_id=p.id`
	selection := vehicles
	daySeriesAmbiguity := `EXISTS(SELECT 1 FROM fuel.fillups other WHERE other.household_id=p.household_id AND other.vehicle_id=f.vehicle_id AND other.occurred_on=f.occurred_on AND other.odometer_series_id<>f.odometer_series_id)`
	if len(series) > 0 {
		from = `fuel.odometer_series p`
		predicate = `p.id=ANY($2::uuid[])`
		match = `x.vehicle_id=p.vehicle_id AND x.odometer_series_id=p.id`
		selection = series
		daySeriesAmbiguity = `false`
	}
	rows, err := tx.Query(ctx, `SELECT f.id,f.vehicle_id,f.odometer_series_id,f.occurred_on::text,f.odometer::text,f.quantity::text,f.fuel_type,left(ft.unit_code,32),f.full_tank,f.previous_fill_missing,`+daySeriesAmbiguity+`
 FROM `+from+` CROSS JOIN LATERAL(SELECT x.id,x.vehicle_id,x.odometer_series_id,x.occurred_on,x.odometer,x.quantity,x.fuel_type,x.full_tank,x.previous_fill_missing FROM fuel.fillups x WHERE x.household_id=p.household_id AND `+match+` AND x.occurred_on<=$3::date ORDER BY x.occurred_on DESC,x.odometer DESC,x.id DESC LIMIT 2) f
 JOIN fuel.fuel_types ft ON ft.code=f.fuel_type WHERE p.household_id=$1 AND `+predicate+` ORDER BY f.vehicle_id,f.occurred_on DESC,f.odometer DESC,f.id DESC`, id.HouseholdID, selection, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r FuelOverviewFill
		if err = scanFuelOverviewFill(rows, &r); err != nil {
			return nil, err
		}
		result[r.VehicleID] = append(result[r.VehicleID], r)
	}
	return result, rows.Err()
}

func scanFuelOverviewFill(row interface{ Scan(...any) error }, r *FuelOverviewFill) error {
	return row.Scan(&r.ID, &r.VehicleID, &r.SeriesID, &r.Date, &r.Odometer, &r.Quantity, &r.Fuel, &r.Unit, &r.Full, &r.Missing, &r.DaySeriesAmbiguous)
}

func fuelOverviewHistoryRows(ctx context.Context, tx pgx.Tx, id auth.Identity, series []string, today string) (map[string][]FuelOverviewFill, bool, error) {
	result := map[string][]FuelOverviewFill{}
	if len(series) == 0 {
		return result, false, nil
	}
	rows, err := tx.Query(ctx, `SELECT f.id,f.vehicle_id,f.odometer_series_id,f.occurred_on::text,f.odometer::text,f.quantity::text,f.fuel_type,left(ft.unit_code,32),f.full_tank,f.previous_fill_missing,false
 FROM fuel.fillups f JOIN fuel.fuel_types ft ON ft.code=f.fuel_type WHERE f.household_id=$1 AND f.odometer_series_id=ANY($2::uuid[]) AND f.occurred_on<=$3::date
 ORDER BY f.odometer_series_id,f.occurred_on,f.odometer,f.id LIMIT 10001`, id.HouseholdID, series, today)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var r FuelOverviewFill
		if err = scanFuelOverviewFill(rows, &r); err != nil {
			return nil, false, err
		}
		count++
		if count > fuelOverviewHistoryLimit {
			return map[string][]FuelOverviewFill{}, true, nil
		}
		result[r.SeriesID] = append(result[r.SeriesID], r)
	}
	return result, false, rows.Err()
}
