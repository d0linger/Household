package store

import (
	"math/big"
	"slices"
	"time"

	"household/internal/auth"
	"household/internal/civiltime"
	"household/internal/energy"
)

func energyReadAnomalies(scope *energyReadScope, id auth.Identity, register Record) ([]energy.Anomaly, error) {
	out := []energy.Anomaly{}
	if String(register["daily_warning"]) != "" {
		rows, err := energyRows(scope.ctx, scope.tx, 100, `SELECT to_jsonb(i)||jsonb_build_object('quantity',i.quantity::text)
 FROM meters.intervals i WHERE household_id=$1 AND register_id=$2 AND through_at<=transaction_timestamp()
 ORDER BY through_at DESC LIMIT 100`, id.HouseholdID, register["id"])
		if err != nil {
			return nil, err
		}
		slices.Reverse(rows)
		intervals := []energy.Interval{}
		for _, row := range rows {
			from, err := energyTime(String(row["from_at"]))
			if err != nil {
				return nil, err
			}
			through, err := energyTime(String(row["through_at"]))
			if err != nil {
				return nil, err
			}
			var quantity *big.Rat
			if text := String(row["quantity"]); text != "" {
				var ok bool
				quantity, ok = new(big.Rat).SetString(text)
				if !ok || quantity.Sign() < 0 || len(text) > 40 {
					return nil, Invalid{"Gespeicherte Verbrauchsintervalle prüfen"}
				}
			}
			intervals = append(intervals, energy.Interval{From: from, Through: through, Quantity: quantity, Quality: String(row["quality"])})
		}
		daily, err := energy.Anomalies(intervals, String(register["daily_warning"]), int(Int(register["min_history"])))
		if err != nil {
			return nil, err
		}
		out = append(out, daily...)
	}
	if String(register["base_load_warning"]) == "" || register["measurement_kind"] != "power" {
		return out, nil
	}
	zone := String(register["timezone"])
	rows, err := energyRows(scope.ctx, scope.tx, 100, `SELECT jsonb_build_object('day',(from_at AT TIME ZONE $3)::date,
 'quantity',sum(quantity)::text,'covered',sum(covered_seconds)) FROM meters.aggregates
 WHERE household_id=$1 AND register_id=$2 AND bucket_kind='hour' AND from_at>=transaction_timestamp()-interval '100 days'
 AND through_at<=transaction_timestamp() AND extract(hour FROM from_at AT TIME ZONE $3)<6 AND quantity IS NOT NULL
 GROUP BY (from_at AT TIME ZONE $3)::date ORDER BY (from_at AT TIME ZONE $3)::date LIMIT 100`, id.HouseholdID, register["id"], zone)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	nights := []energy.Night{}
	for _, row := range rows {
		date, err := time.Parse(time.DateOnly, String(row["day"]))
		if err != nil {
			return nil, err
		}
		start, _, err := civiltime.Resolve(date, "00:00", location)
		if err != nil {
			return nil, err
		}
		end, _, err := civiltime.Resolve(date, "06:00", location)
		if err != nil || !start.Before(end) {
			return nil, Invalid{"Nächtlichen Messzeitraum prüfen"}
		}
		nights = append(nights, energy.Night{Day: String(row["day"]), QuantityKWh: String(row["quantity"]),
			CoveredSeconds: Int(row["covered"]), ExpectedSeconds: int64(end.Sub(start) / time.Second)})
	}
	base, err := energy.BaseLoad(nights, String(register["unit_code"]), String(register["base_load_warning"]), int(Int(register["min_history"])))
	return append(out, base...), err
}
