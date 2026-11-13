package notification

import (
	"errors"
	"time"

	"household/internal/civiltime"
)

// CalendarStage resolves a civil-date reminder without subtracting fixed hours
// across daylight-saving changes. The source retains its own freshness policy.
func CalendarStage(day, zone, clock string, daysBefore int) (time.Time, error) {
	date, err := time.Parse(time.DateOnly, day)
	if err != nil || date.Format(time.DateOnly) != day || date.Year() < 2000 || date.Year() > 9999 || daysBefore < 0 || daysBefore > 365 {
		return time.Time{}, errors.New("invalid notification calendar stage")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	event, _, err := civiltime.Resolve(date.AddDate(0, 0, -daysBefore), clock, location)
	return event, err
}
