package store

import (
	"sort"
	"strings"
	"time"

	"household/internal/value"
)

const suggestionPageLimit = 20

func suggestionPlanningDate(readAt time.Time, timezone, requested string) (string, error) {
	zone, err := time.LoadLocation(timezone)
	if err != nil || readAt.IsZero() {
		return "", Invalid{"Gültigen aktuellen Lesezeitpunkt und Haushaltszeitzone prüfen"}
	}
	today := readAt.In(zone).Format(time.DateOnly)
	if requested == "" {
		return today, nil
	}
	if !value.Date(requested) || requested < today {
		return "", Invalid{"Aktueller Vorrat kann nur für heute oder ein zukünftiges Planungsdatum bewertet werden"}
	}
	return requested, nil
}

type suggestionAvailability struct {
	Unit string
	Free string
}

type suggestionProductCoverage struct {
	ProductID string            `json:"product_id"`
	Name      string            `json:"name"`
	Unit      string            `json:"unit"`
	Complete  bool              `json:"complete"`
	Needed    IngredientAmount  `json:"needed"`
	Free      IngredientAmount  `json:"free"`
	Shortage  *IngredientAmount `json:"shortage,omitempty"`
}

type suggestionCoverage struct {
	Outcome        string
	KnownShortages int
	Products       []suggestionProductCoverage
}

// Every candidate receives the same immutable free pool. Grouping repeats within
// one candidate never claims that independently covered alternatives fit together.
func evaluateSuggestionDemand(lines []planningDemandQuantity, available map[string]suggestionAvailability) (suggestionCoverage, error) {
	out := suggestionCoverage{Outcome: "sufficient", Products: []suggestionProductCoverage{}}
	groups, complete, err := groupPlanningDemand(lines)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		group := groups[id]
		stock, found := available[id]
		if !found || stock.Unit != group.Unit {
			return out, Invalid{"Vollständige aktuelle freie Bestände in derselben Produktbasiseinheit erforderlich"}
		}
		free, parseErr := stockDecimal(stock.Free)
		if parseErr != nil || free.Sign() < 0 {
			return out, Invalid{"Nichtnegativer freier Bestand erforderlich"}
		}
		needed, amountErr := ingredientAmount(group.Quantity)
		if amountErr != nil {
			return out, amountErr
		}
		freeAmount, amountErr := ingredientAmount(free)
		if amountErr != nil {
			return out, amountErr
		}
		shortage, shortageErr := planningDemandShortage(group, stock.Free)
		if shortageErr != nil {
			return out, shortageErr
		}
		if shortage != nil && shortage.Rational != "0" {
			out.KnownShortages++
		}
		out.Products = append(out.Products, suggestionProductCoverage{
			ProductID: id, Unit: group.Unit, Complete: group.Complete, Needed: needed, Free: freeAmount, Shortage: shortage,
		})
	}
	switch {
	case len(lines) == 0:
		out.Outcome = "empty"
	case !complete:
		out.Outcome = "incomplete"
	case out.KnownShortages > 0:
		out.Outcome = "shortage"
	}
	return out, nil
}

type suggestionDateLot struct {
	ID, ProductID               string
	Free                        string
	BestBefore, UseBy, OpenedOn string
	AfterOpenDays               *int64
}

type suggestionDateReason struct {
	LotID                string           `json:"lot_id"`
	ProductID            string           `json:"product_id"`
	Kind                 string           `json:"kind"`
	On                   string           `json:"on"`
	Past                 bool             `json:"past"`
	RequiresReview       bool             `json:"requires_review"`
	ExcludedFromCoverage bool             `json:"excluded_from_coverage"`
	Free                 IngredientAmount `json:"free"`
}

// Coverage and ranking use every bounded lot. The response shows at most fifty
// date reasons per alternative, with review evidence first and an explicit total.
func suggestionDateEvidence(reasons []suggestionDateReason) []suggestionDateReason {
	out := append([]suggestionDateReason{}, reasons...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.RequiresReview != b.RequiresReview {
			return a.RequiresReview
		}
		if a.ExcludedFromCoverage != b.ExcludedFromCoverage {
			return a.ExcludedFromCoverage
		}
		if a.On != b.On {
			return a.On < b.On
		}
		if a.ProductID != b.ProductID {
			return a.ProductID < b.ProductID
		}
		if a.LotID != b.LotID {
			return a.LotID < b.LotID
		}
		return a.Kind < b.Kind
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// The reader must first restrict lots to products and positive quantities that
// this candidate needs. Reasons remain separate; none is a freshness guarantee.
func suggestionLotDateReasons(lot suggestionDateLot, plannedOn string) ([]suggestionDateReason, error) {
	out := []suggestionDateReason{}
	if !value.Date(plannedOn) || !value.ValidUUID(lot.ID) || !value.ValidUUID(lot.ProductID) {
		return nil, Invalid{"Gültige Chargenidentität und Planungsdatum erforderlich"}
	}
	free, err := stockDecimal(lot.Free)
	if err != nil || free.Sign() < 0 {
		return nil, Invalid{"Nichtnegativer freier Chargenbestand erforderlich"}
	}
	if free.Sign() == 0 {
		return out, nil
	}
	for _, date := range []string{lot.BestBefore, lot.UseBy, lot.OpenedOn} {
		if date != "" && !value.Date(date) {
			return nil, Invalid{"Gültige Kalenderdaten für Datumsgründe erforderlich"}
		}
	}
	if lot.AfterOpenDays != nil && (*lot.AfterOpenDays < 0 || *lot.AfterOpenDays > 3660) {
		return nil, Invalid{"Öffnungshinweis von0 bis3660 Tagen erforderlich"}
	}
	opening, err := pantryOpeningHint(lot.OpenedOn, lot.AfterOpenDays)
	if err != nil {
		return nil, err
	}
	amount, err := ingredientAmount(free)
	if err != nil {
		return nil, err
	}
	for _, reason := range []struct{ kind, on string }{
		{"use_by", lot.UseBy}, {"opening_hint", opening}, {"best_before", lot.BestBefore},
	} {
		if reason.on == "" {
			continue
		}
		past := reason.on < plannedOn
		out = append(out, suggestionDateReason{LotID: lot.ID, ProductID: lot.ProductID, Kind: reason.kind,
			On: reason.on, Past: past, RequiresReview: past, Free: amount})
	}
	return out, nil
}

type suggestionRank struct {
	RecipeID, SourceVersionID, Title string
	Outcome, UrgentOn                string
	DateReview                       bool
}

func rankSuggestionPage(input []suggestionRank) ([]suggestionRank, error) {
	if len(input) > suggestionPageLimit {
		return nil, Invalid{"Höchstens20 Rezeptalternativen pro bewerteter Seite"}
	}
	class := map[string]int{"sufficient": 0, "shortage": 1, "incomplete": 2, "empty": 3}
	seen := make(map[string]bool, len(input))
	result := append([]suggestionRank{}, input...)
	for _, item := range result {
		key := strings.ToLower(item.RecipeID)
		_, known := class[item.Outcome]
		if !known || !value.ValidUUID(item.RecipeID) || !value.ValidUUID(item.SourceVersionID) || seen[key] || item.UrgentOn != "" && !value.Date(item.UrgentOn) {
			return nil, Invalid{"Eindeutige aktuelle Rezeptquellen und erklärbare Bewertungszustände erforderlich"}
		}
		seen[key] = true
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if class[a.Outcome] != class[b.Outcome] {
			return class[a.Outcome] < class[b.Outcome]
		}
		if a.DateReview != b.DateReview {
			return !a.DateReview
		}
		// Past dates requiring review cannot provide an urgency boost.
		if !a.DateReview && a.UrgentOn != b.UrgentOn {
			if a.UrgentOn == "" || b.UrgentOn == "" {
				return a.UrgentOn != ""
			}
			return a.UrgentOn < b.UrgentOn
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		if a.RecipeID != b.RecipeID {
			return a.RecipeID < b.RecipeID
		}
		return a.SourceVersionID < b.SourceVersionID
	})
	return result, nil
}
