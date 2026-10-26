// Package mealportions normalizes an explicit participant-based portion review.
package mealportions

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"household/internal/value"
)

const (
	MaxMembers = 100
	MaxGuests  = 100
)

type Input struct {
	Mode     string
	Members  []string
	Guests   int
	Servings string
}

type Review struct {
	Mode            string
	Members         []string
	Guests          int
	DerivedServings string
	Servings        string
}

// Normalize validates quantities and identity syntax. The caller must separately
// authorize each membership against the current household and saved roster.
func Normalize(in Input) (Review, error) {
	var out Review
	if in.Mode == "" {
		in.Mode = "manual"
	}
	if in.Mode != "manual" && in.Mode != "participants" {
		return out, fmt.Errorf("unknown portion mode")
	}
	if len(in.Members) > MaxMembers || in.Guests < 0 || in.Guests > MaxGuests {
		return out, fmt.Errorf("participant scope exceeds its limit")
	}
	out = Review{Mode: in.Mode, Members: make([]string, 0, len(in.Members)), Guests: in.Guests}
	seen := make(map[string]bool, len(in.Members))
	for _, raw := range in.Members {
		if !value.ValidUUID(raw) {
			return Review{}, fmt.Errorf("invalid membership identity")
		}
		id := strings.ToLower(raw)
		if seen[id] {
			return Review{}, fmt.Errorf("duplicate membership identity")
		}
		seen[id] = true
		out.Members = append(out.Members, id)
	}
	slices.Sort(out.Members)
	out.DerivedServings = strconv.Itoa(len(out.Members) + out.Guests)
	if out.Mode == "participants" {
		if out.DerivedServings == "0" {
			return Review{}, fmt.Errorf("derived portions require at least one participant")
		}
		out.Servings = out.DerivedServings + ".000000"
		if in.Servings != "" {
			expected, err := value.Decimal(in.Servings)
			actual, _ := value.Decimal(out.Servings)
			if err != nil || expected.Cmp(actual) != 0 {
				return Review{}, fmt.Errorf("reviewed portions do not match participants")
			}
		}
		return out, nil
	}
	amount, err := value.Decimal(in.Servings)
	if err != nil || amount.Sign() <= 0 {
		return Review{}, fmt.Errorf("manual portions must be a positive exact decimal")
	}
	out.Servings = amount.FloatString(6)
	return out, nil
}

// ComponentServings leaves independent component quantities explicit and derives
// linked quantities from the already reviewed meal, without scaling a recipe.
func ComponentServings(mode, manual, meal string) (string, error) {
	if mode == "" {
		mode = "independent"
	}
	if mode != "independent" && mode != "meal" {
		return "", fmt.Errorf("unknown component portion mode")
	}
	if mode == "meal" {
		manual = meal
	}
	amount, err := value.Decimal(manual)
	if err != nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("component portions must be a positive exact decimal")
	}
	return amount.FloatString(6), nil
}
