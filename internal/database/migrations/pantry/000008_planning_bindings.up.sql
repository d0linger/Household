BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION pantry.guard_planning_ingredient_binding() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE generation bigint; enabled boolean; actual_unit text; archived boolean;
BEGIN
 IF NEW.product_id IS NULL AND NEW.pantry_generation IS NULL AND NEW.unit_code IS NULL THEN RETURN NEW;END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states
  WHERE household_id=NEW.household_id AND module_id='pantry' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.pantry_generation THEN
  RAISE EXCEPTION 'pantry generation changed' USING ERRCODE='23514';END IF;
 SELECT unit_code,archived_at IS NOT NULL INTO actual_unit,archived FROM pantry.products
  WHERE household_id=NEW.household_id AND id=NEW.product_id FOR SHARE NOWAIT;
 IF NOT FOUND OR archived OR actual_unit IS DISTINCT FROM NEW.unit_code THEN
  RAISE EXCEPTION 'active matching product and base unit required' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_planning_ingredient_binding() FROM PUBLIC,household_app;
CREATE TRIGGER planning_binding_target BEFORE INSERT OR UPDATE ON core.planning_ingredient_bindings FOR EACH ROW EXECUTE FUNCTION pantry.guard_planning_ingredient_binding();
CREATE FUNCTION pantry.clear_deleted_planning_bindings() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP<>'DELETE' OR TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_RELID<>'pantry.products'::regclass THEN
  RAISE EXCEPTION 'invalid planning target cleanup context' USING ERRCODE='23514';END IF;
 UPDATE core.planning_ingredient_bindings SET product_id=NULL,pantry_generation=NULL,unit_code=NULL,
  changed_by=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,changed_by)
  WHERE household_id=OLD.household_id AND product_id=OLD.id;
 RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION pantry.clear_deleted_planning_bindings() FROM PUBLIC,household_app;
CREATE TRIGGER planning_binding_product_delete AFTER DELETE ON pantry.products FOR EACH ROW EXECUTE FUNCTION pantry.clear_deleted_planning_bindings();
COMMIT;
