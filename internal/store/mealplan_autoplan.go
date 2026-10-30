package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const (
	planningAutoplanCandidateLimit = 20
	planningAutoplanSlotLimit      = 28
)

type PlanningAutoplanCandidateInput struct {
	SourceVersionID string `json:"source_version_id"`
}

type PlanningAutoplanSlotInput struct {
	PlannedOn       string `json:"planned_on"`
	Slot            string `json:"slot"`
	Servings        string `json:"servings"`
	SourceVersionID string `json:"source_version_id,omitempty"`
	LeaveEmpty      bool   `json:"leave_empty,omitempty"`
}

type PlanningAutoplanInput struct {
	PlanningExtensionCommand
	RecipesGeneration int64                            `json:"recipes_generation"`
	Candidates        []PlanningAutoplanCandidateInput `json:"candidates"`
	Slots             []PlanningAutoplanSlotInput      `json:"slots"`
	Rules             PlanningAutoplanRules            `json:"rules"`
	ReviewFingerprint string                           `json:"review_fingerprint,omitempty"`
	ReviewProof       string                           `json:"review_proof,omitempty"`
	ReviewExpiresAt   int64                            `json:"review_expires_at,omitempty"`
}

// PlanningAutoplanReviewHash binds the user's complete selection and weights,
// excluding the server's review fields, for the HTTP layer's short-lived proof.
func PlanningAutoplanReviewHash(in PlanningAutoplanInput) string {
	in.ReviewFingerprint, in.ReviewProof = "", ""
	in.ReviewExpiresAt = 0
	encoded, _ := json.Marshal(in)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func validatePlanningAutoplan(in PlanningAutoplanInput) error {
	if in.RecipesGeneration <= 0 || len(in.Candidates) == 0 || len(in.Candidates) > planningAutoplanCandidateLimit ||
		len(in.Slots) == 0 || len(in.Slots) > planningAutoplanSlotLimit {
		return Invalid{"1 bis 20 Rezeptstände und 1 bis 28 leere Tagesplätze ausdrücklich auswählen"}
	}
	if err := validatePlanningRules(in.Rules); err != nil {
		return err
	}
	versions := map[string]bool{}
	for _, candidate := range in.Candidates {
		if !value.ValidUUID(candidate.SourceVersionID) || versions[candidate.SourceVersionID] {
			return Invalid{"Rezeptstände ohne doppelte Kennungen auswählen"}
		}
		versions[candidate.SourceVersionID] = true
	}
	slots := map[string]bool{}
	for _, slot := range in.Slots {
		key := slot.PlannedOn + "/" + slot.Slot
		if !value.Date(slot.PlannedOn) || !planningSlot(slot.Slot) || slots[key] ||
			slot.SourceVersionID != "" && !versions[slot.SourceVersionID] || slot.LeaveEmpty && slot.SourceVersionID != "" {
			return Invalid{"Eindeutige Tagesplätze, geprüfte Rezeptauswahl und leere Alternativen prüfen"}
		}
		if _, err := planningDecimal(slot.Servings); err != nil {
			return err
		}
		slots[key] = true
	}
	return nil
}

func (s *Store) PlanningAutoplanPreview(ctx context.Context, id auth.Identity, pid string, in PlanningAutoplanInput) (Record, error) {
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	if err := validatePlanningAutoplan(in); err != nil {
		return nil, err
	}
	tx, plan, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if in.Generation != generation || in.PlanVersion != Int(plan["version"]) {
		return nil, PlanningConflict{"Plan wurde geändert. Wochenvorschlag neu prüfen."}
	}
	preview, _, err := readPlanningAutoplan(ctx, tx, id, plan, in, false)
	if err != nil {
		return nil, err
	}
	return preview, tx.Commit(ctx)
}

func (s *Store) ApplyPlanningAutoplan(ctx context.Context, id auth.Identity, pid, key, requestID string, in PlanningAutoplanInput) (Record, error) {
	if err := validatePlanningAutoplan(in); err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(in.ReviewFingerprint)
	if err != nil || len(decoded) != sha256.Size {
		return nil, Invalid{"Geprüfte Wochenvorschau erforderlich"}
	}
	return s.planningExtensionWrite(ctx, id, pid, key, requestID, "autoplan", in.PlanningExtensionCommand, in,
		func(ctx context.Context, tx pgx.Tx, plan Record) (Record, error) {
			now := time.Now().Unix()
			if in.ReviewExpiresAt <= now || in.ReviewExpiresAt > now+305 {
				return nil, PlanningConflict{"Wochenvorschau ist abgelaufen. Vorschläge erneut prüfen."}
			}
			preview, choices, readErr := readPlanningAutoplan(ctx, tx, id, plan, in, true)
			if readErr != nil {
				return nil, readErr
			}
			if String(preview["review_fingerprint"]) != in.ReviewFingerprint {
				return nil, PlanningConflict{"Bestände, Preise, Quellen oder Planungsregeln haben sich geändert. Vorschau neu prüfen."}
			}
			if len(choices) == 0 {
				return nil, Invalid{"Mindestens einen Vorschlag zur Übernahme auswählen"}
			}
			created := []string{}
			for _, choice := range choices {
				input := PlanningInput{Generation: in.Generation, Action: "save",
					Meal: PlanningMealInput{Title: choice.Title, PlannedOn: choice.Slot.PlannedOn, Slot: choice.Slot.Slot,
						Kind: "recipe", Status: "planned", Servings: choice.Slot.Servings,
						Participation: &PlanningParticipationInput{Mode: "manual", MembershipIDs: []string{}}},
					Components: []PlanningComponentInput{{SourceVersionID: choice.SourceVersionID,
						SourceGeneration: in.RecipesGeneration, Servings: choice.Slot.Servings,
						ServingsMode: "meal", PrepMinutes: choice.PrepMinutes, CookMinutes: choice.CookMinutes}}}
				result, saveErr := s.savePlanningMeal(ctx, tx, id, plan, input)
				if saveErr != nil {
					return nil, saveErr
				}
				created = append(created, String(result["meal_id"]))
			}
			return Record{"plan_id": pid, "week": in.Week, "created_meal_ids": created,
				"review_fingerprint": in.ReviewFingerprint, "stock_changed": false}, nil
		})
}

type planningAutoplanCandidate struct {
	SourceVersionID, RecipeID, Title string
	SnapshotHash                     string
	Bundle                           RecipeBundle
	SnapshotBytes                    int64
	PrepMinutes, CookMinutes         *int64
}

type planningAutoplanChoice struct {
	Slot                     PlanningAutoplanSlotInput
	SourceVersionID, Title   string
	PrepMinutes, CookMinutes *int64
}

type planningAutoplanData struct {
	Scope        planningBudgetScope
	Settings     planningWeekSettings
	Week         PlanningWeek
	Candidates   []planningAutoplanCandidate
	Catalogs     map[string]Record
	Mappings     map[string]Record
	Lots         []suggestionDateLot
	Existing     PlanningDemand
	UnknownLines int
	OwnClaims    map[string]string
}

func readPlanningAutoplan(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, in PlanningAutoplanInput, lock bool) (Record, []planningAutoplanChoice, error) {
	days, err := planningExtensionWindow(plan, in.Week)
	if err != nil {
		return nil, nil, err
	}
	if plan["is_template"] == true {
		return nil, nil, Invalid{"Vorlage zuerst als unabhängige Arbeitswoche übernehmen"}
	}
	data, err := readPlanningAutoplanData(ctx, tx, id, plan, in, lock)
	if err != nil {
		return nil, nil, err
	}
	occupied := map[string]bool{}
	for _, meal := range data.Week.Meals {
		occupied[meal.PlannedOn+"/"+meal.Slot] = true
	}
	active := 0
	for _, slot := range in.Slots {
		if slot.PlannedOn < days[0] || slot.PlannedOn > days[6] || slot.PlannedOn < String(plan["starts_on"]) ||
			slot.PlannedOn > String(plan["ends_on"]) || slot.PlannedOn < data.Scope.Today || occupied[slot.PlannedOn+"/"+slot.Slot] {
			return nil, nil, PlanningConflict{"Nur aktuelle oder zukünftige leere Plätze dieser sieben Tage auswählen. Bestehende Mahlzeiten bleiben erhalten."}
		}
		if !slot.LeaveEmpty {
			active++
		}
	}
	if len(data.Week.Meals)+active > planningMealLimit {
		return nil, nil, Invalid{"Woche auf höchstens 42 Mahlzeiten begrenzen"}
	}
	result, choices, err := simulatePlanningAutoplan(in, data)
	if err != nil {
		return nil, nil, err
	}
	fingerprint, err := planningAutoplanFingerprint(in, data)
	if err != nil {
		return nil, nil, err
	}
	result["review_fingerprint"] = fingerprint
	result["generation"], result["plan_version"], result["week"] = in.Generation, Int(plan["version"]), in.Week
	result["settings_version"], result["calculated_at"] = data.Settings.Version, data.Scope.ReadAt
	result["limits"] = Record{"candidate_limit": planningAutoplanCandidateLimit, "slot_limit": planningAutoplanSlotLimit,
		"ingredient_limit": planningIngredientLimit, "snapshot_byte_limit": planningCopyBytes, "global_optimum": false}
	return result, choices, nil
}

func planningAutoplanFingerprint(in PlanningAutoplanInput, data planningAutoplanData) (string, error) {
	sources := []Record{}
	for _, candidate := range data.Candidates {
		sources = append(sources, Record{"version": candidate.SourceVersionID, "recipe": candidate.RecipeID, "hash": candidate.SnapshotHash})
	}
	scope := data.Scope
	scope.ReadAt = time.Time{}
	// Read time itself is not an input. Expired claims change Lots; a new local
	// calendar day changes Today. Both therefore invalidate the dependency proof.
	encoded, err := json.Marshal(Record{"input": PlanningAutoplanReviewHash(in), "scope": scope, "settings": data.Settings,
		"week": data.Week, "sources": sources, "catalogs": data.Catalogs, "mappings": data.Mappings,
		"lots": data.Lots, "existing": planningAutoplanExistingFingerprint(data.Existing), "own_claims": data.OwnClaims})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func planningAutoplanExistingFingerprint(demand PlanningDemand) Record {
	return Record{"meals": demand.Meals, "lines": demand.Lines, "products": demand.Products, "issues": demand.Issues,
		"generation": demand.Generation, "pantry_generation": demand.PantryGeneration}
}

func planningAutoplanSlotRank(slot string) int {
	switch slot {
	case "breakfast":
		return 0
	case "lunch":
		return 1
	case "dinner":
		return 2
	default:
		return 3
	}
}

func planningAutoplanSortedSlots(slots []PlanningAutoplanSlotInput) []PlanningAutoplanSlotInput {
	out := append([]PlanningAutoplanSlotInput(nil), slots...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlannedOn != out[j].PlannedOn {
			return out[i].PlannedOn < out[j].PlannedOn
		}
		return planningAutoplanSlotRank(out[i].Slot) < planningAutoplanSlotRank(out[j].Slot)
	})
	return out
}

func planningAutoplanRat(raw string) *big.Rat {
	amount, ok := new(big.Rat).SetString(raw)
	if !ok || amount.Sign() < 0 {
		return new(big.Rat)
	}
	return amount
}
