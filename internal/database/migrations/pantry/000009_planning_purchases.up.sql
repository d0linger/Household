BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION pantry.guard_planning_purchase_target() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE generation bigint; enabled boolean; actual_unit text; archived boolean; integer_amount boolean;
BEGIN
 IF NEW.product_id IS NULL THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND (NEW.product_id,NEW.pantry_generation,NEW.unit_code,NEW.pending_quantity,NEW.procurement_disposition,NEW.last_sync_operation_id)
  IS NOT DISTINCT FROM (OLD.product_id,OLD.pantry_generation,OLD.unit_code,OLD.pending_quantity,OLD.procurement_disposition,OLD.last_sync_operation_id) THEN RETURN NEW;END IF;
 -- Terminal checkout/end transitions do not read or lock the original target.
 IF TG_OP='UPDATE' AND NOT NEW.active AND NEW.procurement_disposition='detached' AND NEW.pending_quantity=0 THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND NEW.last_checkout_id IS NOT NULL AND NEW.last_checkout_id IS DISTINCT FROM OLD.last_checkout_id AND NEW.procurement_disposition<>'managed' AND NEW.pending_quantity=0 THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND NEW.procurement_disposition<>'managed' AND NEW.pending_quantity=0 AND NEW.last_sync_operation_id=OLD.last_sync_operation_id THEN RETURN NEW;END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states WHERE household_id=NEW.household_id AND module_id='pantry' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.pantry_generation THEN
  RAISE EXCEPTION 'planning purchase pantry generation changed' USING ERRCODE='23514';END IF;
 SELECT unit_code,archived_at IS NOT NULL,integer_quantities INTO actual_unit,archived,integer_amount FROM pantry.products
  WHERE household_id=NEW.household_id AND id=NEW.product_id FOR SHARE NOWAIT;
 IF NOT FOUND OR archived OR actual_unit IS DISTINCT FROM NEW.unit_code OR (integer_amount AND NEW.pending_quantity<>trunc(NEW.pending_quantity)) THEN
  RAISE EXCEPTION 'planning purchase requires active matching product and exact quantity' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_planning_purchase_target() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_requirement_target BEFORE INSERT OR UPDATE ON core.planning_purchase_requirements FOR EACH ROW EXECUTE FUNCTION pantry.guard_planning_purchase_target();
CREATE FUNCTION pantry.invalidate_deleted_purchase_target() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP<>'DELETE' OR TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_RELID<>'pantry.products'::regclass THEN
  RAISE EXCEPTION 'invalid planning purchase target cleanup context' USING ERRCODE='23514';END IF;
 UPDATE core.planning_purchase_requirements SET binding_id=NULL,binding_version=NULL,product_id=NULL,pantry_generation=NULL,unit_code=NULL,
  review_reason='target_missing',changed_by=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,changed_by)
  WHERE household_id=OLD.household_id AND product_id=OLD.id;
 RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION pantry.invalidate_deleted_purchase_target() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_target_delete AFTER DELETE ON pantry.products FOR EACH ROW EXECUTE FUNCTION pantry.invalidate_deleted_purchase_target();
COMMIT;
