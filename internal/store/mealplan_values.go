package store

import (
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/value"
)

const (
	planningMealLimit      = 42
	planningComponentLimit = 10
	planningComponentBytes = 1 << 20
	planningMealBytes      = 2 << 20
	planningCopyBytes      = 16 << 20
)

type PlanningComponentInput struct {
	ID               string `json:"id"`
	Version          int64  `json:"version"`
	SourceVersionID  string `json:"source_version_id"`
	SourceGeneration int64  `json:"source_generation"`
	Servings         string `json:"servings"`
	PrepMinutes      *int64 `json:"prep_minutes"`
	CookMinutes      *int64 `json:"cook_minutes"`
}

type PlanningMealInput struct {
	Title     string `json:"title"`
	PlannedOn string `json:"planned_on"`
	Slot      string `json:"slot"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Servings  string `json:"servings"`
	Notes     string `json:"notes"`
}

type PlanningInput struct {
	Generation   int64                    `json:"generation"`
	PlanVersion  int64                    `json:"plan_version"`
	Action       string                   `json:"action"`
	MealID       string                   `json:"meal_id"`
	Version      int64                    `json:"version"`
	OtherID      string                   `json:"other_id"`
	OtherVersion int64                    `json:"other_version"`
	TargetDate   string                   `json:"target_date"`
	TargetSlot   string                   `json:"target_slot"`
	Week         string                   `json:"week"`
	Name         string                   `json:"name"`
	IsTemplate   bool                     `json:"is_template"`
	Meal         PlanningMealInput        `json:"meal"`
	Components   []PlanningComponentInput `json:"components"`
}

type PlanningEffort struct {
	PrepMinutes int64 `json:"prep_minutes"`
	CookMinutes int64 `json:"cook_minutes"`
	UnknownPrep int   `json:"unknown_prep"`
	UnknownCook int   `json:"unknown_cook"`
}

func planningWindow(start string) ([]string, error) {
	if !value.Date(start) {
		return nil, Invalid{"Wochenbeginn: gültiges Kalenderdatum erforderlich"}
	}
	date, _ := time.Parse(time.DateOnly, start)
	days := make([]string, 7)
	for offset := range days {
		days[offset] = date.AddDate(0, 0, offset).Format(time.DateOnly)
		if !value.Date(days[offset]) {
			return nil, Invalid{"Wochenende liegt außerhalb des unterstützten Datumsbereichs"}
		}
	}
	return days, nil
}

func planningCopyDate(date, from, to string) (string, error) {
	days, err := planningWindow(from)
	if err != nil {
		return "", err
	}
	target, err := planningWindow(to)
	if err != nil {
		return "", err
	}
	for i, day := range days {
		if day == date {
			return target[i], nil
		}
	}
	return "", Invalid{"Mahlzeit gehört nicht zur geprüften Quellwoche"}
}

func planningDecimal(raw string) (string, error) {
	amount, err := value.Decimal(raw)
	if err != nil || amount.Sign() <= 0 {
		return "", Invalid{"Portionen: positive Zahl mit höchstens sechs Nachkommastellen erforderlich"}
	}
	return amount.FloatString(6), nil
}

func planningSlot(slot string) bool {
	return slot == "breakfast" || slot == "lunch" || slot == "dinner" || slot == "snack"
}
func planningKind(kind string) bool {
	return kind == "recipe" || kind == "free" || kind == "leftovers" || kind == "out" || kind == "order"
}
func planningStatus(status string) bool {
	return status == "open" || status == "planned" || status == "purchased" || status == "cooked" || status == "cancelled"
}

func normalizePlanningMeal(in *PlanningMealInput) error {
	in.Title, in.Notes = strings.TrimSpace(in.Title), strings.TrimSpace(in.Notes)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || !utf8.ValidString(in.Title) || strings.ContainsRune(in.Title, '\x00') {
		return Invalid{"Gericht: 1 bis 200 Zeichen erforderlich"}
	}
	if len(in.Notes) > 20000 || !utf8.ValidString(in.Notes) || strings.ContainsRune(in.Notes, '\x00') {
		return Invalid{"Notizen: höchstens 20000 UTF-8-Bytes ohne Nullzeichen"}
	}
	if !value.Date(in.PlannedOn) || !planningSlot(in.Slot) || !planningKind(in.Kind) || !planningStatus(in.Status) {
		return Invalid{"Datum, Mahlzeitentyp und Planungsstatus prüfen"}
	}
	var err error
	in.Servings, err = planningDecimal(in.Servings)
	return err
}

func planningComponentValues(in *PlanningComponentInput) error {
	var err error
	in.Servings, err = planningDecimal(in.Servings)
	if err != nil {
		return err
	}
	for _, minutes := range []*int64{in.PrepMinutes, in.CookMinutes} {
		if minutes != nil && (*minutes < 0 || *minutes > 525600) {
			return Invalid{"Zeitaufwand: unbekannt oder 0 bis 525600 Minuten"}
		}
	}
	return nil
}

func (e *PlanningEffort) add(prep, cook *int64) {
	if prep == nil {
		e.UnknownPrep++
	} else {
		e.PrepMinutes += *prep
	}
	if cook == nil {
		e.UnknownCook++
	} else {
		e.CookMinutes += *cook
	}
}

// Planned shopping quantities must fit the destination's exact decimal contract.
// Non-terminating fractions are sent back for review instead of rounded silently.
func planningQuantity(raw any, base, servings string) (any, error) {
	if raw == nil {
		return nil, nil
	}
	amount, err := value.Decimal(String(raw))
	if err != nil {
		return nil, Invalid{"Quellmenge im Rezept prüfen"}
	}
	denominator, err := value.Decimal(base)
	if err != nil || denominator.Sign() <= 0 {
		return nil, Invalid{"Basisportionen im Rezept prüfen"}
	}
	numerator, err := value.Decimal(servings)
	if err != nil || numerator.Sign() <= 0 {
		return nil, Invalid{"Komponentenportionen prüfen"}
	}
	scaled := new(big.Rat).Mul(amount, new(big.Rat).Quo(numerator, denominator))
	formatted := scaled.FloatString(6)
	bounded, err := value.Decimal(formatted)
	if err != nil || bounded.Sign() <= 0 || bounded.Cmp(scaled) != 0 {
		return nil, Invalid{"Zutatenmenge ist nicht exakt in sechs Nachkommastellen darstellbar. Komponentenportionen oder Quelle vor der Einkaufsübernahme anpassen."}
	}
	return formatted, nil
}
