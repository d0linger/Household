BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE shopping.planning_purchase_receipts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), operation_id uuid NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32), scope_hash bytea NOT NULL CHECK(octet_length(scope_hash)=32),
 mealplan_generation bigint NOT NULL CHECK(mealplan_generation>0), pantry_generation bigint NOT NULL CHECK(pantry_generation>0),
 shopping_generation bigint NOT NULL CHECK(shopping_generation>0),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=4194304
  AND CASE WHEN jsonb_typeof(snapshot->'sources')='array' THEN jsonb_array_length(snapshot->'sources')<=500 ELSE false END),
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id), CONSTRAINT planning_purchase_operation_receipt UNIQUE(household_id,operation_id),
 FOREIGN KEY(household_id,operation_id) REFERENCES core.planning_purchase_operations(household_id,id) DEFERRABLE INITIALLY DEFERRED
);
REVOKE ALL ON shopping.planning_purchase_receipts FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON shopping.planning_purchase_receipts TO household_app;
CREATE FUNCTION shopping.guard_planning_purchase_receipt() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN NEW.created_at=clock_timestamp();RETURN NEW;END $$;
REVOKE ALL ON FUNCTION shopping.guard_planning_purchase_receipt() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_receipt_insert BEFORE INSERT ON shopping.planning_purchase_receipts FOR EACH ROW EXECUTE FUNCTION shopping.guard_planning_purchase_receipt();
CREATE TRIGGER planning_purchase_receipt_immutable BEFORE UPDATE OR DELETE ON shopping.planning_purchase_receipts FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE CONSTRAINT TRIGGER planning_purchase_receipt_complete AFTER INSERT ON shopping.planning_purchase_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_planning_purchase_complete();

CREATE FUNCTION shopping.guard_planning_purchase_destination() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE generation bigint; enabled boolean; actual_quantity numeric; actual_unit text; actual_version bigint; fingerprint bytea;
BEGIN
 IF NEW.procurement_disposition<>'managed' THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND (NEW.shopping_generation,NEW.list_id,NEW.item_id,NEW.item_version,NEW.item_fingerprint,NEW.pending_quantity,NEW.last_sync_operation_id)
  IS NOT DISTINCT FROM (OLD.shopping_generation,OLD.list_id,OLD.item_id,OLD.item_version,OLD.item_fingerprint,OLD.pending_quantity,OLD.last_sync_operation_id) THEN RETURN NEW;END IF;
 SELECT data_generation,state='enabled' INTO generation,enabled FROM core.module_states WHERE household_id=NEW.household_id AND module_id='shopping' FOR SHARE NOWAIT;
 IF enabled IS DISTINCT FROM true OR generation IS DISTINCT FROM NEW.shopping_generation THEN
  RAISE EXCEPTION 'planning purchase shopping generation changed' USING ERRCODE='23514';END IF;
 PERFORM id FROM shopping.lists WHERE household_id=NEW.household_id AND id=NEW.list_id AND archived_at IS NULL AND NOT is_template FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase open destination missing' USING ERRCODE='23514';END IF;
 SELECT i.quantity,i.unit_code,i.version,sha256(convert_to(to_jsonb(i)::text,'UTF8')) INTO actual_quantity,actual_unit,actual_version,fingerprint
  FROM shopping.items i WHERE i.household_id=NEW.household_id AND i.list_id=NEW.list_id AND i.id=NEW.item_id AND i.checked_at IS NULL FOR UPDATE NOWAIT;
 IF NOT FOUND OR actual_quantity IS DISTINCT FROM NEW.pending_quantity OR actual_unit IS DISTINCT FROM NEW.unit_code
  OR actual_version IS DISTINCT FROM NEW.item_version OR fingerprint IS DISTINCT FROM NEW.item_fingerprint THEN
  RAISE EXCEPTION 'planning purchase reviewed destination changed' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION shopping.guard_planning_purchase_destination() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_requirement_destination BEFORE INSERT OR UPDATE ON core.planning_purchase_requirements FOR EACH ROW EXECUTE FUNCTION shopping.guard_planning_purchase_destination();

CREATE FUNCTION shopping.invalidate_deleted_purchase_item() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP<>'DELETE' OR TG_WHEN<>'AFTER' OR TG_LEVEL<>'ROW' OR TG_RELID<>'shopping.items'::regclass THEN
  RAISE EXCEPTION 'invalid planning purchase destination cleanup context' USING ERRCODE='23514';END IF;
 UPDATE core.planning_purchase_requirements SET procurement_disposition='missing',review_reason='item_missing',
  shopping_generation=NULL,list_id=NULL,item_id=NULL,item_version=NULL,item_fingerprint=NULL,pending_quantity=0,
  changed_by=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,changed_by)
  WHERE household_id=OLD.household_id AND item_id=OLD.id;
 RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION shopping.invalidate_deleted_purchase_item() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_item_delete AFTER DELETE ON shopping.items FOR EACH ROW EXECUTE FUNCTION shopping.invalidate_deleted_purchase_item();
COMMIT;
