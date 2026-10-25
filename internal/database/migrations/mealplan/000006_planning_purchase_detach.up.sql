BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE OR REPLACE FUNCTION mealplan.guard_planning_purchase_source() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE source jsonb; enabled boolean; generation bigint;
BEGIN
 -- A reviewed detach only clears procurement ownership; every source,
 -- binding, review reason, activity and historical checkout field is retained.
 IF TG_OP='UPDATE' AND NEW.procurement_disposition='detached' AND NEW.pending_quantity=0
  AND NEW.shopping_generation IS NULL AND NEW.list_id IS NULL AND NEW.item_id IS NULL AND NEW.item_version IS NULL AND NEW.item_fingerprint IS NULL
  AND NEW.last_sync_operation_id IS DISTINCT FROM OLD.last_sync_operation_id
  AND (to_jsonb(NEW)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','last_sync_operation_id','changed_by','version','updated_at'])=
   (to_jsonb(OLD)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','last_sync_operation_id','changed_by','version','updated_at']) THEN RETURN NEW;END IF;
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
COMMIT;
