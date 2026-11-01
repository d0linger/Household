// Package energy calculates exact quantities and exposes missing evidence explicitly.
package energy

import (
	"errors"
	"math/big"
	"regexp"
	"slices"
	"time"

	"household/internal/civiltime"
)

const MaximumPoints = 10000
const MaximumBuckets = 2500

var ErrInvalid = errors.New("invalid energy measurement or period")
var decimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?$`)

func Decimal(text string) (*big.Rat, error) {
	if !decimalPattern.MatchString(text) {
		return nil, ErrInvalid
	}
	result, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, ErrInvalid
	}
	return result, nil
}

func ValidType(kind, unit string) bool {
	switch kind {
	case "cumulative", "interval":
		return slices.Contains([]string{"kWh", "m3", "l"}, unit)
	case "power":
		return unit == "kW" || unit == "W"
	default:
		return false
	}
}

type Point struct {
	ID                string
	DeviceID          string
	At                time.Time
	StartedAt         *time.Time
	Value             string
	Kind              string
	RolloverConfirmed bool
}

type Interval struct {
	From     time.Time
	Through  time.Time
	DeviceID string
	Quantity *big.Rat
	Unit     string
	Quality  string
	Reason   string
}

func validInstant(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1900 && at.Year() <= 2200
}

// Between never subtracts unrelated device counters or interprets kW as kWh.
func Between(kind, unit string, previous, next Point, rollover string, maxGap time.Duration) (Interval, error) {
	out := Interval{From: previous.At, Through: next.At, DeviceID: next.DeviceID, Unit: unit, Quality: "actual"}
	if !ValidType(kind, unit) || !validInstant(next.At) || maxGap <= 0 {
		return out, ErrInvalid
	}
	current, err := Decimal(next.Value)
	if err != nil || next.Kind != "actual" && next.Kind != "estimated" {
		return out, ErrInvalid
	}
	if kind == "interval" {
		if next.StartedAt == nil || !validInstant(*next.StartedAt) || !next.StartedAt.Before(next.At) || next.RolloverConfirmed {
			return out, ErrInvalid
		}
		out.From, out.Quantity = *next.StartedAt, current
		if next.Kind == "estimated" {
			out.Quality = "estimated"
		}
		return out, nil
	}
	if !validInstant(previous.At) || !previous.At.Before(next.At) || previous.Kind != "actual" && previous.Kind != "estimated" {
		return out, ErrInvalid
	}
	if previous.DeviceID != next.DeviceID {
		out.Quality, out.Reason = "gap", "device_boundary"
		return out, nil
	}
	before, err := Decimal(previous.Value)
	if err != nil {
		return out, err
	}
	if kind == "power" {
		out.Unit = "kWh"
		if next.RolloverConfirmed || next.At.Sub(previous.At) > maxGap {
			out.Quality, out.Reason = "gap", "power_sampling_gap"
			return out, nil
		}
		mean := new(big.Rat).Quo(new(big.Rat).Add(before, current), big.NewRat(2, 1))
		hours := big.NewRat(next.At.Sub(previous.At).Nanoseconds(), int64(time.Hour))
		out.Quantity = new(big.Rat).Mul(mean, hours)
		if unit == "W" {
			out.Quantity.Quo(out.Quantity, big.NewRat(1000, 1))
		}
		out.Quality, out.Reason = "estimated", "trapezoidal_power_integration"
		return out, nil
	}
	out.Quantity = new(big.Rat).Sub(current, before)
	if rollover != "" {
		maximum, err := Decimal(rollover)
		if err != nil || maximum.Sign() <= 0 || before.Cmp(maximum) >= 0 || current.Cmp(maximum) >= 0 {
			return out, ErrInvalid
		}
		if next.RolloverConfirmed {
			out.Quantity.Add(out.Quantity, maximum)
		}
	} else if next.RolloverConfirmed {
		return out, ErrInvalid
	}
	if out.Quantity.Sign() < 0 {
		out.Quantity, out.Quality, out.Reason = nil, "reset_unresolved", "unconfirmed_counter_reset"
		return out, nil
	}
	if previous.Kind == "estimated" || next.Kind == "estimated" || next.At.Sub(previous.At) > maxGap {
		out.Quality, out.Reason = "estimated", "estimated_endpoint_or_sparse_history"
	}
	return out, nil
}

type Bucket struct {
	From             time.Time
	Through          time.Time
	Quantity         *big.Rat
	CoveredSeconds   int64
	EstimatedSeconds int64
}

func BucketSpan(at time.Time, zone, kind string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(zone)
	if err != nil || !validInstant(at) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	return boundary(at, location, kind)
}

func boundary(at time.Time, location *time.Location, kind string) (time.Time, time.Time, error) {
	local := at.In(location)
	var start, end time.Time
	switch kind {
	case "hour":
		// Bind the wall-clock hour to this actual offset, then clip at a zone
		// transition. Fold occurrences remain distinct, including half-hour folds.
		_, offset := local.Zone()
		wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.UTC)
		start = wall.Add(-time.Duration(offset) * time.Second)
		end = start.Add(time.Hour)
		zoneStart, zoneEnd := local.ZoneBounds()
		if !zoneStart.IsZero() && start.Before(zoneStart) {
			start = zoneStart
		}
		if !zoneEnd.IsZero() && end.After(zoneEnd) {
			end = zoneEnd
		}
	case "day":
		date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		var err error
		start, _, err = civiltime.Resolve(date, "00:00", location)
		if err != nil {
			return start, end, err
		}
		end, _, err = civiltime.Resolve(date.AddDate(0, 0, 1), "00:00", location)
		if err != nil {
			return start, end, err
		}
	case "month":
		date := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		var err error
		start, _, err = civiltime.Resolve(date, "00:00", location)
		if err != nil {
			return start, end, err
		}
		end, _, err = civiltime.Resolve(date.AddDate(0, 1, 0), "00:00", location)
		if err != nil {
			return start, end, err
		}
	default:
		return start, end, ErrInvalid
	}
	if !start.Before(end) || at.Before(start) || !at.Before(end) {
		return start, end, ErrInvalid
	}
	return start, end, nil
}

// Buckets apportions only the observed interval. Split quantities are estimates.
func Buckets(intervals []Interval, zone, kind string, from, through time.Time) ([]Bucket, error) {
	location, err := time.LoadLocation(zone)
	if err != nil || !validInstant(from) || !validInstant(through) || !from.Before(through) || len(intervals) > MaximumPoints {
		return nil, ErrInvalid
	}
	result := []Bucket{}
	indices := map[int64]int{}
	for at := from; at.Before(through); {
		start, end, err := boundary(at, location, kind)
		if err != nil || len(result) >= MaximumBuckets {
			return nil, ErrInvalid
		}
		indices[start.UnixNano()] = len(result)
		result = append(result, Bucket{From: start, Through: end, Quantity: new(big.Rat)})
		at = end
	}
	lastEnd := time.Time{}
	for _, interval := range intervals {
		if !interval.From.Before(interval.Through) || !lastEnd.IsZero() && interval.From.Before(lastEnd) {
			return nil, ErrInvalid
		}
		lastEnd = interval.Through
		left, right := interval.From, interval.Through
		if left.Before(from) {
			left = from
		}
		if right.After(through) {
			right = through
		}
		for at := left; at.Before(right); {
			start, end, err := boundary(at, location, kind)
			if err != nil {
				return nil, err
			}
			if end.After(right) {
				end = right
			}
			index, ok := indices[start.UnixNano()]
			if !ok {
				return nil, ErrInvalid
			}
			bucket := &result[index]
			if interval.Quantity != nil {
				part := new(big.Rat).Mul(interval.Quantity, big.NewRat(end.Sub(at).Nanoseconds(), interval.Through.Sub(interval.From).Nanoseconds()))
				bucket.Quantity.Add(bucket.Quantity, part)
				seconds := int64(end.Sub(at) / time.Second)
				bucket.CoveredSeconds += seconds
				if interval.Quality != "actual" || !at.Equal(interval.From) || !end.Equal(interval.Through) {
					bucket.EstimatedSeconds += seconds
				}
			}
			at = end
		}
	}
	for index := range result {
		if result[index].CoveredSeconds == 0 {
			result[index].Quantity = nil
		}
	}
	return result, nil
}
