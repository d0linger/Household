package energy

import (
	"math/big"
	"slices"
	"time"
)

type Tariff struct {
	From         time.Time
	Through      time.Time
	PriceMinor   string
	DailyMinor   string
	CurrencyCode string
}

type Cost struct {
	Minor                  *big.Rat
	Complete               bool
	Estimated              bool
	MissingQuantitySeconds int64
	MissingTariffSeconds   int64
}

// Costs rounds once at the presentation boundary. A missing tariff never becomes a free tariff.
func Costs(intervals []Interval, tariffs []Tariff, zone string, from, through time.Time) (Cost, error) {
	out := Cost{Minor: new(big.Rat), Complete: true}
	location, err := time.LoadLocation(zone)
	if err != nil || !validInstant(from) || !validInstant(through) || !from.Before(through) || len(tariffs) > 500 || len(intervals) > MaximumPoints {
		return out, ErrInvalid
	}
	for index, tariff := range tariffs {
		if !tariff.From.Before(tariff.Through) || tariff.CurrencyCode != "EUR" || index > 0 && tariffs[index-1].Through.After(tariff.From) {
			return out, ErrInvalid
		}
		if _, err := Decimal(tariff.PriceMinor); err != nil {
			return out, err
		}
		if _, err := Decimal(tariff.DailyMinor); err != nil {
			return out, err
		}
	}
	var coveredTariff int64
	for _, tariff := range tariffs {
		left, right := tariff.From, tariff.Through
		if left.Before(from) {
			left = from
		}
		if right.After(through) {
			right = through
		}
		if !left.Before(right) {
			continue
		}
		coveredTariff += int64(right.Sub(left) / time.Second)
		base, _ := Decimal(tariff.DailyMinor)
		for at := left; at.Before(right); {
			day, end, err := boundary(at, location, "day")
			if err != nil {
				return out, err
			}
			fullDay := end.Sub(day)
			if end.After(right) {
				end = right
			}
			out.Minor.Add(out.Minor, new(big.Rat).Mul(base, big.NewRat(end.Sub(at).Nanoseconds(), fullDay.Nanoseconds())))
			at = end
		}
	}
	out.MissingTariffSeconds = int64(through.Sub(from)/time.Second) - coveredTariff
	var observed int64
	lastEnd := time.Time{}
	for _, interval := range intervals {
		if !interval.From.Before(interval.Through) || !lastEnd.IsZero() && interval.From.Before(lastEnd) {
			return out, ErrInvalid
		}
		lastEnd = interval.Through
		left, right := interval.From, interval.Through
		if left.Before(from) {
			left = from
		}
		if right.After(through) {
			right = through
		}
		if !left.Before(right) || interval.Quantity == nil {
			continue
		}
		observed += int64(right.Sub(left) / time.Second)
		for _, tariff := range tariffs {
			start, end := left, right
			if start.Before(tariff.From) {
				start = tariff.From
			}
			if end.After(tariff.Through) {
				end = tariff.Through
			}
			if !start.Before(end) {
				continue
			}
			price, _ := Decimal(tariff.PriceMinor)
			part := new(big.Rat).Mul(interval.Quantity, big.NewRat(end.Sub(start).Nanoseconds(), interval.Through.Sub(interval.From).Nanoseconds()))
			out.Minor.Add(out.Minor, new(big.Rat).Mul(part, price))
			out.Estimated = out.Estimated || interval.Quality != "actual" || !start.Equal(interval.From) || !end.Equal(interval.Through)
		}
	}
	out.MissingQuantitySeconds = int64(through.Sub(from)/time.Second) - observed
	out.Complete = out.MissingQuantitySeconds == 0 && out.MissingTariffSeconds == 0
	return out, nil
}

type Anomaly struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func Anomalies(intervals []Interval, dailyLimit string, minimum int) ([]Anomaly, error) {
	out := []Anomaly{}
	if minimum < 7 || minimum > 100 || len(intervals) > MaximumPoints {
		return nil, ErrInvalid
	}
	if dailyLimit == "" {
		return out, nil
	}
	limit, err := Decimal(dailyLimit)
	if err != nil || limit.Sign() <= 0 {
		return nil, ErrInvalid
	}
	usable := []Interval{}
	for _, interval := range intervals {
		if interval.Quantity != nil && interval.Quality == "actual" && interval.Through.After(interval.From) {
			usable = append(usable, interval)
		}
	}
	if len(usable) < minimum || len(intervals) == 0 || intervals[len(intervals)-1].Quality != "actual" ||
		usable[len(usable)-1].Through.Sub(usable[0].From) < 6*24*time.Hour {
		return append(out, Anomaly{Code: "history_insufficient", Reason: "Für diesen Hinweis fehlen genügend tatsächlich gemessene Intervalle."}), nil
	}
	latest := usable[len(usable)-1]
	perDay := new(big.Rat).Mul(latest.Quantity, big.NewRat(int64(24*time.Hour), latest.Through.Sub(latest.From).Nanoseconds()))
	if perDay.Cmp(limit) > 0 {
		out = append(out, Anomaly{Code: "daily_limit", Reason: "Der auf 24 Stunden normierte Verbrauch überschreitet die ausdrücklich eingestellte Grenze. Messwerte und Zeitraum prüfen."})
	}
	return out, nil
}

type Night struct {
	Day             string
	QuantityKWh     string
	CoveredSeconds  int64
	ExpectedSeconds int64
}

func BaseLoad(nights []Night, powerUnit, threshold string, minimum int) ([]Anomaly, error) {
	out := []Anomaly{}
	if threshold == "" {
		return out, nil
	}
	limit, err := Decimal(threshold)
	if err != nil || limit.Sign() <= 0 || minimum < 7 || minimum > 100 || len(nights) > 100 || powerUnit != "kW" && powerUnit != "W" {
		return nil, ErrInvalid
	}
	usable := []*big.Rat{}
	days := map[string]bool{}
	for _, night := range nights {
		if _, err := time.Parse(time.DateOnly, night.Day); err != nil || days[night.Day] || len(night.QuantityKWh) > 40 ||
			night.ExpectedSeconds <= 0 || night.CoveredSeconds > night.ExpectedSeconds || night.CoveredSeconds < 0 {
			return nil, ErrInvalid
		}
		days[night.Day] = true
		if night.CoveredSeconds*5 < night.ExpectedSeconds*4 || night.CoveredSeconds == 0 {
			continue
		}
		quantity, ok := new(big.Rat).SetString(night.QuantityKWh)
		if !ok || quantity.Sign() < 0 {
			return nil, ErrInvalid
		}
		average := new(big.Rat).Mul(quantity, big.NewRat(3600, night.CoveredSeconds))
		if powerUnit == "W" {
			average.Mul(average, big.NewRat(1000, 1))
		}
		usable = append(usable, average)
	}
	if len(usable) < minimum {
		return append(out, Anomaly{Code: "base_history_insufficient", Reason: "Für die Grundlast fehlen genügend Nächte mit mindestens80%gemessener Zeitabdeckung."}), nil
	}
	slices.SortFunc(usable, func(a, b *big.Rat) int { return a.Cmp(b) })
	if usable[(len(usable)-1)/4].Cmp(limit) > 0 {
		out = append(out, Anomaly{Code: "base_load", Reason: "Die geschätzte nächtliche Grundlast überschreitet die eingestellte Grenze in ausreichend abgedeckten Nächten. Geräte und Messreihe prüfen."})
	}
	return out, nil
}
