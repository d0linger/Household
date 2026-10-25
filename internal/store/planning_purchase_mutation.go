package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningPurchaseInput struct {
	Generation         int64                       `json:"generation"`
	PantryGeneration   int64                       `json:"pantry_generation"`
	ShoppingGeneration int64                       `json:"shopping_generation"`
	Fingerprint        string                      `json:"fingerprint"`
	Selected           []PlanningPurchaseSelection `json:"selected"`
	Decisions          []PlanningPurchaseDecision  `json:"decisions"`
	ListID             string                      `json:"list_id"`
	ListVersion        int64                       `json:"list_version"`
}

func normalizePlanningPurchase(in PlanningPurchaseInput) (PlanningPurchaseInput, map[string][]string, error) {
	plans, err := planningPurchaseSelections(in.Selected)
	if err != nil {
		return in, nil, err
	}
	in.Decisions, _, err = planningPurchaseDecisions(in.Decisions)
	if err != nil {
		return in, nil, err
	}
	fingerprint, err := hex.DecodeString(in.Fingerprint)
	if err != nil || len(fingerprint) != sha256.Size || in.Generation <= 0 || in.PantryGeneration <= 0 || in.ShoppingGeneration <= 0 ||
		in.ListID != "" && (!value.ValidUUID(in.ListID) || in.ListVersion <= 0) || in.ListID == "" && in.ListVersion != 0 {
		return in, nil, Invalid{"Aktuellen vollständigen Vergleich und gültigen Listenstand bestätigen"}
	}
	in.Fingerprint = hex.EncodeToString(fingerprint)
	in.Selected = make([]PlanningPurchaseSelection, 0, len(plans))
	for pid, meals := range plans {
		sort.Strings(meals)
		in.Selected = append(in.Selected, PlanningPurchaseSelection{pid, meals})
	}
	sort.Slice(in.Selected, func(i, j int) bool { return in.Selected[i].PlanID < in.Selected[j].PlanID })
	return in, plans, nil
}

// SyncPlanningPurchases registers complete meal requirements, including zero
// purchases, and applies only the exact changes in the reviewed global scope.
func (s *Store) SyncPlanningPurchases(ctx context.Context, id auth.Identity, key, requestID string, input PlanningPurchaseInput) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Vorgangskennung erforderlich"}
	}
	in, plans, err := normalizePlanningPurchase(input)
	if err != nil {
		return nil, err
	}
	_, decisions, err := planningPurchaseDecisions(in.Decisions)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Kind  string
		Input PlanningPurchaseInput
	}{"sync", in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "mealplan", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	for _, target := range []struct {
		module     string
		generation int64
	}{{"pantry", in.PantryGeneration}, {"shopping", in.ShoppingGeneration}} {
		generation, targetErr := requireModule(ctx, tx, id, target.module)
		if targetErr != nil {
			return nil, targetErr
		}
		if generation != target.generation {
			return nil, ErrConflict
		}
	}
	prior, priorHash, err := readPlanningPurchaseOperation(ctx, tx, id, key)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err = lockPlanningPurchaseScope(ctx, tx, id); err != nil {
		return nil, err
	}
	// Another request may have committed while this transaction was acquiring
	// its shared module locks. Check the durable key again after the mutex.
	prior, priorHash, err = readPlanningPurchaseOperation(ctx, tx, id, key)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	scope, err := readPlanningPurchaseScope(ctx, tx, id, plans, decisions, nil)
	if err != nil {
		return nil, err
	}
	if !scope.view.Ready {
		return nil, PlanningConflict{"Offene Quellen-, Mengen- oder Einkaufsentscheidung zuerst ausdrücklich klären."}
	}
	if err = lockPlanningPurchaseResources(ctx, tx, id, scope, in); err != nil {
		return nil, err
	}
	var at time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return nil, err
	}
	current, err := readPlanningPurchaseScope(ctx, tx, id, plans, decisions, &at)
	if err != nil {
		return nil, err
	}
	if !samePlanningPurchaseLocks(scope, current) || current.view.Fingerprint != scope.view.Fingerprint || current.view.Fingerprint != in.Fingerprint {
		return nil, PlanningConflict{"Bedarf, Zuordnung, Bestand, Reservierung oder Einkauf wurde geändert. Vollständigen aktuellen Vergleich erneut prüfen."}
	}
	if !current.view.Ready {
		return nil, PlanningConflict{"Offene Quellen-, Mengen- oder Einkaufsentscheidung zuerst ausdrücklich klären."}
	}
	op, receipt := value.UUID(), value.UUID()
	sources, err := s.applyPlanningPurchaseScope(ctx, tx, id, current, in, op)
	if err != nil {
		return nil, err
	}
	if err = writePlanningPurchaseEvidence(ctx, tx, id, key, op, receipt, "sync", in, hash[:], sources, current.view); err != nil {
		return nil, err
	}
	result, _, err := readPlanningPurchaseOperation(ctx, tx, id, key)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	// The result belongs to this operation, rather than a later current scope.
	return result, nil
}

func lockPlanningPurchaseScope(ctx context.Context, tx pgx.Tx, id auth.Identity) error {
	var locked bool
	err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))", "planning-purchases/"+id.HouseholdID).Scan(&locked)
	if err != nil {
		return err
	}
	if !locked {
		return PlanningConflict{"Die gemeinsame Einkaufsplanung wird gerade geändert. Vergleich erneut laden und nochmals bestätigen."}
	}
	return nil
}
