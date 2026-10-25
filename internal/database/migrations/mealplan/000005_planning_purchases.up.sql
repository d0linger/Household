BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION mealplan.guard_planning_purchase_source() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE source jsonb; enabled boolean; generation bigint;
BEGIN
 -- Checkout and targeted invalidation must not acquire parent locks inversely.
 IF TG_OP='UPDATE' THEN
  IF NEW.last_checkout_id IS NOT NULL AND NEW.last_checkout_id IS DISTINCT FROM OLD.last_checkout_id AND NEW.procurement_disposition<>'managed' AND NEW.pending_quantity=0 THEN RETURN NEW;END IF;
  IF NOT NEW.active OR NEW.review_reason<>'' OR
   (NEW.active,NEW.reviewed_basis_hash,NEW.binding_id,NEW.binding_version,NEW.product_id,NEW.pantry_generation,NEW.unit_code,NEW.last_sync_operation_id)
    IS NOT DISTINCT FROM (OLD.active,OLD.reviewed_basis_hash,OLD.binding_id,OLD.binding_version,OLD.product_id,OLD.pantry_generation,OLD.unit_code,OLD.last_sync_operation_id) THEN RETURN NEW;END IF;
 END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states WHERE household_id=NEW.household_id AND module_id='mealplan' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.mealplan_generation THEN
  RAISE EXCEPTION 'planning purchase source generation changed' USING ERRCODE='23514';END IF;
 PERFORM id FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id AND NOT is_template FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase working plan missing' USING ERRCODE='23514';END IF;
 PERFORM id FROM mealplan.meals WHERE household_id=NEW.household_id AND id=NEW.meal_id AND plan_id=NEW.plan_id AND kind='recipe' AND status IN('open','planned') FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase whole recipe meal missing' USING ERRCODE='23514';END IF;
 SELECT snapshot INTO source FROM mealplan.components WHERE household_id=NEW.household_id AND meal_id=NEW.meal_id AND id=NEW.component_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR sha256(convert_to(source::text,'UTF8')) IS DISTINCT FROM NEW.source_hash THEN
  RAISE EXCEPTION 'planning purchase frozen source changed' USING ERRCODE='23514';END IF;
 PERFORM id FROM core.planning_ingredient_bindings WHERE household_id=NEW.household_id AND id=NEW.binding_id AND version=NEW.binding_version
  AND mealplan_generation=NEW.mealplan_generation AND plan_id=NEW.plan_id AND meal_id=NEW.meal_id AND component_id=NEW.component_id
  AND ingredient_id=NEW.ingredient_id AND source_hash=NEW.source_hash AND product_id=NEW.product_id AND pantry_generation=NEW.pantry_generation AND unit_code=NEW.unit_code FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase confirmed binding changed' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_planning_purchase_source() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_requirement_source BEFORE INSERT OR UPDATE ON core.planning_purchase_requirements FOR EACH ROW EXECUTE FUNCTION mealplan.guard_planning_purchase_source();

CREATE FUNCTION mealplan.check_planning_purchase_whole_meal() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE total bigint; matched bigint;
BEGIN
 IF TG_OP='UPDATE' AND NEW.active=OLD.active THEN RETURN NULL;END IF;
 IF NOT NEW.active THEN
  IF EXISTS(SELECT 1 FROM core.planning_purchase_requirements WHERE household_id=NEW.household_id AND mealplan_generation=NEW.mealplan_generation AND meal_id=NEW.meal_id AND active) THEN
   IF EXISTS(SELECT 1 FROM core.planning_purchase_operations WHERE household_id=NEW.household_id AND id=NEW.last_sync_operation_id AND kind='end') THEN
    RAISE EXCEPTION 'planning purchase ending must cover the whole meal' USING ERRCODE='23514';END IF;
  ELSE RETURN NULL;END IF;
 END IF;
 IF (SELECT count(*)>500 OR count(DISTINCT meal_id)>42 FROM core.planning_purchase_requirements WHERE household_id=NEW.household_id AND active) THEN
  RAISE EXCEPTION 'planning purchase active scope exceeds its bound' USING ERRCODE='23514';END IF;
 SELECT count(*) INTO total FROM mealplan.components c CROSS JOIN LATERAL jsonb_array_elements(c.snapshot->'ingredients') ingredient
  WHERE c.household_id=NEW.household_id AND c.meal_id=NEW.meal_id;
 IF EXISTS(SELECT 1 FROM mealplan.components WHERE household_id=NEW.household_id AND meal_id=NEW.meal_id AND jsonb_array_length(snapshot->'ingredients')=0) THEN
  RAISE EXCEPTION 'planning purchase meal contains an empty ingredient source' USING ERRCODE='23514';END IF;
 SELECT count(*) INTO matched FROM core.planning_purchase_requirements r JOIN mealplan.components c
  ON c.household_id=r.household_id AND c.meal_id=r.meal_id AND c.id=r.component_id
  CROSS JOIN LATERAL jsonb_array_elements(c.snapshot->'ingredients') ingredient
  WHERE r.household_id=NEW.household_id AND r.mealplan_generation=NEW.mealplan_generation AND r.meal_id=NEW.meal_id AND r.active
   AND ingredient->>'id'=r.ingredient_id::text AND r.source_hash=sha256(convert_to(c.snapshot::text,'UTF8'));
 IF total=0 OR total>500 OR matched<>total OR matched<>(SELECT count(*) FROM core.planning_purchase_requirements WHERE household_id=NEW.household_id AND mealplan_generation=NEW.mealplan_generation AND meal_id=NEW.meal_id AND active) THEN
  RAISE EXCEPTION 'planning purchase registration requires every ingredient of the whole meal' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.check_planning_purchase_whole_meal() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_purchase_whole_meal AFTER INSERT OR UPDATE ON core.planning_purchase_requirements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.check_planning_purchase_whole_meal();

CREATE FUNCTION mealplan.invalidate_deleted_purchase_source() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP<>'DELETE' OR TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_RELID<>'mealplan.components'::regclass THEN
  RAISE EXCEPTION 'invalid planning purchase cleanup context' USING ERRCODE='23514';END IF;
 UPDATE core.planning_purchase_requirements SET review_reason='source_missing',changed_by=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,changed_by)
  WHERE household_id=OLD.household_id AND component_id=OLD.id AND active AND review_reason<>'source_missing';
 RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION mealplan.invalidate_deleted_purchase_source() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_source_delete AFTER DELETE ON mealplan.components FOR EACH ROW EXECUTE FUNCTION mealplan.invalidate_deleted_purchase_source();

CREATE FUNCTION mealplan.end_completed_purchase_requirements() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.status IN('cooked','cancelled') AND OLD.status NOT IN('cooked','cancelled') THEN
  UPDATE core.planning_purchase_requirements SET active=false,review_reason='',procurement_disposition='detached',
   shopping_generation=NULL,list_id=NULL,item_id=NULL,item_version=NULL,item_fingerprint=NULL,pending_quantity=0,
   changed_by=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,changed_by)
   WHERE household_id=NEW.household_id AND meal_id=NEW.id AND active;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.end_completed_purchase_requirements() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_meal_complete AFTER UPDATE ON mealplan.meals FOR EACH ROW EXECUTE FUNCTION mealplan.end_completed_purchase_requirements();
COMMIT;
