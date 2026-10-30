package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PlanningConflict struct{ Message string }

func (e PlanningConflict) Error() string { return e.Message }
func (e PlanningConflict) Unwrap() error { return ErrConflict }

func (s *Store) MutatePlanning(ctx context.Context, id auth.Identity, pid, key, requestID string, in PlanningInput) (Record, error) {
	if !value.ValidUUID(pid) || !value.ValidUUID(key) {
		return nil, Invalid{"Gültige Plan- und Vorgangskennung erforderlich"}
	}
	if len(in.Components) > planningComponentLimit {
		return nil, Invalid{"Höchstens zehn Komponenten pro Mahlzeit"}
	}
	if err := normalizePlanningParticipationPayload(&in); err != nil {
		return nil, err
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "mealplan", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	// Recipe sources are read dependencies. Once copied, the response belongs to
	// mealplan and can be replayed without re-opening a removed source module.
	targets := map[string]int64{}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	op := "mealplan/workspace/" + pid
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(hash[:], priorHash) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if in.Action == "copy" {
		var copied, starts string
		var copiedHash []byte
		err = tx.QueryRow(ctx, "SELECT id,starts_on::text,copy_request_hash FROM mealplan.plans WHERE household_id=$1 AND created_by=$2 AND copy_source_plan=$3 AND copy_key=$4", id.HouseholdID, id.UserID, pid, key).Scan(&copied, &starts, &copiedHash)
		if err == nil {
			if !bytes.Equal(copiedHash, hash[:]) {
				return nil, PlanningConflict{"Diese Wochenkopie wurde bereits mit anderen Angaben angelegt"}
			}
			result := planningResult(copied, "")
			result["url"] = String(result["url"]) + "?week=" + starts
			return result, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT to_jsonb(p) FROM mealplan.plans p WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, pid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	plan, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if Int(plan["version"]) != in.PlanVersion {
		return nil, PlanningConflict{"Der Plan wurde inzwischen geändert. Gespeicherten Stand laden und den Entwurf prüfen."}
	}
	if in.Action == "save" {
		for _, component := range in.Components {
			if component.ID != "" {
				continue
			}
			generation, sourceErr := requireModule(ctx, tx, id, "recipes")
			if sourceErr != nil {
				return nil, sourceErr
			}
			if generation != component.SourceGeneration {
				return nil, PlanningConflict{"Die Rezeptquelle wurde geändert. Quelle erneut prüfen."}
			}
		}
	}
	var result Record
	switch in.Action {
	case "save":
		result, err = s.savePlanningMeal(ctx, tx, id, plan, in)
	case "move", "swap", "delete":
		result, err = s.movePlanningMeal(ctx, tx, id, plan, in)
	case "copy":
		result, err = s.copyPlanningWeek(ctx, tx, id, plan, key, hash[:], in)
	default:
		err = Invalid{"Unbekannte Planungsaktion"}
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	affected := String(result["plan_id"])
	if err = s.projectPlanning(ctx, tx, id, "plans", affected, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "mealplan", "plans", affected, "record_write", requestID); err != nil {
		return nil, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	targetJSON, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload,target_generations) VALUES($1,$2,$3,$4,$5,'mealplan',$6,200,$7,$8)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, response, targetJSON); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, planningLeftoverMutationError("mealplan", shoppingContention(err))
	}
	return result, nil
}

func (s *Store) projectPlanning(ctx context.Context, tx pgx.Tx, id auth.Identity, entity, rid string, generation int64) error {
	if entity != "plans" && entity != "meals" {
		return ErrNotFound
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{"mealplan", entity}.Sanitize()+" t WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&raw); err != nil {
		return err
	}
	record, err := decode(raw)
	if err != nil {
		return err
	}
	m, _ := s.Module("mealplan")
	e, _ := m.Entity(entity)
	return s.afterWrite(ctx, tx, id, m, e, record, generation)
}

func planningResult(pid, mid string) Record {
	return Record{"plan_id": pid, "meal_id": mid, "url": "/m/mealplan/workspace/" + pid}
}

func lockPlanningMeal(ctx context.Context, tx pgx.Tx, id auth.Identity, pid, mid string, version int64) (PlanningMeal, error) {
	if !value.ValidUUID(mid) {
		return PlanningMeal{}, ErrNotFound
	}
	var locked string
	err := tx.QueryRow(ctx, "SELECT id FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id=$3 FOR UPDATE NOWAIT", id.HouseholdID, pid, mid).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanningMeal{}, ErrNotFound
	}
	if err != nil {
		return PlanningMeal{}, shoppingContention(err)
	}
	meal, err := readPlanningMeal(ctx, tx, id, pid, mid)
	if err != nil {
		return meal, err
	}
	if meal.Version != version {
		return meal, PlanningConflict{"Die Mahlzeit „" + meal.Title + "“ wurde geändert. Entwurf mit dem gespeicherten Stand vergleichen."}
	}
	ids := make([]string, 0, len(meal.Components))
	for _, component := range meal.Components {
		ids = append(ids, component.ID)
	}
	return meal, lockPlanningComponents(ctx, tx, id, ids)
}

// The caller holds the plan row. Parent guards prevent child writers from
// committing changes between the bounded metadata read and component locks.
func lockPlanningWeek(ctx context.Context, tx pgx.Tx, id auth.Identity, pid, start string) (PlanningWeek, error) {
	days, err := planningWindow(start)
	if err != nil {
		return PlanningWeek{}, err
	}
	locked, err := tx.Exec(ctx, `SELECT id FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND id IN
 (SELECT id FROM mealplan.meals WHERE household_id=$1 AND plan_id=$2 AND planned_on BETWEEN $3::date AND $4::date ORDER BY planned_on,meal_slot,id LIMIT 43)
 ORDER BY id FOR UPDATE NOWAIT`, id.HouseholdID, pid, days[0], days[6])
	if err != nil {
		return PlanningWeek{}, shoppingContention(err)
	}
	if locked.RowsAffected() > planningMealLimit {
		return PlanningWeek{}, Invalid{"Mehr als 42 Mahlzeiten in diesem Zeitraum. Vorhandene Mahlzeiten in den Einzellisten prüfen und die Woche aufteilen."}
	}
	return readPlanningWeek(ctx, tx, id, pid, start)
}

func lockPlanningComponents(ctx context.Context, tx pgx.Tx, id auth.Identity, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > planningMealLimit*planningComponentLimit {
		return Invalid{"Auswahl überschreitet die Komponentengrenze"}
	}
	locked, err := tx.Exec(ctx, "SELECT id FROM mealplan.components WHERE household_id=$1 AND id=ANY($2::uuid[]) ORDER BY id FOR UPDATE NOWAIT", id.HouseholdID, ids)
	if err != nil {
		return shoppingContention(err)
	}
	if locked.RowsAffected() != int64(len(ids)) {
		return PlanningConflict{"Die Rezeptkomponenten wurden geändert. Gespeicherten Stand erneut laden."}
	}
	return nil
}
