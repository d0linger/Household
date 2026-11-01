package store

import (
	"context"
	"math/big"
	"slices"
	"time"

	"household/internal/auth"
	"household/internal/civiltime"
	"household/internal/energy"
	"household/internal/value"
)

func (s *Store) EnergySeries(ctx context.Context, id auth.Identity, query EnergyQuery) (Record, error) {
	if !value.ValidUUID(query.RegisterID) || !slices.Contains([]string{"hour", "day", "month"}, query.Grain) {
		return nil, Invalid{"Register und Stunden-, Tages- oder Monatsaggregation wählen"}
	}
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	register, err := energyObject(scope.ctx, scope.tx, `SELECT to_jsonb(r) FROM meters.registers r WHERE household_id=$1 AND id=$2`,
		id.HouseholdID, query.RegisterID)
	if err != nil {
		return nil, err
	}
	from, through, err := energyWindow(scope, query)
	if err != nil {
		return nil, err
	}
	zone := String(register["timezone"])
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	if query.Grain == "month" && (from.In(location).Day() != 1 || through.In(location).Day() != 1) {
		return nil, Invalid{"Monatsvergleich benötigt vollständige Kalendermonate; für Teilmonate Tageswerte wählen"}
	}
	var pending bool
	err = scope.tx.QueryRow(scope.ctx, `SELECT EXISTS(SELECT 1 FROM meters.dirty_ranges WHERE household_id=$1 AND register_id=$2 AND from_at<$4 AND through_at>$3)`,
		id.HouseholdID, query.RegisterID, from, through).Scan(&pending)
	if err != nil {
		return nil, err
	}
	unit := String(register["unit_code"])
	if register["measurement_kind"] == "power" {
		unit = "kWh"
	}
	out := Record{"generation": scope.generation, "register_id": query.RegisterID, "measurement_kind": register["measurement_kind"],
		"unit_code": unit, "timezone": zone, "from": from, "through": through, "grain": query.Grain,
		"recalculation_pending": pending, "rows": []Record{}, "total_quantity": nil, "coverage_seconds": 0,
		"period_seconds": int64(through.Sub(from) / time.Second), "estimated_seconds": 0, "cost_complete": false}
	if pending {
		return out, nil
	}
	rows, err := energyRows(scope.ctx, scope.tx, 2500, `SELECT to_jsonb(a)||jsonb_build_object('quantity',a.quantity::text)
 FROM meters.aggregates a WHERE household_id=$1 AND register_id=$2 AND bucket_kind=$3 AND from_at>=$4 AND through_at<=$5
 ORDER BY from_at LIMIT 2501`, id.HouseholdID, query.RegisterID, query.Grain, from, through)
	if err != nil {
		return nil, err
	}
	quantity := new(big.Rat)
	var covered, estimated int64
	for _, row := range rows {
		covered += Int(row["covered_seconds"])
		estimated += Int(row["estimated_seconds"])
		if raw := String(row["quantity"]); raw != "" {
			amount, ok := new(big.Rat).SetString(raw)
			if !ok || amount.Sign() < 0 || len(raw) > 40 {
				return nil, Invalid{"Gespeicherten Aggregatwert prüfen"}
			}
			quantity.Add(quantity, amount)
		}
	}
	if covered > int64(through.Sub(from)/time.Second) {
		return nil, Invalid{"Zeitreihenabdeckung überschreitet den gewählten Zeitraum"}
	}
	out["rows"], out["coverage_seconds"], out["estimated_seconds"] = rows, covered, estimated
	if covered > 0 {
		out["total_quantity"] = quantity.FloatString(6)
		out["daily_average"] = new(big.Rat).Mul(quantity, big.NewRat(int64(24*time.Hour/time.Second), covered)).FloatString(6)
	}
	// Daily aggregates keep the monetary path independent of raw history size.
	days := rows
	if query.Grain != "day" {
		days, err = energyRows(scope.ctx, scope.tx, 367, `SELECT to_jsonb(a)||jsonb_build_object('quantity',a.quantity::text)
 FROM meters.aggregates a WHERE household_id=$1 AND register_id=$2 AND bucket_kind='day' AND from_at>=$3 AND through_at<=$4
 ORDER BY from_at LIMIT 368`, id.HouseholdID, query.RegisterID, from, through)
		if err != nil {
			return nil, err
		}
	}
	intervals := []energy.Interval{}
	for _, row := range days {
		start, err := energyTime(String(row["from_at"]))
		if err != nil {
			return nil, err
		}
		end, err := energyTime(String(row["through_at"]))
		if err != nil {
			return nil, err
		}
		var amount *big.Rat
		if text := String(row["quantity"]); text != "" {
			var ok bool
			amount, ok = new(big.Rat).SetString(text)
			if !ok || amount.Sign() < 0 || len(text) > 40 {
				return nil, Invalid{"Gespeicherten Tageswert prüfen"}
			}
		}
		quality := "actual"
		if Int(row["estimated_seconds"]) > 0 || Int(row["covered_seconds"]) != int64(end.Sub(start)/time.Second) {
			quality = "estimated"
		}
		intervals = append(intervals, energy.Interval{From: start, Through: end, Quantity: amount, Unit: unit, Quality: quality})
	}
	tariffRows, err := energyRows(scope.ctx, scope.tx, 250, `SELECT to_jsonb(t)||jsonb_build_object('price_minor_per_unit',t.price_minor_per_unit::text,
 'base_minor_per_day',t.base_minor_per_day::text) FROM meters.tariff_versions t WHERE household_id=$1 AND register_id=$2
 AND starts_on<($4::timestamptz AT TIME ZONE $5)::date AND (ends_before IS NULL OR ends_before>($3::timestamptz AT TIME ZONE $5)::date)
 ORDER BY starts_on LIMIT 251`, id.HouseholdID, query.RegisterID, from, through, zone)
	if err != nil {
		return nil, err
	}
	tariffs := []energy.Tariff{}
	for _, row := range tariffRows {
		date, err := time.Parse(time.DateOnly, String(row["starts_on"]))
		if err != nil {
			return nil, err
		}
		start, _, err := civiltime.Resolve(date, "00:00", location)
		if err != nil {
			return nil, err
		}
		end := through
		if text := String(row["ends_before"]); text != "" {
			date, err = time.Parse(time.DateOnly, text)
			if err != nil {
				return nil, err
			}
			end, _, err = civiltime.Resolve(date, "00:00", location)
			if err != nil {
				return nil, err
			}
		}
		tariffs = append(tariffs, energy.Tariff{From: start, Through: end, PriceMinor: String(row["price_minor_per_unit"]),
			DailyMinor: String(row["base_minor_per_day"]), CurrencyCode: String(row["currency_code"])})
	}
	cost, err := energy.Costs(intervals, tariffs, zone, from, through)
	if err != nil {
		return nil, Invalid{"Tarifintervalle und Messzeitraum prüfen"}
	}
	cost.MissingQuantitySeconds = int64(through.Sub(from)/time.Second) - covered
	cost.Complete = cost.MissingQuantitySeconds == 0 && cost.MissingTariffSeconds == 0
	var payments string
	err = scope.tx.QueryRow(scope.ctx, `SELECT coalesce(sum(amount_minor),0)::text FROM meters.payments WHERE household_id=$1 AND register_id=$2
 AND paid_on>=($3::timestamptz AT TIME ZONE $5)::date AND paid_on<($4::timestamptz AT TIME ZONE $5)::date`,
		id.HouseholdID, query.RegisterID, from, through, zone).Scan(&payments)
	if err != nil {
		return nil, err
	}
	paid, ok := new(big.Rat).SetString(payments)
	if !ok {
		return nil, Invalid{"Gespeicherte Abschläge prüfen"}
	}
	rounded := cost.Minor.FloatString(0)
	roundedCost, ok := new(big.Rat).SetString(rounded)
	if !ok {
		return nil, Invalid{"Berechneten Kostenbetrag prüfen"}
	}
	out["known_cost_minor"], out["cost_complete"], out["cost_estimated"] = rounded, cost.Complete, cost.Estimated
	out["payments_minor"], out["known_difference_minor"] = payments, new(big.Rat).Sub(roundedCost, paid).FloatString(0)
	out["missing_quantity_seconds"], out["missing_tariff_seconds"], out["currency_code"] = cost.MissingQuantitySeconds, cost.MissingTariffSeconds, "EUR"
	anomalies, err := energyReadAnomalies(scope, id, register)
	if err != nil {
		return nil, Invalid{"Grenzwertkonfiguration prüfen"}
	}
	out["anomalies"] = anomalies
	return out, nil
}
