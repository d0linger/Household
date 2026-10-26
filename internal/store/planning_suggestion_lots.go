package store

import (
	"math/big"
	"sort"
	"strings"
	"time"

	"household/internal/value"
)

const suggestionLotLimit = 5000

type suggestionLotEvaluation struct {
	Coverage   suggestionCoverage
	Reasons    []suggestionDateReason
	UrgentOn   string
	DateReview bool
}

type suggestionEvaluatedLot struct {
	lot      suggestionDateLot
	free     *big.Rat
	priority string
	reasons  []suggestionDateReason
	excluded bool
}

// Free quantities are already net of current claims, at one observation time.
// Passed use-by stock stays physically present but cannot cover a suggestion.
func evaluateSuggestionLots(lines []planningDemandQuantity, lots []suggestionDateLot, plannedOn string) (suggestionLotEvaluation, error) {
	out := suggestionLotEvaluation{Reasons: []suggestionDateReason{}}
	if !value.Date(plannedOn) || len(lots) > suggestionLotLimit {
		return out, Invalid{"Gültiges Planungsdatum und höchstens5000 relevante freie Chargen erforderlich"}
	}
	groups, _, err := groupPlanningDemand(lines)
	if err != nil {
		return out, err
	}
	byProduct := make(map[string][]suggestionEvaluatedLot, len(groups))
	available := make(map[string]suggestionAvailability, len(groups))
	sums := make(map[string]*big.Rat, len(groups))
	for id, group := range groups {
		sums[id] = new(big.Rat)
		available[id] = suggestionAvailability{Unit: group.Unit, Free: "0"}
	}
	seen := make(map[string]bool, len(lots))
	for _, lot := range lots {
		if groups[lot.ProductID] == nil {
			continue
		}
		key := strings.ToLower(lot.ID)
		if seen[key] {
			return out, Invalid{"Eine Charge darf pro Bewertung nur einmal gezählt werden"}
		}
		seen[key] = true
		reasons, reasonErr := suggestionLotDateReasons(lot, plannedOn)
		if reasonErr != nil {
			return out, reasonErr
		}
		free, parseErr := stockDecimal(lot.Free)
		if parseErr != nil || free.Sign() < 0 {
			return out, Invalid{"Nichtnegativer freier Chargenbestand erforderlich"}
		}
		if free.Sign() == 0 {
			continue
		}
		priority := ""
		for _, reason := range reasons {
			if priority == "" || reason.On < priority {
				priority = reason.On
			}
		}
		excluded := lot.UseBy != "" && lot.UseBy < plannedOn
		byProduct[lot.ProductID] = append(byProduct[lot.ProductID], suggestionEvaluatedLot{
			lot: lot, free: free, priority: priority, reasons: reasons, excluded: excluded,
		})
		if !excluded {
			sums[lot.ProductID].Add(sums[lot.ProductID], free)
		}
	}
	for id, sum := range sums {
		stock := available[id]
		stock.Free = sum.FloatString(6)
		available[id] = stock
	}
	out.Coverage, err = evaluateSuggestionDemand(lines, available)
	if err != nil {
		return out, err
	}
	for _, product := range out.Coverage.Products {
		group := groups[product.ProductID]
		remaining := new(big.Rat).Set(group.Quantity)
		entries := byProduct[product.ProductID]
		sort.Slice(entries, func(i, j int) bool {
			a, b := entries[i], entries[j]
			if a.excluded != b.excluded {
				return !a.excluded
			}
			if a.priority != b.priority {
				if a.priority == "" || b.priority == "" {
					return a.priority != ""
				}
				return a.priority < b.priority
			}
			return a.lot.ID < b.lot.ID
		})
		for _, entry := range entries {
			if remaining.Sign() <= 0 {
				break
			}
			used := new(big.Rat).Set(entry.free)
			if used.Cmp(remaining) > 0 {
				used.Set(remaining)
			}
			usedAmount, amountErr := ingredientAmount(used)
			if amountErr != nil {
				return out, amountErr
			}
			for _, reason := range entry.reasons {
				reason.Free, reason.ExcludedFromCoverage = usedAmount, entry.excluded
				out.Reasons = append(out.Reasons, reason)
				if reason.RequiresReview {
					out.DateReview = true
				}
				if !reason.RequiresReview && !entry.excluded && suggestionDateIsSoon(reason.On, plannedOn) && (out.UrgentOn == "" || reason.On < out.UrgentOn) {
					out.UrgentOn = reason.On
				}
			}
			remaining.Sub(remaining, used)
		}
	}
	if out.DateReview {
		out.UrgentOn = ""
	}
	return out, nil
}

func suggestionDateIsSoon(date, plannedOn string) bool {
	on, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return false
	}
	planned, err := time.Parse(time.DateOnly, plannedOn)
	if err != nil || on.Before(planned) {
		return false
	}
	return on.Sub(planned) <= 7*24*time.Hour
}
