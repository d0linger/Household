package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type planningPurchaseEffectCounts struct {
	Changes int `json:"changes"`
	Creates int `json:"creates"`
	Updates int `json:"updates"`
	Removes int `json:"removes"`
}

func planningPurchaseCounts(sources []planningPurchaseEvidence) planningPurchaseEffectCounts {
	var counts planningPurchaseEffectCounts
	for _, source := range sources {
		if source.Changed {
			counts.Changes++
		}
		switch source.Action {
		case "create":
			counts.Creates++
		case "update":
			counts.Updates++
		case "remove":
			counts.Removes++
		}
	}
	return counts
}

func writePlanningPurchaseEvidence(ctx context.Context, tx pgx.Tx, id auth.Identity, key, op, receipt, kind string, in PlanningPurchaseInput, requestHash []byte, sources []planningPurchaseEvidence, review any) error {
	counts := planningPurchaseCounts(sources)
	scopeHash, err := hex.DecodeString(in.Fingerprint)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.planning_purchase_operations(id,household_id,created_by,request_key,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,receipt_id,kind,source_count,change_count,created_count,updated_count,removed_count)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, op, id.HouseholdID, id.UserID, key, requestHash, scopeHash,
		in.Generation, in.PantryGeneration, in.ShoppingGeneration, receipt, kind, len(sources), counts.Changes, counts.Creates, counts.Updates, counts.Removes)
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(Record{"sources": sources, "review": review})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO shopping.planning_purchase_receipts(id,household_id,operation_id,request_hash,scope_hash,
 mealplan_generation,pantry_generation,shopping_generation,snapshot,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, receipt, id.HouseholdID,
		op, requestHash, scopeHash, in.Generation, in.PantryGeneration, in.ShoppingGeneration, snapshot, id.UserID)
	return err
}

func readPlanningPurchaseOperation(ctx context.Context, tx pgx.Tx, id auth.Identity, key string) (Record, []byte, error) {
	return readPlanningPurchaseProof(ctx, tx, id, key, false)
}

func readPlanningPurchaseProof(ctx context.Context, tx pgx.Tx, id auth.Identity, identifier string, byID bool) (Record, []byte, error) {
	var raw, hash []byte
	err := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',p.id,'receipt_id',p.receipt_id,'kind',p.kind,'source_count',p.source_count,
 'counts',jsonb_build_object('changes',p.change_count,'creates',p.created_count,'updates',p.updated_count,'removes',p.removed_count),
 'created_at',p.created_at,'generation',p.mealplan_generation,'pantry_generation',p.pantry_generation,'shopping_generation',p.shopping_generation,
 'details_available',r.id IS NOT NULL,'snapshot',r.snapshot),p.request_hash
 FROM core.planning_purchase_operations p LEFT JOIN core.module_states ms ON ms.household_id=p.household_id AND ms.module_id='shopping'
 LEFT JOIN shopping.planning_purchase_receipts r ON r.household_id=p.household_id AND r.operation_id=p.id
 AND ms.state='enabled' AND ms.data_generation=p.shopping_generation
 WHERE p.household_id=$1 AND (($4 AND p.id=$3::uuid) OR (NOT $4 AND p.request_key=$3::uuid AND p.created_by=$2))`,
		id.HouseholdID, id.UserID, identifier, byID).Scan(&raw, &hash)
	if err != nil {
		return nil, nil, err
	}
	result, err := decode(raw)
	return result, hash, err
}

// PlanningPurchaseOperation reads immutable historical evidence. It never
// evaluates current sources or replays effects, including after a module purge.
func (s *Store) PlanningPurchaseOperation(ctx context.Context, id auth.Identity, op string) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !value.ValidUUID(op) {
		return nil, ErrNotFound
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	result, _, err := readPlanningPurchaseProof(ctx, tx, id, op, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
