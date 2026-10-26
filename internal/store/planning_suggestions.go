package store

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type RecipeSuggestionQuery struct {
	Search, Servings, PlannedOn string
	EntryModule                 string
	AfterCreated                time.Time
	AfterID                     string
	ExpectedScope               *RecipeSuggestionScope
	CursorExpiresAt             time.Time
}

// RecipeSuggestionScope binds paging to the current optional-data identities
// and household calendar. Every page still evaluates freshly observed stock.
type RecipeSuggestionScope struct {
	EntryModule       string `json:"entry_module"`
	MealplanEnabled   bool   `json:"mealplan_enabled"`
	Generation        int64  `json:"generation"`
	RecipesGeneration int64  `json:"recipes_generation"`
	PantryGeneration  int64  `json:"pantry_generation"`
	RecipesEnabled    bool   `json:"recipes_enabled"`
	PantryEnabled     bool   `json:"pantry_enabled"`
	Timezone          string `json:"timezone"`
	Today             string `json:"today"`
	Search            string `json:"search"`
	Servings          string `json:"servings"`
	PlannedOn         string `json:"planned_on"`
}

type suggestionProductIdentity struct {
	Name, Unit string
}

type RecipeSuggestion struct {
	RecipeID        string                      `json:"recipe_id"`
	SourceVersionID string                      `json:"source_version_id"`
	Revision        string                      `json:"revision"`
	Title           string                      `json:"title"`
	BaseServings    string                      `json:"base_servings"`
	Outcome         string                      `json:"outcome"`
	SourceIssue     string                      `json:"source_issue,omitempty"`
	DateReview      bool                        `json:"date_review"`
	UrgentOn        string                      `json:"urgent_on,omitempty"`
	Lines           []IngredientComparisonLine  `json:"lines"`
	Products        []suggestionProductCoverage `json:"products"`
	Dates           []suggestionDateReason      `json:"dates"`
	DateReasonCount int                         `json:"date_reason_count"`
	created         time.Time
	bytes           int64
	count           int
	quantities      []planningDemandQuantity
}

type RecipeSuggestionPage struct {
	EntryModule       string             `json:"entry_module"`
	MealplanEnabled   bool               `json:"mealplan_enabled"`
	ReadAt            time.Time          `json:"read_at"`
	Timezone          string             `json:"timezone"`
	Today             string             `json:"today"`
	Servings          string             `json:"servings"`
	PlannedOn         string             `json:"planned_on"`
	Search            string             `json:"search"`
	Generation        int64              `json:"generation"`
	RecipesGeneration int64              `json:"recipes_generation"`
	PantryGeneration  int64              `json:"pantry_generation"`
	RecipesEnabled    bool               `json:"recipes_enabled"`
	PantryEnabled     bool               `json:"pantry_enabled"`
	Candidates        []RecipeSuggestion `json:"candidates"`
	More              bool               `json:"more"`
	AfterCreated      time.Time          `json:"-"`
	AfterID           string             `json:"-"`
}

// RecipeSuggestions evaluates one explicitly bounded page of alternatives.
// The HTTP caller holds the module lifecycle read gate and signs page cursors.
func (s *Store) RecipeSuggestions(ctx context.Context, id auth.Identity, query RecipeSuggestionQuery) (RecipeSuggestionPage, error) {
	if query.EntryModule == "" {
		query.EntryModule = "mealplan"
	}
	out := RecipeSuggestionPage{Candidates: []RecipeSuggestion{}, Search: strings.TrimSpace(query.Search), EntryModule: query.EntryModule}
	if query.EntryModule != "mealplan" && query.EntryModule != "pantry" {
		return out, Invalid{"Rezeptvorschläge über Vorrat oder Essensplanung aufrufen"}
	}
	if !value.ValidUUID(id.HouseholdID) || !value.ValidUUID(id.UserID) || !value.ValidUUID(id.MembershipID) || !value.ValidUUID(id.SessionID) || id.AuthVersion <= 0 {
		return out, ErrForbidden
	}
	servings, err := value.Decimal(query.Servings)
	if err != nil || servings.Sign() <= 0 || len(out.Search) > 300 || !utf8.ValidString(out.Search) || strings.ContainsRune(out.Search, '\x00') ||
		query.PlannedOn != "" && !value.Date(query.PlannedOn) || query.AfterID != "" && !value.ValidUUID(query.AfterID) ||
		(query.AfterID == "") != query.AfterCreated.IsZero() || (query.AfterID != "") != (query.ExpectedScope != nil) ||
		(query.ExpectedScope == nil) != query.CursorExpiresAt.IsZero() {
		return out, Invalid{"Suchbegriff, positive exakte Portionen, Planungsdatum und Seitenauswahl prüfen"}
	}
	out.Servings = servings.FloatString(6)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	if err = readSuggestionScope(ctx, tx, id, &out); err != nil {
		return out, err
	}
	out.Today, err = suggestionPlanningDate(out.ReadAt, out.Timezone, "")
	if err != nil {
		return out, err
	}
	out.PlannedOn, err = suggestionPlanningDate(out.ReadAt, out.Timezone, query.PlannedOn)
	if err != nil {
		return out, err
	}
	if query.ExpectedScope != nil && (*query.ExpectedScope != out.Scope() || !query.CursorExpiresAt.After(out.ReadAt)) {
		return out, ErrConflict
	}
	if !out.RecipesEnabled {
		return out, tx.Commit(ctx)
	}
	if err = readSuggestionCandidates(ctx, tx, id, query, &out); err != nil {
		return out, err
	}
	products, err := readSuggestionIngredients(ctx, tx, id, servings, &out)
	if err != nil {
		return out, err
	}
	lots := []suggestionDateLot{}
	if out.PantryEnabled {
		units := make(map[string]string, len(products))
		for pid, product := range products {
			units[pid] = product.Unit
		}
		lots, err = readSuggestionLots(ctx, tx, id, units, out.Generation, out.PantryGeneration, out.ReadAt)
		if err != nil {
			return out, err
		}
	}
	for i := range out.Candidates {
		candidate := &out.Candidates[i]
		if candidate.SourceIssue != "" {
			continue
		}
		evaluation, evaluationErr := evaluateSuggestionLots(candidate.quantities, lots, out.PlannedOn)
		if evaluationErr != nil {
			return out, evaluationErr
		}
		candidate.Outcome, candidate.Products = evaluation.Coverage.Outcome, evaluation.Coverage.Products
		for index := range candidate.Products {
			candidate.Products[index].Name = products[candidate.Products[index].ProductID].Name
		}
		candidate.DateReasonCount = len(evaluation.Reasons)
		candidate.Dates = suggestionDateEvidence(evaluation.Reasons)
		candidate.DateReview, candidate.UrgentOn = evaluation.DateReview, evaluation.UrgentOn
	}
	if err = sortRecipeSuggestions(&out); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (p RecipeSuggestionPage) Scope() RecipeSuggestionScope {
	return RecipeSuggestionScope{EntryModule: p.EntryModule, MealplanEnabled: p.MealplanEnabled, Generation: p.Generation, RecipesGeneration: p.RecipesGeneration,
		PantryGeneration: p.PantryGeneration, RecipesEnabled: p.RecipesEnabled, PantryEnabled: p.PantryEnabled,
		Timezone: p.Timezone, Today: p.Today, Search: p.Search, Servings: p.Servings, PlannedOn: p.PlannedOn}
}

func readSuggestionScope(ctx context.Context, tx pgx.Tx, id auth.Identity, out *RecipeSuggestionPage) error {
	err := tx.QueryRow(ctx, `SELECT h.timezone,transaction_timestamp(),coalesce(mp.state='enabled',false),coalesce(mp.data_generation,0),
 coalesce(rp.state='enabled',false),coalesce(rp.data_generation,0),coalesce(pp.state='enabled',false),coalesce(pp.data_generation,0)
 FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.user_id=$2 AND m.id=$3 AND m.role=$4 AND m.inactive_at IS NULL
 JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND u.auth_version=$5 AND NOT u.must_change_password
 JOIN core.sessions s ON s.user_id=u.id AND s.id=$6 AND s.revoked_at IS NULL AND s.auth_version=u.auth_version
 AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
 LEFT JOIN core.module_states mp ON mp.household_id=h.id AND mp.module_id='mealplan'
 LEFT JOIN core.module_states rp ON rp.household_id=h.id AND rp.module_id='recipes'
 LEFT JOIN core.module_states pp ON pp.household_id=h.id AND pp.module_id='pantry'
 WHERE h.id=$1`, id.HouseholdID, id.UserID, id.MembershipID, id.Role, id.AuthVersion, id.SessionID).Scan(
		&out.Timezone, &out.ReadAt, &out.MealplanEnabled, &out.Generation, &out.RecipesEnabled, &out.RecipesGeneration, &out.PantryEnabled, &out.PantryGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if out.EntryModule == "mealplan" && !out.MealplanEnabled || out.EntryModule == "pantry" && !out.PantryEnabled {
		return ErrNotFound
	}
	return nil
}

func sortRecipeSuggestions(out *RecipeSuggestionPage) error {
	input := make([]suggestionRank, 0, len(out.Candidates))
	byID := make(map[string]RecipeSuggestion, len(out.Candidates))
	for _, candidate := range out.Candidates {
		// An unreviewable source remains an explicit empty evaluation, never a
		// silently dropped row or a sufficient recipe with missing ingredients.
		source := candidate.SourceVersionID
		if source == "" {
			source = candidate.RecipeID
		}
		input = append(input, suggestionRank{RecipeID: candidate.RecipeID, SourceVersionID: source, Title: candidate.Title,
			Outcome: candidate.Outcome, DateReview: candidate.DateReview, UrgentOn: candidate.UrgentOn})
		byID[candidate.RecipeID] = candidate
	}
	ranked, err := rankSuggestionPage(input)
	if err != nil {
		return err
	}
	result := make([]RecipeSuggestion, 0, len(ranked))
	for _, item := range ranked {
		result = append(result, byID[item.RecipeID])
	}
	out.Candidates = result
	return nil
}

func suggestionIngredientQuantity(ingredient Record, product Record, base, servings *big.Rat) (*big.Rat, string) {
	quantity, err := value.Decimal(String(ingredient["quantity"]))
	if err != nil || quantity.Sign() <= 0 {
		return nil, "quantity_unknown"
	}
	demand, err := ingredientDemand(String(ingredient["quantity"]), String(ingredient["unit_code"]), String(product["unit"]), base, servings)
	if err != nil {
		return nil, "unit_incompatible"
	}
	return demand, ""
}
