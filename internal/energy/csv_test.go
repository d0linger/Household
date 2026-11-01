package energy

import (
	"strings"
	"testing"
)

func TestCSVRequiresExplicitTimeUnitAndExactValues(t *testing.T) {
	const header = "measured_at;value;kind;unit_code\n"
	for _, tc := range []struct {
		name, row string
		valid     bool
	}{
		{"explicit fold earlier", "2026-10-25T02:30:00+02:00;1.000001;actual;kWh", true},
		{"explicit fold later", "2026-10-25T02:30:00+01:00;1;estimated;kWh", true},
		{"ambiguous fold", "2026-10-25 02:30:00;1;actual;kWh", false},
		{"missing hour", "2026-03-29 02:30:00;1;actual;kWh", false},
		{"wrong unit", "2026-01-01T00:00:00Z;1;actual;kW", false},
		{"formula", "2026-01-01T00:00:00Z;=1+2;actual;kWh", false},
		{"numeric exponent", "2026-01-01T00:00:00Z;1e3;actual;kWh", false},
		{"submicrosecond", "2026-01-01T00:00:00.0000001Z;1;actual;kWh", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, problems, err := ParseCSV(header+tc.row, "Europe/Vienna", "kWh", "cumulative", ";")
			if err != nil || len(rows) != 1 || (len(problems) == 0) != tc.valid {
				t.Fatal(rows, problems, err)
			}
		})
	}
	if _, problems, err := ParseCSV(header+"2026-01-01T00:00:00Z;1;actual;kWh\n2026-01-01T01:00:00+01:00;2;actual;kWh", "UTC", "kWh", "cumulative", ";"); err != nil || len(problems) != 1 {
		t.Fatal("same instant escaped duplicate check", problems, err)
	}
	if _, _, err := ParseCSV(header+strings.Repeat("2026-01-01T00:00:00Z;1;actual;kWh\n", 1001), "UTC", "kWh", "cumulative", ";"); err == nil {
		t.Fatal("row budget bypassed")
	}
	if _, _, err := ParseCSV("value;value;kind;unit_code\n1;1;actual;kWh", "UTC", "kWh", "cumulative", ";"); err == nil {
		t.Fatal("duplicate header accepted")
	}
}
