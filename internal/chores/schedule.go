// Package chores contains bounded, database-independent household task rules.
package chores

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // The scratch runtime must resolve the same IANA zones as tests.
)

const DSTPolicy = "civil-v1/forward-gap/early-fold"
const MaximumWindow = 366

var ErrNoOccurrence = errors.New("kein Termin im gewählten Zeitraum")

type Schedule struct {
	Kind      string `json:"kind"`
	Interval  int    `json:"interval"`
	Anchor    string `json:"anchor"`
	StartsOn  string `json:"starts_on"`
	LocalTime string `json:"local_time"`
	Timezone  string `json:"timezone"`
	Weekdays  []int  `json:"weekdays"`
	Months    []int  `json:"months"`
	MonthDay  int    `json:"month_day"`
	EndsOn    string `json:"ends_on,omitempty"`
	Policy    string `json:"dst_policy"`
}

type Slot struct {
	Day        string    `json:"day"`
	LocalTime  string    `json:"local_time"`
	Timezone   string    `json:"timezone"`
	DueAt      time.Time `json:"due_at"`
	Resolution string    `json:"resolution"`
}

type Projection struct {
	Slots   []Slot `json:"slots"`
	More    bool   `json:"more"`
	Through string `json:"through"`
}

// ParseDay uses civil UTC dates; Unix seconds avoid Duration overflow across
// the supported 1900–2200 range. UTC here is a calendar coordinate, not a due time.
func ParseDay(day string) (time.Time, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil || date.Year() < 1900 || date.Year() > 2200 || date.Format(time.DateOnly) != day {
		return time.Time{}, errors.New("bitte Datum zwischen 1900 und 2200 im Format JJJJ-MM-TT angeben")
	}
	return date, nil
}

func orderedValues(values []int, maximum int) ([]int, error) {
	if len(values) > maximum {
		return nil, errors.New("zu viele Kalenderwerte")
	}
	result := append([]int{}, values...)
	slices.Sort(result)
	for index, value := range result {
		if value < 1 || value > maximum || index > 0 && result[index-1] == value {
			return nil, errors.New("ungültige oder doppelte Kalenderwerte")
		}
	}
	return result, nil
}

func Normalize(schedule Schedule) (Schedule, error) {
	schedule.StartsOn = strings.TrimSpace(schedule.StartsOn)
	start, err := ParseDay(schedule.StartsOn)
	if err != nil {
		return Schedule{}, err
	}
	if schedule.Interval == 0 {
		schedule.Interval = 1
	}
	if schedule.Anchor == "" {
		schedule.Anchor = "scheduled"
	}
	if schedule.LocalTime == "" {
		schedule.LocalTime = "09:00"
	}
	if schedule.Policy == "" {
		schedule.Policy = DSTPolicy
	}
	if schedule.Interval < 1 || schedule.Interval > 366 || schedule.Policy != DSTPolicy {
		return Schedule{}, errors.New("nicht unterstütztes Intervall oder Kalenderregelversion")
	}
	clock, err := time.Parse("15:04", schedule.LocalTime)
	if err != nil || clock.Format("15:04") != schedule.LocalTime {
		return Schedule{}, errors.New("ungültige Uhrzeit; Format HH:MM verwenden")
	}
	if len(schedule.Timezone) < 1 || len(schedule.Timezone) > 100 {
		return Schedule{}, errors.New("gültige IANA-Zeitzone auswählen")
	}
	if _, err = time.LoadLocation(schedule.Timezone); err != nil {
		return Schedule{}, errors.New("IANA-Zeitzone ist nicht verfügbar")
	}
	schedule.Weekdays, err = orderedValues(schedule.Weekdays, 7)
	if err != nil {
		return Schedule{}, err
	}
	schedule.Months, err = orderedValues(schedule.Months, 12)
	if err != nil {
		return Schedule{}, err
	}
	if schedule.Anchor != "scheduled" && schedule.Anchor != "completion" {
		return Schedule{}, errors.New("festen Kalender oder letzte Erledigung ausdrücklich auswählen")
	}
	if schedule.Anchor == "completion" && (schedule.Kind != "days" || len(schedule.Months) != 0) {
		return Schedule{}, errors.New("für Erledigungsanker ist ein Tagesintervall ohne Saisonfilter erforderlich")
	}
	switch schedule.Kind {
	case "once", "days":
		if len(schedule.Weekdays) != 0 || schedule.MonthDay != 0 || schedule.Kind == "once" && len(schedule.Months) != 0 {
			return Schedule{}, errors.New("unpassende Kalenderauswahl für die gewählte Wiederholung")
		}
	case "weekly":
		if len(schedule.Weekdays) == 0 || schedule.MonthDay != 0 {
			return Schedule{}, errors.New("mindestens einen Wochentag auswählen")
		}
	case "monthly":
		if schedule.MonthDay == 0 {
			schedule.MonthDay = start.Day()
		}
		if len(schedule.Weekdays) != 0 || schedule.MonthDay < 1 || schedule.MonthDay > 31 {
			return Schedule{}, errors.New("gültigen Monatstag zwischen 1 und 31 auswählen")
		}
	default:
		return Schedule{}, errors.New("nicht unterstützte Wiederholungsart")
	}
	if schedule.EndsOn != "" {
		end, parseErr := ParseDay(schedule.EndsOn)
		if parseErr != nil || end.Before(start) {
			return Schedule{}, errors.New("ungültiges Enddatum; muss am oder nach dem Startdatum liegen")
		}
	}
	return schedule, nil
}

func wallSeconds(value time.Time) int64 {
	return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(),
		value.Second(), 0, time.UTC).Unix()
}

// ResolveLocal handles both folds and gaps explicitly. Go's implicit Date
// choice for an ambiguous or missing wall time is not the persisted policy.
func ResolveLocal(day, clock string, location *time.Location) (time.Time, string, error) {
	date, err := ParseDay(day)
	if err != nil {
		return time.Time{}, "", err
	}
	return resolveLocal(date, clock, location)
}

// DayBoundary additionally accepts the exclusive end of the supported date
// range. It applies the same persisted gap/fold policy as task clock times.
func DayBoundary(day string, location *time.Location) (time.Time, error) {
	date, err := ParseDay(day)
	if day == "2201-01-01" {
		date, err = time.Parse(time.DateOnly, day)
	}
	if err != nil {
		return time.Time{}, err
	}
	instant, _, err := resolveLocal(date, "00:00", location)
	return instant, err
}

func resolveLocal(date time.Time, clock string, location *time.Location) (time.Time, string, error) {
	if location == nil {
		return time.Time{}, "", errors.New("fehlende Zeitzone")
	}
	hour, err := time.Parse("15:04", clock)
	if err != nil || hour.Format("15:04") != clock {
		return time.Time{}, "", errors.New("ungültige lokale Uhrzeit")
	}
	wall := time.Date(date.Year(), date.Month(), date.Day(), hour.Hour(), hour.Minute(), 0, 0, time.UTC)
	offsets := make(map[int]struct{}, 4)
	for delta := -72; delta <= 72; delta += 6 {
		_, offset := wall.Add(time.Duration(delta) * time.Hour).In(location).Zone()
		offsets[offset] = struct{}{}
	}
	matches := make([]time.Time, 0, 2)
	var forward time.Time
	var forwardDistance int64
	for offset := range offsets {
		candidate := time.Unix(wall.Unix()-int64(offset), 0).UTC()
		distance := wallSeconds(candidate.In(location)) - wall.Unix()
		if distance == 0 {
			matches = append(matches, candidate)
		} else if distance > 0 && distance <= 48*60*60 &&
			(forward.IsZero() || distance < forwardDistance || distance == forwardDistance && candidate.Before(forward)) {
			forward, forwardDistance = candidate, distance
		}
	}
	if len(matches) != 0 {
		slices.SortFunc(matches, func(left, right time.Time) int { return left.Compare(right) })
		if len(matches) > 1 {
			return matches[0], "fold_earlier", nil
		}
		return matches[0], "exact", nil
	}
	if !forward.IsZero() {
		return forward, "gap_forward", nil
	}
	return time.Time{}, "", errors.New("lokale Uhrzeit lässt sich mit der gewählten Regel nicht sicher auflösen")
}

func matchesDay(schedule Schedule, start, day time.Time) bool {
	if day.Before(start) || schedule.EndsOn != "" && day.Format(time.DateOnly) > schedule.EndsOn {
		return false
	}
	if len(schedule.Months) > 0 && !slices.Contains(schedule.Months, int(day.Month())) {
		return false
	}
	days := (day.Unix() - start.Unix()) / 86400
	switch schedule.Kind {
	case "once":
		return days == 0
	case "days":
		return days%int64(schedule.Interval) == 0
	case "weekly":
		weekday := (int(day.Weekday())+6)%7 + 1
		startWeekday := (int(start.Weekday()) + 6) % 7
		return (days+int64(startWeekday))/7%int64(schedule.Interval) == 0 && slices.Contains(schedule.Weekdays, weekday)
	case "monthly":
		months := (day.Year()-start.Year())*12 + int(day.Month()) - int(start.Month())
		last := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		return months%schedule.Interval == 0 && day.Day() == min(schedule.MonthDay, last)
	}
	return false
}

// Project examines at most 366 civil days. More is explicit, so a bounded
// preview or worker batch cannot be mistaken for a complete horizon.
func Project(input Schedule, from, through string, limit int) (Projection, error) {
	schedule, err := Normalize(input)
	if err != nil {
		return Projection{}, err
	}
	start, _ := ParseDay(schedule.StartsOn)
	first, err := ParseDay(from)
	if err != nil {
		return Projection{}, err
	}
	last, err := ParseDay(through)
	if err != nil || last.Before(first) || (last.Unix()-first.Unix())/86400 >= MaximumWindow || limit < 1 || limit > 1000 {
		return Projection{}, errors.New("zu große Vorschau; auf höchstens 366 Tage und 1.000 Termine begrenzen")
	}
	location, _ := time.LoadLocation(schedule.Timezone)
	result := Projection{Slots: []Slot{}, Through: through}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		if !matchesDay(schedule, start, day) {
			continue
		}
		if len(result.Slots) == limit {
			result.More = true
			result.Through = result.Slots[len(result.Slots)-1].Day
			break
		}
		date := day.Format(time.DateOnly)
		due, resolution, resolveErr := ResolveLocal(date, schedule.LocalTime, location)
		if resolveErr != nil {
			return Projection{}, resolveErr
		}
		result.Slots = append(result.Slots, Slot{Day: date, LocalTime: schedule.LocalTime,
			Timezone: schedule.Timezone, DueAt: due, Resolution: resolution})
	}
	return result, nil
}

func AfterCompletion(input Schedule, completed time.Time) (Slot, error) {
	schedule, err := Normalize(input)
	if err != nil {
		return Slot{}, err
	}
	if schedule.Anchor != "completion" || completed.IsZero() {
		return Slot{}, errors.New("tatsächliche Erledigung und Erledigungsanker erforderlich")
	}
	location, _ := time.LoadLocation(schedule.Timezone)
	local := completed.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, schedule.Interval)
	if day.Year() > 2200 || schedule.EndsOn != "" && day.Format(time.DateOnly) > schedule.EndsOn {
		return Slot{}, ErrNoOccurrence
	}
	due, resolution, err := ResolveLocal(day.Format(time.DateOnly), schedule.LocalTime, location)
	if err != nil {
		return Slot{}, err
	}
	return Slot{Day: day.Format(time.DateOnly), LocalTime: schedule.LocalTime,
		Timezone: schedule.Timezone, DueAt: due, Resolution: resolution}, nil
}

func Explain(input Schedule) (string, error) {
	schedule, err := Normalize(input)
	if err != nil {
		return "", err
	}
	if schedule.Anchor == "completion" {
		return fmt.Sprintf("%d Tage nach der tatsächlichen Erledigung · %s · %s", schedule.Interval,
			schedule.LocalTime, schedule.Timezone), nil
	}
	label := "Einmaliger Termin"
	switch schedule.Kind {
	case "days":
		label = fmt.Sprintf("Alle %d Kalendertage", schedule.Interval)
	case "weekly":
		names := []string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}
		selected := make([]string, 0, len(schedule.Weekdays))
		for _, weekday := range schedule.Weekdays {
			selected = append(selected, names[weekday-1])
		}
		label = fmt.Sprintf("Alle %d Wochen: %s", schedule.Interval, strings.Join(selected, ", "))
	case "monthly":
		label = fmt.Sprintf("Alle %d Monate am %d.; kurze Monate bis zum Monatsende", schedule.Interval, schedule.MonthDay)
	}
	if len(schedule.Months) > 0 {
		names := []string{"Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"}
		selected := make([]string, 0, len(schedule.Months))
		for _, month := range schedule.Months {
			selected = append(selected, names[month-1])
		}
		label += " · Saison: " + strings.Join(selected, ", ")
	}
	return fmt.Sprintf("%s · %s · %s", label, schedule.LocalTime, schedule.Timezone), nil
}
