BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE core.planning_ingredient_bindings(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 mealplan_generation bigint NOT NULL CHECK(mealplan_generation>0), plan_id uuid NOT NULL, meal_id uuid NOT NULL,
 component_id uuid NOT NULL, ingredient_id uuid NOT NULL,
 source_hash bytea NOT NULL CHECK(octet_length(source_hash)=32),
 product_id uuid, pantry_generation bigint, unit_code text REFERENCES core.units(code),
 created_by uuid NOT NULL REFERENCES core.users(id), changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK((product_id IS NULL AND pantry_generation IS NULL AND unit_code IS NULL) OR
  (product_id IS NOT NULL AND pantry_generation IS NOT NULL AND pantry_generation>0 AND unit_code IS NOT NULL)),
 UNIQUE(household_id,id), CONSTRAINT planning_ingredient_source UNIQUE(household_id,mealplan_generation,component_id,ingredient_id)
);
CREATE INDEX planning_ingredient_component ON core.planning_ingredient_bindings(household_id,component_id);
CREATE INDEX planning_ingredient_target ON core.planning_ingredient_bindings(household_id,product_id) WHERE product_id IS NOT NULL;
REVOKE ALL ON core.planning_ingredient_bindings FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE ON core.planning_ingredient_bindings TO household_app;
CREATE FUNCTION core.guard_planning_ingredient_binding() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.mealplan_generation,NEW.plan_id,NEW.meal_id,NEW.component_id,NEW.ingredient_id,NEW.source_hash,NEW.created_by,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.mealplan_generation,OLD.plan_id,OLD.meal_id,OLD.component_id,OLD.ingredient_id,OLD.source_hash,OLD.created_by,OLD.created_at) THEN
   RAISE EXCEPTION 'planning ingredient identity is immutable' USING ERRCODE='23514';
  END IF;
  NEW.version=OLD.version+1;
 ELSE
  NEW.version=1;NEW.created_at=clock_timestamp();
 END IF;
 NEW.updated_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_planning_ingredient_binding() FROM PUBLIC,household_app;
CREATE TRIGGER planning_binding_identity BEFORE INSERT OR UPDATE ON core.planning_ingredient_bindings FOR EACH ROW EXECUTE FUNCTION core.guard_planning_ingredient_binding();
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','planning_ingredient_bindings',ARRAY['id'],ARRAY['mealplan_generation','plan_id','meal_id','component_id','ingredient_id','product_id','pantry_generation','unit_code','version'],NULL);
CREATE FUNCTION core.planning_bindings_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.planning_ingredient_bindings','planning_binding_identity','core.guard_planning_ingredient_binding()',23,false),
 ('core.planning_ingredient_bindings','planning_binding_source','mealplan.guard_planning_ingredient_binding()',23,false),
 ('core.planning_ingredient_bindings','planning_binding_target','pantry.guard_planning_ingredient_binding()',23,false),
 ('mealplan.components','planning_binding_component_delete','mealplan.remove_deleted_ingredient_bindings()',9,true),
 ('pantry.products','planning_binding_product_delete','pantry.clear_deleted_planning_bindings()',9,false)
 ) required(relation,name,function_id,trigger_type,definer) WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgfoid=to_regprocedure(required.function_id)
 AND t.tgtype=required.trigger_type AND t.tgenabled IN('O','A') AND p.prosecdef=required.definer AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND (NOT required.definer OR pg_get_userbyid(p.proowner)='household_owner')))
 AND NOT has_function_privilege('household_app','mealplan.remove_deleted_ingredient_bindings()','EXECUTE')
 AND NOT EXISTS(SELECT 1 FROM (VALUES('planning_ingredient_bindings','SELECT'),('planning_ingredient_bindings','INSERT'),('planning_ingredient_bindings','UPDATE')) required(relation,privilege)
 WHERE NOT has_table_privilege('household_app',format('core.%I',required.relation),required.privilege))
 AND NOT has_table_privilege('household_app','core.planning_ingredient_bindings','DELETE,TRUNCATE,TRIGGER')
 AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid WHERE c.conrelid='core.planning_ingredient_bindings'::regclass
 AND c.conname='planning_ingredient_source' AND c.contype='u' AND c.convalidated AND i.indisvalid AND i.indisunique)
$$;
REVOKE ALL ON FUNCTION core.planning_bindings_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.planning_bindings_ready() TO household_app;
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=70 AND bool_and(
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
