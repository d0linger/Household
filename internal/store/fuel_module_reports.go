package store

import (
	"context"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type fuelReportGroup struct {
	VehicleID, SeriesID, Fuel, Unit, From, To string
	Quantity, Distance                        *big.Rat
	Costs                                     map[string]*big.Int
	Intervals                                 int
	CostsUnknown                              bool
}

func fuelSegmentPlausibility(segment fuelComputedSegment, settings Record) []FuelModuleWarning {
	warnings := []FuelModuleWarning{}
	if segment.State != "available" || segment.Quantity == nil || segment.Distance == nil || String(settings["plausibility_unit"]) != segment.Unit {
		return warnings
	}
	quantity, quantityErr := fuelAggregateDecimal(*segment.Quantity)
	distance, distanceErr := fuelAggregateDecimal(*segment.Distance)
	if quantityErr != nil || distanceErr != nil || distance.Sign() <= 0 {
		return warnings
	}
	rate := new(big.Rat).Mul(new(big.Rat).Quo(quantity, distance), big.NewRat(100, 1))
	for _, rule := range []struct {
		field, code, message string
		minimum              bool
	}{
		{"minimum_consumption", "consumption_low", "Der berechnete Verbrauch liegt unter der persönlich festgelegten Grenze", true},
		{"maximum_consumption", "consumption_high", "Der berechnete Verbrauch liegt über der persönlich festgelegten Grenze", false},
	} {
		bound, err := value.Decimal(String(settings[rule.field]))
		if err != nil || bound.Sign() < 0 || !rule.minimum && bound.Sign() == 0 {
			continue
		}
		if compared := rate.Cmp(bound); rule.minimum && compared < 0 || !rule.minimum && compared > 0 {
			warnings = append(warnings, FuelModuleWarning{Code: rule.code, Message: rule.message,
				NextAction: "Volltankstatus, fehlende Tankungen, Einheiten und Originalbelege prüfen", RecordID: segment.ToID})
		}
	}
	return warnings
}

// A second, explicitly different cost view uses recorded trips as its distance
// denominator. It does not assert that unrecorded driving is zero or that this
// is the vehicle's whole-life total cost of ownership.
func (s *Store) fuelModuleReportPane(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, cursor fuelModuleCursor, limit int) (FuelModuleView, error) {
	view := FuelModuleView{Context: scope, Pane: query.Pane, Records: []Record{}, Options: Record{}, Warnings: []FuelModuleWarning{}}
	if query.Kind == "costs" {
		return fuelModuleCostReportPane(ctx, tx, id, scope, query, cursor, limit)
	}
	chargeQuery := query
	chargeQuery.Pane, chargeQuery.Cursor, chargeQuery.Unit, chargeQuery.Kind, chargeQuery.Search = "charging", "", "", "", ""
	energy, err := fuelChargingTotalsSQL(ctx, tx, id, scope, query)
	if err != nil {
		return view, err
	}
	allRows := []Record{}
	dataset := "charging_live"
	if query.Unit == "kWh" || query.Kind == "energy" {
		if cursor.Dataset != "" && cursor.Dataset != dataset {
			return view, ErrConflict
		}
		cursor.Dataset = dataset
		charges, more, next, readErr := fuelModuleRawPage(ctx, tx, id, chargeQuery, cursor, limit)
		if readErr != nil {
			return view, readErr
		}
		for _, charge := range charges {
			charge["row_kind"], charge["unit"] = "charging", "kWh"
		}
		view.Records, view.More, view.Next = charges, more, next
		view.Totals = Record{"charging": energy, "kwh_per_100km": nil,
			"reason": "charged_energy_is_not_documented_energy_consumption"}
		view.Options["chart"] = Record{"x": "occurred_on", "y": "energy_kwh", "unit": "kWh", "value_type": "decimal_string", "rows_are_table_data": true,
			"series_fields": []string{"measurement_basis"}, "wall_battery_combined": false, "chart_scope": "current_table_page"}
		view.Options["totals_scope"] = "entire_filtered_window_not_only_current_page"
		return view, nil
	} else {
		version, status, readErr := fuelSegmentCacheVersion(ctx, tx, id, scope, query.VehicleID)
		if readErr != nil {
			return view, readErr
		}
		if status == "ready" {
			return s.fuelCachedReportPane(ctx, tx, id, scope, query, cursor, limit, version, energy)
		}
		if cursor.ID != "" {
			return view, ErrConflict
		}
		view.Warnings = append(view.Warnings, fuelReason(status))
		segments, status, readErr := fuelWindowSegments(ctx, tx, id, scope, query)
		if readErr != nil {
			return view, readErr
		}
		if status != "live_complete_window" {
			view.Warnings = append(view.Warnings, fuelReason(status))
			view.Options["segments_status"] = status
			return view, nil
		}
		if query.Kind != "" && query.Kind != "consumption" {
			filtered := []fuelComputedSegment{}
			for _, segment := range segments {
				if segment.Fuel == query.Kind {
					filtered = append(filtered, segment)
				}
			}
			segments = filtered
		}
		dataset = status + "/" + strconv.Itoa(fuelSegmentAlgorithm)
		totals, warnings, summaryErr := fuelReportSummary(ctx, segments, query)
		if summaryErr != nil {
			return view, summaryErr
		}
		settings, settingsErr := fuelModuleSettings(ctx, tx, id, scope.Generation)
		if settingsErr != nil {
			return view, settingsErr
		}
		for _, segment := range segments {
			record := fuelSegmentRecord(segment)
			record["row_kind"] = "consumption_interval"
			record["warnings"] = fuelSegmentPlausibility(segment, settings)
			allRows = append(allRows, record)
		}
		sort.Slice(allRows, func(i, j int) bool {
			if String(allRows[i]["to_on"]) != String(allRows[j]["to_on"]) {
				return String(allRows[i]["to_on"]) > String(allRows[j]["to_on"])
			}
			if String(allRows[i]["series_id"]) != String(allRows[j]["series_id"]) {
				return String(allRows[i]["series_id"]) < String(allRows[j]["series_id"])
			}
			return String(allRows[i]["to_id"]) < String(allRows[j]["to_id"])
		})
		totals["charging"] = energy
		view.Totals = totals
		view.Warnings = append(view.Warnings, warnings...)
		view.Options["segments_status"] = status
		view.Options["chart"] = Record{"x": "to_on", "y": "per_100", "unit_field": "unit", "value_type": "decimal_string",
			"series_fields": []string{"series_id", "fuel_type", "unit"}, "rows_are_table_data": true, "chart_scope": "current_table_page", "prorated": false}
	}
	if cursor.Dataset != "" && cursor.Dataset != dataset {
		return view, ErrConflict
	}
	cursor.Dataset = dataset
	if cursor.Offset > len(allRows) {
		return view, ErrConflict
	}
	end := min(cursor.Offset+limit, len(allRows))
	view.Records = allRows[cursor.Offset:end]
	view.More = end < len(allRows)
	view.Next, err = fuelModuleNext(cursor, len(view.Records), view.More)
	view.Options["totals_scope"] = "entire_filtered_window_not_only_current_page"
	return view, err
}

func fuelChargingTotalsSQL(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery) (Record, error) {
	physical, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('measurement_basis',measurement_basis,'energy_kwh',sum(energy_kwh)::text,
 'events',count(*)::text,'unit','kWh','kwh_per_100km',NULL,'efficiency_reason','charged_energy_is_not_documented_energy_consumption')
 FROM fuel.charging_sessions WHERE household_id=$1 AND vehicle_id=$2 AND occurred_on BETWEEN $3::date AND $4::date AND occurred_on<=$5::date
 GROUP BY measurement_basis ORDER BY measurement_basis`, id.HouseholdID, query.VehicleID, query.From, query.To, scope.Today)
	if err != nil {
		return nil, err
	}
	money, err := fuelReadRecords(ctx, tx, `SELECT jsonb_build_object('measurement_basis',measurement_basis,'currency',currency_code,
 'amount_minor',sum(total_minor)::text,'priced_energy_kwh',sum(energy_kwh) FILTER(WHERE total_minor IS NOT NULL)::text,
 'unknown_cost_events',count(*) FILTER(WHERE total_minor IS NULL)::text)
 FROM fuel.charging_sessions WHERE household_id=$1 AND vehicle_id=$2 AND occurred_on BETWEEN $3::date AND $4::date AND occurred_on<=$5::date
 GROUP BY measurement_basis,currency_code ORDER BY measurement_basis,currency_code LIMIT 1001`, id.HouseholdID, query.VehicleID, query.From, query.To, scope.Today)
	if err != nil {
		return nil, err
	}
	if len(money) > 1000 {
		return nil, Invalid{"Ladezeitraum für höchstens 1.000 getrennte Währungsgruppen eingrenzen"}
	}
	for _, row := range money {
		row["cost_per_kwh"] = nil
		if row["amount_minor"] != nil && row["priced_energy_kwh"] != nil {
			amount, ok := new(big.Int).SetString(String(row["amount_minor"]), 10)
			energy, energyErr := fuelAggregateDecimal(String(row["priced_energy_kwh"]))
			if !ok || energyErr != nil || energy.Sign() <= 0 {
				return nil, Invalid{"Ladeenergiebilanz enthält ungültige Beträge oder Mengen"}
			}
			rate := new(big.Rat).Quo(new(big.Rat).SetInt(amount), energy)
			row["cost_per_kwh"] = rate.Quo(rate, big.NewRat(100, 1)).FloatString(4)
		}
		row["cost_scope"] = "only_events_with_documented_price"
	}
	return Record{"groups": physical, "costs": money, "wall_and_battery_combined": false,
		"charging_losses_assumed": false, "liter_conversion": false, "currency_conversion": false}, nil
}

const fuelModuleCostsCTE = `WITH costs AS(
 SELECT id,vehicle_id,occurred_on,total_minor,currency_code,'fillup' AS source_kind FROM fuel.fillups
 WHERE household_id=$1 AND vehicle_id=$2 AND occurred_on BETWEEN $3::date AND $4::date AND $6='' AND($7='' OR quantity_unit=$7)
 AND($5='' OR strpos(lower(left(station,4000)),lower($5))>0)
 UNION ALL SELECT id,vehicle_id,occurred_on,total_minor,currency_code,'charging' FROM fuel.charging_sessions
 WHERE household_id=$1 AND vehicle_id=$2 AND occurred_on BETWEEN $3::date AND $4::date AND $6='' AND($7='' OR $7='kWh')
 AND($5='' OR strpos(lower(location),lower($5))>0)
 UNION ALL SELECT f.id,f.vehicle_id,f.occurred_on,f.total_minor,f.currency_code,'vehicle_cost' FROM fuel.vehicle_costs f
 WHERE f.household_id=$1 AND f.vehicle_id=$2 AND f.occurred_on BETWEEN $3::date AND $4::date
 AND($5='' OR strpos(lower(f.title),lower($5))>0) AND($6='' OR f.kind=$6) AND $7=''
 AND NOT EXISTS(SELECT 1 FROM fuel.vehicle_costs correction WHERE correction.household_id=f.household_id AND correction.corrects_id=f.id)) `

func fuelModuleCostArgs(id auth.Identity, query FuelModuleQuery) []any {
	return []any{id.HouseholdID, query.VehicleID, query.From, query.To, query.Search, query.Kind, query.Unit}
}

func fuelModuleCostTotals(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery) (Record, []FuelModuleWarning, error) {
	groups, err := fuelReadRecords(ctx, tx, fuelModuleCostsCTE+`SELECT jsonb_build_object('currency',currency_code,
 'known_amount_minor',sum(total_minor)::text,'events',count(*)::text,'unknown_cost_events',count(*) FILTER(WHERE total_minor IS NULL)::text)
 FROM costs GROUP BY currency_code ORDER BY currency_code LIMIT 1001`, fuelModuleCostArgs(id, query)...)
	if err != nil {
		return nil, nil, err
	}
	if len(groups) > 1000 {
		return nil, nil, Invalid{"Kostenzeitraum für höchstens 1.000 Währungen eingrenzen"}
	}
	tripRows, err := fuelReadRecords(ctx, tx, `WITH scope AS(SELECT * FROM fuel.trips WHERE household_id=$1 AND vehicle_id=$2
 AND started_on<=$4::date AND ended_on>=$3::date), inside AS(SELECT * FROM scope WHERE started_on>=$3::date AND ended_on<=$4::date),
 ordered AS(SELECT *,lag(end_odometer) OVER(PARTITION BY odometer_series_id ORDER BY start_odometer,id) AS prior_reading,
 lag(ended_on) OVER(PARTITION BY odometer_series_id ORDER BY start_odometer,id) AS prior_day FROM inside),
 series AS(SELECT odometer_series_id,min(started_on) AS first_day,max(ended_on) AS last_day,sum(end_odometer-start_odometer) AS distance,
 count(*) AS events,bool_or(prior_reading>start_odometer OR prior_day>started_on) AS invalid FROM ordered GROUP BY odometer_series_id),
 ranges AS(SELECT *,max(last_day) OVER(ORDER BY first_day,odometer_series_id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS prior_end FROM series)
 SELECT jsonb_build_object('distance',sum(distance)::text,'trip_count',coalesce(sum(events),0)::text,
 'valid',bool_and(NOT coalesce(invalid,false) AND(prior_end IS NULL OR prior_end<first_day)),
 'cross_boundary_trips_excluded',(SELECT count(*)::text FROM scope WHERE started_on<$3::date OR ended_on>$4::date)) FROM ranges`,
		id.HouseholdID, query.VehicleID, query.From, query.To)
	if err != nil {
		return nil, nil, err
	}
	if len(tripRows) != 1 {
		return nil, nil, Invalid{"Streckenbilanz ist nicht eindeutig"}
	}
	distanceScope := tripRows[0]
	valid, _ := distanceScope["valid"].(bool)
	distance, distanceErr := fuelAggregateDecimal(String(distanceScope["distance"]))
	valid = valid && distanceErr == nil && distance.Sign() > 0
	warnings := []FuelModuleWarning{}
	if !valid {
		code := "overlapping_distance"
		if String(distanceScope["trip_count"]) == "0" {
			code = "no_documented_distance"
		}
		warnings = append(warnings, fuelReason(code))
	}
	count := new(big.Int)
	for _, row := range groups {
		row["amount_minor"], row["distance"], row["cost_per_km"] = row["known_amount_minor"], nil, nil
		if String(row["unknown_cost_events"]) != "0" {
			row["amount_minor"] = nil
			warnings = append(warnings, fuelReason("unknown_cost_currency"))
		}
		if n, ok := new(big.Int).SetString(String(row["events"]), 10); ok {
			count.Add(count, n)
		}
		if valid {
			row["distance"] = distance.FloatString(6)
			if row["amount_minor"] != nil {
				amount, ok := new(big.Int).SetString(String(row["amount_minor"]), 10)
				if !ok {
					return nil, nil, Invalid{"Kostenbilanz enthält keinen exakten Betrag"}
				}
				rate := new(big.Rat).Quo(new(big.Rat).SetInt(amount), distance)
				row["cost_per_km"] = rate.Quo(rate, big.NewRat(100, 1)).FloatString(4)
			}
		}
		row["partial_currency"], row["currency_conversion"] = len(groups) > 1, false
	}
	return Record{"groups": groups, "record_count": count.String(), "distance_basis": distanceScope, "from_on": query.From, "to_on": query.To,
		"distance_scope": "recorded_nonoverlapping_trips_wholly_inside_window", "unrecorded_distance_known": false,
		"whole_vehicle_tco": false, "currency_conversion": false, "cost_scope": "effective_vehicle_costs_plus_refuels_and_charges",
		"records_scope": "other_effective_vehicle_costs_only", "cost_kind_filter": query.Kind, "search_filter": query.Search, "household_today": scope.Today}, warnings, nil
}

func fuelModuleCostReportPane(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, cursor fuelModuleCursor, limit int) (FuelModuleView, error) {
	view := FuelModuleView{Context: scope, Pane: query.Pane, Records: []Record{}, Options: Record{}, Warnings: []FuelModuleWarning{}}
	if cursor.Dataset != "" && cursor.Dataset != "costs_current" {
		return view, ErrConflict
	}
	cursor.Dataset = "costs_current"
	costQuery := query
	costQuery.Kind = ""
	if cursor.Offset != 0 || cursor.Day != "" && !value.Date(cursor.Day) || cursor.Source != "" && cursor.Source != "fillup" && cursor.Source != "charging" && cursor.Source != "vehicle_cost" {
		return view, ErrConflict
	}
	var day, pointID, source any
	if cursor.ID != "" {
		day, pointID, source = cursor.Day, cursor.ID, cursor.Source
	}
	args := append(fuelModuleCostArgs(id, costQuery), day, pointID, source, limit+1)
	rows, err := fuelReadRecords(ctx, tx, fuelModuleCostsCTE+`SELECT jsonb_build_object('id',id,'vehicle_id',vehicle_id,'occurred_on',occurred_on,
 'amount_minor',total_minor::text,'currency_code',currency_code,'source_kind',source_kind,'row_kind','cost_event')
 FROM costs WHERE($8::date IS NULL OR(occurred_on,id,source_kind)<($8::date,$9::uuid,$10::text))
 ORDER BY occurred_on DESC,id DESC,source_kind DESC LIMIT $11`, args...)
	if err != nil {
		return view, err
	}
	view.Totals, view.Warnings, err = fuelModuleCostTotals(ctx, tx, id, scope, costQuery)
	if err != nil {
		return view, err
	}
	view.More = len(rows) > limit
	if view.More {
		rows = rows[:limit]
	}
	view.Records = rows
	if view.More {
		last := rows[len(rows)-1]
		cursor.Day, cursor.ID, cursor.Source = String(last["occurred_on"]), String(last["id"]), String(last["source_kind"])
		encoded, encodeErr := json.Marshal(cursor)
		if encodeErr != nil {
			return view, encodeErr
		}
		view.Next = string(encoded)
	}
	view.Options["chart"] = Record{"x": "occurred_on", "y": "amount_minor", "currency_field": "currency_code", "value_type": "integer_minor_string",
		"series_fields": []string{"currency_code"}, "rows_are_table_data": true, "currency_conversion": false, "chart_scope": "current_table_page"}
	view.Options["totals_scope"] = "entire_filtered_window_not_only_current_page"
	return view, err
}

const fuelCacheReportCTE = `WITH base AS(SELECT c.* FROM fuel.consumption_segments c
 WHERE c.household_id=$1 AND c.vehicle_id=$2 AND c.source_version=$3 AND c.algorithm_version=$4
 AND c.to_on>=$5::date AND c.from_on<=$6::date AND c.to_on<=$7::date
 AND($8::text='' OR c.quantity_unit=$8) AND($9::text='' OR c.fuel_type=$9)) `

func fuelCachedReportArgs(id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, version int64) []any {
	fuel := query.Kind
	if fuel == "consumption" {
		fuel = ""
	}
	return []any{id.HouseholdID, query.VehicleID, version, fuelSegmentAlgorithm, query.From, query.To, scope.Today, query.Unit, fuel}
}

func (s *Store) fuelCachedReportPane(ctx context.Context, tx pgx.Tx, id auth.Identity, scope FuelModuleContext, query FuelModuleQuery, cursor fuelModuleCursor, limit int, version int64, energy Record) (FuelModuleView, error) {
	view := FuelModuleView{Context: scope, Pane: query.Pane, Records: []Record{}, Options: Record{}, Warnings: []FuelModuleWarning{}}
	dataset := "cache/" + strconv.FormatInt(version, 10) + "/" + strconv.Itoa(fuelSegmentAlgorithm)
	if cursor.Offset != 0 || cursor.Day != "" && !value.Date(cursor.Day) || cursor.Dataset != "" && cursor.Dataset != dataset {
		return view, ErrConflict
	}
	cursor.Dataset = dataset
	args := fuelCachedReportArgs(id, scope, query, version)
	physical, err := fuelReadRecords(ctx, tx, fuelCacheReportCTE+`, ordered AS(SELECT b.*,
 lag(to_odometer) OVER(PARTITION BY odometer_series_id ORDER BY from_on,from_odometer,id) AS previous_reading,
 lag(to_on) OVER(PARTITION BY odometer_series_id ORDER BY from_on,from_odometer,id) AS previous_day
 FROM base b WHERE state='known')
 SELECT jsonb_build_object('vehicle_id',vehicle_id,'series_id',odometer_series_id,'fuel_type',fuel_type,'unit',quantity_unit,
 'from_on',min(from_on),'to_on',max(to_on),'quantity',sum(quantity)::text,'distance',sum(distance)::text,'intervals',count(*)::text,
 'invalid',bool_or(quantity IS NULL OR distance IS NULL OR quantity<=0 OR distance<=0 OR from_odometer IS NULL OR to_odometer IS NULL
 OR to_odometer-from_odometer<>distance OR previous_reading>from_odometer OR previous_day>from_on),
 'cost_unknown',bool_or(costs_minor='{}'::jsonb OR jsonb_typeof(costs_minor)<>'object'))
 FROM ordered GROUP BY vehicle_id,odometer_series_id,fuel_type,quantity_unit ORDER BY odometer_series_id,fuel_type,quantity_unit LIMIT 1001`, args...)
	if err != nil {
		return view, err
	}
	if len(physical) > 1000 {
		return view, Invalid{"Zeitraum oder Mengenart für höchstens 1.000 Vergleichsgruppen eingrenzen"}
	}
	if len(physical) == 0 {
		view.Warnings = append(view.Warnings, fuelReason("no_completed_interval"))
	}
	groups := map[string]Record{}
	for _, group := range physical {
		if invalid, _ := group["invalid"].(bool); invalid {
			return view, Invalid{"Verbrauchsprojektion enthält widersprüchliche Abschnitte; neu aufbauen"}
		}
		quantity, qErr := fuelAggregateDecimal(String(group["quantity"]))
		distance, dErr := fuelAggregateDecimal(String(group["distance"]))
		if qErr != nil || dErr != nil || quantity.Sign() <= 0 || distance.Sign() <= 0 {
			return view, Invalid{"Verbrauchsprojektion erneut aufbauen"}
		}
		rate := new(big.Rat).Mul(new(big.Rat).Quo(quantity, distance), big.NewRat(100, 1))
		group["per_100"], group["below_precision"] = rate.FloatString(2), rate.Sign() > 0 && rate.Cmp(big.NewRat(1, 100)) < 0
		group["coverage"], group["prorated"] = "completed_full_tank_intervals", false
		delete(group, "invalid")
		key := String(group["series_id"]) + "/" + String(group["fuel_type"]) + "/" + String(group["unit"])
		groups[key] = group
	}
	money, err := fuelReadRecords(ctx, tx, fuelCacheReportCTE+` SELECT jsonb_build_object('series_id',b.odometer_series_id,'fuel_type',b.fuel_type,'unit',b.quantity_unit,
 'currency',currency.key,'amount_minor',sum(CASE WHEN currency.key~'^[A-Z]{3}$' AND currency.value~'^[0-9]{1,64}$'
 THEN currency.value::numeric ELSE 0 END)::text,'valid',bool_and(currency.key~'^[A-Z]{3}$' AND currency.value~'^[0-9]{1,64}$'))
 FROM base b CROSS JOIN LATERAL jsonb_each_text(CASE WHEN jsonb_typeof(b.costs_minor)='object' THEN b.costs_minor ELSE '{}'::jsonb END) currency
 WHERE b.state='known' GROUP BY b.odometer_series_id,b.fuel_type,b.quantity_unit,currency.key
 ORDER BY b.odometer_series_id,b.fuel_type,b.quantity_unit,currency.key LIMIT 1001`, args...)
	if err != nil {
		return view, err
	}
	if len(money) > 1000 {
		return view, Invalid{"Kostenvergleich enthält mehr als 1.000 getrennte Währungsgruppen"}
	}
	currencyCounts := map[string]int{}
	for _, row := range money {
		currencyCounts[String(row["series_id"])+"/"+String(row["fuel_type"])+"/"+String(row["unit"])]++
	}
	for _, row := range money {
		if valid, _ := row["valid"].(bool); !valid {
			return view, Invalid{"Kostenprojektion enthält eine ungültige Währung oder einen ungültigen Betrag"}
		}
		key := String(row["series_id"]) + "/" + String(row["fuel_type"]) + "/" + String(row["unit"])
		group := groups[key]
		if group == nil {
			return view, Invalid{"Kostenprojektion besitzt keinen eindeutigen Streckenbezug"}
		}
		row["distance"], row["from_on"], row["to_on"] = group["distance"], group["from_on"], group["to_on"]
		row["cost_scope"], row["partial_currency"], row["currency_conversion"] = "refuels_after_anchor_through_closing_full", currencyCounts[key] > 1, false
		if unknown, _ := group["cost_unknown"].(bool); unknown {
			row["amount_minor"], row["cost_per_km"] = nil, nil
		} else {
			amount, ok := new(big.Int).SetString(String(row["amount_minor"]), 10)
			distance, distanceErr := fuelAggregateDecimal(String(group["distance"]))
			if !ok || distanceErr != nil || distance.Sign() <= 0 {
				return view, Invalid{"Kostenprojektion erneut aufbauen"}
			}
			rate := new(big.Rat).Quo(new(big.Rat).SetInt(amount), distance)
			rate.Quo(rate, big.NewRat(100, 1))
			row["cost_per_km"], row["below_precision"] = rate.FloatString(4), rate.Sign() > 0 && rate.Cmp(big.NewRat(1, 10000)) < 0
		}
		delete(row, "valid")
	}
	states, err := fuelReadRecords(ctx, tx, fuelCacheReportCTE+` SELECT jsonb_build_object('state',state,'reason',left(reason,80),'count',count(*)::text)
 FROM base GROUP BY state,left(reason,80) ORDER BY state,left(reason,80) LIMIT 65`, args...)
	if err != nil {
		return view, err
	}
	if len(states) > 64 {
		return view, Invalid{"Verbrauchsprojektion enthält zu viele unbekannte Zustände"}
	}
	count := new(big.Int)
	for _, state := range states {
		number, ok := new(big.Int).SetString(String(state["count"]), 10)
		if !ok {
			return view, Invalid{"Projektionszählung erneut aufbauen"}
		}
		count.Add(count, number)
		if String(state["state"]) != "known" {
			view.Warnings = append(view.Warnings, fuelReason(String(state["reason"])))
		}
	}
	var after, afterID any
	if cursor.ID != "" {
		after, afterID = cursor.Day, cursor.ID
	}
	pageArgs := append(append([]any{}, args...), after, afterID, limit+1)
	view.Records, err = fuelReadRecords(ctx, tx, fuelCacheReportCTE+` SELECT jsonb_build_object('id',id,'vehicle_id',vehicle_id,'series_id',odometer_series_id,
 'from_id',from_fillup_id,'to_id',to_fillup_id,'from_on',from_on,'to_on',to_on,'from_reading',from_odometer::text,'to_reading',to_odometer::text,
 'quantity',quantity::text,'distance',distance::text,'unit',quantity_unit,'fuel_type',fuel_type,
 'state',CASE WHEN state='known' THEN 'available' ELSE reason END,'below_precision',below_precision,
 'costs_minor',CASE WHEN octet_length(costs_minor::text)<=65536 THEN costs_minor END,'row_kind','consumption_interval','_cursor_day',to_on)
 FROM base WHERE($10::date IS NULL OR(to_on,id)<($10::date,$11::uuid)) ORDER BY to_on DESC,id DESC LIMIT $12`, pageArgs...)
	if err != nil {
		return view, err
	}
	view.More = len(view.Records) > limit
	if view.More {
		view.Records = view.Records[:limit]
	}
	view.Next, err = fuelModuleKeyNext(cursor, view.Records, view.More)
	if err != nil {
		return view, err
	}
	settings, err := fuelModuleSettings(ctx, tx, id, scope.Generation)
	if err != nil {
		return view, err
	}
	for _, row := range view.Records {
		delete(row, "_cursor_day")
		if String(row["state"]) == "available" {
			quantityText, distanceText := String(row["quantity"]), String(row["distance"])
			quantity, qErr := fuelAggregateDecimal(quantityText)
			distance, dErr := fuelAggregateDecimal(distanceText)
			if qErr != nil || dErr != nil || distance.Sign() <= 0 {
				return view, Invalid{"Verbrauchsprojektion erneut aufbauen"}
			}
			row["per_100"] = new(big.Rat).Mul(new(big.Rat).Quo(quantity, distance), big.NewRat(100, 1)).FloatString(2)
			row["warnings"] = fuelSegmentPlausibility(fuelComputedSegment{State: "available", Unit: String(row["unit"]), ToID: String(row["to_id"]), Quantity: &quantityText, Distance: &distanceText}, settings)
		}
	}
	view.Totals = Record{"groups": physical, "costs_per_km": money, "states": states, "record_count": count.String(), "charging": energy,
		"requested_from": query.From, "requested_to": query.To, "basis": "actual_completed_intervals_overlapping_window", "window_prorated": false}
	view.Options["segments_status"], view.Options["totals_scope"] = "ready", "entire_filtered_window_not_only_current_page"
	view.Options["chart"] = Record{"x": "to_on", "y": "per_100", "unit_field": "unit", "value_type": "decimal_string",
		"series_fields": []string{"series_id", "fuel_type", "unit"}, "rows_are_table_data": true, "chart_scope": "current_table_page", "prorated": false}
	return view, nil
}

// Table rows are the chart source. Values are never recalculated through
// JavaScript Number, and incomparable units, currencies and counters remain in
// distinct groups. The cost denominator is the same documented full interval.
func fuelReportSummary(ctx context.Context, segments []fuelComputedSegment, query FuelModuleQuery) (Record, []FuelModuleWarning, error) {
	if len(segments) > 10000 {
		return nil, nil, Invalid{"Auswertungsfenster für höchstens 10.000 Abschnitte eingrenzen"}
	}
	groups := map[string]*fuelReportGroup{}
	states := Record{}
	warnings := []FuelModuleWarning{}
	seenWarnings := map[string]bool{}
	ordered := append([]fuelComputedSegment(nil), segments...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].SeriesID != ordered[j].SeriesID {
			return ordered[i].SeriesID < ordered[j].SeriesID
		}
		if ordered[i].From != ordered[j].From {
			return ordered[i].From < ordered[j].From
		}
		left, leftErr := fuelAggregateDecimal(ordered[i].FromReading)
		right, rightErr := fuelAggregateDecimal(ordered[j].FromReading)
		if leftErr == nil && rightErr == nil && left.Cmp(right) != 0 {
			return left.Cmp(right) < 0
		}
		return ordered[i].FromID < ordered[j].FromID
	})
	previous := map[string]fuelComputedSegment{}
	for _, segment := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		states[segment.State] = Int(states[segment.State]) + 1
		if segment.State != "available" || segment.Quantity == nil || segment.Distance == nil {
			if !seenWarnings[segment.State] {
				warnings = append(warnings, fuelReason(segment.State))
				seenWarnings[segment.State] = true
			}
			continue
		}
		quantity, quantityErr := fuelAggregateDecimal(*segment.Quantity)
		distance, distanceErr := fuelAggregateDecimal(*segment.Distance)
		fromReading, fromErr := fuelAggregateDecimal(segment.FromReading)
		toReading, toErr := fuelAggregateDecimal(segment.ToReading)
		if quantityErr != nil || distanceErr != nil || fromErr != nil || toErr != nil || quantity.Sign() <= 0 || distance.Sign() <= 0 ||
			new(big.Rat).Sub(toReading, fromReading).Cmp(distance) != 0 {
			return nil, nil, Invalid{"Verbrauchsprojektion erneut aufbauen"}
		}
		if prior, exists := previous[segment.SeriesID]; exists {
			priorTo, priorErr := fuelAggregateDecimal(prior.ToReading)
			if priorErr != nil || priorTo.Cmp(fromReading) > 0 || prior.To > segment.From {
				return nil, []FuelModuleWarning{fuelReason("overlapping_distance")}, Invalid{"Verbrauchsabschnitte überschneiden sich"}
			}
		}
		previous[segment.SeriesID] = segment
		key := segment.VehicleID + "/" + segment.SeriesID + "/" + segment.Fuel + "/" + segment.Unit
		group := groups[key]
		if group == nil {
			group = &fuelReportGroup{VehicleID: segment.VehicleID, SeriesID: segment.SeriesID, Fuel: segment.Fuel, Unit: segment.Unit,
				From: segment.From, To: segment.To, Quantity: new(big.Rat), Distance: new(big.Rat), Costs: map[string]*big.Int{}}
			groups[key] = group
		}
		group.Quantity.Add(group.Quantity, quantity)
		group.Distance.Add(group.Distance, distance)
		group.Intervals++
		if segment.From < group.From {
			group.From = segment.From
		}
		if segment.To > group.To {
			group.To = segment.To
		}
		if len(segment.Costs) == 0 {
			group.CostsUnknown = true
		}
		for currency, text := range segment.Costs {
			amount, ok := new(big.Int).SetString(text, 10)
			if !ok || amount.Sign() < 0 || len(text) > 64 || len(currency) != 3 ||
				strings.IndexFunc(currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
				return nil, nil, Invalid{"Kostenprojektion erneut aufbauen"}
			}
			if group.Costs[currency] == nil {
				group.Costs[currency] = new(big.Int)
			}
			group.Costs[currency].Add(group.Costs[currency], amount)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	physical, costs := []Record{}, []Record{}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		group := groups[key]
		rate := new(big.Rat).Mul(new(big.Rat).Quo(group.Quantity, group.Distance), big.NewRat(100, 1))
		physical = append(physical, Record{"vehicle_id": group.VehicleID, "series_id": group.SeriesID, "fuel_type": group.Fuel,
			"unit": group.Unit, "from_on": group.From, "to_on": group.To, "quantity": group.Quantity.FloatString(6),
			"distance": group.Distance.FloatString(6), "per_100": rate.FloatString(2), "intervals": group.Intervals,
			"below_precision": rate.Sign() > 0 && rate.Cmp(big.NewRat(1, 100)) < 0,
			"coverage":        "completed_full_tank_intervals", "prorated": false, "cost_unknown": group.CostsUnknown})
		if group.CostsUnknown && !seenWarnings["unknown_cost_currency"] {
			warnings = append(warnings, fuelReason("unknown_cost_currency"))
			seenWarnings["unknown_cost_currency"] = true
		}
		currencies := make([]string, 0, len(group.Costs))
		for currency := range group.Costs {
			currencies = append(currencies, currency)
		}
		sort.Strings(currencies)
		for _, currency := range currencies {
			var perKM any
			var amount any
			belowPrecision := false
			if !group.CostsUnknown {
				amount = group.Costs[currency].String()
				ratio := new(big.Rat).Quo(new(big.Rat).SetInt(group.Costs[currency]), group.Distance)
				ratio.Quo(ratio, big.NewRat(100, 1))
				perKM = ratio.FloatString(4)
				belowPrecision = ratio.Sign() > 0 && ratio.Cmp(big.NewRat(1, 10000)) < 0
			}
			costs = append(costs, Record{"vehicle_id": group.VehicleID, "series_id": group.SeriesID, "fuel_type": group.Fuel,
				"unit": group.Unit, "currency": currency, "amount_minor": amount, "distance": group.Distance.FloatString(6),
				"cost_per_km": perKM, "below_precision": belowPrecision, "from_on": group.From, "to_on": group.To,
				"cost_scope": "refuels_after_anchor_through_closing_full", "other_vehicle_costs_included": false,
				"currency_conversion": false, "partial_currency": len(currencies) > 1, "prorated": false})
		}
	}
	if len(physical) == 0 {
		warnings = append(warnings, fuelReason("no_documented_distance"))
	}
	return Record{"groups": physical, "costs_per_km": costs, "states": states, "requested_from": query.From, "requested_to": query.To,
		"basis": "actual_completed_intervals_overlapping_window", "window_prorated": false,
		"all_vehicle_cost_per_km": nil, "all_vehicle_cost_reason": "no_shared_unambiguous_distance_allocation"}, warnings, nil
}
