package store

import "slices"

func fuelModuleWireRecord(record Record) Record {
	for name, entry := range record {
		if entry == nil {
			continue
		}
		if slices.Contains([]string{"version", "cycle", "row_number", "job_version", "total_minor", "amount_minor", "quantity", "unit_price",
			"initial_reading", "final_reading", "distance_offset", "odometer", "reading", "energy_kwh", "soc_start", "soc_end", "start_odometer", "end_odometer",
			"tank_capacity", "battery_capacity", "due_odometer", "repeat_distance", "repeat_days", "minimum_consumption", "maximum_consumption",
			"maximum_fill_quantity", "maximum_daily_distance", "maximum_unit_price"}, name) {
			record[name] = String(entry)
			continue
		}
		if child, ok := entry.(map[string]any); ok {
			record[name] = fuelModuleWireRecord(child)
		}
	}
	return record
}
