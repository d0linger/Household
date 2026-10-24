package store

import (
	"context"
	"math/big"
	"time"

	"household/internal/auth"
	"household/internal/value"
)

// ReportHistoryLimit bounds chronological consumption calculations. Queries read
// one additional row so exceeding the bound cannot silently produce a partial report.
const ReportHistoryLimit = 10000

// FuelConsumption joins partial fills only between two full fills in one series.
// A missing fill resets the baseline. Different fuels never share a segment.
func FuelConsumption(rows []Record) []Record {
	type segment struct {
		baseline *big.Rat
		quantity *big.Rat
		fuel     string
		id       string
		date     string
	}
	series := map[string]*segment{}
	result := []Record{}
	for _, row := range rows {
		key := String(row["series"])
		reading, err := value.Decimal(String(row["odometer"]))
		if err != nil {
			continue
		}
		quantity, err := value.Decimal(String(row["quantity"]))
		if err != nil {
			continue
		}
		full, _ := row["full"].(bool)
		missing, _ := row["missing"].(bool)
		fuel := String(row["fuel_type"])
		state := series[key]
		if state == nil || missing || state.fuel != fuel {
			state = &segment{quantity: new(big.Rat), fuel: fuel}
			series[key] = state
		}
		if state.baseline != nil {
			state.quantity.Add(state.quantity, quantity)
		}
		if !full {
			continue
		}
		if state.baseline != nil {
			distance := new(big.Rat).Sub(reading, state.baseline)
			if distance.Sign() > 0 {
				consumption := new(big.Rat).Mul(new(big.Rat).Quo(state.quantity, distance), big.NewRat(100, 1))
				result = append(result, Record{"from": state.id, "to": row["id"], "from_date": state.date, "to_date": row["date"], "series": key, "vehicle": row["vehicle"], "vehicle_name": row["vehicle_name"], "series_label": row["series_label"], "date": row["date"], "distance": distance.FloatString(3), "quantity": state.quantity.FloatString(3), "per_100km": consumption.FloatString(2), "unit": map[string]string{"cng": "kg/100 km"}[fuel]})
				if fuel != "cng" {
					result[len(result)-1]["unit"] = "l/100 km"
				}
			}
		}
		state.baseline = new(big.Rat).Set(reading)
		state.quantity = new(big.Rat)
		state.id = String(row["id"])
		state.date = String(row["date"])
	}
	return result
}

func (s *Store) MeterConsumption(ctx context.Context, id auth.Identity) ([]Record, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.device_id,m.name,m.unit_code,r.measured_at,r.value::text,d.initial_reading::text,d.installed_on,d.rollover_value::text,d.serial_number FROM meters.readings r JOIN meters.devices d ON d.household_id=r.household_id AND d.id=r.device_id JOIN meters.meters m ON m.household_id=d.household_id AND m.id=d.meter_id WHERE r.household_id=$1 ORDER BY r.device_id,r.measured_at LIMIT $2`, id.HouseholdID, ReportHistoryLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	lastID := ""
	var previous *big.Rat
	var previousDate time.Time
	for rows.Next() {
		if len(result) == ReportHistoryLimit {
			return nil, Invalid{Message: "Diese Auswertung unterstützt höchstens 10.000 Zählerstände. Es werden keine unvollständigen Ergebnisse angezeigt; für den vollständigen Datenbestand die Betreiber-Sicherung verwenden."}
		}
		var device, name, unit, current, initial, serial string
		var at, installed time.Time
		var rollover *string
		if err = rows.Scan(&device, &name, &unit, &at, &current, &initial, &installed, &rollover, &serial); err != nil {
			return nil, err
		}
		if device != lastID {
			previous, err = value.Decimal(initial)
			if err != nil {
				return nil, err
			}
			previousDate = installed
			lastID = device
		}
		reading, err := value.Decimal(current)
		if err != nil {
			return nil, err
		}
		delta := new(big.Rat).Sub(reading, previous)
		inferred := false
		if delta.Sign() < 0 && rollover != nil {
			max, err := value.Decimal(*rollover)
			if err != nil {
				return nil, err
			}
			delta.Add(delta, max)
			inferred = true
		}
		result = append(result, Record{"device": device, "device_label": serial, "name": name, "unit": unit, "from": previousDate.Format(time.RFC3339), "to": at.Format(time.RFC3339), "consumption": delta.FloatString(3), "rollover_inferred": inferred})
		previous = reading
		previousDate = at
	}
	return result, rows.Err()
}
