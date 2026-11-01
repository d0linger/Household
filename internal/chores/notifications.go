package chores

import (
	"errors"
	"fmt"
	"time"
)

func QuietAt(minute int, start, end *int) bool {
	if start == nil || end == nil {
		return false
	}
	if *start < *end {
		return minute >= *start && minute < *end
	}
	return minute >= *start || minute < *end
}

// DigestInstant moves an explicitly chosen civil delivery time to the end of
// its quiet interval. A cross-midnight interval belongs to the original day.
func DigestInstant(day string, minute int, start, end *int, location *time.Location) (time.Time, error) {
	if minute < 0 || minute > 1439 || (start == nil) != (end == nil) || start != nil && (*start < 0 || *start > 1439 || *end < 0 || *end > 1439 || *start == *end) {
		return time.Time{}, errors.New("ungültige Zusammenfassungszeit")
	}
	if QuietAt(minute, start, end) {
		if *start > *end && minute >= *start {
			date, err := ParseDay(day)
			if err != nil {
				return time.Time{}, err
			}
			day = date.AddDate(0, 0, 1).Format(time.DateOnly)
		}
		minute = *end
	}
	instant, _, err := ResolveLocal(day, fmt.Sprintf("%02d:%02d", minute/60, minute%60), location)
	return instant, err
}
