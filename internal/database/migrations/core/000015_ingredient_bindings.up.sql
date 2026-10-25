BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE core.ingredient_product_bindings(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 catalog_id uuid NOT NULL, source_generation bigint NOT NULL CHECK(source_generation>0),
 product_id uuid, target_generation bigint,
 created_by uuid NOT NULL REFERENCES core.users(id), changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK((product_id IS NULL AND target_generation IS NULL) OR (product_id IS NOT NULL AND target_generation IS NOT NULL AND target_generation>0)),
 UNIQUE(household_id,id), UNIQUE(household_id,source_generation,catalog_id)
);
CREATE INDEX ingredient_binding_target ON core.ingredient_product_bindings(household_id,product_id) WHERE product_id IS NOT NULL;
REVOKE ALL ON core.ingredient_product_bindings FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON core.ingredient_product_bindings TO household_app;
CREATE FUNCTION core.guard_ingredient_binding() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.catalog_id,NEW.source_generation,NEW.created_by,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.catalog_id,OLD.source_generation,OLD.created_by,OLD.created_at) THEN
   RAISE EXCEPTION 'ingredient binding identity is immutable' USING ERRCODE='23514';
  END IF;
  NEW.version=OLD.version+1;
 ELSE
  NEW.version=1;
 END IF;
 NEW.updated_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_ingredient_binding() FROM PUBLIC,household_app;
CREATE TRIGGER ingredient_binding_identity BEFORE INSERT OR UPDATE ON core.ingredient_product_bindings FOR EACH ROW EXECUTE FUNCTION core.guard_ingredient_binding();
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['catalog_id'] WHERE schema_name='recipes' AND table_name='ingredients';
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','ingredient_product_bindings',ARRAY['id'],ARRAY['catalog_id','source_generation','product_id','target_generation','version'],NULL),
 ('recipes','ingredient_catalog',ARRAY['id'],ARRAY['archived','version'],NULL),
 ('recipes','ingredient_aliases',ARRAY['id'],ARRAY['catalog_id','version'],NULL);
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=61 AND bool_and(
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
