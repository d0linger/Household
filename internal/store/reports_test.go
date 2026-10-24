package store

import "testing"

func TestFuelConsumption(t *testing.T) {
	t.Parallel()
	row := func(id, series, odo, qty string, full, missing bool) Record {
		return Record{"id": id, "series": series, "odometer": odo, "quantity": qty, "fuel_type": "diesel", "full": full, "missing": missing}
	}
	cases := []struct {
		name  string
		rows  []Record
		count int
		want  string
	}{{"full to full", []Record{row("a", "one", "1000", "30", true, false), row("b", "one", "1500", "25", true, false)}, 1, "5.00"}, {"partial included", []Record{row("a", "one", "1000", "30", true, false), row("b", "one", "1200", "10", false, false), row("c", "one", "1500", "15", true, false)}, 1, "5.00"}, {"missing resets", []Record{row("a", "one", "1000", "30", true, false), row("b", "one", "1500", "25", true, true)}, 0, ""}, {"series reset", []Record{row("a", "one", "1000", "30", true, false), row("b", "two", "1500", "25", true, false)}, 0, ""}, {"zero distance", []Record{row("a", "one", "1000", "30", true, false), row("b", "one", "1000", "25", true, false)}, 0, ""}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FuelConsumption(tc.rows)
			if len(got) != tc.count {
				t.Fatalf("segments %d", len(got))
			}
			if tc.count > 0 && got[0]["per_100km"] != tc.want {
				t.Fatalf("consumption %v", got[0])
			}
		})
	}
}

func TestFuelConsumptionKeepsVehicleAndPeriod(t *testing.T) {
	t.Parallel()
	rows := []Record{
		{"id": "a", "series": "one", "vehicle": "car", "vehicle_name": "Family car", "series_label": "Original counter", "date": "2026-01-01", "odometer": "1000", "quantity": "20", "fuel_type": "diesel", "full": true},
		{"id": "b", "series": "one", "vehicle": "car", "vehicle_name": "Family car", "series_label": "Original counter", "date": "2026-02-01", "odometer": "1500", "quantity": "25", "fuel_type": "diesel", "full": true},
	}
	got := FuelConsumption(rows)
	if len(got) != 1 || got[0]["vehicle_name"] != "Family car" || got[0]["series_label"] != "Original counter" || got[0]["from_date"] != "2026-01-01" || got[0]["to_date"] != "2026-02-01" {
		t.Fatalf("consumption lost its vehicle or period: %v", got)
	}
}
