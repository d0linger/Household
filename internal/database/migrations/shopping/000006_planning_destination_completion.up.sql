BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE OR REPLACE FUNCTION shopping.guard_planning_purchase_destination() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE generation bigint; enabled boolean; actual_quantity numeric; actual_unit text; actual_version bigint; fingerprint bytea;
BEGIN
 IF NEW.procurement_disposition<>'managed' THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND (NEW.shopping_generation,NEW.list_id,NEW.item_id,NEW.item_version,NEW.item_fingerprint,NEW.pending_quantity,NEW.last_sync_operation_id)
  IS NOT DISTINCT FROM (OLD.shopping_generation,OLD.list_id,OLD.item_id,OLD.item_version,OLD.item_fingerprint,OLD.pending_quantity,OLD.last_sync_operation_id) THEN RETURN NEW;END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states WHERE household_id=NEW.household_id AND module_id='shopping' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.shopping_generation THEN
  RAISE EXCEPTION 'planning purchase shopping generation changed' USING ERRCODE='23514';END IF;
 PERFORM id FROM shopping.lists l WHERE l.household_id=NEW.household_id AND l.id=NEW.list_id AND l.archived_at IS NULL AND NOT l.is_template
  AND NOT EXISTS(SELECT 1 FROM shopping.checkouts c WHERE c.household_id=l.household_id AND c.list_id=l.id) FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase uncompleted open destination missing' USING ERRCODE='23514';END IF;
 SELECT i.quantity,i.unit_code,i.version,sha256(convert_to(to_jsonb(i)::text,'UTF8')) INTO actual_quantity,actual_unit,actual_version,fingerprint
  FROM shopping.items i WHERE i.household_id=NEW.household_id AND i.list_id=NEW.list_id AND i.id=NEW.item_id AND i.checked_at IS NULL FOR UPDATE NOWAIT;
 IF NOT FOUND OR actual_quantity IS DISTINCT FROM NEW.pending_quantity OR actual_unit IS DISTINCT FROM NEW.unit_code
  OR actual_version IS DISTINCT FROM NEW.item_version OR fingerprint IS DISTINCT FROM NEW.item_fingerprint THEN
  RAISE EXCEPTION 'planning purchase reviewed destination changed' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION shopping.guard_planning_purchase_destination() FROM PUBLIC,household_app;
COMMIT;
