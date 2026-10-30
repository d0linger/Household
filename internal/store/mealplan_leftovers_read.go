package store

import (
	"context"

	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

const planningLeftoverSourceCurrent = `(m.kind='recipe' AND m.status<>'cancelled' AND m.servings_mode='manual'
 AND s.reviewed_meal_version=m.version AND s.reviewed_component_version=c.version
 AND s.source_hash=sha256(convert_to(c.snapshot::text,'UTF8')) AND s.expected_servings<=c.servings)`

func (s *Store) PlanningLeftovers(ctx context.Context, id auth.Identity, pid, week string) (Record, error) {
	tx, plan, generation, err := s.planningExtensionRead(ctx, id, pid)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	out, err := readPlanningLeftovers(ctx, tx, id, plan, generation, week)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func readPlanningLeftovers(ctx context.Context, tx pgx.Tx, id auth.Identity, plan Record, generation int64, week string) (Record, error) {
	days, err := planningExtensionWindow(plan, week)
	if err != nil {
		return nil, err
	}
	pid := String(plan["id"])
	out := Record{"generation": generation, "plan_version": plan["version"], "week": week,
		"sources": []Record{}, "allocations": []Record{}, "physical_stock": false}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(s)||jsonb_build_object('expected_servings',s.expected_servings::text,
 'source_hash',encode(s.source_hash,'hex'),'source_meal_title',m.title,'source_component_title',c.snapshot->'recipe'->>'title',
 'source_date',m.planned_on,'source_slot',m.meal_slot,'source_meal_version',m.version,'source_component_version',c.version,
 'production_servings',c.servings::text,'source_use_servings',(c.servings-s.expected_servings)::text,
 'allocated_servings',coalesce((SELECT sum(a.servings) FROM mealplan.leftover_allocations a WHERE a.household_id=s.household_id AND a.source_id=s.id),0)::text,
 'free_servings',(s.expected_servings-coalesce((SELECT sum(a.servings) FROM mealplan.leftover_allocations a WHERE a.household_id=s.household_id AND a.source_id=s.id),0))::text,
 'current',`+planningLeftoverSourceCurrent+`,'stale',NOT `+planningLeftoverSourceCurrent+`)
 FROM mealplan.leftover_sources s JOIN mealplan.meals m ON m.household_id=s.household_id AND m.id=s.source_meal_id
 JOIN mealplan.components c ON c.household_id=s.household_id AND c.id=s.source_component_id
 WHERE s.household_id=$1 AND s.plan_id=$2 AND(m.planned_on BETWEEN $3::date AND $4::date OR EXISTS(
 SELECT 1 FROM mealplan.leftover_allocations a JOIN mealplan.meals t ON t.household_id=a.household_id AND t.id=a.target_meal_id
 WHERE a.household_id=s.household_id AND a.source_id=s.id AND t.planned_on BETWEEN $3::date AND $4::date))
 ORDER BY m.planned_on,mealplan.slot_order(m.meal_slot),s.id LIMIT 463`, id.HouseholdID, pid, days[0], days[6])
	if err != nil {
		return nil, err
	}
	sources := []Record{}
	ids := []string{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		source, decodeErr := decode(raw)
		if decodeErr != nil {
			rows.Close()
			return nil, decodeErr
		}
		sources = append(sources, source)
		ids = append(ids, String(source["id"]))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(sources) > 462 {
		return nil, Invalid{"Resteübersicht auf höchstens42 Mahlzeiten mit zehn Komponenten und42 verknüpften Quellen begrenzen"}
	}
	out["sources"] = sources
	if len(ids) == 0 {
		return out, nil
	}
	rows, err = tx.Query(ctx, `SELECT to_jsonb(a)||jsonb_build_object('servings',a.servings::text,
 'target_title',t.title,'target_date',t.planned_on,'target_slot',t.meal_slot,'target_servings',t.servings::text,
 'target_version',t.version,'source_date',m.planned_on,'source_meal_id',m.id,'source_component_id',c.id,
 'source_version',s.version,'current',valid.current,'stale',NOT valid.current,
 'covered_servings',CASE WHEN valid.current THEN a.servings::text ELSE '0' END,
 'uncovered_servings',(t.servings-CASE WHEN valid.current THEN a.servings ELSE 0 END)::text)
 FROM mealplan.leftover_allocations a JOIN mealplan.leftover_sources s ON s.household_id=a.household_id AND s.id=a.source_id
 JOIN mealplan.meals m ON m.household_id=s.household_id AND m.id=s.source_meal_id
 JOIN mealplan.components c ON c.household_id=s.household_id AND c.id=s.source_component_id
 JOIN mealplan.meals t ON t.household_id=a.household_id AND t.id=a.target_meal_id
 CROSS JOIN LATERAL(SELECT `+planningLeftoverSourceCurrent+` AND t.kind='leftovers' AND t.status<>'cancelled'
 AND a.reviewed_source_version=s.version AND a.reviewed_target_version=t.version AND a.servings<=t.servings
 AND(m.planned_on,mealplan.slot_order(m.meal_slot))<(t.planned_on,mealplan.slot_order(t.meal_slot)) AS current) valid
 WHERE a.household_id=$1 AND a.plan_id=$2 AND a.source_id=ANY($3::uuid[])
 ORDER BY t.planned_on,mealplan.slot_order(t.meal_slot),a.id LIMIT 501`, id.HouseholdID, pid, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allocations := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		allocation, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		allocations = append(allocations, allocation)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(allocations) > 500 {
		return nil, Invalid{"Mehr als500 verknüpfte Restemahlzeiten; Verknüpfungen zuerst aufteilen oder ausdrücklich lösen"}
	}
	out["allocations"] = allocations
	return out, nil
}
