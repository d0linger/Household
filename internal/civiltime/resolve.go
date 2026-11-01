// Package civiltime resolves household calendar boundaries with explicit fold/gap policy.
package civiltime

import (
	"errors"
	"slices"
	"time"
	_ "time/tzdata"
)

func wallSeconds(value time.Time) int64 {
	return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(),
		value.Second(), 0, time.UTC).Unix()
}

// Resolve chooses the earlier fold occurrence and moves gaps forward by their wall-clock gap.
func Resolve(date time.Time, clock string, location *time.Location) (time.Time, string, error) {
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
