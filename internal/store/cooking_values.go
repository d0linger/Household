package store

import (
	"math/big"
	"strings"
	"time"

	"household/internal/value"
)

type CookingQuantity struct {
	Display     string `json:"display"`
	Approximate bool   `json:"approximate"`
	Exact       string `json:"exact"`
}

func cookingServings(raw string) (*big.Rat, error) {
	n, err := value.Decimal(raw)
	if err != nil || n.Sign() <= 0 {
		return nil, Invalid{"Portionen: positive Menge mit höchstens sechs Nachkommastellen erforderlich"}
	}
	return n, nil
}

// Results may exceed the input decimal range. Keep their rational representation
// and label display rounding instead of re-parsing them as bounded input values.
func cookingQuantity(raw any, base, servings *big.Rat) (*CookingQuantity, error) {
	if raw == nil {
		return nil, nil
	}
	amount, err := cookingServings(String(raw))
	if err != nil {
		return nil, err
	}
	amount.Mul(amount, servings)
	amount.Quo(amount, base)
	rounded := amount.FloatString(6)
	display, _ := new(big.Rat).SetString(rounded)
	approximate := display.Cmp(amount) != 0
	if display.Sign() == 0 {
		rounded = "<0.000001"
	} else {
		rounded = strings.TrimRight(strings.TrimRight(rounded, "0"), ".")
	}
	return &CookingQuantity{Display: rounded, Approximate: approximate, Exact: amount.RatString()}, nil
}

type CookingTimer struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	DurationSeconds int64      `json:"duration_seconds"`
	State           string     `json:"state"`
	Deadline        *time.Time `json:"deadline"`
	RemainingMillis *int64     `json:"remaining_millis"`
	Version         int64      `json:"version"`
}

func cookingTimerRemaining(timer CookingTimer, now time.Time) int64 {
	if timer.State == "running" && timer.Deadline != nil {
		return max(0, timer.Deadline.Sub(now).Milliseconds())
	}
	if timer.State == "paused" && timer.RemainingMillis != nil {
		return max(0, *timer.RemainingMillis)
	}
	return 0
}

func cookingTimerTransition(timer CookingTimer, action string, seconds int64, now time.Time) (CookingTimer, error) {
	switch action {
	case "restart":
		if seconds < 1 || seconds > 86400 {
			return timer, Invalid{"Timer: Dauer zwischen einer Sekunde und 24 Stunden wählen"}
		}
		deadline := now.Add(time.Duration(seconds) * time.Second)
		timer.DurationSeconds, timer.State = seconds, "running"
		timer.Deadline, timer.RemainingMillis = &deadline, nil
	case "pause":
		if timer.State != "running" {
			return timer, ErrConflict
		}
		remaining := cookingTimerRemaining(timer, now)
		if remaining == 0 {
			return timer, Invalid{"Dieser Timer ist bereits abgelaufen. Bestätigen oder ausdrücklich neu starten."}
		}
		timer.State, timer.Deadline, timer.RemainingMillis = "paused", nil, &remaining
	case "resume":
		if timer.State != "paused" || cookingTimerRemaining(timer, now) == 0 {
			return timer, ErrConflict
		}
		deadline := now.Add(time.Duration(*timer.RemainingMillis) * time.Millisecond)
		timer.State, timer.Deadline, timer.RemainingMillis = "running", &deadline, nil
	case "acknowledge":
		if timer.State != "running" || cookingTimerRemaining(timer, now) > 0 {
			return timer, ErrConflict
		}
		timer.State, timer.Deadline, timer.RemainingMillis = "done", nil, nil
	default:
		return timer, Invalid{"Unbekannte Timeraktion"}
	}
	timer.Version++
	return timer, nil
}
