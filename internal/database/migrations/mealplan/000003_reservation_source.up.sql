BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION mealplan.reservation_basis(h uuid,m uuid) RETURNS bytea LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT sha256(convert_to(jsonb_build_object('date',planned_on,'slot',meal_slot,'kind',kind,'status',status,'servings',servings,
  'components',coalesce((SELECT jsonb_agg(jsonb_build_object('id',c.id,'servings',c.servings,'source',c.source_version_id,
   'generation',c.source_generation,'snapshot',encode(sha256(convert_to(c.snapshot::text,'UTF8')),'hex')) ORDER BY c.id)
   FROM mealplan.components c WHERE c.household_id=h AND c.meal_id=m),'[]'::jsonb))::text,'UTF8'))
 FROM mealplan.meals WHERE household_id=h AND id=m
$$;
REVOKE ALL ON FUNCTION mealplan.reservation_basis(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mealplan.reservation_basis(uuid,uuid) TO household_app;

CREATE FUNCTION mealplan.guard_reservation_source() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE template boolean; day date; kind text; status text; zone text; current_state text; generation bigint;
BEGIN
 SELECT is_template INTO template FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR template THEN RAISE EXCEPTION 'reservation needs a non-template plan' USING ERRCODE='23514';END IF;
 SELECT planned_on,m.kind,m.status INTO day,kind,status FROM mealplan.meals m
  WHERE household_id=NEW.household_id AND plan_id=NEW.plan_id AND id=NEW.meal_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR kind NOT IN('recipe','free','leftovers') OR status IN('cooked','cancelled') THEN
  RAISE EXCEPTION 'reservation needs an open household meal' USING ERRCODE='23514';
 END IF;
 SELECT state,data_generation INTO current_state,generation FROM core.module_states WHERE household_id=NEW.household_id AND module_id='mealplan' FOR SHARE NOWAIT;
 IF current_state IS DISTINCT FROM 'enabled' OR generation IS DISTINCT FROM NEW.mealplan_generation THEN
  RAISE EXCEPTION 'planning generation changed' USING ERRCODE='23514';
 END IF;
 SELECT state,data_generation INTO current_state,generation FROM core.module_states WHERE household_id=NEW.household_id AND module_id='pantry' FOR SHARE NOWAIT;
 IF current_state IS DISTINCT FROM 'enabled' OR generation IS DISTINCT FROM NEW.pantry_generation THEN
  RAISE EXCEPTION 'pantry generation changed' USING ERRCODE='23514';
 END IF;
 IF NEW.source_basis IS DISTINCT FROM mealplan.reservation_basis(NEW.household_id,NEW.meal_id) THEN
  RAISE EXCEPTION 'reservation source changed' USING ERRCODE='23514';
 END IF;
 SELECT coalesce(max(revision),0)+1 INTO NEW.revision FROM core.meal_reservations WHERE household_id=NEW.household_id AND mealplan_generation=NEW.mealplan_generation AND meal_id=NEW.meal_id;
 SELECT timezone INTO zone FROM core.households WHERE id=NEW.household_id;
 IF NEW.expires_at>((day+1)::timestamp AT TIME ZONE zone) THEN
  RAISE EXCEPTION 'reservation cannot outlive the planned calendar day' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_reservation_source() FROM PUBLIC,household_app;
CREATE TRIGGER meal_reservation_source BEFORE INSERT ON core.meal_reservations FOR EACH ROW EXECUTE FUNCTION mealplan.guard_reservation_source();

CREATE FUNCTION mealplan.release_changed_reservations() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; m uuid;
BEGIN
 IF TG_TABLE_NAME='plans' THEN
  IF TG_OP='UPDATE' AND NEW.is_template IS NOT DISTINCT FROM OLD.is_template THEN RETURN NEW;END IF;
  UPDATE core.meal_reservations SET state='released',release_reason='source_changed' WHERE household_id=OLD.household_id AND plan_id=OLD.id AND state='active';
 ELSIF TG_TABLE_NAME='meals' THEN
  IF TG_OP='UPDATE' AND (NEW.planned_on,NEW.meal_slot,NEW.kind,NEW.status,NEW.servings)
   IS NOT DISTINCT FROM (OLD.planned_on,OLD.meal_slot,OLD.kind,OLD.status,OLD.servings) THEN RETURN NEW;END IF;
  UPDATE core.meal_reservations SET state='released',release_reason='source_changed' WHERE household_id=OLD.household_id AND meal_id=OLD.id AND state='active';
 ELSE
  IF TG_OP='UPDATE' AND (NEW.servings,NEW.source_version_id,NEW.source_generation,NEW.snapshot)
   IS NOT DISTINCT FROM (OLD.servings,OLD.source_version_id,OLD.source_generation,OLD.snapshot) THEN RETURN NEW;END IF;
  IF TG_OP='INSERT' THEN h=NEW.household_id;m=NEW.meal_id;ELSE h=OLD.household_id;m=OLD.meal_id;END IF;
  UPDATE core.meal_reservations SET state='released',release_reason='source_changed' WHERE household_id=h AND meal_id=m AND state='active';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.release_changed_reservations() FROM PUBLIC,household_app;
CREATE TRIGGER planning_reservation_plan AFTER UPDATE OR DELETE ON mealplan.plans FOR EACH ROW EXECUTE FUNCTION mealplan.release_changed_reservations();
CREATE TRIGGER planning_reservation_meal AFTER UPDATE OR DELETE ON mealplan.meals FOR EACH ROW EXECUTE FUNCTION mealplan.release_changed_reservations();
CREATE TRIGGER planning_reservation_component AFTER INSERT OR UPDATE OR DELETE ON mealplan.components FOR EACH ROW EXECUTE FUNCTION mealplan.release_changed_reservations();
COMMIT;
