package chores

import (
	"cmp"
	"errors"
	"slices"
	"time"
)

const MaximumPlanItems = 3000

type PlanItem struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Area             string    `json:"area"`
	DueAt            time.Time `json:"due_at"`
	EstimatedMinutes *int      `json:"estimated_minutes"`
	Difficulty       *int      `json:"difficulty"`
	Priority         int       `json:"priority"`
	Overdue          bool      `json:"overdue"`
	Eligible         bool      `json:"eligible"`
	BlockedReason    string    `json:"blocked_reason,omitempty"`
}

type PlanChoice struct {
	Item       PlanItem `json:"item"`
	Reason     string   `json:"reason"`
	ReasonCode string   `json:"reason_code"`
}

type DayPlan struct {
	BudgetMinutes    int          `json:"budget_minutes"`
	SelectedMinutes  int          `json:"selected_minutes"`
	RemainingMinutes int          `json:"remaining_minutes"`
	Selected         []PlanChoice `json:"selected"`
	Unestimated      []PlanChoice `json:"unestimated"`
	Excluded         []PlanChoice `json:"excluded"`
	Method           string       `json:"method"`
}

// PlanDay is an explained priority-first proposal. It does not claim an
// optimal knapsack solution and never treats missing durations as zero.
func PlanDay(input []PlanItem, budget int, maximumDifficulty *int) (DayPlan, error) {
	if budget < 0 || budget > 1440 || len(input) > MaximumPlanItems ||
		maximumDifficulty != nil && (*maximumDifficulty < 1 || *maximumDifficulty > 5) {
		return DayPlan{}, errors.New("ungültiges Tagesbudget, Eignung oder Umfang des Vorschlags")
	}
	items := append([]PlanItem{}, input...)
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.ID == "" || seen[item.ID] || item.Priority < 0 || item.Priority > 3 ||
			item.EstimatedMinutes != nil && (*item.EstimatedMinutes < 1 || *item.EstimatedMinutes > 1440) ||
			item.Difficulty != nil && (*item.Difficulty < 1 || *item.Difficulty > 5) {
			return DayPlan{}, errors.New("ungültige oder doppelte Aufgabe im Tagesvorschlag")
		}
		seen[item.ID] = true
	}
	slices.SortStableFunc(items, func(left, right PlanItem) int {
		if order := cmp.Compare(right.Priority, left.Priority); order != 0 {
			return order
		}
		if left.Overdue != right.Overdue {
			if left.Overdue {
				return -1
			}
			return 1
		}
		if order := left.DueAt.Compare(right.DueAt); order != 0 {
			return order
		}
		return cmp.Compare(left.ID, right.ID)
	})
	result := DayPlan{BudgetMinutes: budget, RemainingMinutes: budget, Selected: []PlanChoice{},
		Unestimated: []PlanChoice{}, Excluded: []PlanChoice{}, Method: "priority_due_budget_v1"}
	for _, item := range items {
		choice := PlanChoice{Item: item}
		switch {
		case item.BlockedReason != "":
			choice.ReasonCode, choice.Reason = "blocked", item.BlockedReason
		case !item.Eligible:
			choice.ReasonCode, choice.Reason = "not_eligible", "Zuständigkeit oder Eignung passt nicht"
		case maximumDifficulty != nil && item.Difficulty != nil && *item.Difficulty > *maximumDifficulty:
			choice.ReasonCode, choice.Reason = "difficulty", "Schwierigkeit liegt über der gewählten Grenze"
		case maximumDifficulty != nil && item.Difficulty == nil:
			choice.ReasonCode, choice.Reason = "unknown_difficulty", "Schwierigkeit ist noch nicht eingeschätzt"
		case item.EstimatedMinutes == nil:
			choice.ReasonCode, choice.Reason = "unknown_duration", "Dauer fehlt und wird nicht als null Minuten eingeplant"
			result.Unestimated = append(result.Unestimated, choice)
			continue
		case *item.EstimatedMinutes > result.RemainingMinutes:
			choice.ReasonCode, choice.Reason = "budget", "Passt nicht in das verbleibende Zeitbudget"
		default:
			choice.ReasonCode, choice.Reason = "selected", "Nach Priorität und Fälligkeit ausgewählt; passt ins Zeitbudget"
			result.Selected = append(result.Selected, choice)
			result.SelectedMinutes += *item.EstimatedMinutes
			result.RemainingMinutes -= *item.EstimatedMinutes
			continue
		}
		result.Excluded = append(result.Excluded, choice)
	}
	return result, nil
}

type Candidate struct {
	MembershipID string `json:"membership_id"`
	Active       bool   `json:"active"`
	Eligible     bool   `json:"eligible"`
	Absent       bool   `json:"absent"`
	SubstituteID string `json:"substitute_id,omitempty"`
}

type Assignment struct {
	MembershipID string `json:"membership_id,omitempty"`
	ReasonCode   string `json:"reason_code"`
	Reason       string `json:"reason"`
	Position     int    `json:"position"`
}

func Rotate(candidates []Candidate, position uint64) (Assignment, error) {
	if len(candidates) == 0 || len(candidates) > 32 {
		return Assignment{}, errors.New("für eine Rotation sind eine bis 32 Personen erforderlich")
	}
	byID := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.MembershipID == "" {
			return Assignment{}, errors.New("fehlende Personenkennung")
		}
		if _, exists := byID[candidate.MembershipID]; exists {
			return Assignment{}, errors.New("doppelte Person in der Rotation")
		}
		byID[candidate.MembershipID] = candidate
	}
	rotationPosition := position % uint64(len(candidates))
	if rotationPosition > 31 {
		return Assignment{}, errors.New("ungültige Rotationsposition")
	}
	start := int(rotationPosition)
	for offset := range candidates {
		index := (start + offset) % len(candidates)
		candidate := candidates[index]
		if !candidate.Active || !candidate.Eligible {
			continue
		}
		if candidate.Absent {
			substitute, ok := byID[candidate.SubstituteID]
			if ok && substitute.Active && substitute.Eligible && !substitute.Absent {
				return Assignment{MembershipID: substitute.MembershipID, ReasonCode: "substitute",
					Reason: "Ausdrücklich gewählte, geeignete Vertretung", Position: index}, nil
			}
			continue
		}
		return Assignment{MembershipID: candidate.MembershipID, ReasonCode: "rotation",
			Reason: "Nächste aktive, geeignete und anwesende Person in der Rotation", Position: index}, nil
	}
	return Assignment{ReasonCode: "no_available_member", Reason: "Keine geeignete anwesende Person; bleibt unzugewiesen", Position: start}, nil
}

// ValidateGraph uses three-color DFS over bounded vertices and edges. It
// enumerates no paths, so a dense acyclic graph cannot expand exponentially.
func ValidateGraph(graph map[string][]string) error {
	if len(graph) > 2000 {
		return errors.New("zu viele Vorlagen für die begrenzte Abhängigkeitsprüfung")
	}
	keys := make([]string, 0, len(graph))
	for key, edges := range graph {
		if key == "" || len(edges) > 16 {
			return errors.New("ungültige Vorlage oder mehr als 16 Voraussetzungen")
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	state := make(map[string]uint8, len(graph))
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return errors.New("zyklische Voraussetzungen")
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		seen := make(map[string]bool, len(graph[key]))
		for _, next := range graph[key] {
			if _, exists := graph[next]; !exists || next == key || seen[next] {
				return errors.New("nicht verfügbare, selbstbezogene oder doppelte Voraussetzung")
			}
			seen[next] = true
			if err := visit(next); err != nil {
				return err
			}
		}
		state[key] = 2
		return nil
	}
	for _, key := range keys {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
