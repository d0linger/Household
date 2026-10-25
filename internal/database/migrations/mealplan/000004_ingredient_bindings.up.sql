BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION mealplan.guard_planning_ingredient_binding() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE generation bigint; enabled boolean; source jsonb; total bigint; distinct_ids bigint; valid_ids boolean; matches bigint;
BEGIN
 -- Unmap preserves identity and never takes parent locks: product cleanup may
 -- hold a product lock while planning is disabled or being edited.
 IF TG_OP='UPDATE' AND NEW.product_id IS NULL AND NEW.pantry_generation IS NULL AND NEW.unit_code IS NULL THEN RETURN NEW;END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states
  WHERE household_id=NEW.household_id AND module_id='mealplan' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.mealplan_generation THEN
  RAISE EXCEPTION 'planning generation changed' USING ERRCODE='23514';END IF;
 PERFORM id FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning source missing' USING ERRCODE='23514';END IF;
 PERFORM id FROM mealplan.meals WHERE household_id=NEW.household_id AND plan_id=NEW.plan_id AND id=NEW.meal_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning meal missing' USING ERRCODE='23514';END IF;
 SELECT snapshot INTO source FROM mealplan.components WHERE household_id=NEW.household_id AND meal_id=NEW.meal_id AND id=NEW.component_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR sha256(convert_to(source::text,'UTF8')) IS DISTINCT FROM NEW.source_hash OR jsonb_typeof(source->'ingredients') IS DISTINCT FROM 'array' THEN
  RAISE EXCEPTION 'planning ingredient source changed' USING ERRCODE='23514';END IF;
 SELECT count(*),count(DISTINCT item->>'id'),coalesce(bool_and(coalesce(jsonb_typeof(item)='object' AND
  coalesce(item->>'id','')~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND
  jsonb_typeof(item->'name')='string' AND length(btrim(item->>'name')) BETWEEN 1 AND 200,false)),false),
  count(*) FILTER(WHERE item->>'id'=NEW.ingredient_id::text) INTO total,distinct_ids,valid_ids,matches FROM jsonb_array_elements(source->'ingredients') item;
 IF total>500 OR total<>distinct_ids OR NOT valid_ids OR matches<>1 THEN
  RAISE EXCEPTION 'planning ingredient identity is incomplete or ambiguous' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_planning_ingredient_binding() FROM PUBLIC,household_app;
CREATE TRIGGER planning_binding_source BEFORE INSERT OR UPDATE ON core.planning_ingredient_bindings FOR EACH ROW EXECUTE FUNCTION mealplan.guard_planning_ingredient_binding();

-- The existing meal->component cascade also covers removal of a meal. A plan
-- cannot be removed while it still has meals. Only the deleted component ID is
-- used; no broad orphan sweep can erase a concurrently confirmed mapping.
CREATE FUNCTION mealplan.remove_deleted_ingredient_bindings() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP<>'DELETE' OR TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_RELID<>'mealplan.components'::regclass THEN
  RAISE EXCEPTION 'invalid planning cleanup context' USING ERRCODE='23514';END IF;
 DELETE FROM core.planning_ingredient_bindings WHERE household_id=OLD.household_id AND component_id=OLD.id;
 RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION mealplan.remove_deleted_ingredient_bindings() FROM PUBLIC,household_app;
CREATE TRIGGER planning_binding_component_delete AFTER DELETE ON mealplan.components FOR EACH ROW EXECUTE FUNCTION mealplan.remove_deleted_ingredient_bindings();
COMMIT;
