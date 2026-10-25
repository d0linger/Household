BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE core.planning_purchase_operations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 created_by uuid NOT NULL REFERENCES core.users(id), request_key uuid NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32), scope_hash bytea NOT NULL CHECK(octet_length(scope_hash)=32),
 mealplan_generation bigint NOT NULL CHECK(mealplan_generation>0), pantry_generation bigint NOT NULL CHECK(pantry_generation>0),
 shopping_generation bigint NOT NULL CHECK(shopping_generation>0), receipt_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN('sync','end','detach','checkout')),
 source_count integer NOT NULL CHECK(source_count BETWEEN 0 AND 500),
 change_count integer NOT NULL CHECK(change_count BETWEEN 0 AND source_count),
 created_count integer NOT NULL CHECK(created_count BETWEEN 0 AND change_count),
 updated_count integer NOT NULL CHECK(updated_count BETWEEN 0 AND change_count),
 removed_count integer NOT NULL CHECK(removed_count BETWEEN 0 AND change_count),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(created_count+updated_count+removed_count<=change_count),
 UNIQUE(household_id,id), CONSTRAINT planning_purchase_request UNIQUE(household_id,created_by,request_key),
 CONSTRAINT planning_purchase_receipt UNIQUE(household_id,receipt_id)
);
CREATE TABLE core.planning_purchase_requirements(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 mealplan_generation bigint NOT NULL CHECK(mealplan_generation>0), plan_id uuid NOT NULL, meal_id uuid NOT NULL,
 component_id uuid NOT NULL, ingredient_id uuid NOT NULL, source_hash bytea NOT NULL CHECK(octet_length(source_hash)=32),
 reviewed_basis_hash bytea NOT NULL CHECK(octet_length(reviewed_basis_hash)=32),
 active boolean NOT NULL DEFAULT true,
 review_reason text NOT NULL DEFAULT '' CHECK(review_reason IN('','source_missing','mapping_changed','target_missing','pantry_purged','shopping_purged','item_missing','unresolved_checkout')),
 binding_id uuid, binding_version bigint, product_id uuid, pantry_generation bigint, unit_code text REFERENCES core.units(code),
 shopping_generation bigint, list_id uuid, item_id uuid, item_version bigint, item_fingerprint bytea,
 pending_quantity numeric(18,6) NOT NULL DEFAULT 0 CHECK(pending_quantity>=0 AND pending_quantity<>'NaN'::numeric),
 procurement_disposition text NOT NULL DEFAULT 'none' CHECK(procurement_disposition IN('none','managed','stored','no_stock','skipped','missing','detached','unresolved')),
 last_sync_operation_id uuid NOT NULL,
 last_checkout_generation bigint, last_checkout_id uuid, last_checkout_item_id uuid,
 created_by uuid NOT NULL REFERENCES core.users(id), changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK((binding_id IS NULL AND binding_version IS NULL AND product_id IS NULL AND pantry_generation IS NULL AND unit_code IS NULL) OR
  (binding_id IS NOT NULL AND binding_version IS NOT NULL AND binding_version>0 AND product_id IS NOT NULL AND pantry_generation IS NOT NULL AND pantry_generation>0 AND unit_code IS NOT NULL)),
 CHECK(NOT active OR review_reason<>'' OR binding_id IS NOT NULL),
 CHECK((procurement_disposition='managed' AND active AND shopping_generation IS NOT NULL AND shopping_generation>0 AND list_id IS NOT NULL
  AND item_id IS NOT NULL AND item_version IS NOT NULL AND item_version>0 AND item_fingerprint IS NOT NULL AND octet_length(item_fingerprint)=32 AND pending_quantity>0) OR
  (procurement_disposition<>'managed' AND shopping_generation IS NULL AND list_id IS NULL AND item_id IS NULL AND item_version IS NULL AND item_fingerprint IS NULL AND pending_quantity=0)),
 CHECK((last_checkout_generation IS NULL AND last_checkout_id IS NULL AND last_checkout_item_id IS NULL) OR
  (last_checkout_generation IS NOT NULL AND last_checkout_generation>0 AND last_checkout_id IS NOT NULL AND last_checkout_item_id IS NOT NULL)),
 UNIQUE(household_id,id), CONSTRAINT planning_purchase_source UNIQUE(household_id,mealplan_generation,component_id,ingredient_id),
 FOREIGN KEY(household_id,last_sync_operation_id) REFERENCES core.planning_purchase_operations(household_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX planning_purchase_item ON core.planning_purchase_requirements(household_id,item_id) WHERE item_id IS NOT NULL;
CREATE INDEX planning_purchase_active ON core.planning_purchase_requirements(household_id,mealplan_generation,meal_id) WHERE active;
CREATE INDEX planning_purchase_component ON core.planning_purchase_requirements(household_id,component_id);
CREATE INDEX planning_purchase_product ON core.planning_purchase_requirements(household_id,product_id) WHERE product_id IS NOT NULL;
REVOKE ALL ON core.planning_purchase_operations,core.planning_purchase_requirements FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON core.planning_purchase_operations TO household_app;
GRANT SELECT,INSERT,UPDATE ON core.planning_purchase_requirements TO household_app;

CREATE FUNCTION core.guard_planning_purchase_operation() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE valid boolean; enabled boolean;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'planning purchase mutation requires read committed' USING ERRCODE='23514';END IF;
 PERFORM module_id FROM core.module_states WHERE household_id=NEW.household_id AND module_id IN('mealplan','pantry','shopping') ORDER BY module_id FOR SHARE NOWAIT;
 SELECT count(*)=3 AND bool_and(s.data_generation=r.generation),bool_and(s.state='enabled') INTO valid,enabled
 FROM (VALUES('mealplan',NEW.mealplan_generation),('pantry',NEW.pantry_generation),('shopping',NEW.shopping_generation)) r(module_id,generation)
 JOIN core.module_states s ON s.household_id=NEW.household_id AND s.module_id=r.module_id;
 IF valid IS DISTINCT FROM true OR (NEW.kind='sync' AND enabled IS DISTINCT FROM true) THEN
  RAISE EXCEPTION 'planning purchase module basis changed' USING ERRCODE='23514';END IF;
 NEW.created_at=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_planning_purchase_operation() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_operation_insert BEFORE INSERT ON core.planning_purchase_operations FOR EACH ROW EXECUTE FUNCTION core.guard_planning_purchase_operation();
CREATE TRIGGER planning_purchase_operation_immutable BEFORE UPDATE OR DELETE ON core.planning_purchase_operations FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE FUNCTION core.guard_planning_purchase_requirement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' OR NEW.active IS DISTINCT FROM OLD.active THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION 'planning purchase scope mutation requires read committed' USING ERRCODE='23514';END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('planning-purchases/'||NEW.household_id::text,0)) THEN
   RAISE EXCEPTION 'planning purchase scope is busy' USING ERRCODE='55P03';END IF;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.mealplan_generation,NEW.plan_id,NEW.meal_id,NEW.component_id,NEW.ingredient_id,NEW.source_hash,NEW.created_by,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.mealplan_generation,OLD.plan_id,OLD.meal_id,OLD.component_id,OLD.ingredient_id,OLD.source_hash,OLD.created_by,OLD.created_at) THEN
   RAISE EXCEPTION 'planning purchase source identity is immutable' USING ERRCODE='23514';END IF;
  NEW.version=OLD.version+1;
 ELSE NEW.version=1;NEW.created_at=clock_timestamp();END IF;
 NEW.updated_at=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_planning_purchase_requirement() FROM PUBLIC,household_app;
CREATE TRIGGER planning_purchase_requirement_identity BEFORE INSERT OR UPDATE ON core.planning_purchase_requirements FOR EACH ROW EXECUTE FUNCTION core.guard_planning_purchase_requirement();
CREATE INDEX planning_purchase_audit_effect ON core.audit_log(household_id,resource_id,action)
 WHERE module_id='shopping' AND resource_type='items' AND action IN('CREATE','UPDATE','DELETE');

-- Preserve the existing privacy whitelist/redaction exactly. Only Shopping item
-- events gain bounded fingerprints; full private row values never enter audit.
CREATE OR REPLACE FUNCTION core.write_audit_row(p_schema text,p_table text,p_action text,p_before jsonb,p_after jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 policy core.audit_policies%ROWTYPE;
 row_value jsonb := coalesce(p_after,p_before);
 context_value jsonb;
 changed jsonb := '{}';
 identity_key jsonb := '{}';
 column_name text;
 old_value jsonb;
 new_value jsonb;
 old_present boolean;
 new_present boolean;
 permitted boolean;
 household uuid;
 context_household uuid;
 resource uuid;
 label text;
BEGIN
 IF p_action NOT IN('CREATE','UPDATE','DELETE') THEN RAISE EXCEPTION 'invalid audit operation'; END IF;
 SELECT * INTO STRICT policy FROM core.audit_policies WHERE schema_name=p_schema AND table_name=p_table;
 FOR column_name IN SELECT jsonb_object_keys(coalesce(p_before,'{}')||coalesce(p_after,'{}')) LOOP
  IF column_name=ANY(policy.ignored_columns) THEN CONTINUE; END IF;
  old_present := coalesce(p_before ? column_name,false);
  new_present := coalesce(p_after ? column_name,false);
  old_value := p_before->column_name;
  new_value := p_after->column_name;
  IF old_present=new_present AND old_value IS NOT DISTINCT FROM new_value THEN CONTINUE; END IF;
  permitted := column_name=ANY(policy.safe_columns||ARRAY['id','household_id','created_by','version']);
  IF NOT permitted THEN
   IF old_present THEN old_value := to_jsonb('***REDACTED***'::text); END IF;
   IF new_present THEN new_value := to_jsonb('***REDACTED***'::text); END IF;
  ELSIF jsonb_typeof(old_value) IN('object','array') OR jsonb_typeof(new_value) IN('object','array') THEN
   RAISE EXCEPTION 'audit policy permits an unbounded structured value';
  ELSE
   IF jsonb_typeof(old_value)='string' THEN old_value := to_jsonb(left(old_value#>>'{}',256)); END IF;
   IF jsonb_typeof(new_value)='string' THEN new_value := to_jsonb(left(new_value#>>'{}',256)); END IF;
  END IF;
  changed := changed||jsonb_build_object(column_name,jsonb_build_object('before',old_value,'after',new_value,'before_present',old_present,'after_present',new_present,'redacted',NOT permitted));
 END LOOP;
 IF changed='{}'::jsonb THEN RETURN; END IF;
 FOREACH column_name IN ARRAY policy.key_columns LOOP
  IF NOT row_value ? column_name THEN RAISE EXCEPTION 'audit key missing'; END IF;
  identity_key := identity_key||jsonb_build_object(column_name,row_value->column_name);
 END LOOP;
 context_value := core.audit_context();
 context_household := (context_value->>'household')::uuid;
 household := (row_value->>'household_id')::uuid;
 IF p_schema='core' AND p_table='households' THEN household := (row_value->>'id')::uuid; END IF;
 IF household IS NOT NULL AND context_household IS NOT NULL AND household<>context_household THEN
  RAISE EXCEPTION 'audit household mismatch' USING ERRCODE='42501';
 END IF;
 household := coalesce(household,context_household,(SELECT id FROM core.households WHERE singleton=1));
 resource := coalesce(row_value->>'id',CASE WHEN p_table='recipe_tags' THEN row_value->>'recipe_id' ELSE row_value->>'user_id' END)::uuid;
 label := p_schema||'.'||p_table;
 IF policy.label_column IS NOT NULL AND policy.label_column=ANY(policy.safe_columns) THEN
  label := coalesce(nullif(left(row_value->>policy.label_column,160),''),label);
 ELSIF resource IS NOT NULL THEN label := label||' · '||left(resource::text,8);
 END IF;
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,actor_kind,db_role,entity_key,entity_label,changes,details,created_at)
 VALUES(household,(context_value->>'actor')::uuid,p_schema,p_table,resource,p_action,'success',(context_value->>'request')::uuid,context_value->>'kind',session_user,identity_key,label,changed,
  CASE WHEN p_schema='shopping' AND p_table='items' THEN jsonb_build_object(
   'row_before_sha256',CASE WHEN p_before IS NOT NULL THEN encode(sha256(convert_to(p_before::text,'UTF8')),'hex') END,
   'row_after_sha256',CASE WHEN p_after IS NOT NULL THEN encode(sha256(convert_to(p_after::text,'UTF8')),'hex') END) ELSE '{}'::jsonb END,clock_timestamp());
END $$;
REVOKE ALL ON FUNCTION core.write_audit_row(text,text,text,jsonb,jsonb) FROM PUBLIC,household_app;

-- Both halves schedule this check. A proof-only or unrelated-receipt commit
-- cannot become an irreversible idempotency result.
CREATE FUNCTION core.check_planning_purchase_complete() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE proof_id uuid; proof core.planning_purchase_operations%ROWTYPE; detail record; source_line jsonb; item jsonb; effect text;
 source_total bigint; distinct_sources bigint; changed bigint; added bigint; edited bigint; removed bigint; valid boolean;
BEGIN
 IF TG_TABLE_SCHEMA='core' THEN proof_id=NEW.id;ELSE proof_id=NEW.operation_id;END IF;
 SELECT * INTO proof FROM core.planning_purchase_operations WHERE household_id=NEW.household_id AND id=proof_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'planning purchase proof missing' USING ERRCODE='23514';END IF;
 SELECT r.* INTO detail FROM shopping.planning_purchase_receipts r WHERE r.household_id=NEW.household_id AND r.operation_id=proof.id;
 IF NOT FOUND OR detail.id<>proof.receipt_id OR detail.request_hash<>proof.request_hash OR detail.scope_hash<>proof.scope_hash
  OR detail.created_by<>proof.created_by OR detail.mealplan_generation<>proof.mealplan_generation OR detail.pantry_generation<>proof.pantry_generation OR detail.shopping_generation<>proof.shopping_generation THEN
  RAISE EXCEPTION 'planning purchase receipt identity mismatch' USING ERRCODE='23514';END IF;
 SELECT count(*),count(DISTINCT (entry->>'component_id',entry->>'ingredient_id')),
  count(*) FILTER(WHERE entry->'changed'='true'::jsonb),count(*) FILTER(WHERE entry->>'item_action'='create'),
  count(*) FILTER(WHERE entry->>'item_action'='update'),count(*) FILTER(WHERE entry->>'item_action'='remove'),
  coalesce(bool_and(coalesce(jsonb_typeof(entry)='object' AND jsonb_typeof(entry->'changed')='boolean'
   AND coalesce(entry->>'component_id','')~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND coalesce(entry->>'ingredient_id','')~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND coalesce(entry->>'requirement_id','')~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND coalesce(entry->>'requirement_version','')~'^[1-9][0-9]{0,18}$'
   AND entry ?& ARRAY['requirement_before','requirement_after','item_before','item_after']
   AND coalesce(entry->>'item_action','') IN('none','create','update','remove')
   AND (entry->>'item_action'='none' OR entry->'changed'='true'::jsonb),false)),true)
 INTO source_total,distinct_sources,changed,added,edited,removed,valid FROM jsonb_array_elements(detail.snapshot->'sources') entry;
 IF NOT valid OR source_total<>distinct_sources OR source_total<>proof.source_count OR changed<>proof.change_count
  OR added<>proof.created_count OR edited<>proof.updated_count OR removed<>proof.removed_count THEN
  RAISE EXCEPTION 'planning purchase receipt counters or sources mismatch' USING ERRCODE='23514';END IF;
 IF (SELECT count(*) FROM core.planning_purchase_requirements WHERE household_id=proof.household_id AND last_sync_operation_id=proof.id)<>proof.change_count
  OR EXISTS(SELECT 1 FROM jsonb_array_elements(detail.snapshot->'sources') entry WHERE NOT EXISTS(
   SELECT 1 FROM core.planning_purchase_requirements r WHERE r.household_id=proof.household_id AND r.id::text=entry->>'requirement_id'
    AND r.mealplan_generation=proof.mealplan_generation AND r.component_id::text=entry->>'component_id' AND r.ingredient_id::text=entry->>'ingredient_id'
    AND r.version::text=entry->>'requirement_version' AND entry->'requirement_after'=to_jsonb(r)
    AND (entry->'changed'='false'::jsonb OR r.last_sync_operation_id=proof.id))) THEN
  RAISE EXCEPTION 'planning purchase receipt does not describe current committed requirements' USING ERRCODE='23514';END IF;
 FOR source_line IN SELECT value FROM jsonb_array_elements(detail.snapshot->'sources') LOOP
  effect=source_line->>'item_action';
  IF source_line->'changed'='false'::jsonb AND source_line->'requirement_before' IS DISTINCT FROM source_line->'requirement_after' THEN
   RAISE EXCEPTION 'unchanged requirement receipt differs' USING ERRCODE='23514';END IF;
  IF effect='none' THEN
   IF source_line->'item_before' IS DISTINCT FROM source_line->'item_after' THEN
    RAISE EXCEPTION 'unchanged purchase receipt differs' USING ERRCODE='23514';END IF;
   IF coalesce(source_line->'requirement_after'->>'item_id',source_line->'requirement_before'->>'item_id') IS NULL THEN
    IF source_line->'item_after'<>'null'::jsonb THEN RAISE EXCEPTION 'unlinked requirement has an unrelated item receipt' USING ERRCODE='23514';END IF;
   ELSE
    SELECT to_jsonb(i) INTO item FROM shopping.items i WHERE i.household_id=proof.household_id
     AND i.id::text=coalesce(source_line->'requirement_after'->>'item_id',source_line->'requirement_before'->>'item_id');
    IF NOT FOUND OR source_line->'item_after' IS DISTINCT FROM item THEN
     RAISE EXCEPTION 'retained purchase receipt differs from its actual item' USING ERRCODE='23514';END IF;
   END IF;
  ELSE
   IF effect='create' THEN
    IF source_line->'item_before'<>'null'::jsonb OR jsonb_typeof(source_line->'item_after') IS DISTINCT FROM 'object' THEN
     RAISE EXCEPTION 'created purchase receipt requires only an after state' USING ERRCODE='23514';END IF;
   ELSIF jsonb_typeof(source_line->'item_before') IS DISTINCT FROM 'object' THEN
    RAISE EXCEPTION 'purchase change requires a before state' USING ERRCODE='23514';
   END IF;
   IF effect IN('create','update') THEN
    SELECT to_jsonb(i) INTO item FROM shopping.items i WHERE i.household_id=proof.household_id AND i.id::text=source_line->'item_after'->>'id';
    IF NOT FOUND OR item IS DISTINCT FROM source_line->'item_after' OR source_line->'requirement_after'->>'item_id'<>item->>'id'
     OR source_line->'requirement_after'->>'procurement_disposition'<>'managed' THEN
     RAISE EXCEPTION 'purchase effect has no matching managed item' USING ERRCODE='23514';END IF;
   ELSE
    IF source_line->'item_after'<>'null'::jsonb OR EXISTS(SELECT 1 FROM shopping.items i WHERE i.household_id=proof.household_id AND i.id::text=source_line->'item_before'->>'id')
     OR source_line->'requirement_after'->>'item_id' IS NOT NULL THEN
     RAISE EXCEPTION 'removed purchase receipt still has an item' USING ERRCODE='23514';END IF;
   END IF;
   IF NOT EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=proof.household_id AND a.module_id='shopping' AND a.resource_type='items'
    AND a.resource_id::text=coalesce(source_line->'item_after'->>'id',source_line->'item_before'->>'id')
    AND a.action=CASE effect WHEN 'create' THEN 'CREATE' WHEN 'update' THEN 'UPDATE' ELSE 'DELETE' END
    AND a.xmin=pg_current_xact_id()::xid AND a.created_at>=transaction_timestamp()
    AND (effect='create' OR a.changes->'version'->'before'=source_line->'item_before'->'version')
    AND a.details->>'row_before_sha256' IS NOT DISTINCT FROM CASE WHEN effect='create' THEN NULL ELSE encode(sha256(convert_to((source_line->'item_before')::text,'UTF8')),'hex') END
    AND a.details->>'row_after_sha256' IS NOT DISTINCT FROM CASE WHEN effect='remove' THEN NULL ELSE encode(sha256(convert_to((source_line->'item_after')::text,'UTF8')),'hex') END) THEN
    RAISE EXCEPTION 'purchase effect lacks same-transaction immutable audit evidence' USING ERRCODE='23514';END IF;
  END IF;
 END LOOP;
 IF EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=proof.household_id AND a.module_id='shopping' AND a.resource_type='items'
  AND a.action IN('CREATE','UPDATE','DELETE') AND a.xmin=pg_current_xact_id()::xid AND a.created_at>=transaction_timestamp()
  AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(detail.snapshot->'sources') entry
   WHERE a.resource_id::text=coalesce(entry->'item_after'->>'id',entry->'item_before'->>'id')
    AND a.action=CASE entry->>'item_action' WHEN 'create' THEN 'CREATE' WHEN 'update' THEN 'UPDATE' WHEN 'remove' THEN 'DELETE' ELSE '' END)) THEN
  RAISE EXCEPTION 'purchase operation has unreported item effects' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_planning_purchase_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_purchase_operation_complete AFTER INSERT ON core.planning_purchase_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_planning_purchase_complete();

CREATE FUNCTION core.check_planning_requirement_evidence() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.last_sync_operation_id=OLD.last_sync_operation_id THEN
  IF NEW.review_reason='source_missing' AND OLD.active AND NOT EXISTS(SELECT 1 FROM mealplan.components WHERE household_id=OLD.household_id AND id=OLD.component_id)
   AND (to_jsonb(NEW)-ARRAY['review_reason','changed_by','version','updated_at'])=(to_jsonb(OLD)-ARRAY['review_reason','changed_by','version','updated_at']) THEN RETURN NULL;END IF;
  IF OLD.product_id IS NOT NULL AND NEW.product_id IS NULL AND NEW.review_reason IN('target_missing','pantry_purged')
   AND ((NEW.review_reason='target_missing' AND NOT EXISTS(SELECT 1 FROM pantry.products WHERE household_id=OLD.household_id AND id=OLD.product_id))
    OR (NEW.review_reason='pantry_purged' AND current_user='household_owner'))
   AND (to_jsonb(NEW)-ARRAY['binding_id','binding_version','product_id','pantry_generation','unit_code','review_reason','changed_by','version','updated_at'])=
    (to_jsonb(OLD)-ARRAY['binding_id','binding_version','product_id','pantry_generation','unit_code','review_reason','changed_by','version','updated_at']) THEN RETURN NULL;END IF;
  IF OLD.item_id IS NOT NULL AND NEW.procurement_disposition='missing' AND NEW.review_reason IN('item_missing','shopping_purged')
   AND ((NEW.review_reason='item_missing' AND NOT EXISTS(SELECT 1 FROM shopping.items WHERE household_id=OLD.household_id AND id=OLD.item_id))
    OR (NEW.review_reason='shopping_purged' AND current_user='household_owner'))
   AND (to_jsonb(NEW)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at'])=
    (to_jsonb(OLD)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at'])
   AND (current_user='household_owner' OR (NEW.last_checkout_generation,NEW.last_checkout_id,NEW.last_checkout_item_id) IS NOT DISTINCT FROM (OLD.last_checkout_generation,OLD.last_checkout_id,OLD.last_checkout_item_id)) THEN RETURN NULL;END IF;
  IF current_user='household_owner' AND OLD.item_id IS NULL AND NEW.last_checkout_id IS NULL
   AND (to_jsonb(NEW)-ARRAY['last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at'])=
    (to_jsonb(OLD)-ARRAY['last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at']) THEN RETURN NULL;END IF;
  IF OLD.active AND NOT NEW.active AND NEW.procurement_disposition='detached' AND NEW.review_reason=''
   AND EXISTS(SELECT 1 FROM mealplan.meals WHERE household_id=OLD.household_id AND id=OLD.meal_id AND status IN('cooked','cancelled'))
   AND (to_jsonb(NEW)-ARRAY['active','shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','changed_by','version','updated_at'])=
    (to_jsonb(OLD)-ARRAY['active','shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','changed_by','version','updated_at']) THEN RETURN NULL;END IF;
  RAISE EXCEPTION 'reviewed planning changes require a new operation' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM core.planning_purchase_operations p JOIN shopping.planning_purchase_receipts r
  ON r.household_id=p.household_id AND r.operation_id=p.id AND r.id=p.receipt_id
  CROSS JOIN LATERAL jsonb_array_elements(r.snapshot->'sources') entry
  WHERE p.household_id=NEW.household_id AND p.id=NEW.last_sync_operation_id AND p.mealplan_generation=NEW.mealplan_generation
   AND entry->>'requirement_id'=NEW.id::text AND entry->>'requirement_version'=NEW.version::text
   AND entry->>'component_id'=NEW.component_id::text AND entry->>'ingredient_id'=NEW.ingredient_id::text AND entry->'changed'='true'::jsonb
   AND entry->'requirement_after'=to_jsonb(NEW)
   AND entry->'requirement_before'=CASE WHEN TG_OP='INSERT' THEN 'null'::jsonb ELSE to_jsonb(OLD) END
   AND (entry->>'item_action' NOT IN('update','remove') OR entry->'item_before'->>'id'=OLD.item_id::text)
   AND (entry->>'item_action'<>'create' OR TG_OP='INSERT' OR OLD.item_id IS NULL)) THEN
  RAISE EXCEPTION 'changed planning requirement needs matching operation evidence' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_planning_requirement_evidence() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_purchase_requirement_evidence AFTER INSERT OR UPDATE ON core.planning_purchase_requirements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_planning_requirement_evidence();

CREATE FUNCTION core.check_planning_checkout_evidence() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE purchase jsonb; line jsonb; total bigint; disposition text;
BEGIN
 IF NEW.last_checkout_id IS NULL THEN RETURN NULL;END IF;
 IF TG_OP='UPDATE' AND (NEW.last_checkout_generation,NEW.last_checkout_id,NEW.last_checkout_item_id) IS NOT DISTINCT FROM
  (OLD.last_checkout_generation,OLD.last_checkout_id,OLD.last_checkout_item_id) THEN RETURN NULL;END IF;
 IF TG_OP='INSERT' OR OLD.item_id IS NULL OR NEW.last_checkout_item_id IS DISTINCT FROM OLD.item_id OR NEW.last_checkout_generation IS DISTINCT FROM OLD.shopping_generation
  OR (to_jsonb(NEW)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','last_sync_operation_id','last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at']) IS DISTINCT FROM
   (to_jsonb(OLD)-ARRAY['shopping_generation','list_id','item_id','item_version','item_fingerprint','pending_quantity','procurement_disposition','review_reason','last_sync_operation_id','last_checkout_generation','last_checkout_id','last_checkout_item_id','changed_by','version','updated_at']) THEN
  RAISE EXCEPTION 'planning checkout must retain requirement identity and activity' USING ERRCODE='23514';END IF;
 SELECT snapshot INTO purchase FROM shopping.checkouts WHERE household_id=NEW.household_id AND id=NEW.last_checkout_id AND list_id=OLD.list_id AND created_by=NEW.changed_by AND xmin=pg_current_xact_id()::xid;
 IF NOT FOUND OR jsonb_typeof(purchase->'lines') IS DISTINCT FROM 'array' THEN
  RAISE EXCEPTION 'planning checkout receipt missing' USING ERRCODE='23514';END IF;
 SELECT count(*) INTO total FROM jsonb_array_elements(purchase->'lines') value WHERE value->>'item_id'=OLD.item_id::text;
 IF total<>1 THEN RAISE EXCEPTION 'planning checkout line is ambiguous' USING ERRCODE='23514';END IF;
 SELECT value INTO line FROM jsonb_array_elements(purchase->'lines') value WHERE value->>'item_id'=OLD.item_id::text;
 IF (SELECT count(*)<>count(DISTINCT value->'stock'->>'movement_id') FROM jsonb_array_elements(purchase->'lines') value WHERE value->>'decision'='store') THEN
  RAISE EXCEPTION 'planning checkout reuses or omits a stock movement' USING ERRCODE='23514';END IF;
 IF line->>'decision'='store' THEN
  IF NOT EXISTS(SELECT 1 FROM pantry.stock_movements m JOIN pantry.lots l ON l.household_id=m.household_id AND l.id=m.lot_id
   JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
   WHERE m.household_id=NEW.household_id AND m.id::text=line->'stock'->>'movement_id' AND l.id::text=line->'stock'->>'lot_id'
    AND p.id::text=line->'stock'->>'product_id' AND p.unit_code=line->'stock'->>'unit' AND m.quantity_delta::text=line->'stock'->>'quantity' AND m.reason='receive'
    AND m.correlation_id=NEW.last_checkout_id AND m.created_by=NEW.changed_by AND m.xmin=pg_current_xact_id()::xid
    AND EXISTS(SELECT 1 FROM core.resource_links link WHERE link.household_id=NEW.household_id AND link.source_module='shopping' AND link.source_type='checkouts'
     AND link.source_id=NEW.last_checkout_id AND link.target_module='pantry' AND link.target_type='lots' AND link.target_id=l.id AND link.relation='shopping_stock')) THEN
   RAISE EXCEPTION 'planning checkout stock evidence missing' USING ERRCODE='23514';END IF;
  IF line->'stock'->>'product_id'=OLD.product_id::text AND line->'stock'->>'unit'=OLD.unit_code AND purchase->>'pantry_generation'=OLD.pantry_generation::text THEN
   disposition='stored';
  ELSE disposition='unresolved';END IF;
 ELSIF line->>'decision'='no_stock' THEN disposition='no_stock';
 ELSIF line->>'decision'='skip' THEN disposition='skipped';
 ELSE RAISE EXCEPTION 'planning checkout decision missing' USING ERRCODE='23514';END IF;
 IF NEW.procurement_disposition<>disposition OR NEW.pending_quantity<>0 OR NEW.item_id IS NOT NULL
  OR (disposition<>'stored' AND NEW.review_reason<>'unresolved_checkout') OR (disposition='stored' AND NEW.review_reason<>OLD.review_reason) THEN
  RAISE EXCEPTION 'planning checkout disposition differs from actual outcome' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_planning_checkout_evidence() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_purchase_checkout_evidence AFTER INSERT OR UPDATE ON core.planning_purchase_requirements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_planning_checkout_evidence();

CREATE FUNCTION core.planning_purchases_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.planning_purchase_operations','planning_purchase_operation_insert','core.guard_planning_purchase_operation()',7),
 ('core.planning_purchase_operations','planning_purchase_operation_complete','core.check_planning_purchase_complete()',5),
 ('core.planning_purchase_requirements','planning_purchase_requirement_identity','core.guard_planning_purchase_requirement()',23),
 ('core.planning_purchase_requirements','planning_purchase_requirement_evidence','core.check_planning_requirement_evidence()',21),
 ('core.planning_purchase_requirements','planning_purchase_checkout_evidence','core.check_planning_checkout_evidence()',21),
 ('core.planning_purchase_requirements','planning_purchase_requirement_source','mealplan.guard_planning_purchase_source()',23),
 ('core.planning_purchase_requirements','planning_purchase_requirement_target','pantry.guard_planning_purchase_target()',23),
 ('core.planning_purchase_requirements','planning_purchase_requirement_destination','shopping.guard_planning_purchase_destination()',23),
 ('core.planning_purchase_requirements','planning_purchase_whole_meal','mealplan.check_planning_purchase_whole_meal()',21),
 ('shopping.planning_purchase_receipts','planning_purchase_receipt_insert','shopping.guard_planning_purchase_receipt()',7),
 ('shopping.planning_purchase_receipts','planning_purchase_receipt_complete','core.check_planning_purchase_complete()',5),
 ('shopping.items','planning_purchase_item_delete','shopping.invalidate_deleted_purchase_item()',9),
 ('mealplan.components','planning_purchase_source_delete','mealplan.invalidate_deleted_purchase_source()',9),
 ('mealplan.meals','planning_purchase_meal_complete','mealplan.end_completed_purchase_requirements()',17),
 ('pantry.products','planning_purchase_target_delete','pantry.invalidate_deleted_purchase_target()',9)
 ) required(relation,name,function_id,trigger_type) WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
  WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgfoid=to_regprocedure(required.function_id)
   AND t.tgtype=required.trigger_type AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
   AND pg_get_userbyid(p.proowner)='household_owner' AND NOT has_function_privilege('household_app',p.oid,'EXECUTE')))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.planning_purchase_operations','planning_purchase_operation_complete'),('core.planning_purchase_requirements','planning_purchase_requirement_evidence'),
  ('core.planning_purchase_requirements','planning_purchase_checkout_evidence'),
  ('core.planning_purchase_requirements','planning_purchase_whole_meal'),('shopping.planning_purchase_receipts','planning_purchase_receipt_complete')) required(relation,name)
  WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgdeferrable AND t.tginitdeferred))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.planning_purchase_operations','planning_purchase_operation_immutable'),('shopping.planning_purchase_receipts','planning_purchase_receipt_immutable')) required(relation,name)
  WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgfoid='core.reject_mutation()'::regprocedure AND t.tgtype=27 AND t.tgenabled IN('O','A')))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.planning_purchase_operations','SELECT'),('core.planning_purchase_operations','INSERT'),('core.planning_purchase_requirements','SELECT'),
  ('core.planning_purchase_requirements','INSERT'),('core.planning_purchase_requirements','UPDATE'),
  ('shopping.planning_purchase_receipts','SELECT'),('shopping.planning_purchase_receipts','INSERT')) required(relation,privilege)
  WHERE NOT has_table_privilege('household_app',required.relation,required.privilege))
 AND NOT has_table_privilege('household_app','core.planning_purchase_operations','UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES')
 AND NOT has_table_privilege('household_app','core.planning_purchase_requirements','DELETE,TRUNCATE,TRIGGER,REFERENCES')
 AND NOT has_table_privilege('household_app','shopping.planning_purchase_receipts','UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES')
 AND NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.planning_purchase_operations','planning_purchase_request',ARRAY['household_id','created_by','request_key']),
  ('core.planning_purchase_operations','planning_purchase_receipt',ARRAY['household_id','receipt_id']),
  ('core.planning_purchase_requirements','planning_purchase_source',ARRAY['household_id','mealplan_generation','component_id','ingredient_id']),
  ('shopping.planning_purchase_receipts','planning_purchase_operation_receipt',ARRAY['household_id','operation_id'])) required(relation,name,columns)
  WHERE NOT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid
   WHERE c.conrelid=to_regclass(required.relation) AND c.conname=required.name AND c.contype='u' AND c.convalidated AND i.indisvalid AND i.indisready AND i.indisunique
    AND (SELECT array_agg(a.attname::text ORDER BY key.position) FROM unnest(c.conkey) WITH ORDINALITY key(number,position)
     JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.number)=required.columns))
 AND EXISTS(SELECT 1 FROM pg_index i WHERE i.indexrelid='core.planning_purchase_item'::regclass AND i.indisvalid AND i.indisready AND i.indisunique AND i.indnkeyatts=2
  AND pg_get_expr(i.indpred,i.indrelid)='(item_id IS NOT NULL)'
  AND (SELECT array_agg(a.attname::text ORDER BY key.position) FROM unnest(i.indkey) WITH ORDINALITY key(number,position)
   JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=key.number WHERE key.position<=i.indnkeyatts)=ARRAY['household_id','item_id'])
$$;
REVOKE ALL ON FUNCTION core.planning_purchases_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.planning_purchases_ready() TO household_app;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','planning_purchase_operations',ARRAY['id'],ARRAY['mealplan_generation','pantry_generation','shopping_generation','kind','source_count','change_count','created_count','updated_count','removed_count'],NULL),
 ('core','planning_purchase_requirements',ARRAY['id'],ARRAY['mealplan_generation','plan_id','meal_id','component_id','ingredient_id','active','review_reason','product_id','pantry_generation','shopping_generation','procurement_disposition','version'],NULL),
 ('shopping','planning_purchase_receipts',ARRAY['id'],ARRAY['operation_id'],NULL);
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=73 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types')
  AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;
COMMIT;
