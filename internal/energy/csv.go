package energy

import (
	"encoding/csv"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type CSVRow struct {
	Number    int        `json:"number"`
	At        time.Time  `json:"measured_at"`
	StartedAt *time.Time `json:"interval_started_at,omitempty"`
	Value     string     `json:"value"`
	Kind      string     `json:"kind"`
	Unit      string     `json:"unit_code"`
}

type CSVProblem struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// ParseTimestamp refuses ambiguous or nonexistent local times. An explicit UTC
// offset remains the portable way to import either side of a clock transition.
func ParseTimestamp(text, zone string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
		if !validInstant(at) || at.Nanosecond()%1000 != 0 {
			return time.Time{}, ErrInvalid
		}
		return at.UTC(), nil
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	const layout = "2006-01-02 15:04:05"
	wall, err := time.Parse(layout, text)
	if err != nil || !validInstant(wall) {
		return time.Time{}, ErrInvalid
	}
	offsets := map[int]bool{}
	for hours := -36; hours <= 36; hours += 6 {
		_, offset := wall.Add(time.Duration(hours) * time.Hour).In(location).Zone()
		offsets[offset] = true
	}
	matches := []time.Time{}
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.In(location).Format(layout) == text {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return time.Time{}, ErrInvalid
	}
	return matches[0].UTC(), nil
}

func ParseCSV(text, zone, unit, kind, delimiter string) ([]CSVRow, []CSVProblem, error) {
	if len(text) == 0 || len(text) > 512<<10 || !utf8.ValidString(text) || !ValidType(kind, unit) || delimiter != ";" && delimiter != "," {
		return nil, nil, ErrInvalid
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return nil, nil, ErrInvalid
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	reader.Comma = rune(delimiter[0])
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil || len(header) < 4 || len(header) > 5 {
		return nil, nil, ErrInvalid
	}
	positions := map[string]int{}
	for index, name := range header {
		if !slices.Contains([]string{"measured_at", "value", "kind", "unit_code", "interval_started_at"}, name) {
			return nil, nil, ErrInvalid
		}
		if _, duplicate := positions[name]; duplicate {
			return nil, nil, ErrInvalid
		}
		positions[name] = index
	}
	for _, name := range []string{"measured_at", "value", "kind", "unit_code"} {
		if _, exists := positions[name]; !exists {
			return nil, nil, ErrInvalid
		}
	}
	rows := []CSVRow{}
	problems := []CSVProblem{}
	seen := map[string]bool{}
	for number := 1; ; number++ {
		fields, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(fields) != len(header) || number > 1000 {
			return nil, nil, ErrInvalid
		}
		for _, field := range fields {
			if len(field) > 300 {
				return nil, nil, ErrInvalid
			}
		}
		field := func(name string) string {
			index, exists := positions[name]
			if !exists {
				return ""
			}
			return strings.TrimSpace(fields[index])
		}
		row := CSVRow{Number: number, Value: field("value"), Kind: field("kind"), Unit: field("unit_code")}
		at, timeErr := ParseTimestamp(field("measured_at"), zone)
		row.At = at
		_, valueErr := Decimal(row.Value)
		valid := timeErr == nil && valueErr == nil && row.Unit == unit && slices.Contains([]string{"actual", "estimated"}, row.Kind)
		if kind == "interval" {
			start, err := ParseTimestamp(field("interval_started_at"), zone)
			valid = valid && err == nil && start.Before(at)
			row.StartedAt = &start
		} else if field("interval_started_at") != "" {
			valid = false
		}
		key := at.Format(time.RFC3339Nano)
		if seen[key] {
			valid = false
		}
		seen[key] = true
		if !valid {
			if len(problems) < 50 {
				problems = append(problems, CSVProblem{Row: number + 1,
					Message: "Zeitpunkt, eindeutigen UTC-Offset, exakten Dezimalwert, Registereinheit, Art und doppelte Zeitpunkte prüfen."})
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, ErrInvalid
	}
	return rows, problems, nil
}
